// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package rankingweb

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// The state lives in data_dir (D14), in two files:
//
//	state.json     a snapshot of every entity, written atomically (a
//	               temporary file, fsync, rename, fsync of the directory)
//	journal.jsonl  the writes since the snapshot, one JSON line each,
//	               appended and fsynced before the write is applied and
//	               acknowledged
//
// At start the snapshot is loaded, the journal replayed (a torn last line,
// from a crash in the middle of a write that was never acknowledged, is
// dropped) and a new snapshot written; the journal is also folded into the
// snapshot when it grows past compactAt. One fsync per request keeps a
// full resend of 100 000 submissions fast, unlike the one file per entity
// of CMS (whose layout, the directories contests/, tasks/... of lib_dir,
// is imported when data_dir has no snapshot yet). Logos, faces and flags
// stay where CMS keeps them: data_dir/logo.<ext>, faces/, flags/.
const (
	snapshotFile = "state.json"
	journalFile  = "journal.jsonl"
	compactAt    = 32 << 20
)

type journal struct {
	f    *os.File
	size int64
}

// Open loads the store of dir. hub (may be nil) receives the events of
// the changes made after Open.
func Open(dir string, hub *live.Hub, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("ranking data_dir: %w", err)
	}
	s := &Store{log: log, dir: dir, st: newState()}
	loaded, err := s.loadSnapshot()
	if err != nil {
		return nil, err
	}
	if !loaded {
		n, err := s.importCMS()
		if err != nil {
			return nil, err
		}
		if n > 0 {
			log.Info("imported the data of a CMS RankingWebServer", "entities", n, "dir", dir)
		}
	}
	s.newScorings()
	replayed, err := s.replay()
	if err != nil {
		return nil, err
	}
	if replayed > 0 || !loaded {
		if err := s.writeSnapshot(); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, journalFile), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("ranking journal: %w", err)
	}
	if err := syncDir(dir); err != nil {
		f.Close()
		return nil, err
	}
	s.j = &journal{f: f}
	s.hub = hub
	return s, nil
}

// Close closes the journal.
func (s *Store) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.j == nil {
		return nil
	}
	err := s.j.f.Close()
	s.j = nil
	return err
}

func (s *Store) loadSnapshot() (bool, error) {
	path := filepath.Join(s.dir, snapshotFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("ranking snapshot: %w", err)
	}
	var snap map[string]json.RawMessage
	if err := json.Unmarshal(data, &snap); err != nil {
		return false, fmt.Errorf("ranking snapshot %s: %w", path, err)
	}
	for k := range numKinds {
		raw, ok := snap[kinds[k].path]
		if !ok {
			continue
		}
		var items map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return false, fmt.Errorf("ranking snapshot %s: %s: %w", path, kinds[k].path, err)
		}
		for key, v := range items {
			e, err := kinds[k].parse(v)
			if err != nil {
				return false, fmt.Errorf("ranking snapshot %s: %s %s: %w", path, kinds[k].name, key, err)
			}
			s.st.items[k][key] = e
		}
	}
	return true, nil
}

// importCMS loads the one-file-per-entity layout of CMS lib_dir.
func (s *Store) importCMS() (int, error) {
	n := 0
	for k := range numKinds {
		dir := filepath.Join(s.dir, kinds[k].path)
		ents, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return n, err
		}
		for _, de := range ents {
			key, ok := strings.CutSuffix(de.Name(), ".json")
			if !ok || !ValidKey(key) || de.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, de.Name()))
			if err != nil {
				return n, err
			}
			e, err := kinds[k].parse(data)
			if err != nil {
				s.log.Error("skipping an invalid CMS ranking file", "file", filepath.Join(dir, de.Name()), "err", err)
				continue
			}
			s.st.items[k][key] = e
			n++
		}
	}
	return n, nil
}

func (s *Store) replay() (int, error) {
	path := filepath.Join(s.dir, journalFile)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("ranking journal: %w", err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	n := 0
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(bytes.TrimSpace(line)) > 0 {
				s.log.Warn("dropping the torn last line of the ranking journal (a write interrupted by a crash)")
			}
			return n, nil
		}
		if err != nil {
			return n, fmt.Errorf("ranking journal: %w", err)
		}
		var o op
		if err := json.Unmarshal(line, &o); err != nil {
			s.log.Warn("dropping the rest of the ranking journal after an invalid line", "line", n+1, "err", err)
			return n, nil
		}
		if err := s.apply(o); err != nil {
			s.log.Warn("skipping an invalid ranking journal entry", "line", n+1, "err", err)
		}
		n++
	}
}

func (s *Store) snapshotBytes() ([]byte, error) {
	snap := map[string]any{"version": 1}
	for k := range numKinds {
		snap[kinds[k].path] = s.st.items[k]
	}
	return json.Marshal(snap)
}

func (s *Store) writeSnapshot() error {
	data, err := s.snapshotBytes()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.dir, snapshotFile), data)
}

// persist makes a write durable before it is applied.
func (s *Store) persist(o op) error {
	if s.j == nil {
		return errors.New("the ranking store is closed")
	}
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err = s.j.f.Write(b); err == nil {
		err = s.j.f.Sync()
	}
	if err != nil {
		err = fmt.Errorf("writing the ranking journal: %w", err)
		s.persistErr.Store(&err)
		s.log.Error("cannot store a write of the ranking", "err", err)
		return err
	}
	s.persistErr.Store(nil)
	s.j.size += int64(len(b))
	return nil
}

// maybeCompact folds the journal into the snapshot once it is large. The
// caller holds writeMu. A crash between the two steps only replays writes
// already in the snapshot, which converges to the same state.
func (s *Store) maybeCompact() {
	if s.j == nil || s.j.size < compactAt {
		return
	}
	s.mu.RLock()
	data, err := s.snapshotBytes()
	s.mu.RUnlock()
	if err == nil {
		err = writeFileAtomic(filepath.Join(s.dir, snapshotFile), data)
	}
	if err == nil {
		err = s.j.f.Truncate(0)
	}
	if err == nil {
		err = s.j.f.Sync()
	}
	if err != nil {
		s.log.Error("cannot compact the ranking journal", "err", err)
		return
	}
	s.j.size = 0
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(name, 0o640); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

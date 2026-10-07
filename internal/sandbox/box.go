// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// HomeDir is where the home of a box is mounted inside it, and the
// working directory of its programs (CMS uses /tmp too: compilers need a
// writable /tmp anyway).
const HomeDir = "/tmp"

// Box is an initialized isolate box and its home directory. A box is not
// safe for concurrent use, except Close, which may be called from any
// goroutine (it kills what is still running).
type Box struct {
	s     *Sandbox
	id    int
	outer string // host directory: home/, run.meta.N, commands.log
	home  string // outer/home, mounted at HomeDir
	root  *os.Root

	mu      sync.Mutex
	execNum int
	running map[*Process]struct{}
	closed  bool
	once    sync.Once
	err     error
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// NewBox takes a free box id (waiting if they are all in use), creates
// the home directory and initializes the isolate box. name only appears
// in the directory name, for debugging.
func (s *Sandbox) NewBox(ctx context.Context, name string) (b *Box, err error) {
	id, err := s.pool.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			s.pool.release(id)
		}
	}()
	if !s.cfg.KeepSandbox {
		// Homes left behind by a worker that died (nobody else can
		// be using this id: we hold its lock).
		old, _ := filepath.Glob(filepath.Join(s.cfg.WorkDir, fmt.Sprintf("box%d-*", id)))
		for _, d := range old {
			_ = os.RemoveAll(d)
		}
	}
	name = unsafeName.ReplaceAllString(name, "_")
	if len(name) > 40 {
		name = name[:40]
	}
	outer, err := os.MkdirTemp(s.cfg.WorkDir, fmt.Sprintf("box%d-%s-", id, name))
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	b = &Box{s: s, id: id, outer: outer, home: filepath.Join(outer, "home"), running: map[*Process]struct{}{}}
	if err := checkMountPath(b.home); err != nil {
		os.RemoveAll(outer)
		return nil, fmt.Errorf("sandbox: work directory: %w", err)
	}
	if err := os.Mkdir(b.home, 0o755); err == nil {
		// Like CMS: writable by the box user until a run restricts it
		// (the mode is set explicitly, the umask could remove bits).
		err = os.Chmod(b.home, 0o777)
	}
	if err == nil {
		b.root, err = os.OpenRoot(b.home)
	}
	if err != nil {
		os.RemoveAll(outer)
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	// A previous run may have left the box initialized (a worker that
	// died); --cleanup is idempotent.
	if err := s.cleanupBox(ctx, id); err != nil {
		b.root.Close()
		os.RemoveAll(outer)
		return nil, err
	}
	if _, err := s.isolate(ctx, s.cfg.Cgroups, "--box-id="+itoa(id), "--init"); err != nil {
		b.root.Close()
		os.RemoveAll(outer)
		if s.cfg.Cgroups {
			err = fmt.Errorf("%w (isolate --cg needs cgroups v2 and isolate.service; run `cms ctl verify-host`)", err)
		}
		return nil, fmt.Errorf("sandbox: initializing box %d: %w", id, err)
	}
	return b, nil
}

// cleanupBox runs isolate --cleanup. A box initialized in the other
// cgroup mode (sandbox.cgroups changed since) can only be cleaned up in
// that mode, so it is retried.
func (s *Sandbox) cleanupBox(ctx context.Context, id int) error {
	_, err := s.isolate(ctx, s.cfg.Cgroups, "--box-id="+itoa(id), "--cleanup")
	if err != nil && strings.Contains(err.Error(), "incompatible control group mode") {
		_, err = s.isolate(ctx, !s.cfg.Cgroups, "--box-id="+itoa(id), "--cleanup")
	}
	if err != nil {
		return fmt.Errorf("sandbox: cleaning up box %d: %w", id, err)
	}
	return nil
}

// ID is the isolate box id.
func (b *Box) ID() int { return b.id }

// Home is the host path of the home directory (HomeDir inside the box).
// Prefer the file helpers, which refuse to follow links out of it.
func (b *Box) Home() string { return b.home }

// Dir is the host directory of the box: the home, the meta files and
// the log of the commands run (commands.log).
func (b *Box) Dir() string { return b.outer }

// Close kills the processes still running, releases the isolate box and
// the box id, and deletes the directory of the box unless keep_sandbox
// is set. It is idempotent and safe to defer right after NewBox, so that
// boxes are released after errors, timeouts and panics.
func (b *Box) Close() error {
	b.once.Do(func() { b.err = b.close() })
	return b.err
}

func (b *Box) close() error {
	b.mu.Lock()
	b.closed = true
	procs := make([]*Process, 0, len(b.running))
	for p := range b.running {
		procs = append(procs, p)
	}
	b.mu.Unlock()
	for _, p := range procs {
		p.Kill()
	}
	for _, p := range procs {
		<-p.done
	}
	// Cleaning up must not be cut short by the context of the job.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b.root.Close()
	var errs []error
	if !b.s.cfg.KeepSandbox {
		if err := os.RemoveAll(b.outer); err != nil {
			// Directories the box user created are not writable by a
			// worker that is not root: let the box user open them up
			// (CMS does the same) and try again.
			_, _ = b.s.isolate(ctx, b.s.cfg.Cgroups, "--box-id="+itoa(b.id),
				"--dir="+HomeDir+"="+b.home+":rw", "--run", "--", "/bin/chmod", "-R", "777", HomeDir)
			if err := os.RemoveAll(b.outer); err != nil {
				errs = append(errs, fmt.Errorf("sandbox: deleting %s: %w", b.outer, err))
			}
		}
	}
	if err := b.s.cleanupBox(ctx, b.id); err != nil {
		errs = append(errs, err)
	}
	b.s.pool.release(b.id)
	return errors.Join(errs...)
}

// cleanName checks a path relative to the home: no absolute paths, no
// ".." components, no empty name.
func cleanName(name string) (string, error) {
	if name == "" || path.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("sandbox: invalid file name %q", name)
	}
	c := path.Clean(name)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("sandbox: file name %q is outside the box", name)
	}
	return c, nil
}

// CreateFile writes a new file in the home (CMS create_file). It fails if
// the file exists. Executables get mode 0755, other files 0644.
func (b *Box) CreateFile(name string, data []byte, executable bool) error {
	return b.CreateFileFrom(name, bytes.NewReader(data), executable)
}

// CreateFileFrom is CreateFile with the content read from r.
func (b *Box) CreateFileFrom(name string, r io.Reader, executable bool) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if dir := path.Dir(name); dir != "." {
		if err := b.root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
	}
	f, err := b.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, mode)
	if err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("sandbox: writing %s: %w", name, err)
	}
	if err := f.Chmod(mode); err != nil { // the umask may have removed bits
		f.Close()
		return fmt.Errorf("sandbox: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("sandbox: writing %s: %w", name, err)
	}
	return nil
}

// Open opens a regular file of the home for reading. Files are written by
// untrusted programs: links leaving the home are refused, a final link is
// refused, and so are special files (a FIFO would block forever).
func (b *Box) Open(name string) (*os.File, error) {
	name, err := cleanName(name)
	if err != nil {
		return nil, err
	}
	// os.Root follows links that stay inside the home, even with
	// O_NOFOLLOW: refuse a final link explicitly (nothing runs in the box
	// while the worker reads it, so the name cannot change in between).
	if st, err := b.root.Lstat(name); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	} else if st.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("sandbox: %s is a symbolic link", name)
	}
	f, err := b.root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("sandbox: %s is not a regular file", name)
	}
	return f, nil
}

// ReadFile returns the first max bytes of a file of the home (all of it
// when max < 0, which is only for files the sandbox user cannot have
// grown: use a limit for anything a contestant program wrote) and whether
// it was longer than that (CMS get_file_to_string truncates the same way).
func (b *Box) ReadFile(name string, max int64) (data []byte, truncated bool, err error) {
	f, err := b.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if max < 0 {
		data, err = io.ReadAll(f)
		return data, false, err
	}
	data, err = io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, false, fmt.Errorf("sandbox: reading %s: %w", name, err)
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

// Stat describes a file of the home without following a final link.
func (b *Box) Stat(name string) (fs.FileInfo, error) {
	name, err := cleanName(name)
	if err != nil {
		return nil, err
	}
	return b.root.Lstat(name)
}

// Exists reports whether a file of the home exists (a link counts as
// existing, whatever it points to).
func (b *Box) Exists(name string) bool {
	_, err := b.Stat(name)
	return err == nil
}

// Remove deletes a file of the home (or an empty directory).
func (b *Box) Remove(name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	return b.root.Remove(name)
}

// allowWriting applies the write policy of a run (CMS allow_writing_all
// and allow_writing_only): either everything in the home is writable, or
// only the given files (created empty when missing) and nothing new can
// be created. Like CMS, only the entries directly in the home change.
func (b *Box) allowWriting(restrict bool, writable []string) error {
	entries, err := os.ReadDir(b.home)
	if err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	if !restrict {
		if err := os.Chmod(b.home, 0o777); err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if err := b.root.Chmod(e.Name(), 0o777); err != nil {
				return fmt.Errorf("sandbox: %w", err)
			}
		}
		return nil
	}
	allowed := map[string]bool{}
	for _, w := range writable {
		// Paths outside the home (a FIFO in a shared directory, for
		// example) are not ours to change.
		inner := w
		if !path.IsAbs(inner) {
			inner = path.Join(HomeDir, inner)
		}
		inner = path.Clean(inner)
		rel, ok := strings.CutPrefix(inner, HomeDir+"/")
		if !ok || rel == "" {
			continue
		}
		allowed[rel] = true
	}
	for rel := range allowed {
		if dir := path.Dir(rel); dir != "." {
			if err := b.root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("sandbox: %w", err)
			}
		}
		f, err := b.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o644)
		if err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
		f.Close()
	}
	if err := os.Chmod(b.home, 0o755); err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 || allowed[e.Name()] {
			continue
		}
		if err := b.root.Chmod(e.Name(), 0o755); err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
	}
	for rel := range allowed {
		if err := b.root.Chmod(rel, 0o722); err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
	}
	return nil
}

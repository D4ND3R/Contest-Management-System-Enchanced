// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// cacheVersion is part of every key: changing it forgets every entry
// (when what a key covers or what an entry holds changes).
const cacheVersion = "1"

// Cache remembers successful compilations in Valkey (R2.3), shared by
// every worker: a compilation whose inputs were already compiled reuses
// the executables instead of running the compiler. That happens when an
// identical source is submitted again, when submissions are recompiled
// (reevaluation, a new dataset judged in the background) and when a user
// test is compiled again.
//
// The key (Key) is a SHA-256 of everything that decides what a
// compilation produces:
//
//   - the operation kind (submission or user test) and the task type with
//     its parameters;
//   - the language name and its definition (language.Language.Digest:
//     commands, flags, directories, environment);
//   - the compilers: the SHA-256 of each compiler binary the commands
//     run (CompilerIdentity), so a compiler upgrade misses;
//   - the submitted files and the managers that take part in the
//     compilation (graders, headers, objects, TwoSteps sources: the
//     managers of the language, as task types only compile those), as
//     sorted name/digest pairs;
//   - the multithreaded flag, the compilation limits, cgroups and the
//     architecture of the worker.
//
// The value holds what the result needs: outcome, text, statistics and
// the executables (name -> digest; the files stay in the blob store). An
// entry is used only if every executable is still in the store.
//
// Only successful compilations are remembered: a failure may come from
// the worker (a full disk makes the compiler fail) and must not stick.
// Jobs that archive or keep their boxes always compile: the admins want
// to see the box.
type Cache struct {
	kv    *kv.Client
	store blob.Store
	langs *language.Registry
	ttl   time.Duration
	salt  string
	ident func(*language.Language) (string, error)
	log   *slog.Logger

	mu     sync.Mutex
	idents map[string]*identity // by language digest
}

type identity struct {
	once  sync.Once
	value string
	err   error
}

// CacheOptions configures a Cache.
type CacheOptions struct {
	// TTL is how long an entry lives (worker.compilation_cache_ttl).
	TTL time.Duration
	// Limits are the compilation limits and Cgroups sandbox.cgroups: part
	// of every key.
	Limits  config.SandboxLimit
	Cgroups bool
	// Identity returns the identity of the compilers of a language
	// (default CompilerIdentity).
	Identity func(*language.Language) (string, error)
	Log      *slog.Logger
}

// NewCache returns a compilation cache on c. Executables are looked up in
// store and languages in langs.
func NewCache(c *kv.Client, store blob.Store, langs *language.Registry, opts CacheOptions) *Cache {
	if opts.Identity == nil {
		opts.Identity = CompilerIdentity
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	l := opts.Limits
	salt := strings.Join([]string{
		runtime.GOOS, runtime.GOARCH, strconv.FormatBool(opts.Cgroups),
		strconv.Itoa(l.MaxProcesses), strconv.FormatFloat(l.MaxTimeSec, 'g', -1, 64), strconv.FormatInt(l.MaxMemoryKiB, 10),
	}, "/")
	return &Cache{
		kv: c, store: store, langs: langs, ttl: opts.TTL, salt: salt,
		ident: opts.Identity, log: opts.Log, idents: map[string]*identity{},
	}
}

// cacheEntry is the value of a key.
type cacheEntry struct {
	Outcome     string            `json:"outcome"`
	Text        []string          `json:"text,omitempty"`
	Stats       *job.Stats        `json:"plus,omitempty"`
	Executables map[string]string `json:"executables"`
	// Job and Worker say where the entry comes from (debugging).
	Job    string    `json:"job"`
	Worker string    `json:"worker"`
	At     time.Time `json:"at"`
}

// Key returns the cache key of a job, or "" when the job must not use the
// cache (not a compilation, boxes to archive or keep, no or unknown
// language, a file whose content is gone, unknown compiler).
func (c *Cache) Key(j *job.Job) string {
	if c == nil || c.ttl <= 0 || !j.Operation.Type.IsCompilation() || j.Operation.ArchiveSandbox || j.KeepSandbox || j.Language == "" {
		return ""
	}
	lang, ok := c.langs.Lookup(j.Language)
	if !ok {
		return ""
	}
	ident, err := c.identity(lang)
	if err != nil {
		c.log.Warn("compilation cache: cannot identify the compilers", "language", lang.Name, "err", err)
		return ""
	}
	params := j.TaskTypeParameters
	var compact bytes.Buffer
	if json.Compact(&compact, params) == nil {
		params = compact.Bytes()
	}
	h := sha256.New()
	field(h, "cms-compilation-cache", cacheVersion, string(j.Operation.Type), j.TaskType, string(params),
		lang.Name, lang.Digest(), ident, c.salt, strconv.FormatBool(j.MultithreadedSandbox))
	if !files(h, "files", j.Files, nil) || !files(h, "managers", j.Managers, lang.IsCompilationFile) {
		return ""
	}
	return c.kv.Key("compcache", hex.EncodeToString(h.Sum(nil)))
}

// field writes length-prefixed strings, so that no two different lists
// of strings hash the same.
func field(h hash.Hash, parts ...string) {
	var n [binary.MaxVarintLen64]byte
	for _, p := range parts {
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(p)))])
		io.WriteString(h, p)
	}
}

// files writes the files (name -> digest) kept by keep (nil: all), in name
// order. It returns false if one of them is the tombstone.
func files(h hash.Hash, what string, m map[string]string, keep func(string) bool) bool {
	names := make([]string, 0, len(m))
	for name := range m {
		if keep == nil || keep(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	field(h, what, strconv.Itoa(len(names)))
	for _, name := range names {
		if m[name] == job.TombstoneDigest {
			return false
		}
		field(h, name, m[name])
	}
	return true
}

// identity returns the compiler identity of a language, computed once.
func (c *Cache) identity(l *language.Language) (string, error) {
	c.mu.Lock()
	id := c.idents[l.Digest()]
	if id == nil {
		id = &identity{}
		c.idents[l.Digest()] = id
	}
	c.mu.Unlock()
	id.once.Do(func() { id.value, id.err = c.ident(l) })
	return id.value, id.err
}

// Get fills res (a successful compilation) from the entry of key and
// reports whether there was a usable one. Errors (Valkey, the blob store)
// are logged and count as a miss: the job then compiles.
func (c *Cache) Get(ctx context.Context, key string, res *job.Result) bool {
	b, err := c.kv.Redis().Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false
	}
	if err != nil {
		c.log.Warn("compilation cache: reading", "err", err)
		return false
	}
	var e cacheEntry
	if err := json.Unmarshal(b, &e); err != nil || e.Outcome != job.CompilationOK || len(e.Executables) == 0 {
		c.forget(ctx, key, "unreadable entry")
		return false
	}
	// Entries are written by workers only, but Valkey is shared: an
	// entry that would make an invalid result is dropped.
	r := *res
	r.Success, r.Error = true, ""
	r.CompilationOutcome, r.Text, r.Stats, r.Executables = e.Outcome, e.Text, e.Stats, e.Executables
	if err := r.Validate(); err != nil {
		c.forget(ctx, key, "invalid entry: "+err.Error())
		return false
	}
	for name, d := range e.Executables {
		if _, err := c.store.Stat(ctx, d); err != nil {
			if errors.Is(err, blob.ErrNotFound) {
				c.forget(ctx, key, "executable "+name+" is gone from the store")
			} else {
				c.log.Warn("compilation cache: checking an executable", "err", err)
			}
			return false
		}
	}
	*res = r
	return true
}

func (c *Cache) forget(ctx context.Context, key, why string) {
	c.log.Info("compilation cache: dropping an entry", "reason", why)
	if err := c.kv.Redis().Del(ctx, key).Err(); err != nil {
		c.log.Warn("compilation cache: dropping an entry", "err", err)
	}
}

// Cacheable reports whether a result can be remembered: a successful
// compilation that produced executables.
func Cacheable(res *job.Result) bool {
	return res.Success && res.Operation.Type.IsCompilation() && res.CompilationOutcome == job.CompilationOK &&
		len(res.Executables) > 0
}

// Put remembers a cacheable result under key.
func (c *Cache) Put(ctx context.Context, key string, res *job.Result) error {
	if !Cacheable(res) {
		return nil
	}
	b, err := json.Marshal(&cacheEntry{
		Outcome: res.CompilationOutcome, Text: res.Text, Stats: res.Stats, Executables: res.Executables,
		Job: res.JobID, Worker: res.Worker, At: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := c.kv.Redis().Set(ctx, key, b, c.ttl).Err(); err != nil {
		return fmt.Errorf("compilation cache: writing: %w", err)
	}
	return nil
}

// maxCompilerHash bounds the bytes of a compiler binary that are hashed;
// larger files are identified by path, size and modification time.
const maxCompilerHash = 256 << 20

// CompilerIdentity identifies the compilers a language runs: for each
// distinct program of its compilation commands, the resolved path and the
// SHA-256 of the file. Interpreted languages (whose compilation copies the
// source) have none. Restart the workers after upgrading a compiler: the
// identity is computed once per process.
func CompilerIdentity(l *language.Language) (string, error) {
	h := sha256.New()
	seen := map[string]bool{}
	for _, cmd := range l.Compilation.Commands {
		if len(cmd.Args) == 0 {
			continue
		}
		prog := cmd.Args[0]
		if seen[prog] || !filepath.IsAbs(prog) {
			continue
		}
		seen[prog] = true
		real, err := filepath.EvalSymlinks(prog)
		if err != nil {
			// A missing compiler fails the compilation, which is not cached.
			return "", fmt.Errorf("compiler %s: %w", prog, err)
		}
		field(h, prog, real)
		if err := hashFile(h, real); err != nil {
			return "", fmt.Errorf("compiler %s: %w", prog, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(h hash.Hash, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > maxCompilerHash {
		field(h, "size", strconv.FormatInt(st.Size(), 10), st.ModTime().UTC().Format(time.RFC3339Nano))
		return nil
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	field(h, "sha256", hex.EncodeToString(sum.Sum(nil)))
	return nil
}

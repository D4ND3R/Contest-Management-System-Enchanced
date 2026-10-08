// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	cmstest "github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func testCache(t *testing.T, c *kv.Client, store blob.Store, limits config.SandboxLimit) *Cache {
	t.Helper()
	return NewCache(c, store, language.Builtin(), CacheOptions{
		TTL:      time.Hour,
		Limits:   limits,
		Identity: func(*language.Language) (string, error) { return "compiler-v1", nil },
	})
}

func TestCacheKey(t *testing.T) {
	c := kv.New(nil, "test:")
	limits := config.Default().Sandbox.Compilation
	cache := testCache(t, c, nil, limits)
	base := func() *job.Job {
		j := compileJob(1, "a")
		j.Managers = map[string]string{"grader.cpp": digest("grader"), "task.h": digest("header"), "checker": digest("checker")}
		return j
	}
	key := cache.Key(base())
	if key == "" || !strings.HasPrefix(key, "test:compcache:") {
		t.Fatalf("key %q", key)
	}
	same := map[string]func(*job.Job){
		"another submission": func(j *job.Job) {
			j.ID, j.Operation.ObjectID, j.Attempt, j.Generation, j.Info = "other", 99, 3, 7, "info"
		},
		"another dataset":       func(j *job.Job) { j.Operation.DatasetID = 2 },
		"another checker":       func(j *job.Job) { j.Managers["checker"] = digest("checker 2") },
		"a manager not for C++": func(j *job.Job) { j.Managers["grader.py"] = digest("python grader") },
		"spaces in parameters":  func(j *job.Job) { j.TaskTypeParameters = json.RawMessage(`["alone",["",""],"diff"]`) },
		"evaluation limits":     func(j *job.Job) { j.TimeLimit, j.MemoryLimit = 0, 0 },
	}
	for name, change := range same {
		j := base()
		change(j)
		if k := cache.Key(j); k != key {
			t.Errorf("%s changed the key", name)
		}
	}
	different := map[string]func(*job.Job){
		"another source":      func(j *job.Job) { j.Files["sum.%l"] = digest("b") },
		"another file name":   func(j *job.Job) { j.Files = map[string]string{"prod.%l": digest("a")} },
		"one more file":       func(j *job.Job) { j.Files["extra.%l"] = digest("a") },
		"another grader":      func(j *job.Job) { j.Managers["grader.cpp"] = digest("grader 2") },
		"another header":      func(j *job.Job) { j.Managers["task.h"] = digest("header 2") },
		"no grader":           func(j *job.Job) { delete(j.Managers, "grader.cpp") },
		"another language":    func(j *job.Job) { j.Language = "C++20 / g++" },
		"another task type":   func(j *job.Job) { j.TaskType = "Communication" },
		"other parameters":    func(j *job.Job) { j.TaskTypeParameters = json.RawMessage(`["grader", ["", ""], "diff"]`) },
		"a user test":         func(j *job.Job) { j.Operation.Type = job.UserTestCompilation },
		"multithreaded":       func(j *job.Job) { j.MultithreadedSandbox = true },
		"name/digest swapped": func(j *job.Job) { j.Files = map[string]string{"sum.%l" + digest("a"): ""} },
	}
	for name, change := range different {
		j := base()
		change(j)
		if k := cache.Key(j); k == key || k == "" {
			t.Errorf("%s: key %q", name, k)
		}
	}
	none := map[string]func(*job.Job){
		"an evaluation":      func(j *job.Job) { j.Operation = evalJob(1, "tc1").Operation },
		"archived sandboxes": func(j *job.Job) { j.Operation.ArchiveSandbox = true },
		"kept sandboxes":     func(j *job.Job) { j.KeepSandbox = true },
		"no language":        func(j *job.Job) { j.Language = "" },
		"unknown language":   func(j *job.Job) { j.Language = "Brainfuck" },
		"tombstone source":   func(j *job.Job) { j.Files["sum.%l"] = job.TombstoneDigest },
		"tombstone grader":   func(j *job.Job) { j.Managers["grader.cpp"] = job.TombstoneDigest },
	}
	for name, change := range none {
		j := base()
		change(j)
		if k := cache.Key(j); k != "" {
			t.Errorf("%s: key %q", name, k)
		}
	}

	// The limits, cgroups and the compilers are part of the key.
	more := limits
	more.MaxTimeSec++
	if testCache(t, c, nil, more).Key(base()) == key {
		t.Error("the compilation limits are not in the key")
	}
	other := NewCache(c, nil, language.Builtin(), CacheOptions{TTL: time.Hour, Limits: limits,
		Identity: func(*language.Language) (string, error) { return "compiler-v2", nil }})
	if other.Key(base()) == key {
		t.Error("the compiler identity is not in the key")
	}
	cg := NewCache(c, nil, language.Builtin(), CacheOptions{TTL: time.Hour, Limits: limits, Cgroups: true,
		Identity: func(*language.Language) (string, error) { return "compiler-v1", nil }})
	if cg.Key(base()) == key {
		t.Error("cgroups is not in the key")
	}
	broken := NewCache(c, nil, language.Builtin(), CacheOptions{TTL: time.Hour, Limits: limits,
		Identity: func(*language.Language) (string, error) { return "", errors.New("no compiler") }})
	if k := broken.Key(base()); k != "" {
		t.Errorf("unknown compiler: key %q", k)
	}
	if k := NewCache(c, nil, language.Builtin(), CacheOptions{}).Key(base()); k != "" {
		t.Errorf("TTL 0: key %q", k)
	}
	var nilCache *Cache
	if k := nilCache.Key(base()); k != "" {
		t.Errorf("nil cache: key %q", k)
	}
}

func TestCompilationCache(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exec := &fakeExec{store: store}
	r := h.start("judge", exec, Options{Cache: testCache(t, h.kv, store, config.Default().Sandbox.Compilation)})
	cacheCount := func(result string) float64 { return metricValue(r.w.m.cache.WithLabelValues(result)) }

	h.enqueue(compileJob(1, "a"), 0)
	first := h.result(10 * time.Second)
	if !first.Success || first.CompilationOutcome != job.CompilationOK || exec.ncalls() != 1 {
		t.Fatalf("first compilation: %+v", first)
	}

	// The same source submitted again: no compilation.
	h.enqueue(compileJob(2, "a"), 0)
	second := h.result(10 * time.Second)
	if exec.ncalls() != 1 {
		t.Fatalf("the second compilation ran (%d calls)", exec.ncalls())
	}
	if !second.Success || second.JobID != "compile-2" || second.Operation.ObjectID != 2 || second.Worker != "judge" ||
		second.CompilationOutcome != job.CompilationOK || !reflect.DeepEqual(second.Executables, first.Executables) ||
		!reflect.DeepEqual(second.Text, first.Text) || !reflect.DeepEqual(second.Stats, first.Stats) {
		t.Errorf("cached result:\n%+v\nfirst:\n%+v", second, first)
	}
	if len(second.Sandboxes) != 0 {
		t.Errorf("sandboxes of a cached compilation: %v", second.Sandboxes)
	}
	if hits, misses, stores := cacheCount("hit"), cacheCount("miss"), cacheCount("store"); hits != 1 || misses != 1 || stores != 1 {
		t.Errorf("hits %v, misses %v, stores %v", hits, misses, stores)
	}

	// Another source compiles.
	h.enqueue(compileJob(3, "b"), 0)
	h.result(10 * time.Second)
	if exec.ncalls() != 2 {
		t.Errorf("%d calls", exec.ncalls())
	}

	// The executable vanished from the store: compile again.
	for _, d := range first.Executables {
		if err := store.Delete(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	h.enqueue(compileJob(4, "a"), 0)
	if res := h.result(10 * time.Second); !res.Success || exec.ncalls() != 3 {
		t.Errorf("after the executable vanished: %d calls, %+v", exec.ncalls(), res)
	}
	h.enqueue(compileJob(5, "a"), 0)
	if h.result(10 * time.Second); exec.ncalls() != 3 {
		t.Errorf("the entry was not written again: %d calls", exec.ncalls())
	}

	// Archived sandboxes always compile.
	j := compileJob(6, "a")
	j.Operation.ArchiveSandbox = true
	h.enqueue(j, 0)
	if h.result(10 * time.Second); exec.ncalls() != 4 {
		t.Errorf("archived compilation: %d calls", exec.ncalls())
	}
	// The entries expire.
	keys, err := h.kv.Redis().Keys(context.Background(), h.kv.Key("compcache", "*")).Result()
	if err != nil || len(keys) != 2 {
		t.Fatalf("entries %v %v", keys, err)
	}
	for _, k := range keys {
		if ttl := h.kv.Redis().PTTL(context.Background(), k).Val(); ttl <= 0 || ttl > time.Hour {
			t.Errorf("ttl %v", ttl)
		}
	}
}

func TestFailedCompilationIsNotCached(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exec := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		res.Success = true
		res.CompilationOutcome = job.CompilationFail
		res.Text = []string{"Compilation failed"}
		res.Stats = &job.Stats{ExitStatus: job.ExitNonzeroReturn, Stderr: "error: expected ';'"}
		return nil
	}}
	h.start("judge", exec, Options{Cache: testCache(t, h.kv, store, config.Default().Sandbox.Compilation)})
	for i := range 2 {
		h.enqueue(compileJob(int32(i+1), "bad"), 0)
		if res := h.result(10 * time.Second); res.CompilationOutcome != job.CompilationFail {
			t.Errorf("%+v", res)
		}
	}
	if exec.ncalls() != 2 {
		t.Errorf("%d calls", exec.ncalls())
	}
}

func TestCompilerIdentity(t *testing.T) {
	dir := t.TempDir()
	compiler := filepath.Join(dir, "cc-1.0")
	if err := os.WriteFile(compiler, []byte("#!/bin/sh\necho version 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cc")
	if err := os.Symlink(compiler, link); err != nil {
		t.Fatal(err)
	}
	lang := func(prog string) *language.Language {
		l, err := language.Parse([]byte(`
name: Test
source_extensions: [.t]
compilation:
  commands:
    - ["`+prog+`", -o, "{executable}", "{sources}"]
    - ["`+prog+`", --strip, "{executable}"]
evaluation:
  commands:
    - ["./{executable}"]
`), "test.yaml")
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	id1, err := CompilerIdentity(lang(link))
	if err != nil || id1 == "" {
		t.Fatalf("identity %q, %v", id1, err)
	}
	if id, _ := CompilerIdentity(lang(link)); id != id1 {
		t.Error("the identity is not stable")
	}
	// An upgrade replaces the binary behind the link.
	if err := os.WriteFile(compiler, []byte("#!/bin/sh\necho version 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if id, _ := CompilerIdentity(lang(link)); id == id1 {
		t.Error("the identity ignores the content of the compiler")
	}
	if _, err := CompilerIdentity(lang(filepath.Join(dir, "missing"))); err == nil {
		t.Error("missing compiler accepted")
	}
	// Interpreted languages compile with a copy.
	py, err := language.Builtin().Get("Python 3 / CPython")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompilerIdentity(py); err != nil {
		t.Error(err)
	}
}

// TestCompilerIdentityFollowsWrappers: upgrading what a compiler runs
// changes the identity even when the program of the command stays the
// same (wrapper scripts, compilers installed as <home>/bin + <home>/lib,
// shell commands).
func TestCompilerIdentityFollowsWrappers(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lang := func(env string, commands ...string) *language.Language {
		t.Helper()
		l, err := language.Parse([]byte(`
name: Test
source_extensions: [.t]
compilation:
  commands:
`+strings.Join(commands, "\n")+`
  env: [`+env+`]
evaluation:
  commands:
    - ["./{executable}"]
`), "test.yaml")
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	// Each case: the language, then the file an upgrade changes.
	interp := filepath.Join(dir, "interp", "sh")
	write(interp, "interpreter v1")
	write(filepath.Join(dir, "wrapper", "real-cc"), "compiler v1")
	write(filepath.Join(dir, "wrapper", "cc"), "#!/bin/sh\nexec "+filepath.Join(dir, "wrapper", "real-cc")+` "$@"`+"\n")
	write(filepath.Join(dir, "kotlin", "bin", "kotlinc"), "#!/bin/sh\nexec java -cp \"$(dirname $0)/../lib/compiler.jar\" \"$@\"\n")
	write(filepath.Join(dir, "kotlin", "lib", "compiler.jar"), "jar v1")
	write(filepath.Join(dir, "jdk", "bin", "javac"), "launcher")
	write(filepath.Join(dir, "jdk", "release"), `JAVA_VERSION="21.0.1"`)
	write(filepath.Join(dir, "path", "jar"), "jar tool v1")
	write(filepath.Join(dir, "script", "cc"), "#!"+interp+" -e\n")
	for name, tc := range map[string]struct {
		lang    *language.Language
		upgrade string
	}{
		"wrapper script":  {lang("", `    - ["`+filepath.Join(dir, "wrapper", "cc")+`", "{sources}"]`), filepath.Join(dir, "wrapper", "real-cc")},
		"interpreter":     {lang("", `    - ["`+filepath.Join(dir, "script", "cc")+`", "{sources}"]`), interp},
		"installation":    {lang("", `    - ["`+filepath.Join(dir, "kotlin", "bin", "kotlinc")+`", "{sources}"]`), filepath.Join(dir, "kotlin", "lib", "compiler.jar")},
		"release file":    {lang("", `    - ["`+filepath.Join(dir, "jdk", "bin", "javac")+`", "{sources}"]`), filepath.Join(dir, "jdk", "release")},
		"shell from PATH": {lang("PATH="+filepath.Join(dir, "path"), `    - [/bin/sh, -c, "jar cf {executable|quote} *.class"]`), filepath.Join(dir, "path", "jar")},
	} {
		before, err := CompilerIdentity(tc.lang)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if again, _ := CompilerIdentity(tc.lang); again != before {
			t.Errorf("%s: the identity is not stable", name)
		}
		old, err := os.ReadFile(tc.upgrade)
		if err != nil {
			t.Fatal(err)
		}
		write(tc.upgrade, string(old)+" upgraded")
		if after, err := CompilerIdentity(tc.lang); err != nil || after == before {
			t.Errorf("%s: upgrading %s does not change the identity (%v)", name, tc.upgrade, err)
		}
	}
}

func TestShellProgram(t *testing.T) {
	for args, want := range map[string]string{
		"/bin/sh|-c|jar cf {executable|quote} *.class": "jar",
		"/bin/bash|-c|/opt/x/tool --flag":              "/opt/x/tool",
		"/bin/sh|-c|{compiler} x":                      "",
		"/bin/sh|-c|FOO=1 tool":                        "",
		"/bin/sh|-c|$CC x":                             "",
		"/bin/sh|script.sh":                            "",
		"/usr/bin/g++|-c|x.cpp":                        "",
	} {
		if got := shellProgram(strings.Split(args, "|")); got != want {
			t.Errorf("%q: %q, want %q", args, got, want)
		}
	}
}

func TestCacheWithRealCompilers(t *testing.T) {
	cpp, err := language.Builtin().Get("C++17 / g++")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/usr/bin/g++"); err != nil {
		t.Skip("g++ is not installed")
	}
	id, err := CompilerIdentity(cpp)
	if err != nil || len(id) != 64 {
		t.Fatalf("identity %q, %v", id, err)
	}
	cache := NewCache(kv.New(nil, "test:"), nil, language.Builtin(), CacheOptions{TTL: time.Hour})
	if cache.Key(compileJob(1, "a")) == "" {
		t.Error("no key with the real compiler identity")
	}
}

func TestInvalidCacheEntryIsDropped(t *testing.T) {
	c := cmstest.Redis(t)
	ctx := context.Background()
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := testCache(t, c, store, config.Default().Sandbox.Compilation)
	exe, err := store.PutBytes(ctx, []byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	j := compileJob(1, "a")
	key := cache.Key(j)
	for name, entry := range map[string]string{
		"not json":       "{",
		"failure":        `{"outcome": "fail", "executables": {"sum": "` + exe + `"}}`,
		"bad file name":  `{"outcome": "ok", "executables": {"../sum": "` + exe + `"}}`,
		"bad exit":       `{"outcome": "ok", "executables": {"sum": "` + exe + `"}, "plus": {"exit_status": "great"}}`,
		"bad digest":     `{"outcome": "ok", "executables": {"sum": "../../etc/passwd"}}`,
		"missing binary": `{"outcome": "ok", "executables": {"sum": "` + digest("gone") + `"}}`,
	} {
		if err := c.Redis().Set(ctx, key, entry, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
		res := j.NewResult("judge")
		if cache.Get(ctx, key, res) {
			t.Errorf("%s: hit", name)
		}
		if res.CompilationOutcome != "" || res.Executables != nil {
			t.Errorf("%s: result changed: %+v", name, res)
		}
		if n, _ := c.Redis().Exists(ctx, key).Result(); n != 0 {
			t.Errorf("%s: entry kept", name)
		}
	}
	// A good entry.
	ok := `{"outcome": "ok", "text": ["Compilation succeeded"], "executables": {"sum": "` + exe + `"}}`
	if err := c.Redis().Set(ctx, key, ok, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	res := j.NewResult("judge")
	if !cache.Get(ctx, key, res) || !res.Success || res.Executables["sum"] != exe || res.Text[0] != "Compilation succeeded" {
		t.Errorf("good entry: %+v", res)
	}
}

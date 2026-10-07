// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cms.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestFileThenEnvOverride(t *testing.T) {
	p := writeFile(t, `
secret_key: "00112233445566778899aabbccddeeff"
database:
  url: postgres://file@db/cms
  max_conns: 7
contest_web:
  listen: ":8000"
  cookie_duration: 2h
`)
	cfg, used, err := load(p, env(map[string]string{
		"CMS_DATABASE_URL":                 "postgres://env@db/cms",
		"CMS_CONTEST_WEB_COOKIE_DURATION":  "45",
		"CMS_WORKER_KEEP_SANDBOX":          "true",
		"CMS_SANDBOX_TRUSTED_MAX_TIME_SEC": "2.5",
	}), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if used != p {
		t.Errorf("used path %q, want %q", used, p)
	}
	if cfg.Database.URL != "postgres://env@db/cms" {
		t.Errorf("env did not override the file: %q", cfg.Database.URL)
	}
	if cfg.Database.MaxConns != 7 {
		t.Errorf("file value lost: max_conns=%d", cfg.Database.MaxConns)
	}
	if cfg.ContestWeb.Listen != ":8000" {
		t.Errorf("listen=%q", cfg.ContestWeb.Listen)
	}
	if cfg.ContestWeb.CookieDuration.D() != 45*time.Second {
		t.Errorf("bare integers are seconds: got %v", cfg.ContestWeb.CookieDuration)
	}
	if !cfg.Worker.KeepSandbox {
		t.Error("bool override ignored")
	}
	if cfg.Sandbox.Trusted.MaxTimeSec != 2.5 {
		t.Errorf("float override: %v", cfg.Sandbox.Trusted.MaxTimeSec)
	}
	// Untouched sections keep their defaults.
	if cfg.AdminWeb.Listen != Default().AdminWeb.Listen {
		t.Errorf("admin listen default lost: %q", cfg.AdminWeb.Listen)
	}
}

func TestEnvFileVariant(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("ffeeddccbbaa99887766554433221100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := load(writeFile(t, ""), env(map[string]string{"CMS_SECRET_KEY_FILE": secret}), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretKey != "ffeeddccbbaa99887766554433221100" {
		t.Errorf("secret from file: %q", cfg.SecretKey)
	}
}

func TestConfigPathFromEnv(t *testing.T) {
	p := writeFile(t, "timezone: America/Mexico_City\n")
	cfg, used, err := load("", env(map[string]string{EnvConfigPath: p}), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if used != p || cfg.Timezone != "America/Mexico_City" {
		t.Errorf("used=%q tz=%q", used, cfg.Timezone)
	}
	if cfg.Location().String() != "America/Mexico_City" {
		t.Errorf("location %v", cfg.Location())
	}
}

func TestExplicitMissingFileFails(t *testing.T) {
	_, _, err := load(filepath.Join(t.TempDir(), "nope.yaml"), env(nil), os.ReadFile)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want not-exist error, got %v", err)
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, _, err := load(writeFile(t, "databse:\n  url: x\n"), env(nil), os.ReadFile)
	if err == nil || !strings.Contains(err.Error(), "databse") {
		t.Fatalf("typo not reported: %v", err)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"secret_key: zz":              "hexadecimal",
		"secret_key: \"0011\"":        "at least 16",
		"timezone: Mars/Olympus":      "timezone",
		"log:\n  level: loud":         "log.level",
		"storage:\n  backend: tape":   "storage.backend",
		"storage:\n  backend: remote": "storage.remote.url must be set",
		"storage:\n  backend: remote\n  remote:\n    url: http://dispatcher:8891":                 "storage.remote.token must be set",
		"storage:\n  backend: remote\n  remote:\n    url: http://dispatcher:8891\n    token: abc": "storage.remote.token must be 64",
		"storage:\n  backend: s3":                                          "storage.s3.bucket must be set",
		"storage:\n  cache_size_mb: -1":                                    "storage.cache_size_mb",
		"storage:\n  backend: s3\n  s3:\n    bucket: b\n  cache_dir: \"\"": "storage.cache_dir",
		"storage:\n  local:\n    path: \"\"":                               "storage.local.path",
		"contest_web:\n  listen: nonsense":                                 "contest_web.listen",
		"worker:\n  concurrency: 0":                                        "worker.concurrency",
		"contest_web:\n  cookie_duration: 3 d":                             "invalid duration",
	}
	for yml, want := range cases {
		_, _, err := load(writeFile(t, yml), env(nil), os.ReadFile)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error containing %q, got %v", yml, want, err)
		}
	}
	if _, _, err := load(writeFile(t, ""), env(map[string]string{"CMS_WORKER_CONCURRENCY": "many"}), os.ReadFile); err == nil {
		t.Error("bad integer in env accepted")
	}
}

func TestStorageBackends(t *testing.T) {
	token := strings.Repeat("0f", 32)
	// A judge on another machine: no secret_key, only the blob token.
	cfg, _, err := load(writeFile(t, "storage:\n  backend: remote\n"), env(map[string]string{
		"CMS_STORAGE_REMOTE_URL":   "http://10.0.0.1:8891",
		"CMS_STORAGE_REMOTE_TOKEN": token,
	}), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Backend != "remote" || cfg.Storage.Remote.URL != "http://10.0.0.1:8891" ||
		cfg.Storage.Remote.Token != token || cfg.SecretKey != "" {
		t.Errorf("storage: %+v", cfg.Storage)
	}
	for _, bad := range []string{"10.0.0.1:8891", "ftp://host/", "http://", "://x"} {
		_, _, err := load(writeFile(t, "storage:\n  backend: remote\n"), env(map[string]string{
			"CMS_STORAGE_REMOTE_URL":   bad,
			"CMS_STORAGE_REMOTE_TOKEN": token,
		}), os.ReadFile)
		if err == nil || !strings.Contains(err.Error(), "storage.remote.url") {
			t.Errorf("remote url %q: got %v", bad, err)
		}
	}
	for _, bad := range []string{strings.ToUpper(token), token[:62], token + "00", strings.Repeat("zz", 32)} {
		_, _, err := load(writeFile(t, "storage:\n  backend: remote\n"), env(map[string]string{
			"CMS_STORAGE_REMOTE_URL":   "http://10.0.0.1:8891",
			"CMS_STORAGE_REMOTE_TOKEN": bad,
		}), os.ReadFile)
		if err == nil || !strings.Contains(err.Error(), "storage.remote.token") {
			t.Errorf("remote token %q: got %v", bad, err)
		}
	}
	if _, _, err := load(writeFile(t, "storage:\n  backend: s3\n  s3:\n    bucket: cms\n"), env(nil), os.ReadFile); err != nil {
		t.Errorf("s3 with a bucket: %v", err)
	}
	// Without a cache (disabled, or the local backend) the directory is
	// irrelevant.
	for _, yml := range []string{
		"storage:\n  backend: s3\n  s3:\n    bucket: b\n  cache_size_mb: 0\n  cache_dir: \"\"\n",
		"storage:\n  backend: local\n  cache_dir: \"\"\n",
	} {
		if _, _, err := load(writeFile(t, yml), env(nil), os.ReadFile); err != nil {
			t.Errorf("%q: %v", yml, err)
		}
	}
}

func TestRequireSecretKey(t *testing.T) {
	c := Default()
	if err := c.RequireSecretKey(); err == nil {
		t.Fatal("missing secret accepted")
	}
	c.SecretKey = "00112233445566778899aabbccddeeff"
	if err := c.RequireSecretKey(); err != nil {
		t.Fatal(err)
	}
	if len(c.SecretKeyBytes()) != 16 {
		t.Fatal("secret bytes")
	}
}

func TestEnvNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, s := range Settings(Default()) {
		if prev, ok := seen[s.Env]; ok {
			t.Errorf("%s and %s share the variable %s", prev, s.Path, s.Env)
		}
		seen[s.Env] = s.Path
	}
	if seen["CMS_DATABASE_URL"] != "database.url" || seen["CMS_SANDBOX_COMPILATION_MAX_MEMORY_KIB"] == "" {
		t.Errorf("unexpected names: %v", seen)
	}
}

func TestRedacted(t *testing.T) {
	c := Default()
	c.SecretKey = "00112233445566778899aabbccddeeff"
	c.Database.URL = "postgres://cms:hunter2@db:5432/cms"
	c.Redis.URL = "redis://:tok3n@kv:6379/0"
	c.Storage.S3.SecretKey = "s3cr3t"
	c.Storage.Remote.Token = strings.Repeat("5b", 32)
	r := c.Redacted()
	out, err := yaml.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"hunter2", "tok3n", "s3cr3t", "8899aabb", "5b5b5b"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("redacted output leaks %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(string(out), "postgres://cms:<redacted>@db:5432/cms") {
		t.Errorf("database url not redacted as expected:\n%s", out)
	}
	if c.Database.URL != "postgres://cms:hunter2@db:5432/cms" {
		t.Error("Redacted modified the original")
	}
	// The marshalled form must load back (durations as strings).
	var back Config
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.ContestWeb.CookieDuration != c.ContestWeb.CookieDuration {
		t.Errorf("duration round trip: %v", back.ContestWeb.CookieDuration)
	}
}

func TestSampleConfigLoads(t *testing.T) {
	cfg, _, err := load("../../config/cms.sample.yaml", env(nil), os.ReadFile)
	if err != nil {
		t.Fatalf("config/cms.sample.yaml: %v", err)
	}
	// The sample documents every setting with its default value, except the
	// secret key which has no default.
	want := Default()
	want.SecretKey = cfg.SecretKey
	a, _ := yaml.Marshal(cfg)
	b, _ := yaml.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("sample config differs from the defaults:\n--- sample\n%s\n--- defaults\n%s", a, b)
	}
}

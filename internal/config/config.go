// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package config loads cms.yaml. Every leaf setting can be overridden by an
// environment variable named CMS_ followed by its YAML path in upper case,
// with "_" between levels (database.url -> CMS_DATABASE_URL). Appending
// _FILE to that name reads the value from a file instead (Docker secrets).
//
// The same file is shared by every service of an installation: the shared
// secret_key in particular must be identical everywhere (see DECISIONS.md).
package config

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // the timezone setting must work in minimal containers

	"go.yaml.in/yaml/v3"
)

// EnvConfigPath names the variable holding the path of the config file.
const EnvConfigPath = "CMS_CONFIG"

// DefaultPaths are tried, in order, when no path is given explicitly.
var DefaultPaths = []string{"cms.yaml", "/etc/cms/cms.yaml"}

// Config is the whole configuration of an installation.
type Config struct {
	// SecretKey is the root of the keys that sign cookies and CSRF tokens,
	// of the private scoreboard links and of the blob server token (every
	// use derives its own key with DeriveKey). Hex encoded, at least 16
	// bytes. It must be the same in every service; the installer generates
	// it once. Judges on other machines (storage.backend remote) do not
	// need it: they only get storage.remote.token.
	SecretKey string `yaml:"secret_key" secret:"true"`
	// SourceURL is the repository linked from the footer of every site
	// (AGPL §13). Change it if you run a modified version.
	SourceURL string `yaml:"source_url"`
	// DataDir holds local state (blobs when storage is local, backups...).
	DataDir string `yaml:"data_dir"`
	// Timezone is the initial server timezone. Once the administration
	// changes it on the Server page the value stored in the database wins.
	Timezone string `yaml:"timezone"`

	Log        Log        `yaml:"log"`
	Database   Database   `yaml:"database"`
	Redis      Redis      `yaml:"redis"`
	Storage    Storage    `yaml:"storage"`
	ContestWeb ContestWeb `yaml:"contest_web"`
	AdminWeb   AdminWeb   `yaml:"admin_web"`
	RankingWeb RankingWeb `yaml:"ranking_web"`
	Dispatcher Dispatcher `yaml:"dispatcher"`
	Worker     Worker     `yaml:"worker"`
	Monitor    Monitor    `yaml:"monitor"`
	Sandbox    Sandbox    `yaml:"sandbox"`
}

// Log configures the structured logger every service writes to stdout.
type Log struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json (production) or text (development)
}

// Database configures the PostgreSQL connection pool.
type Database struct {
	URL      string `yaml:"url" secret:"true"`
	MaxConns int    `yaml:"max_conns"`
}

// Redis configures the Valkey/Redis connection (job streams and live events).
type Redis struct {
	URL string `yaml:"url" secret:"true"`
	// KeyPrefix namespaces every key, so several installations can share
	// one Valkey.
	KeyPrefix string `yaml:"key_prefix"`
}

// Storage configures the content-addressed blob store.
type Storage struct {
	// Backend is local (a directory), s3 (an S3-compatible bucket) or
	// remote (the blob server of the dispatcher, for judges on other
	// machines, which have no database access).
	Backend string     `yaml:"backend"`
	Local   LocalBlob  `yaml:"local"`
	S3      S3Blob     `yaml:"s3"`
	Remote  RemoteBlob `yaml:"remote"`
	// CacheDir and CacheSizeMB configure the on-disk LRU cache in front of
	// the s3 and remote backends (workers, web servers). Each service keeps
	// its own cache, of at most CacheSizeMB, in CacheDir/<service>; 0
	// disables it. CacheDir/<service>/tmp also holds the temporary copies
	// of uploads to s3 and remote (up to 1 GiB each), so it must be on
	// disk, not on a tmpfs. Empty CacheDir (allowed without a cache) puts
	// them in $TMPDIR.
	CacheDir    string `yaml:"cache_dir"`
	CacheSizeMB int    `yaml:"cache_size_mb"`
}

// LocalBlob is the on-disk blob store.
type LocalBlob struct {
	Path string `yaml:"path"`
}

// S3Blob is an S3-compatible blob store. Objects are named
// <prefix>/ab/<digest>.
type S3Blob struct {
	// Endpoint is host[:port]; empty means AWS (s3.amazonaws.com).
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	Prefix    string `yaml:"prefix"`
	AccessKey string `yaml:"access_key" secret:"true"`
	SecretKey string `yaml:"secret_key" secret:"true"`
	UseSSL    bool   `yaml:"use_ssl"`
}

// RemoteBlob is the blob server of the dispatcher, reached by judges on
// other machines.
type RemoteBlob struct {
	// URL is the base http(s) URL of the server; blobs are under URL/blobs/.
	URL string `yaml:"url"`
	// Token authenticates the judge: 64 hex characters derived from
	// secret_key, printed by `cms ctl blob-token` on a machine that has
	// it. It only lets its holder read and add blobs, so judges get this
	// token instead of secret_key.
	Token string `yaml:"token" secret:"true"`
}

// ContestWeb configures the contestant site (CMS ContestWebServer).
type ContestWeb struct {
	Listen    string `yaml:"listen"`
	OpsListen string `yaml:"ops_listen"`
	// Contest restricts the site to one contest (by name). Empty serves the
	// contest list at "/" and every contest under "/<name>/", as CMS does.
	Contest string `yaml:"contest"`
	// NumProxies is the number of reverse proxies in front of the site,
	// used to find the client address in X-Forwarded-For.
	NumProxies     int      `yaml:"num_proxies"`
	CookieDuration Duration `yaml:"cookie_duration"`
	// MaxSubmissionLength and MaxInputLength are in bytes.
	MaxSubmissionLength int64 `yaml:"max_submission_length"`
	MaxInputLength      int64 `yaml:"max_input_length"`
	// AdminToken lets administrators impersonate users through the API
	// (CMS contest_admin_token). Empty disables impersonation.
	AdminToken string `yaml:"admin_token" secret:"true"`
	// DocsPath is a directory served under /docs (language documentation).
	DocsPath string `yaml:"docs_path"`
}

// AdminWeb configures the administration site (CMS AdminWebServer).
type AdminWeb struct {
	Listen         string   `yaml:"listen"`
	OpsListen      string   `yaml:"ops_listen"`
	NumProxies     int      `yaml:"num_proxies"`
	CookieDuration Duration `yaml:"cookie_duration"`
}

// RankingWeb configures the public scoreboard (CMS RankingWebServer).
type RankingWeb struct {
	Listen     string `yaml:"listen"`
	OpsListen  string `yaml:"ops_listen"`
	NumProxies int    `yaml:"num_proxies"`
	// DataDir stores the scoreboard state pushed by the dispatcher, so the
	// site can run on a separate host without database access.
	DataDir string `yaml:"data_dir"`
}

// Dispatcher configures the service that replaces EvaluationService,
// ScoringService and ProxyService.
type Dispatcher struct {
	OpsListen string `yaml:"ops_listen"`
}

// Worker configures a judging host. Workers never connect to PostgreSQL:
// each job carries what it needs and files are fetched by digest.
type Worker struct {
	OpsListen string `yaml:"ops_listen"`
	// Name identifies the worker in the judges page; empty means hostname.
	Name string `yaml:"name"`
	// Concurrency is the number of jobs run in parallel. Use at most one
	// per physical core reserved for judging.
	Concurrency int `yaml:"concurrency"`
	// BoxIDBase is the first isolate box id used by this worker.
	BoxIDBase int `yaml:"box_id_base"`
	// WorkDir holds sandboxes and compiled files. It is converted to an
	// absolute path at startup because isolate rejects relative --dir.
	WorkDir     string `yaml:"work_dir"`
	IsolatePath string `yaml:"isolate_path"`
	KeepSandbox bool   `yaml:"keep_sandbox"`
}

// Monitor configures the health/metrics aggregator (CMS Checker,
// ResourceService and LogService).
type Monitor struct {
	Listen string `yaml:"listen"`
}

// Sandbox holds the limits that are not part of a task (CMS [sandbox]).
type Sandbox struct {
	MaxFileSizeKiB int64        `yaml:"max_file_size_kib"`
	Compilation    SandboxLimit `yaml:"compilation"`
	Trusted        SandboxLimit `yaml:"trusted"`
}

// SandboxLimit bounds compilations and trusted executions (checkers,
// managers, generators).
type SandboxLimit struct {
	MaxProcesses int     `yaml:"max_processes"`
	MaxTimeSec   float64 `yaml:"max_time_sec"`
	MaxMemoryKiB int64   `yaml:"max_memory_kib"`
}

// Default returns the configuration used for every key the file and the
// environment leave unset. Ports match the CMS defaults.
func Default() *Config {
	return &Config{
		SourceURL: "",
		DataDir:   "/var/lib/cms",
		Timezone:  "UTC",
		Log:       Log{Level: "info", Format: "json"},
		Database:  Database{URL: "postgres://cms@localhost:5432/cms?sslmode=disable", MaxConns: 20},
		Redis:     Redis{URL: "redis://localhost:6379/0", KeyPrefix: "cms:"},
		Storage: Storage{
			Backend:     "local",
			Local:       LocalBlob{Path: "/var/lib/cms/blobs"},
			S3:          S3Blob{UseSSL: true},
			CacheDir:    "/var/cache/cms/blobs",
			CacheSizeMB: 2048,
		},
		ContestWeb: ContestWeb{
			Listen:              "127.0.0.1:8888",
			OpsListen:           "127.0.0.1:9188",
			CookieDuration:      Duration(3 * time.Hour),
			MaxSubmissionLength: 100_000,
			MaxInputLength:      5_000_000,
			DocsPath:            "/usr/share/cms/docs",
		},
		AdminWeb: AdminWeb{
			Listen:         "127.0.0.1:8889",
			OpsListen:      "127.0.0.1:9189",
			CookieDuration: Duration(10 * time.Hour),
		},
		RankingWeb: RankingWeb{
			Listen:    "127.0.0.1:8890",
			OpsListen: "127.0.0.1:9190",
			DataDir:   "/var/lib/cms/ranking",
		},
		Dispatcher: Dispatcher{OpsListen: "127.0.0.1:9191"},
		Worker: Worker{
			OpsListen:   "127.0.0.1:9192",
			Concurrency: 1,
			WorkDir:     "/var/lib/cms/worker",
			IsolatePath: "isolate",
		},
		Monitor: Monitor{Listen: "127.0.0.1:9193"},
		Sandbox: Sandbox{
			MaxFileSizeKiB: 1 << 20,
			Compilation:    SandboxLimit{MaxProcesses: 1000, MaxTimeSec: 10, MaxMemoryKiB: 512 << 10},
			Trusted:        SandboxLimit{MaxProcesses: 1000, MaxTimeSec: 10, MaxMemoryKiB: 4 << 20},
		},
	}
}

// Load reads the configuration. An explicit path must exist; otherwise
// $CMS_CONFIG and then DefaultPaths are tried, and with no file at all the
// defaults plus the environment are used (handy for containers).
// The environment always wins over the file.
func Load(path string) (*Config, string, error) {
	return load(path, os.LookupEnv, os.ReadFile)
}

func load(path string, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) (*Config, string, error) {
	cfg := Default()
	explicit := path != ""
	if !explicit {
		if p, ok := lookup(EnvConfigPath); ok && p != "" {
			path, explicit = p, true
		}
	}
	if !explicit {
		for _, p := range DefaultPaths {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}
	if path != "" {
		data, err := readFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading config: %w", err)
		}
		if err := decodeYAML(data, cfg); err != nil {
			return nil, "", fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	if err := applyEnv(cfg, lookup, readFile); err != nil {
		return nil, "", err
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

func decodeYAML(data []byte, cfg *Config) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// A typo in a key must be an error, not a silently ignored setting.
	dec.KnownFields(true)
	return dec.Decode(cfg)
}

// Validate checks the settings that every service relies on. Settings only
// some services need are checked by Require* when those services start.
func (c *Config) Validate() error {
	var errs []error
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level: %q is not one of debug, info, warn, error", c.Log.Level))
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("log.format: %q is not json or text", c.Log.Format))
	}
	if c.SecretKey != "" {
		if err := checkSecretKey(c.SecretKey); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("timezone: %w", err))
	}
	errs = append(errs, c.Storage.validate()...)
	if c.Database.MaxConns < 1 {
		errs = append(errs, errors.New("database.max_conns must be at least 1"))
	}
	if c.Worker.Concurrency < 1 {
		errs = append(errs, errors.New("worker.concurrency must be at least 1"))
	}
	for name, addr := range map[string]string{
		"contest_web.listen":     c.ContestWeb.Listen,
		"contest_web.ops_listen": c.ContestWeb.OpsListen,
		"admin_web.listen":       c.AdminWeb.Listen,
		"admin_web.ops_listen":   c.AdminWeb.OpsListen,
		"ranking_web.listen":     c.RankingWeb.Listen,
		"ranking_web.ops_listen": c.RankingWeb.OpsListen,
		"dispatcher.ops_listen":  c.Dispatcher.OpsListen,
		"worker.ops_listen":      c.Worker.OpsListen,
		"monitor.listen":         c.Monitor.Listen,
	} {
		if addr == "" {
			continue // empty disables an ops listener
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Storage) validate() []error {
	var errs []error
	switch s.Backend {
	case "local":
		if s.Local.Path == "" {
			errs = append(errs, errors.New("storage.local.path must be set when storage.backend is local"))
		}
	case "s3":
		if s.S3.Bucket == "" {
			errs = append(errs, errors.New("storage.s3.bucket must be set when storage.backend is s3"))
		}
	case "remote":
		if s.Remote.URL == "" {
			errs = append(errs, errors.New("storage.remote.url must be set when storage.backend is remote"))
		} else if u, err := url.Parse(s.Remote.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("storage.remote.url: %q is not an http(s) URL", s.Remote.URL))
		}
		switch {
		case s.Remote.Token == "":
			errs = append(errs, errors.New("storage.remote.token must be set when storage.backend is remote "+
				"(print it with `cms ctl blob-token` on a machine that has secret_key)"))
		case !isHexKey(s.Remote.Token, 32):
			errs = append(errs, errors.New("storage.remote.token must be 64 lowercase hexadecimal characters "+
				"(print it with `cms ctl blob-token`)"))
		}
	default:
		errs = append(errs, fmt.Errorf("storage.backend: %q is not local, s3 or remote", s.Backend))
	}
	if s.CacheSizeMB < 0 {
		errs = append(errs, errors.New("storage.cache_size_mb must not be negative"))
	}
	// The local backend is never cached (see blob.Open).
	if s.Backend != "local" && s.CacheSizeMB > 0 && s.CacheDir == "" {
		errs = append(errs, errors.New("storage.cache_dir must be set when storage.cache_size_mb is not 0"))
	}
	return errs
}

func checkSecretKey(k string) error {
	b, err := hex.DecodeString(k)
	if err != nil {
		return errors.New("secret_key must be hexadecimal (generate one with `cms ctl gen-secret`)")
	}
	if len(b) < 16 {
		return fmt.Errorf("secret_key is %d bytes long, it must be at least 16", len(b))
	}
	return nil
}

// RequireSecretKey fails when secret_key is missing. Services never make up
// their own key: a per-process key silently breaks cookies across
// replicas and the secret scoreboard links (lesson from the previous
// project, see DECISIONS.md D5).
func (c *Config) RequireSecretKey() error {
	if c.SecretKey == "" {
		return errors.New("secret_key is not set: put the same value in every service " +
			"(cms.yaml or CMS_SECRET_KEY); generate it once with `cms ctl gen-secret`")
	}
	return nil
}

// SecretKeyBytes returns the decoded secret key.
func (c *Config) SecretKeyBytes() []byte {
	b, _ := hex.DecodeString(c.SecretKey)
	return b
}

// Location returns the configured timezone (validated by Load).
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Duration is a time.Duration written as "90s", "3h", ... in YAML. A bare
// integer is taken as seconds, the unit CMS used.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := parseDuration(n.Value)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func parseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	var secs int64
	if _, err := fmt.Sscan(s, &secs); err == nil && fmt.Sprint(secs) == s {
		return Duration(time.Duration(secs) * time.Second), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 90s, 3h)", s)
	}
	return Duration(v), nil
}

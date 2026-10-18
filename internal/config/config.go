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
	"io/fs"
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
	// LanguagesDir holds language definitions (*.yaml) added to the
	// built-in ones (config/languages); a definition with the name of a
	// built-in language replaces it. Empty means only the built-in
	// languages. Every service, judges included, must see the same files.
	LanguagesDir string `yaml:"languages_dir"`

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
	// Username and Password are the HTTP basic credentials of the writes
	// (the REST API of CMS RankingWebServer). An empty password is derived
	// from secret_key (RankingPushPassword), which the dispatcher derives
	// too: set one only for other clients (an upstream ProxyService,
	// cmsRWSHelper) or a dispatcher with another secret_key.
	Username string `yaml:"username"`
	Password string `yaml:"password" secret:"true"`
	// Visibility is who sees the scoreboard: "public" (everyone, as CMS),
	// "secret" (whoever opened the secret link of one of its contests,
	// printed by the admin ranking page, and administrators) or "admins"
	// (administrators coming from the admin site only). Administrators
	// also see the scores hidden by a freeze.
	Visibility string `yaml:"visibility"`
	// ShowIDColumn shows the username column (CMS [public]
	// show_id_column).
	ShowIDColumn bool `yaml:"show_id_column"`
}

// Dispatcher configures the service that replaces EvaluationService,
// ScoringService and ProxyService. Several dispatchers may run (on other
// machines, for failover): one is active and the others wait as hot
// standbys.
type Dispatcher struct {
	OpsListen string `yaml:"ops_listen"`
	// BlobListen serves the blob store over HTTP to judges on other
	// machines, whose storage.backend is remote with storage.remote.url
	// pointing here and storage.remote.token printed by `cms ctl
	// blob-token`. Empty disables it. It needs secret_key and the local or
	// s3 storage backend.
	BlobListen string `yaml:"blob_listen"`
	// SweepInterval is how often the dispatcher looks in the database for
	// work it was not told about (a lost notification, a crash): such work
	// waits at most this long. A sweep only reads unfinished results.
	SweepInterval Duration `yaml:"sweep_interval"`
	// MaxCompilationTries and MaxEvaluationTries bound the failed attempts
	// (failures of the judging infrastructure, never a contestant's error)
	// of a compilation, and of the evaluations, of a submission or a user
	// test (CMS MAX_*_TRIES). After them the dispatcher gives up until an
	// admin reevaluates it. The admin site and `cms ctl` read them too.
	MaxCompilationTries int `yaml:"max_compilation_tries"`
	MaxEvaluationTries  int `yaml:"max_evaluation_tries"`
	// ResultBatch is the largest number of results stored in one
	// transaction.
	ResultBatch int `yaml:"result_batch"`
	// Rankings are the public scoreboards (ranking-web or CMS
	// RankingWebServer) the active dispatcher keeps up to date (CMS
	// ProxyService and its rankings list).
	Rankings []RankingTarget `yaml:"rankings"`
	// RankingRefresh is how often the contests, tasks, teams and users are
	// read again and what changed is sent to the rankings (edits of the
	// admin site do not notify the dispatcher). Scores are sent at once.
	RankingRefresh Duration `yaml:"ranking_refresh"`
}

// RankingTarget is a scoreboard the dispatcher pushes to.
type RankingTarget struct {
	// URL is the address of the ranking as the dispatcher reaches it
	// ("http://127.0.0.1:8890/", with the path prefix of a proxy if any).
	// Credentials in the URL (CMS style) are accepted.
	URL string `yaml:"url" secret:"true"`
	// Username and Password are the basic credentials of the ranking.
	// Empty: "cms" and the password derived from secret_key, which a
	// ranking-web sharing secret_key accepts.
	Username string `yaml:"username"`
	Password string `yaml:"password" secret:"true"`
	// Contests are the names of the contests sent; empty sends every
	// contest.
	Contests []string `yaml:"contests"`
	// Freeze hides from the public the scores of submissions (and tokens)
	// made in the last Freeze of each contest: the ranking keeps them and
	// shows them to administrators, and reveals them when the freeze is
	// removed (set 0 and restart the dispatchers). 0: no freeze.
	Freeze Duration `yaml:"freeze"`
	// Prune deletes from the ranking the contests, tasks, teams and users
	// that this installation no longer sends (removed, or only hidden
	// participations), so the scoreboard mirrors the database. Nil means
	// true; set false for a ranking that several installations feed.
	Prune *bool `yaml:"prune"`
}

// PruneEnabled reports whether the ranking is pruned (Prune, default true).
func (t RankingTarget) PruneEnabled() bool { return t.Prune == nil || *t.Prune }

// Worker configures a judging host. Workers never connect to PostgreSQL:
// each job carries what it needs and files are fetched by digest.
type Worker struct {
	OpsListen string `yaml:"ops_listen"`
	// Name identifies the worker in the judges page and in the job queue;
	// empty means the hostname. Two running workers cannot have the same
	// name: give each worker of a machine its own. Letters, digits, ".",
	// "_" and "-", at most 64.
	Name string `yaml:"name"`
	// Concurrency is the number of jobs run in parallel. Use at most one
	// per physical core reserved for judging.
	Concurrency int `yaml:"concurrency"`
	// BoxIDBase is the first isolate box id used by this worker and
	// BoxCount the number of ids it may use: [BoxIDBase, BoxIDBase+BoxCount).
	// Workers on the same machine need disjoint ranges (`cms ctl
	// verify-host` checks it). BoxCount must be at least MinBoxesPerJob
	// times Concurrency, and at least the number of boxes the largest
	// job uses at the same time (Communication: one per process plus the
	// manager; Interactive: the process limit plus the controller): a job
	// that needs more fails.
	BoxIDBase int `yaml:"box_id_base"`
	BoxCount  int `yaml:"box_count"`
	// WorkDir holds sandboxes and compiled files. It is converted to an
	// absolute path at startup because isolate rejects relative --dir.
	WorkDir     string `yaml:"work_dir"`
	IsolatePath string `yaml:"isolate_path"`
	// KeepSandbox leaves the box directories on disk after each job (for
	// debugging; they are never deleted automatically).
	KeepSandbox bool `yaml:"keep_sandbox"`
	// Seccomp runs contestant programs (evaluations and user tests, never
	// compilers, checkers or managers) under a seccomp filter that kills
	// them with the "Security violation" verdict on forbidden system calls.
	Seccomp bool `yaml:"seccomp"`
	// ShutdownTimeout is how long a stopping worker lets its running jobs
	// finish; the jobs still running then go back to the queue for
	// another worker (0: at once). Keep it below the stop timeout of the
	// supervisor (Docker waits 10s by default, systemd 90s).
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
	// JobTimeout is the longest a job may take (evaluations get four
	// times their time limit on top); a job still running then fails and
	// the dispatcher retries it. It only stops jobs stuck because of a
	// fault: the sandbox enforces the real limits.
	JobTimeout Duration `yaml:"job_timeout"`
	// CompilationCacheTTL is how long the result of a successful
	// compilation is kept in Valkey: an identical compilation (same
	// language, compiler, task type, sources and graders) within that
	// time reuses the executable instead of compiling again. 0 disables
	// the cache.
	CompilationCacheTTL Duration `yaml:"compilation_cache_ttl"`
}

// Monitor configures the health/metrics aggregator (CMS Checker,
// ResourceService and LogService).
type Monitor struct {
	Listen string `yaml:"listen"`
}

// Sandbox holds the limits that are not part of a task (CMS [sandbox]).
type Sandbox struct {
	// Cgroups runs isolate with --cg (cgroups v2): memory limits cover the
	// whole process tree, memory use is measured and "Memory limit
	// exceeded" is detected. false is only for development machines
	// without cgroups v2: memory is limited per process as address space
	// (--mem) and exceeding it looks like a runtime error.
	Cgroups bool `yaml:"cgroups"`
	// MaxFileSizeKiB bounds every file a sandboxed program writes.
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
			Listen:     "127.0.0.1:8890",
			OpsListen:  "127.0.0.1:9190",
			DataDir:    "/var/lib/cms/ranking",
			Username:   "cms",
			Visibility: RankingPublic,
		},
		Dispatcher: Dispatcher{
			OpsListen:           "127.0.0.1:9191",
			SweepInterval:       Duration(time.Minute),
			MaxCompilationTries: 3,
			MaxEvaluationTries:  3,
			ResultBatch:         128,
			Rankings:            []RankingTarget{},
			RankingRefresh:      Duration(30 * time.Second),
		},
		Worker: Worker{
			OpsListen:   "127.0.0.1:9192",
			Concurrency: 1,
			BoxCount:    100,
			WorkDir:     "/var/lib/cms/worker",
			IsolatePath: "isolate",
			Seccomp:     true,

			ShutdownTimeout:     Duration(8 * time.Second),
			JobTimeout:          Duration(10 * time.Minute),
			CompilationCacheTTL: Duration(7 * 24 * time.Hour),
		},
		Monitor: Monitor{Listen: "127.0.0.1:9193"},
		Sandbox: Sandbox{
			Cgroups:        true,
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
//
// A file that exists but cannot be read (/etc/cms/cms.yaml is readable
// only by root and the cms group) is an error, never a reason to fall
// back to the defaults: a command would otherwise fail later, connecting
// to the database with the default credentials.
func Load(path string) (*Config, string, error) {
	return load(path, os.LookupEnv, os.ReadFile)
}

// statFile is os.Stat; tests replace it.
var statFile = os.Stat

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
			_, err := statFile(p)
			if err == nil {
				path = p
				break
			}
			if !errors.Is(err, fs.ErrNotExist) {
				// Typically a directory the user cannot enter.
				return nil, "", unreadable(p, err)
			}
		}
	}
	if path != "" {
		data, err := readFile(path)
		if err != nil {
			return nil, "", unreadable(path, err)
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

// unreadable is the error of a configuration file that cannot be read.
func unreadable(path string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot read %s: permission denied: run the command with sudo (sudo cmsctl ...) "+
			"or as a user of its group: %w", path, err)
	}
	return fmt.Errorf("reading config: %w", err)
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
	errs = append(errs, c.validateSandbox()...)
	errs = append(errs, c.validateDispatcher()...)
	errs = append(errs, c.validateRankings()...)
	for name, addr := range map[string]string{
		"contest_web.listen":     c.ContestWeb.Listen,
		"contest_web.ops_listen": c.ContestWeb.OpsListen,
		"admin_web.listen":       c.AdminWeb.Listen,
		"admin_web.ops_listen":   c.AdminWeb.OpsListen,
		"ranking_web.listen":     c.RankingWeb.Listen,
		"ranking_web.ops_listen": c.RankingWeb.OpsListen,
		"dispatcher.ops_listen":  c.Dispatcher.OpsListen,
		"dispatcher.blob_listen": c.Dispatcher.BlobListen,
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

// Bounds of the dispatcher settings.
const (
	MinSweepInterval = time.Second
	MaxSweepInterval = 24 * time.Hour
	MaxTries         = 100
	MaxResultBatch   = 10000
)

func (c *Config) validateDispatcher() []error {
	var errs []error
	d := &c.Dispatcher
	if d.SweepInterval.D() < MinSweepInterval || d.SweepInterval.D() > MaxSweepInterval {
		errs = append(errs, fmt.Errorf("dispatcher.sweep_interval must be between %v and %v", MinSweepInterval, MaxSweepInterval))
	}
	if d.MaxCompilationTries < 1 || d.MaxCompilationTries > MaxTries {
		errs = append(errs, fmt.Errorf("dispatcher.max_compilation_tries must be between 1 and %d", MaxTries))
	}
	if d.MaxEvaluationTries < 1 || d.MaxEvaluationTries > MaxTries {
		errs = append(errs, fmt.Errorf("dispatcher.max_evaluation_tries must be between 1 and %d", MaxTries))
	}
	if d.ResultBatch < 1 || d.ResultBatch > MaxResultBatch {
		errs = append(errs, fmt.Errorf("dispatcher.result_batch must be between 1 and %d", MaxResultBatch))
	}
	if d.BlobListen != "" && c.Storage.Backend == "remote" {
		errs = append(errs, errors.New("dispatcher.blob_listen serves the blob store of the dispatcher, "+
			"so its storage.backend must be local or s3, not remote"))
	}
	return errs
}

// MinBoxesPerJob is the number of sandbox boxes worker.box_count must
// provide for each job slot (worker.concurrency): what the common task
// types use at the same time (Batch with a checker, TwoSteps). Tasks that
// use more (Communication: one per process plus one; Interactive: the
// process limit plus one) need a larger worker.box_count, or their jobs
// fail with an explicit error.
const MinBoxesPerJob = 2

// maxBoxID bounds box ids: isolate accepts at most 65536 boxes with its
// default configuration (`cms ctl verify-host` checks the real limit).
const maxBoxID = 1 << 16

func (c *Config) validateSandbox() []error {
	var errs []error
	w := &c.Worker
	if w.BoxIDBase < 0 {
		errs = append(errs, errors.New("worker.box_id_base must not be negative"))
	}
	if w.BoxCount < 1 {
		errs = append(errs, errors.New("worker.box_count must be at least 1"))
	} else if w.BoxCount < MinBoxesPerJob*w.Concurrency {
		errs = append(errs, fmt.Errorf("worker.box_count (%d) must be at least %d times worker.concurrency (%d): "+
			"a job of a Batch task with a checker or of a TwoSteps task uses %d boxes",
			w.BoxCount, MinBoxesPerJob, w.Concurrency, MinBoxesPerJob))
	}
	if w.BoxIDBase >= 0 && w.BoxCount >= 1 && w.BoxIDBase+w.BoxCount > maxBoxID {
		errs = append(errs, fmt.Errorf("worker.box_id_base + worker.box_count must not exceed %d", maxBoxID))
	}
	if w.WorkDir == "" {
		errs = append(errs, errors.New("worker.work_dir must be set"))
	}
	if w.IsolatePath == "" {
		errs = append(errs, errors.New("worker.isolate_path must be set"))
	}
	if w.Name != "" && !ValidWorkerName(w.Name) {
		errs = append(errs, fmt.Errorf("worker.name: %q must be 1 to 64 letters, digits, \".\", \"_\" or \"-\"", w.Name))
	}
	if w.ShutdownTimeout < 0 {
		errs = append(errs, errors.New("worker.shutdown_timeout must not be negative"))
	}
	if w.JobTimeout.D() < 10*time.Second {
		errs = append(errs, errors.New("worker.job_timeout must be at least 10s"))
	}
	if w.CompilationCacheTTL < 0 {
		errs = append(errs, errors.New("worker.compilation_cache_ttl must not be negative (0 disables the cache)"))
	}
	if c.Sandbox.MaxFileSizeKiB < 1 {
		errs = append(errs, errors.New("sandbox.max_file_size_kib must be at least 1"))
	}
	return errs
}

// ValidWorkerName reports whether s can name a worker (worker.name): 1 to
// 64 ASCII letters, digits, ".", "_" or "-", not starting with "." or
// "-". Hostnames qualify.
func ValidWorkerName(s string) bool {
	if s == "" || len(s) > 64 || s[0] == '.' || s[0] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
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

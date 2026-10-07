// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Open returns the store described by cfg (storage.backend local, s3 or
// remote) for the process named name (its service: "worker",
// "contest-web"...). S3 and remote stores are wrapped in an on-disk LRU
// cache of at most storage.cache_size_mb in storage.cache_dir/<name>,
// unless cache_size_mb is 0; the local store is not, since the cache would
// only copy files within the same machine. Each service of a machine thus
// has its own cache directory, which NewCache locks: two processes with
// the same name and cache_dir fail to start rather than share it. reg (may
// be nil) receives the cache metrics. Close releases the cache directory.
func Open(cfg config.Storage, name string, reg prometheus.Registerer) (Store, error) {
	s, err := OpenBackend(cfg, name)
	if err != nil {
		return nil, err
	}
	if cfg.Backend == "local" || cfg.CacheSizeMB <= 0 {
		return s, nil
	}
	if cfg.CacheDir == "" {
		return nil, errors.New("blob: storage.cache_dir must be set when storage.cache_size_mb is not 0")
	}
	return NewCache(s, filepath.Join(cfg.CacheDir, name), int64(cfg.CacheSizeMB)<<20, reg)
}

// OpenBackend returns the store described by cfg without a cache (tools
// such as the garbage collector, named by name). Uploads to s3 and remote
// are spooled (to learn their digest) in storage.cache_dir/<name>/tmp,
// which is on disk, rather than in $TMPDIR, often a tmpfs that would hold
// uploads of up to DefaultMaxUpload in memory; with an empty cache_dir
// they go to $TMPDIR.
func OpenBackend(cfg config.Storage, name string) (Store, error) {
	if !validName(name) {
		return nil, fmt.Errorf("blob: invalid store name %q (want lowercase letters, digits and dashes)", name)
	}
	switch cfg.Backend {
	case "local":
		return NewLocal(cfg.Local.Path)
	case "s3":
		tmp, err := spoolDir(cfg, name)
		if err != nil {
			return nil, err
		}
		return NewS3(cfg.S3, WithS3TempDir(tmp))
	case "remote":
		tmp, err := spoolDir(cfg, name)
		if err != nil {
			return nil, err
		}
		return NewClient(cfg.Remote.URL, cfg.Remote.Token, WithClientTempDir(tmp))
	default:
		return nil, fmt.Errorf("blob: storage.backend %q is not local, s3 or remote", cfg.Backend)
	}
}

// spoolDir creates and returns the directory where name spools uploads:
// the tmp directory of its cache ("" means os.TempDir()).
func spoolDir(cfg config.Storage, name string) (string, error) {
	if cfg.CacheDir == "" {
		return "", nil
	}
	dir, err := filepath.Abs(filepath.Join(cfg.CacheDir, name, tmpName))
	if err != nil {
		return "", fmt.Errorf("blob: storage.cache_dir: %w", err)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("blob: storage.cache_dir: %w", err)
	}
	return dir, nil
}

// validName reports whether name can name a subdirectory of cache_dir: a
// service name such as "contest-web".
func validName(name string) bool {
	if name == "" || len(name) > 64 || name[0] == '-' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

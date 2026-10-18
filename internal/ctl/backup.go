// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/fsowner"
)

// derivedTables are computed from the other tables: backups leave them out
// and restore recomputes them (task_scores, D34).
var derivedTables = []string{"task_scores"}

// BackupOptions configures Backup.
type BackupOptions struct {
	// Output is the archive to write or, if it is an existing directory
	// or ends in "/", the directory where a timestamped archive is
	// created. Empty: <data_dir>/backups/.
	Output string
	// NoBlobs leaves out the blob store (for example a large S3 bucket
	// with its own versioning).
	NoBlobs bool
	// Keep deletes, after a successful backup, all but the newest Keep
	// archives of the output directory (0 keeps everything).
	Keep int
	// Label is appended to the generated file name ("pre-upgrade").
	Label string
}

func backupCommand() *cli.Command {
	var opts BackupOptions
	return &cli.Command{
		Name:  "backup",
		Args:  "[flags]",
		Short: "back up the database, the blob store and the configuration in one archive",
		Long: `Writes one archive (.tar.gz) with the database (every table in COPY format,
read in a single consistent snapshot), the files of the blob store and the
configuration file, plus a manifest and the SHA-256 of every entry.
Services may keep running: the database is read in one snapshot and blobs
are only ever added (do not run a blob garbage collection at the same time).

The archive contains every secret of cms.yaml: it is created readable only
by its owner; keep it as safe as the server itself.

By default it goes to <data_dir>/backups/cms-backup-<UTC time>.tar.gz; it is
written under a temporary name and renamed when complete. --keep N then
deletes all but the newest N archives of that directory (the systemd timer
of the installer uses it). Restore with "cms ctl restore".`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.Output, "output", "", "archive file, or directory for a timestamped archive (default <data_dir>/backups/)")
			fs.BoolVar(&opts.NoBlobs, "no-blobs", false, "leave out the blob store")
			fs.IntVar(&opts.Keep, "keep", 0, "keep only the newest N archives of the output directory (0: all)")
			fs.StringVar(&opts.Label, "label", "", "suffix of the generated file name")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			cfg, cfgPath, err := config.Load(env.ConfigPath)
			if err != nil {
				return err
			}
			_, err = Backup(ctx, cfg, cfgPath, opts, env.Stdout)
			return err
		},
	}
}

var labelChars = strings.NewReplacer("/", "-", " ", "-", "\\", "-")

// Backup writes a backup archive and returns its path. Progress goes to out.
func Backup(ctx context.Context, cfg *config.Config, cfgPath string, opts BackupOptions, out io.Writer) (string, error) {
	dest, err := backupPath(cfg, opts)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(dest)
	// Run with sudo, the archive (and a new backups directory) go to the
	// owner of the directory, like those of the backup timer.
	if err := fsowner.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".cms-backup-*.partial")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return "", err
	}
	start := time.Now()
	if err := writeBackup(ctx, cfg, cfgPath, opts, tmp, dir, out); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := fsowner.Of(dir).Apply(tmp.Name()); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	ok = true
	if st, err := os.Stat(dest); err == nil {
		fmt.Fprintf(out, "backup written: %s (%s, %s)\n", dest, humanBytes(st.Size()), time.Since(start).Round(time.Millisecond))
	}
	if opts.Keep > 0 {
		deleted, err := pruneBackups(dir, opts.Keep)
		if err != nil {
			return dest, fmt.Errorf("the backup is complete, but deleting old ones failed: %w", err)
		}
		for _, d := range deleted {
			fmt.Fprintf(out, "deleted old backup %s\n", d)
		}
	}
	return dest, nil
}

func backupPath(cfg *config.Config, opts BackupOptions) (string, error) {
	name := BackupPrefix + time.Now().UTC().Format("20060102T150405Z")
	if opts.Label != "" {
		name += "-" + labelChars.Replace(opts.Label)
	}
	name += BackupSuffix
	switch {
	case opts.Output == "":
		return filepath.Join(cfg.DataDir, "backups", name), nil
	case strings.HasSuffix(opts.Output, "/"):
		return filepath.Join(opts.Output, name), nil
	}
	if st, err := os.Stat(opts.Output); err == nil && st.IsDir() {
		return filepath.Join(opts.Output, name), nil
	} else if err == nil {
		return "", fmt.Errorf("%s already exists", opts.Output)
	}
	return opts.Output, nil
}

func writeBackup(ctx context.Context, cfg *config.Config, cfgPath string, opts BackupOptions, w io.Writer, spoolDir string, out io.Writer) error {
	pool, err := openDB(ctx, cfg, cfgPath)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.CheckSchema(ctx, pool); err != nil {
		return fmt.Errorf("back up with the binary that matches the database: %w", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// One snapshot for every table.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	m := backupManifest{
		Format: archiveFormat, CreatedAt: time.Now().UTC(), CMSVersion: buildinfo.String(),
		Derived: derivedTables, Blobs: !opts.NoBlobs, BlobBackend: cfg.Storage.Backend,
	}
	m.Hostname, _ = os.Hostname()
	rows, err := tx.Query(ctx, `SELECT checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	if m.Migrations, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return err
	}
	m.SchemaVersion = len(m.Migrations)
	if m.Tables, err = publicTables(ctx, tx); err != nil {
		return err
	}
	var configData []byte
	if cfgPath != "" {
		if configData, err = os.ReadFile(cfgPath); err != nil {
			return err
		}
		m.ConfigPath = cfgPath
	}
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	a := newArchiveWriter(w)
	if err := a.addBytes(manifestName, mj); err != nil {
		return err
	}

	// COPY output has no known size and tar needs it first: spool each
	// table to a temporary file next to the archive.
	for _, t := range m.Tables {
		n, err := copyTableOut(ctx, tx, t, a, spoolDir)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  table %-28s %s\n", t.Name, humanBytes(n))
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Blobs after the snapshot: everything it references exists already.
	if !opts.NoBlobs {
		store, err := blob.OpenBackend(cfg.Storage, "backup")
		if err != nil {
			return err
		}
		defer blob.Close(store)
		var count, total int64
		err = store.Walk(ctx, func(digest string, size int64) error {
			rc, err := store.Open(ctx, digest)
			if err != nil {
				return fmt.Errorf("blob %s: %w", digest, err)
			}
			defer rc.Close()
			if err := a.add(blobEntryPrefix+digest, size, rc); err != nil {
				return err
			}
			count++
			total += size
			return nil
		})
		if errors.Is(err, blob.ErrUnsupported) {
			return fmt.Errorf("storage.backend %s cannot be listed: back up on the machine of the dispatcher, or use --no-blobs", cfg.Storage.Backend)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  blobs: %d files, %s\n", count, humanBytes(total))
	}
	if configData != nil {
		if err := a.addBytes(configEntryName, configData); err != nil {
			return err
		}
	}
	return a.close()
}

// publicTables lists the ordinary tables of the public schema with their
// stored columns, leaving out schema_migrations (rebuilt by the
// migrations) and the derived tables.
func publicTables(ctx context.Context, q pgx.Tx) ([]backupTable, error) {
	rows, err := q.Query(ctx, `
SELECT c.relname::text, array_agg(a.attname::text ORDER BY a.attnum)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
WHERE n.nspname = 'public' AND c.relkind = 'r'
GROUP BY c.relname
ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	var out []backupTable
	for rows.Next() {
		var t backupTable
		if err := rows.Scan(&t.Name, &t.Columns); err != nil {
			return nil, err
		}
		if t.Name == "schema_migrations" || slices.Contains(derivedTables, t.Name) {
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func copyStatement(t backupTable, dir string) string {
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	return fmt.Sprintf("COPY %s (%s) %s", pgx.Identifier{"public", t.Name}.Sanitize(), strings.Join(cols, ", "), dir)
}

func copyTableOut(ctx context.Context, tx pgx.Tx, t backupTable, a *archiveWriter, spoolDir string) (int64, error) {
	f, err := os.CreateTemp(spoolDir, ".cms-backup-spool-*")
	if err != nil {
		return 0, err
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()
	if _, err := tx.Conn().PgConn().CopyTo(ctx, f, copyStatement(t, "TO STDOUT")); err != nil {
		return 0, fmt.Errorf("copying table %s: %w", t.Name, err)
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return size, a.add(dbEntryPrefix+t.Name+dbEntrySuffix, size, f)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// RestoreOptions configures Restore.
type RestoreOptions struct {
	// Clean drops everything in the public schema first; otherwise the
	// database must be empty.
	Clean bool
	// Force restores even if other sessions are connected to the database.
	Force bool
	// NoBlobs skips the blob store of the archive.
	NoBlobs bool
	// ConfigTo writes the archived configuration file there (it must not
	// exist).
	ConfigTo string
	// VerifyOnly checks the archive and stops.
	VerifyOnly bool
}

func restoreCommand() *cli.Command {
	var opts RestoreOptions
	return &cli.Command{
		Name:  "restore",
		Args:  "[flags] ARCHIVE",
		Short: "restore a backup made by `cms ctl backup` into an empty (or --clean) database",
		Long: `Restores an archive of "cms ctl backup".

The archive is read twice: first every checksum is verified (nothing is
touched if one fails), then it is loaded. The database is restored in ONE
transaction, so a failure leaves it as it was:

  1. with --clean, every table, function and type of the public schema is
     dropped; without it the database must be empty;
  2. the migrations of this binary rebuild the schema at the version of the
     backup (they must match the ones of the backup);
  3. the tables seeded by the migrations are emptied, user triggers are
     disabled and foreign keys dropped, and every table is loaded with COPY;
     then triggers are enabled again, foreign keys added back (which checks
     them) and identity sequences moved past the restored ids;
  4. the remaining migrations of this binary are applied (a backup of an
     older version is upgraded), and the derived tables (task_scores) are
     recomputed.

Files of the blob store are added to the configured storage (adding is
idempotent). Stop every service first (systemctl stop cms.target, or
docker compose stop): the restore refuses while other sessions use the
database unless --force. With the local storage backend run it as the user
that owns the blob directory (sudo -u cms cmsctl restore ...).

The configuration of the archive is only written with --config-to PATH.`,
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&opts.Clean, "clean", false, "drop the current contents of the database first")
			fs.BoolVar(&opts.Force, "force", false, "restore even if other sessions are connected")
			fs.BoolVar(&opts.NoBlobs, "no-blobs", false, "do not restore the blob store")
			fs.StringVar(&opts.ConfigTo, "config-to", "", "write the archived cms.yaml to this path (must not exist)")
			fs.BoolVar(&opts.VerifyOnly, "verify-only", false, "only verify the archive")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if len(args) != 1 {
				return cli.UsageError("expected one ARCHIVE")
			}
			cfg, cfgPath, err := config.Load(env.ConfigPath)
			if err != nil {
				return err
			}
			return withConfigHint(Restore(ctx, cfg, args[0], opts, env.Stdout), cfgPath)
		},
	}
}

// Restore verifies and restores an archive.
func Restore(ctx context.Context, cfg *config.Config, file string, opts RestoreOptions, out io.Writer) error {
	start := time.Now()
	m, err := verifyArchive(file)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "archive verified: %s (cms %s, schema %d, %d tables, created %s)\n",
		file, m.CMSVersion, m.SchemaVersion, len(m.Tables), m.CreatedAt.Format(time.RFC3339))
	if opts.VerifyOnly {
		return nil
	}
	all, err := db.Migrations()
	if err != nil {
		return err
	}
	if m.SchemaVersion > len(all) {
		return fmt.Errorf("the backup has schema version %d but this binary only knows %d: restore it with a newer cms", m.SchemaVersion, len(all))
	}
	for i, sum := range m.Migrations {
		if all[i].Checksum != sum {
			return fmt.Errorf("migration %04d_%s of this binary differs from the one of the backup: restore it with the version that made it (%s)", all[i].Version, all[i].Name, m.CMSVersion)
		}
	}
	if opts.ConfigTo != "" {
		if _, err := os.Stat(opts.ConfigTo); err == nil {
			return fmt.Errorf("--config-to %s already exists", opts.ConfigTo)
		}
	}

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	var store blob.Store
	if m.Blobs && !opts.NoBlobs {
		if store, err = blob.OpenBackend(cfg.Storage, "restore"); err != nil {
			return err
		}
		defer blob.Close(store)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if !opts.Force {
		var others int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'`).Scan(&others); err != nil {
			return err
		}
		if others > 0 {
			return fmt.Errorf("%d other sessions are connected to the database: stop the services first (or --force)", others)
		}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	r := &restorer{tx: tx, m: m, out: out}
	if err := r.prepare(ctx, opts.Clean); err != nil {
		return err
	}
	var blobs int
	err = readArchive(file, func(e archiveEntry) error {
		switch {
		case e.Name == manifestName || e.Name == sumsName:
			return nil
		case strings.HasPrefix(e.Name, dbEntryPrefix):
			return r.load(ctx, e)
		}
		if err := r.finish(ctx); err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(e.Name, blobEntryPrefix):
			if store == nil {
				return nil
			}
			want := strings.TrimPrefix(e.Name, blobEntryPrefix)
			got, _, err := store.Put(ctx, e.R)
			if err != nil {
				return fmt.Errorf("blob %s: %w", want, err)
			}
			if got != want {
				return fmt.Errorf("blob %s: stored as %s", want, got)
			}
			blobs++
		case e.Name == configEntryName && opts.ConfigTo != "":
			data, err := readAllLimited(e.R, 16<<20)
			if err != nil {
				return err
			}
			if err := os.WriteFile(opts.ConfigTo, data, 0o640); err != nil {
				return err
			}
			fmt.Fprintf(out, "configuration written to %s\n", opts.ConfigTo)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.finish(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if store != nil {
		fmt.Fprintf(out, "  blobs: %d files\n", blobs)
	}
	fmt.Fprintf(out, "restore complete (%s)\n", time.Since(start).Round(time.Millisecond))
	return nil
}

type foreignKey struct{ table, name, def string }

// restorer loads the database part of an archive inside one transaction.
type restorer struct {
	tx       pgx.Tx
	m        *backupManifest
	out      io.Writer
	fks      []foreignKey
	loaded   map[string]bool
	finished bool
}

// prepare empties the database (with clean), rebuilds the schema of the
// backup and gets the tables ready for COPY.
func (r *restorer) prepare(ctx context.Context, clean bool) error {
	tx := r.tx
	if clean {
		if err := dropPublicSchema(ctx, tx); err != nil {
			return fmt.Errorf("--clean: %w", err)
		}
	} else {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'S')`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errors.New("the database is not empty: restore into a new database, or use --clean to replace its contents")
		}
	}
	if _, err := db.MigrateTx(ctx, tx, r.m.SchemaVersion); err != nil {
		return err
	}
	// The schema must be exactly the one of the backup.
	have, err := publicTables(ctx, tx)
	if err != nil {
		return err
	}
	byName := map[string][]string{}
	for _, t := range have {
		byName[t.Name] = t.Columns
	}
	var names []string
	for _, t := range r.m.Tables {
		cols, ok := byName[t.Name]
		if !ok {
			return fmt.Errorf("table %s of the backup does not exist at schema version %d", t.Name, r.m.SchemaVersion)
		}
		if !slices.Equal(cols, t.Columns) {
			return fmt.Errorf("table %s: the columns differ from the backup", t.Name)
		}
		names = append(names, pgx.Identifier{"public", t.Name}.Sanitize())
	}
	if len(names) > 0 {
		// Rows seeded by the migrations are replaced by the backup's.
		if _, err := tx.Exec(ctx, `TRUNCATE `+strings.Join(names, ", ")+` CASCADE`); err != nil {
			return err
		}
	}
	// Foreign keys are dropped during the load (the tables have cycles,
	// e.g. tasks and datasets) and added back afterwards, which checks
	// every row; user triggers are disabled.
	rows, err := tx.Query(ctx, `
SELECT c.conrelid::regclass::text, c.conname::text, pg_get_constraintdef(c.oid)
FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
WHERE c.contype = 'f' AND n.nspname = 'public'
ORDER BY 1, 2`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var fk foreignKey
		if err := rows.Scan(&fk.table, &fk.name, &fk.def); err != nil {
			return err
		}
		r.fks = append(r.fks, fk)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, fk := range r.fks {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT %s`, fk.table, pgx.Identifier{fk.name}.Sanitize())); err != nil {
			return err
		}
	}
	for _, n := range names {
		if _, err := tx.Exec(ctx, `ALTER TABLE `+n+` DISABLE TRIGGER USER`); err != nil {
			return err
		}
	}
	r.loaded = map[string]bool{}
	return nil
}

func (r *restorer) load(ctx context.Context, e archiveEntry) error {
	if r.finished {
		return fmt.Errorf("%s comes after the end of the database part", e.Name)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(e.Name, dbEntryPrefix), dbEntrySuffix)
	i := slices.IndexFunc(r.m.Tables, func(t backupTable) bool { return t.Name == name })
	if i < 0 || r.loaded[name] {
		return fmt.Errorf("unexpected entry %s", e.Name)
	}
	t := r.m.Tables[i]
	res, err := r.tx.Conn().PgConn().CopyFrom(ctx, e.R, copyStatement(t, "FROM STDIN"))
	if err != nil {
		return fmt.Errorf("loading table %s: %w", name, err)
	}
	r.loaded[name] = true
	fmt.Fprintf(r.out, "  table %-28s %d rows\n", name, res.RowsAffected())
	return nil
}

// finish ends the database part: triggers, foreign keys, sequences, the
// remaining migrations and the derived tables.
func (r *restorer) finish(ctx context.Context) error {
	if r.finished {
		return nil
	}
	r.finished = true
	tx := r.tx
	for _, t := range r.m.Tables {
		if !r.loaded[t.Name] {
			return fmt.Errorf("the archive has no data for table %s", t.Name)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE `+pgx.Identifier{"public", t.Name}.Sanitize()+` ENABLE TRIGGER USER`); err != nil {
			return err
		}
	}
	for _, fk := range r.fks {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ADD CONSTRAINT %s %s`, fk.table, pgx.Identifier{fk.name}.Sanitize(), fk.def)); err != nil {
			return fmt.Errorf("foreign key %s of %s: %w", fk.name, fk.table, err)
		}
	}
	if err := resetSequences(ctx, tx); err != nil {
		return err
	}
	applied, err := db.MigrateTx(ctx, tx, 0)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		fmt.Fprintf(r.out, "  applied %d newer migration(s)\n", len(applied))
	}
	if err := resetSequences(ctx, tx); err != nil {
		return err
	}
	// Derived tables: the task score cache, from scratch.
	rows, err := tx.Query(ctx, `SELECT id FROM tasks ORDER BY id`)
	if err != nil {
		return err
	}
	tasks, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		return err
	}
	for _, id := range tasks {
		if _, err := judging.RecomputeTask(ctx, tx, id); err != nil {
			return fmt.Errorf("recomputing the scores of task %d: %w", id, err)
		}
	}
	fmt.Fprintf(r.out, "  task scores recomputed for %d tasks\n", len(tasks))
	return nil
}

// resetSequences moves every identity or serial sequence of the public
// schema past the largest id of its column.
func resetSequences(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `
SELECT c.relname::text, a.attname::text, pg_get_serial_sequence(quote_ident(c.relname), a.attname)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE n.nspname = 'public' AND c.relkind = 'r'
  AND pg_get_serial_sequence(quote_ident(c.relname), a.attname) IS NOT NULL`)
	if err != nil {
		return err
	}
	type seq struct{ table, column, seq string }
	var seqs []seq
	for rows.Next() {
		var s seq
		if err := rows.Scan(&s.table, &s.column, &s.seq); err != nil {
			return err
		}
		seqs = append(seqs, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range seqs {
		_, err := tx.Exec(ctx, fmt.Sprintf(`SELECT setval($1::regclass, COALESCE(max(%s), 0) + 1, false) FROM %s`,
			pgx.Identifier{s.column}.Sanitize(), pgx.Identifier{"public", s.table}.Sanitize()), s.seq)
		if err != nil {
			return fmt.Errorf("sequence of %s.%s: %w", s.table, s.column, err)
		}
	}
	return nil
}

// dropPublicSchema drops the tables, views, sequences, functions and types
// of the public schema (not the schema itself, which the cms role may not
// own, nor objects of extensions).
func dropPublicSchema(ctx context.Context, tx pgx.Tx) error {
	stmts := []string{
		`SELECT 'DROP VIEW IF EXISTS ' || string_agg(format('%I.%I', n.nspname, c.relname), ', ') || ' CASCADE'
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind IN ('v', 'm')
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')`,
		`SELECT 'DROP TABLE IF EXISTS ' || string_agg(format('%I.%I', n.nspname, c.relname), ', ') || ' CASCADE'
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')`,
		`SELECT 'DROP SEQUENCE IF EXISTS ' || string_agg(format('%I.%I', n.nspname, c.relname), ', ') || ' CASCADE'
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind = 'S'
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype IN ('e', 'a', 'i'))`,
		`SELECT 'DROP ROUTINE IF EXISTS ' || string_agg(p.oid::regprocedure::text, ', ') || ' CASCADE'
		 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'public'
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`,
		`SELECT 'DROP TYPE IF EXISTS ' || string_agg(format('%I.%I', n.nspname, t.typname), ', ') || ' CASCADE'
		 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		 WHERE n.nspname = 'public' AND t.typtype IN ('e', 'd', 'c', 'r')
		   AND (t.typrelid = 0 OR (SELECT c.relkind FROM pg_class c WHERE c.oid = t.typrelid) = 'c')
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')`,
	}
	for _, q := range stmts {
		var drop *string
		if err := tx.QueryRow(ctx, q).Scan(&drop); err != nil {
			return err
		}
		if drop == nil {
			continue
		}
		if _, err := tx.Exec(ctx, *drop); err != nil {
			return err
		}
	}
	return nil
}

// RestoreForUpgrade is the rollback of `cms ctl upgrade`: it replaces the
// database with the backup taken before the upgrade (blobs are kept: they
// are only ever added).
func RestoreForUpgrade(ctx context.Context, cfg *config.Config, file string, out io.Writer) error {
	return Restore(ctx, cfg, file, RestoreOptions{Clean: true, Force: true, NoBlobs: true}, out)
}

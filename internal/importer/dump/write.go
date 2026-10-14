// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// The rows are written in one transaction, table by table with COPY, so
// the number of statements does not depend on the size of the dump. Dumps
// carry no database ids: the ids of the rows other rows point to are
// taken from the identity sequences first (one statement per table, in
// the natural order of the rows: contests in the order of the dump,
// submissions by time...), so on an empty database they follow the order
// in which the original installation created them; the other rows get
// theirs from the sequences as they are copied.

// ConflictError is returned when the database already has contests,
// tasks, users or teams with the names of the dump: nothing was written.
type ConflictError struct {
	Contests, Tasks, Users, Teams []string
}

func (e *ConflictError) Error() string {
	var parts []string
	add := func(what string, names []string) {
		if len(names) == 0 {
			return
		}
		if len(names) > 1 {
			what += "s"
		}
		shown, more := names, ""
		if len(shown) > 10 {
			shown, more = shown[:10], fmt.Sprintf(" and %d more", len(names)-10)
		}
		parts = append(parts, fmt.Sprintf("%s %s%s", what, strings.Join(shown, ", "), more))
	}
	add("contest", e.Contests)
	add("task", e.Tasks)
	add("user", e.Users)
	add("team", e.Teams)
	hint := "--drop replaces the contest data of the database with the dump"
	if len(e.Contests) == 0 && len(e.Tasks) == 0 {
		hint = "--reuse-users uses the existing users and teams; " + hint
	}
	return fmt.Sprintf("the database already has %s (%s)", strings.Join(parts, "; "), hint)
}

// errRollback rolls back the transaction of a dry run.
var errRollback = errors.New("dump: dry run")

// dbWriter writes a dump in a transaction.
type dbWriter struct {
	ctx  context.Context
	tx   pgx.Tx
	d    *Dump
	h    *hashes
	opts *Options
	// dryRun: the transaction is rolled back.
	dryRun bool
	// real are the tables of the database hidden by the temporary tables
	// of a dry run with Drop, by name (shadow).
	real map[string]string
	// ids are the database ids of the objects, by key index (0: none).
	ids   []int32
	order []int32 // rank of each key index in the order of the dump
	res   *importer.Result
}

func (w *dbWriter) id(i int32) any {
	if i == none {
		return nil
	}
	return w.ids[i]
}

// write runs the whole import in tx (it may run again on a serialization
// failure: it starts from scratch).
func (w *dbWriter) write() error {
	w.ids = make([]int32, len(w.d.keys))
	w.real = nil
	w.res.Changes = nil
	w.res.Events = nil
	w.res.TaskIDs = map[string]int32{}
	w.res.ContestID = 0
	for _, a := range w.d.admins {
		a.existing = 0
	}
	for _, u := range w.d.users {
		u.existing = 0
	}
	for _, t := range w.d.teams {
		t.existing = 0
	}
	if w.opts.Drop {
		if err := w.drop(); err != nil {
			return err
		}
	}
	if err := w.existing(); err != nil {
		return err
	}
	if err := w.allocate(); err != nil {
		return err
	}
	steps := []func() error{
		w.copyAdmins, w.copyContests, w.copyGroups, w.copyUsers, w.copyTeams, w.copyParticipations,
		w.copyCommunication, w.copyTasks, w.copyTaskFiles, w.copyDatasets, w.copyTestcases,
		w.copySubmissions, w.copyResults, w.copyUserTests, w.links, w.taskScores,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	w.changes()
	return nil
}

// dropped are the tables drop empties: the contest data of the database.
var dropped = []string{"contests", "groups", "announcements", "users", "teams", "participations", "messages",
	"questions", "tasks", "statements", "attachments", "datasets", "managers", "testcases", "submissions", "files",
	"tokens", "submission_results", "executables", "evaluations", "user_tests", "user_test_files",
	"user_test_managers", "user_test_results", "user_test_executables", "task_scores"}

// drop deletes the contest data of the database: everything but the
// admins, the server settings and the audit log (whose rows lose their
// contest), as cmsDumpImporter -d drops the database. The sequences go
// on: ids are not reused. A dry run hides the tables instead (shadow).
func (w *dbWriter) drop() error {
	var contests, tasks, users, teams, submissions int64
	err := w.tx.QueryRow(w.ctx, `SELECT (SELECT count(*) FROM contests), (SELECT count(*) FROM tasks),
		(SELECT count(*) FROM users), (SELECT count(*) FROM teams), (SELECT count(*) FROM submissions)`).
		Scan(&contests, &tasks, &users, &teams, &submissions)
	if err != nil {
		return err
	}
	w.res.Changes = append(w.res.Changes, importer.Change{Action: importer.Deleted, Kind: "database", Name: "contest data",
		Details: fmt.Sprintf("%s, %s, %s, %s, %s", plural(int(contests), "contest"), plural(int(tasks), "task"),
			plural(int(users), "user"), plural(int(teams), "team"), plural(int(submissions), "submission"))})
	if w.dryRun {
		return w.shadow()
	}
	// TRUNCATE is immediate; CASCADE also empties any table that points to
	// these. Groups are deleted with the contests (the contests point to
	// their main group, and the audit log to the contests).
	if _, err := w.tx.Exec(w.ctx, `TRUNCATE announcements, users, teams, participations, messages, questions,
		tasks, statements, attachments, datasets, managers, testcases, submissions, files, tokens,
		submission_results, executables, evaluations, user_tests, user_test_files, user_test_managers,
		user_test_results, user_test_executables, task_scores CASCADE`); err != nil {
		return fmt.Errorf("emptying the database: %w", err)
	}
	if _, err := w.tx.Exec(w.ctx, `DELETE FROM contests`); err != nil {
		return fmt.Errorf("emptying the database: %w", err)
	}
	return nil
}

// shadow is the drop of a dry run. TRUNCATE takes ACCESS EXCLUSIVE locks:
// it would wait for every transaction that uses the tables and, until the
// dry run ends, block every reader of the installation (the contestants'
// pages). Instead each table drop empties is hidden behind an empty
// temporary table of the same name and shape (columns, defaults,
// identities, CHECK and unique constraints; not the foreign keys, which the
// checks of the dump already cover): the temporary schema comes first in
// the search path, so the import writes there, finds no names to conflict
// with (but the admins, which are kept) and leaves the tables of the
// installation alone. The temporary tables go with the transaction, which
// runs on a connection of its own (isolated).
func (w *dbWriter) shadow() error {
	rows, err := w.tx.Query(w.ctx, `SELECT t, format('%I.%I', n.nspname, c.relname)
		FROM unnest($1::text[]) AS t JOIN pg_class c ON c.oid = to_regclass(t) JOIN pg_namespace n ON n.oid = c.relnamespace`,
		dropped)
	if err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	w.real = map[string]string{}
	for rows.Next() {
		var t, real string
		if err := rows.Scan(&t, &real); err != nil {
			rows.Close()
			return fmt.Errorf("dry run: %w", err)
		}
		w.real[t] = real
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	var sql strings.Builder
	for _, t := range dropped {
		real, ok := w.real[t]
		if !ok {
			return fmt.Errorf("dry run: the database has no table %s", t)
		}
		fmt.Fprintf(&sql, "CREATE TEMPORARY TABLE %s (LIKE %s INCLUDING ALL) ON COMMIT DROP;\n", pgx.Identifier{t}.Sanitize(), real)
	}
	if _, err := w.tx.Exec(w.ctx, sql.String()); err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	return nil
}

// existing looks for the names of the dump in the database: contests and
// tasks must be new; users and teams too, unless ReuseUsers; admins are
// matched by username.
func (w *dbWriter) existing() error {
	names := func(n int, name func(int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = name(i)
		}
		return out
	}
	b := &pgx.Batch{}
	b.Queue(`SELECT 0, name FROM contests WHERE name = ANY($1::text[]) ORDER BY name`,
		names(len(w.d.contests), func(i int) string { return w.d.contests[i].name }))
	b.Queue(`SELECT 0, name FROM tasks WHERE name = ANY($1::text[]) ORDER BY name`,
		names(len(w.d.tasks), func(i int) string { return w.d.tasks[i].name }))
	b.Queue(`SELECT id, username FROM users WHERE username = ANY($1::text[]) ORDER BY username`,
		names(len(w.d.users), func(i int) string { return w.d.users[i].username }))
	b.Queue(`SELECT id, code FROM teams WHERE code = ANY($1::text[]) ORDER BY code`,
		names(len(w.d.teams), func(i int) string { return w.d.teams[i].code }))
	b.Queue(`SELECT id, username FROM admins WHERE username = ANY($1::text[]) ORDER BY username`,
		names(len(w.d.admins), func(i int) string { return w.d.admins[i].username }))
	br := w.tx.SendBatch(w.ctx, b)
	defer br.Close()
	type found struct {
		id   int32
		name string
	}
	read := func() ([]found, error) {
		rows, err := br.Query()
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (found, error) {
			var f found
			err := r.Scan(&f.id, &f.name)
			return f, err
		})
	}
	var lists [5][]found
	for i := range lists {
		var err error
		if lists[i], err = read(); err != nil {
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	conflict := &ConflictError{}
	for _, f := range lists[0] {
		conflict.Contests = append(conflict.Contests, f.name)
	}
	for _, f := range lists[1] {
		conflict.Tasks = append(conflict.Tasks, f.name)
	}
	byName := func(l []found) map[string]int32 {
		m := make(map[string]int32, len(l))
		for _, f := range l {
			m[f.name] = f.id
		}
		return m
	}
	users, teams, admins := byName(lists[2]), byName(lists[3]), byName(lists[4])
	for _, u := range w.d.users {
		if id, ok := users[u.username]; ok {
			if !w.opts.ReuseUsers {
				conflict.Users = append(conflict.Users, u.username)
			}
			u.existing = id
		}
	}
	for _, t := range w.d.teams {
		if id, ok := teams[t.code]; ok {
			if !w.opts.ReuseUsers {
				conflict.Teams = append(conflict.Teams, t.code)
			}
			t.existing = id
		}
	}
	for _, a := range w.d.admins {
		a.existing = admins[a.username]
	}
	if len(conflict.Contests)+len(conflict.Tasks)+len(conflict.Users)+len(conflict.Teams) > 0 {
		slices.Sort(conflict.Users)
		slices.Sort(conflict.Teams)
		return conflict
	}
	return nil
}

// rank orders key indexes as the keys of the dump.
func (w *dbWriter) rankKeys() {
	idx := make([]int32, len(w.d.keys))
	nums := make([]uint64, len(w.d.keys))
	isNum := make([]bool, len(w.d.keys))
	for i, k := range w.d.keys {
		idx[i] = int32(i)
		n, err := strconv.ParseUint(k, 10, 63)
		nums[i], isNum[i] = n, err == nil
	}
	slices.SortFunc(idx, func(a, b int32) int {
		switch {
		case isNum[a] && isNum[b]:
			return cmp.Compare(nums[a], nums[b])
		case isNum[a]:
			return -1
		case isNum[b]:
			return 1
		}
		return strings.Compare(w.d.keys[a], w.d.keys[b])
	})
	w.order = make([]int32, len(idx))
	for r, i := range idx {
		w.order[i] = int32(r)
	}
}

// byKey compares two key indexes in the order of the dump.
func (w *dbWriter) byKey(a, b int32) int { return cmp.Compare(w.order[a], w.order[b]) }

// allocate takes the ids of the rows that other rows point to from the
// identity sequences, in one round trip, and gives them out in the
// natural order of the rows.
func (w *dbWriter) allocate() error {
	w.rankKeys()
	rootRank := map[string]int{}
	for i, k := range w.d.roots {
		if _, ok := rootRank[k]; !ok {
			rootRank[k] = i
		}
	}
	sortRows(w.d.contests, func(a, b *contest) int {
		ra, oka := rootRank[w.d.keys[a.key]]
		rb, okb := rootRank[w.d.keys[b.key]]
		switch {
		case oka && okb && ra != rb:
			return cmp.Compare(ra, rb)
		case oka != okb:
			if oka {
				return -1
			}
			return 1
		}
		return w.byKey(a.key, b.key)
	})
	// Rows sorted by their parents are sorted once the parents have ids:
	// ids are given in the order of these lists.
	type table struct {
		name string
		keys []int32
	}
	var tables []table
	add := func(name string, keys []int32) {
		if len(keys) > 0 {
			tables = append(tables, table{name, keys})
		}
	}
	var keys []int32
	for _, a := range w.d.admins {
		if a.existing == 0 {
			keys = append(keys, a.key)
		}
	}
	add("admins", keys)
	add("contests", keysOf(w.d.contests, func(c *contest) int32 { return c.key }))
	// Groups and tasks follow their contest (in the order above).
	contestRank := map[int32]int{}
	for i, c := range w.d.contests {
		contestRank[c.key] = i
	}
	sortRows(w.d.groups, func(a, b *group) int {
		return cmp.Or(cmp.Compare(contestRank[a.contest], contestRank[b.contest]), w.byKey(a.key, b.key))
	})
	add("groups", keysOf(w.d.groups, func(g *group) int32 { return g.key }))
	sortRows(w.d.users, func(a, b *user) int { return w.byKey(a.key, b.key) })
	keys = nil
	for _, u := range w.d.users {
		if u.existing == 0 {
			keys = append(keys, u.key)
		}
	}
	add("users", keys)
	sortRows(w.d.teams, func(a, b *team) int { return w.byKey(a.key, b.key) })
	keys = nil
	for _, t := range w.d.teams {
		if t.existing == 0 {
			keys = append(keys, t.key)
		}
	}
	add("teams", keys)
	sortRows(w.d.participations, func(a, b *participation) int {
		return cmp.Or(cmp.Compare(contestRank[a.contest], contestRank[b.contest]), w.byKey(a.key, b.key))
	})
	add("participations", keysOf(w.d.participations, func(p *participation) int32 { return p.key }))
	taskRank := func(t *task) int {
		if t.contest == none {
			return len(w.d.contests)
		}
		return contestRank[t.contest]
	}
	sortRows(w.d.tasks, func(a, b *task) int {
		return cmp.Or(cmp.Compare(taskRank(a), taskRank(b)), cmpNum(a.num, b.num), w.byKey(a.key, b.key))
	})
	add("tasks", keysOf(w.d.tasks, func(t *task) int32 { return t.key }))
	tRank := map[int32]int{}
	for i, t := range w.d.tasks {
		tRank[t.key] = i
	}
	sortRows(w.d.datasets, func(a, b *dataset) int {
		return cmp.Or(cmp.Compare(tRank[a.task], tRank[b.task]), w.byKey(a.key, b.key))
	})
	add("datasets", keysOf(w.d.datasets, func(x *dataset) int32 { return x.key }))
	dRank := map[int32]int{}
	for i, x := range w.d.datasets {
		dRank[x.key] = i
	}
	sortRows(w.d.testcases, func(a, b *testcase) int {
		return cmp.Or(cmp.Compare(dRank[a.dataset], dRank[b.dataset]), strings.Compare(a.codename, b.codename))
	})
	add("testcases", keysOf(w.d.testcases, func(t *testcase) int32 { return t.key }))
	sortRows(w.d.submissions, func(a, b *submission) int {
		return cmp.Or(a.timestamp.Compare(b.timestamp), w.byKey(a.key, b.key))
	})
	add("submissions", keysOf(w.d.submissions, func(s *submission) int32 { return s.key }))
	sortRows(w.d.userTests, func(a, b *userTest) int {
		return cmp.Or(a.timestamp.Compare(b.timestamp), w.byKey(a.key, b.key))
	})
	add("user_tests", keysOf(w.d.userTests, func(u *userTest) int32 { return u.key }))
	if len(tables) == 0 {
		return nil
	}

	b := &pgx.Batch{}
	for _, t := range tables {
		if w.dryRun {
			// nextval cannot be rolled back: a dry run must not move the
			// sequences (the real import would not start where it says),
			// so it predicts what nextval would give (the identities
			// increase by 1). With --drop the sequences go on: those of
			// the tables of the database, not of their shadows.
			table := t.name
			if real, ok := w.real[t.name]; ok {
				table = real
			}
			b.Queue(`SELECT (coalesce(pg_sequence_last_value(pg_get_serial_sequence($1, 'id')::regclass), 0) + g)::integer
				FROM generate_series(1, $2::integer) AS g`, table, len(t.keys))
			continue
		}
		b.Queue(`SELECT nextval(pg_get_serial_sequence($1, 'id'))::integer FROM generate_series(1, $2::integer)`,
			t.name, len(t.keys))
	}
	br := w.tx.SendBatch(w.ctx, b)
	defer br.Close()
	for _, t := range tables {
		rows, err := br.Query()
		if err != nil {
			return fmt.Errorf("allocating the ids of %s: %w", t.name, err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int32])
		if err != nil {
			return fmt.Errorf("allocating the ids of %s: %w", t.name, err)
		}
		if len(ids) != len(t.keys) {
			return fmt.Errorf("allocating the ids of %s: got %d ids for %d rows", t.name, len(ids), len(t.keys))
		}
		slices.Sort(ids)
		for i, k := range t.keys {
			w.ids[k] = ids[i]
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	for _, a := range w.d.admins {
		if a.existing != 0 {
			w.ids[a.key] = a.existing
		}
	}
	for _, u := range w.d.users {
		if u.existing != 0 {
			w.ids[u.key] = u.existing
		}
	}
	for _, t := range w.d.teams {
		if t.existing != 0 {
			w.ids[t.key] = t.existing
		}
	}
	return nil
}

func sortRows[T any](rows []*T, cmp func(a, b *T) int) { slices.SortStableFunc(rows, cmp) }

func keysOf[T any](rows []*T, key func(*T) int32) []int32 {
	out := make([]int32, len(rows))
	for i, r := range rows {
		out[i] = key(r)
	}
	return out
}

// copyRows copies n rows into table.
func (w *dbWriter) copyRows(table string, cols []string, n int, row func(i int) []any) error {
	if n == 0 {
		return nil
	}
	_, err := w.tx.CopyFrom(w.ctx, pgx.Identifier{table}, cols, pgx.CopyFromSlice(n, func(i int) ([]any, error) {
		return row(i), nil
	}))
	if err != nil {
		return fmt.Errorf("writing %s: %w", table, explain(err))
	}
	return nil
}

// explain makes a constraint violation readable.
func explain(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	msg := pg.Message
	if pg.Detail != "" {
		msg += " (" + pg.Detail + ")"
	}
	return fmt.Errorf("the database rejected the dump: %s", msg)
}

// Helpers for nullable columns: a nil interface is NULL.

func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

func intervalp(d *time.Duration) any {
	if d == nil {
		return nil
	}
	return interval(*d)
}

func strp(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func timep(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func int32p(n *int32) any {
	if n == nil {
		return nil
	}
	return *n
}

func int64p(n *int64) any {
	if n == nil {
		return nil
	}
	return *n
}

func floatp(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func strings0(l []string) any {
	if l == nil {
		return nil
	}
	return l
}

func jsonp(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

func (w *dbWriter) digest(sha1 string) string { return w.d.digestOf(w.h, sha1) }

// digests rewrites a list of digests of archived sandboxes, dropping
// those whose file is not in the dump.
func (w *dbWriter) digests(l []string) any {
	if l == nil {
		return nil
	}
	out := make([]string, 0, len(l))
	for _, s := range l {
		if n := w.digest(s); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (w *dbWriter) copyAdmins() error {
	var rows []*admin
	for _, a := range w.d.admins {
		if a.existing == 0 {
			rows = append(rows, a)
		}
	}
	sortRows(rows, func(a, b *admin) int { return cmp.Compare(w.ids[a.key], w.ids[b.key]) })
	// Admins come from a dump only as authors of announcements and answers:
	// they are created disabled, to be enabled by an administrator.
	return w.copyRows("admins", []string{"id", "name", "username", "authentication", "enabled",
		"permission_all", "permission_messaging"}, len(rows), func(i int) []any {
		a := rows[i]
		return []any{w.ids[a.key], a.name, a.username, a.auth, false, a.permissionAll, a.permissionMessaging}
	})
}

func tokenColumns(prefix string) []string {
	return []string{prefix + "mode", prefix + "max_number", prefix + "min_interval", prefix + "gen_initial",
		prefix + "gen_number", prefix + "gen_interval", prefix + "gen_max"}
}

func tokenValues(t tokens) []any {
	return []any{t.mode, int32p(t.maxNumber), interval(t.minInterval), t.genInitial, t.genNumber,
		interval(t.genInterval), int32p(t.genMax)}
}

func (w *dbWriter) copyContests() error {
	cols := append([]string{"id", "name", "description", "allowed_localizations", "languages",
		"submissions_download_allowed", "allow_questions", "allow_user_tests",
		"allow_unofficial_submission_before_analysis_mode", "block_hidden_participations",
		"allow_password_authentication", "allow_registration", "ip_restriction", "ip_autologin"},
		tokenColumns("token_")...)
	cols = append(cols, "timezone", "max_submission_number", "max_user_test_number", "min_submission_interval",
		"min_submission_interval_grace_period", "min_user_test_interval", "score_precision")
	return w.copyRows("contests", cols, len(w.d.contests), func(i int) []any {
		c := w.d.contests[i]
		row := []any{w.ids[c.key], c.name, c.description, c.allowedLocalizations, c.languages, c.downloadAllowed,
			c.allowQuestions, c.allowUserTests, c.allowUnofficial, c.blockHidden, c.allowPassword,
			c.allowRegistration, c.ipRestriction, c.ipAutologin}
		row = append(row, tokenValues(c.tokens)...)
		return append(row, strp(c.timezone), int32p(c.maxSubmissionNumber), int32p(c.maxUserTestNumber),
			intervalp(c.minSubmissionInterval), intervalp(c.minSubmissionIntervalGrace),
			intervalp(c.minUserTestInterval), c.scorePrecision)
	})
}

func (w *dbWriter) copyGroups() error {
	return w.copyRows("groups", []string{"id", "name", "start", "stop", "analysis_enabled", "analysis_start",
		"analysis_stop", "per_user_time", "contest_id"}, len(w.d.groups), func(i int) []any {
		g := w.d.groups[i]
		return []any{w.ids[g.key], g.name, g.start, g.stop, g.analysisEnabled, g.analysisStart, g.analysisStop,
			intervalp(g.perUserTime), w.ids[g.contest]}
	})
}

func (w *dbWriter) copyUsers() error {
	var rows []*user
	for _, u := range w.d.users {
		if u.existing == 0 {
			rows = append(rows, u)
		}
	}
	return w.copyRows("users", []string{"id", "first_name", "last_name", "username", "password", "email",
		"timezone", "preferred_languages"}, len(rows), func(i int) []any {
		u := rows[i]
		return []any{w.ids[u.key], u.firstName, u.lastName, u.username, u.passwd, strp(u.email),
			strp(u.timezone), u.preferredLanguages}
	})
}

func (w *dbWriter) copyTeams() error {
	var rows []*team
	for _, t := range w.d.teams {
		if t.existing == 0 {
			rows = append(rows, t)
		}
	}
	return w.copyRows("teams", []string{"id", "code", "name"}, len(rows), func(i int) []any {
		t := rows[i]
		return []any{w.ids[t.key], t.code, t.name}
	})
}

func (w *dbWriter) copyParticipations() error {
	return w.copyRows("participations", []string{"id", "ip", "starting_time", "delay_time", "extra_time",
		"password", "hidden", "unrestricted", "contest_id", "user_id", "group_id", "team_id"},
		len(w.d.participations), func(i int) []any {
			p := w.d.participations[i]
			var ip any
			if p.ip != nil {
				ip = p.ip
			}
			return []any{w.ids[p.key], ip, timep(p.startingTime), interval(p.delayTime), interval(p.extraTime),
				strp(p.password), p.hidden, p.unrestricted, w.ids[p.contest], w.ids[p.user], w.ids[p.group],
				w.id(p.team)}
		})
}

// copyCommunication writes announcements, messages and questions.
func (w *dbWriter) copyCommunication() error {
	sortRows(w.d.announcements, func(a, b *announcement) int {
		return cmp.Or(a.timestamp.Compare(b.timestamp), w.byKey(a.key, b.key))
	})
	if err := w.copyRows("announcements", []string{"timestamp", "subject", "text", "contest_id", "admin_id"},
		len(w.d.announcements), func(i int) []any {
			a := w.d.announcements[i]
			return []any{a.timestamp, a.subject, a.text, w.ids[a.contest], w.id(a.admin)}
		}); err != nil {
		return err
	}
	sortRows(w.d.messages, func(a, b *message) int {
		return cmp.Or(a.timestamp.Compare(b.timestamp), w.byKey(a.key, b.key))
	})
	if err := w.copyRows("messages", []string{"timestamp", "subject", "text", "participation_id", "admin_id"},
		len(w.d.messages), func(i int) []any {
			m := w.d.messages[i]
			return []any{m.timestamp, m.subject, m.text, w.ids[m.participation], w.id(m.admin)}
		}); err != nil {
		return err
	}
	sortRows(w.d.questions, func(a, b *question) int {
		return cmp.Or(a.questionTimestamp.Compare(b.questionTimestamp), w.byKey(a.key, b.key))
	})
	return w.copyRows("questions", []string{"question_timestamp", "subject", "text", "reply_timestamp", "ignored",
		"reply_subject", "reply_text", "participation_id", "admin_id"}, len(w.d.questions), func(i int) []any {
		q := w.d.questions[i]
		return []any{q.questionTimestamp, q.subject, q.text, timep(q.replyTimestamp), q.ignored,
			strp(q.replySubject), strp(q.replyText), w.ids[q.participation], w.id(q.admin)}
	})
}

func (w *dbWriter) copyTasks() error {
	cols := []string{"id", "num", "contest_id", "name", "title", "submission_format", "primary_statements",
		"allowed_languages"}
	cols = append(cols, tokenColumns("token_")...)
	cols = append(cols, "max_submission_number", "max_user_test_number", "min_submission_interval",
		"min_user_test_interval", "feedback_level", "score_precision", "score_mode")
	return w.copyRows("tasks", cols, len(w.d.tasks), func(i int) []any {
		t := w.d.tasks[i]
		row := []any{w.ids[t.key], int32p(t.num), w.id(t.contest), t.name, t.title, t.submissionFormat,
			t.primaryStatements, strings0(t.allowedLanguages)}
		row = append(row, tokenValues(t.tokens)...)
		return append(row, int32p(t.maxSubmissionNumber), int32p(t.maxUserTestNumber),
			intervalp(t.minSubmissionInterval), intervalp(t.minUserTestInterval), t.feedbackLevel,
			t.scorePrecision, t.scoreMode)
	})
}

// copyFileRows writes rows of name and digest under a parent.
func (w *dbWriter) copyFileRows(table, parentCol, nameCol string, rows []*fileRow, withDataset bool) error {
	sortRows(rows, func(a, b *fileRow) int {
		return cmp.Or(cmp.Compare(w.ids[a.parent], w.ids[b.parent]), cmp.Compare(w.idOr0(a.dataset), w.idOr0(b.dataset)),
			strings.Compare(a.name, b.name))
	})
	cols := []string{parentCol, nameCol, "digest"}
	if withDataset {
		cols = []string{parentCol, "dataset_id", nameCol, "digest"}
	}
	return w.copyRows(table, cols, len(rows), func(i int) []any {
		f := rows[i]
		if withDataset {
			return []any{w.ids[f.parent], w.ids[f.dataset], f.name, w.digest(f.digest)}
		}
		return []any{w.ids[f.parent], f.name, w.digest(f.digest)}
	})
}

func (w *dbWriter) idOr0(i int32) int32 {
	if i == none {
		return 0
	}
	return w.ids[i]
}

func (w *dbWriter) copyTaskFiles() error {
	if err := w.copyFileRows("statements", "task_id", "language", w.d.statements, false); err != nil {
		return err
	}
	return w.copyFileRows("attachments", "task_id", "filename", w.d.attachments, false)
}

func (w *dbWriter) copyDatasets() error {
	if err := w.copyRows("datasets", []string{"id", "task_id", "description", "autojudge", "time_limit",
		"memory_limit", "task_type", "task_type_parameters", "score_type", "score_type_parameters"},
		len(w.d.datasets), func(i int) []any {
			x := w.d.datasets[i]
			return []any{w.ids[x.key], w.ids[x.task], x.description, x.autojudge, floatp(x.timeLimit),
				int64p(x.memoryLimit), x.taskType, jsonp(x.taskParams), x.scoreType, jsonp(x.scoreParams)}
		}); err != nil {
		return err
	}
	return w.copyFileRows("managers", "dataset_id", "filename", w.d.managers, false)
}

func (w *dbWriter) copyTestcases() error {
	return w.copyRows("testcases", []string{"id", "dataset_id", "codename", "public", "input", "output"},
		len(w.d.testcases), func(i int) []any {
			t := w.d.testcases[i]
			return []any{w.ids[t.key], w.ids[t.dataset], t.codename, t.public, w.digest(t.input), w.digest(t.output)}
		})
}

func (w *dbWriter) copySubmissions() error {
	if err := w.copyRows("submissions", []string{"id", "opaque_id", "participation_id", "task_id", "timestamp",
		"language", "comment", "official"}, len(w.d.submissions), func(i int) []any {
		s := w.d.submissions[i]
		return []any{w.ids[s.key], s.opaqueID, w.ids[s.participation], w.ids[s.task], s.timestamp,
			strp(s.language), s.comment, s.official}
	}); err != nil {
		return err
	}
	if err := w.copyFileRows("files", "submission_id", "filename", w.d.files, false); err != nil {
		return err
	}
	sortRows(w.d.tokens, func(a, b *token) int { return cmp.Compare(w.ids[a.submission], w.ids[b.submission]) })
	return w.copyRows("tokens", []string{"submission_id", "timestamp"}, len(w.d.tokens), func(i int) []any {
		t := w.d.tokens[i]
		return []any{w.ids[t.submission], t.timestamp}
	})
}

func compilationColumns() []string {
	return []string{"compilation_outcome", "compilation_text", "compilation_tries", "compilation_stdout",
		"compilation_stderr", "compilation_time", "compilation_wall_clock_time", "compilation_memory",
		"compilation_shard", "compilation_sandbox_paths", "compilation_sandbox_digests"}
}

func (w *dbWriter) compilationValues(c *compilation) []any {
	return []any{strp(c.outcome), c.text, c.tries, strp(c.stdout), strp(c.stderr), floatp(c.time),
		floatp(c.wallTime), int64p(c.memory), int32p(c.shard), strings0(c.paths), w.digests(c.digests)}
}

func executionColumns() []string {
	return []string{"execution_time", "execution_wall_clock_time", "execution_memory", "evaluation_shard",
		"evaluation_sandbox_paths", "evaluation_sandbox_digests"}
}

func (w *dbWriter) executionValues(e *execution) []any {
	return []any{floatp(e.time), floatp(e.wallTime), int64p(e.memory), int32p(e.shard), strings0(e.paths),
		w.digests(e.digests)}
}

func (w *dbWriter) copyResults() error {
	sortRows(w.d.results, func(a, b *submissionResult) int {
		return cmp.Or(cmp.Compare(w.ids[a.submission], w.ids[b.submission]), cmp.Compare(w.ids[a.dataset], w.ids[b.dataset]))
	})
	cols := append([]string{"submission_id", "dataset_id"}, compilationColumns()...)
	cols = append(cols, "evaluation_outcome", "evaluation_tries", "score", "score_details", "scored_at",
		"public_score", "public_score_details", "ranking_score_details")
	if err := w.copyRows("submission_results", cols, len(w.d.results), func(i int) []any {
		r := w.d.results[i]
		row := append([]any{w.ids[r.submission], w.ids[r.dataset]}, w.compilationValues(&r.compilation)...)
		return append(row, strp(r.evaluation), r.evaluationTries, floatp(r.score), jsonp(r.scoreDetails),
			timep(r.scoredAt), floatp(r.publicScore), jsonp(r.publicDetails), strings0(r.rankingDetails))
	}); err != nil {
		return err
	}
	if err := w.copyFileRows("executables", "submission_id", "filename", w.d.executables, true); err != nil {
		return err
	}
	sortRows(w.d.evaluations, func(a, b *evaluation) int {
		return cmp.Or(cmp.Compare(w.ids[a.submission], w.ids[b.submission]), cmp.Compare(w.ids[a.dataset], w.ids[b.dataset]),
			cmp.Compare(w.ids[a.testcase], w.ids[b.testcase]))
	})
	cols = append([]string{"submission_id", "dataset_id", "testcase_id", "outcome", "text", "admin_text"},
		executionColumns()...)
	return w.copyRows("evaluations", cols, len(w.d.evaluations), func(i int) []any {
		e := w.d.evaluations[i]
		row := []any{w.ids[e.submission], w.ids[e.dataset], w.ids[e.testcase], strp(e.outcome), e.text, strp(e.adminText)}
		return append(row, w.executionValues(&e.execution)...)
	})
}

func (w *dbWriter) copyUserTests() error {
	if err := w.copyRows("user_tests", []string{"id", "participation_id", "task_id", "timestamp", "language", "input"},
		len(w.d.userTests), func(i int) []any {
			u := w.d.userTests[i]
			return []any{w.ids[u.key], w.ids[u.participation], w.ids[u.task], u.timestamp, strp(u.language),
				w.digest(u.input)}
		}); err != nil {
		return err
	}
	if err := w.copyFileRows("user_test_files", "user_test_id", "filename", w.d.userTestFiles, false); err != nil {
		return err
	}
	if err := w.copyFileRows("user_test_managers", "user_test_id", "filename", w.d.userTestManager, false); err != nil {
		return err
	}
	sortRows(w.d.userTestResults, func(a, b *userTestResult) int {
		return cmp.Or(cmp.Compare(w.ids[a.userTest], w.ids[b.userTest]), cmp.Compare(w.ids[a.dataset], w.ids[b.dataset]))
	})
	cols := append([]string{"user_test_id", "dataset_id", "output"}, compilationColumns()...)
	cols = append(cols, "evaluation_outcome", "evaluation_text", "evaluation_tries")
	cols = append(cols, executionColumns()...)
	if err := w.copyRows("user_test_results", cols, len(w.d.userTestResults), func(i int) []any {
		r := w.d.userTestResults[i]
		var output any
		if r.output != nil {
			if o := w.digest(*r.output); o != "" {
				output = o
			}
		}
		row := append([]any{w.ids[r.userTest], w.ids[r.dataset], output}, w.compilationValues(&r.compilation)...)
		row = append(row, strp(r.evaluation), r.evaluationText, r.evaluationTries)
		return append(row, w.executionValues(&r.execution)...)
	}); err != nil {
		return err
	}
	return w.copyFileRows("user_test_executables", "user_test_id", "filename", w.d.userTestExecs, true)
}

// links sets the main groups of the contests and the active datasets of
// the tasks, which point to rows written after them.
func (w *dbWriter) links() error {
	var contests, groups []int32
	for _, c := range w.d.contests {
		if c.mainGroup != none {
			contests = append(contests, w.ids[c.key])
			groups = append(groups, w.ids[c.mainGroup])
		}
	}
	var tasks, datasets []int32
	for _, t := range w.d.tasks {
		if t.activeDataset != none {
			tasks = append(tasks, w.ids[t.key])
			datasets = append(datasets, w.ids[t.activeDataset])
		}
	}
	b := &pgx.Batch{}
	if len(contests) > 0 {
		b.Queue(`UPDATE contests AS c SET main_group_id = v.g FROM unnest($1::integer[], $2::integer[]) AS v(c, g) WHERE c.id = v.c`,
			contests, groups)
	}
	if len(tasks) > 0 {
		b.Queue(`UPDATE tasks AS t SET active_dataset_id = v.d FROM unnest($1::integer[], $2::integer[]) AS v(t, d) WHERE t.id = v.t`,
			tasks, datasets)
	}
	if b.Len() == 0 {
		return nil
	}
	if err := w.tx.SendBatch(w.ctx, b).Close(); err != nil {
		return fmt.Errorf("linking contests and tasks: %w", explain(err))
	}
	return nil
}

// taskScores fills the task score cache with the imported results.
func (w *dbWriter) taskScores() error {
	seen := map[judging.TaskKey]bool{}
	var keys []judging.TaskKey
	for _, s := range w.d.submissions {
		k := judging.TaskKey{ParticipationID: w.ids[s.participation], TaskID: w.ids[s.task]}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	if _, err := judging.RefreshTaskScores(w.ctx, w.tx, keys); err != nil {
		return fmt.Errorf("computing the task scores: %w", err)
	}
	return nil
}

// changes lists what the import created.
func (w *dbWriter) changes() {
	add := func(a importer.Action, kind, name string, id int32, details string) {
		w.res.Changes = append(w.res.Changes, importer.Change{Action: a, Kind: kind, Name: name, ID: id, Details: details})
	}
	for _, a := range w.d.admins {
		if a.existing != 0 {
			add(importer.Unchanged, "admin", a.username, a.existing, "already in the database")
		} else {
			add(importer.Created, "admin", a.username, w.ids[a.key], "disabled: an author of announcements or answers")
		}
	}
	type counts struct{ groups, tasks, participations, submissions, results, userTests, announcements, questions, messages int }
	per := map[int32]*counts{}
	for _, c := range w.d.contests {
		per[c.key] = &counts{}
	}
	partContest := map[int32]int32{}
	for _, g := range w.d.groups {
		per[g.contest].groups++
	}
	for _, t := range w.d.tasks {
		if t.contest != none {
			per[t.contest].tasks++
		}
	}
	for _, p := range w.d.participations {
		per[p.contest].participations++
		partContest[p.key] = p.contest
	}
	subContest := map[int32]int32{}
	for _, s := range w.d.submissions {
		c := partContest[s.participation]
		per[c].submissions++
		subContest[s.key] = c
	}
	for _, r := range w.d.results {
		if r.score != nil {
			per[subContest[r.submission]].results++
		}
	}
	for _, u := range w.d.userTests {
		per[partContest[u.participation]].userTests++
	}
	for _, a := range w.d.announcements {
		per[a.contest].announcements++
	}
	for _, q := range w.d.questions {
		per[partContest[q.participation]].questions++
	}
	for _, m := range w.d.messages {
		per[partContest[m.participation]].messages++
	}
	for _, c := range w.d.contests {
		n := per[c.key]
		details := []string{plural(n.groups, "group"), plural(n.tasks, "task"), plural(n.participations, "participation"),
			fmt.Sprintf("%s (%d scored)", plural(n.submissions, "submission"), n.results), plural(n.userTests, "user test"),
			plural(n.announcements, "announcement"), plural(n.questions, "question"), plural(n.messages, "message")}
		add(importer.Created, "contest", c.name, w.ids[c.key], strings.Join(details, ", "))
		w.res.Events = append(w.res.Events, judging.Event{Kind: judging.EventSweep, ContestID: w.ids[c.key]})
	}
	// The contest imported is the one the dump was exported for (with -c,
	// the dump also has the other contests of its contestants).
	var exported []int32
	for _, c := range w.d.contests {
		if w.d.exported[c.key] {
			exported = append(exported, c.key)
		}
	}
	if len(exported) == 1 {
		w.res.ContestID = w.ids[exported[0]]
	}
	datasets, testcases := map[int32]int{}, map[int32]int{}
	dsTask := map[int32]int32{}
	for _, x := range w.d.datasets {
		datasets[x.task]++
		dsTask[x.key] = x.task
	}
	for _, t := range w.d.testcases {
		testcases[dsTask[t.dataset]]++
	}
	for _, t := range w.d.tasks {
		add(importer.Created, "task", t.name, w.ids[t.key],
			plural(datasets[t.key], "dataset")+", "+plural(testcases[t.key], "testcase"))
		w.res.TaskIDs[t.name] = w.ids[t.key]
	}
	for _, u := range w.d.users {
		if u.existing != 0 {
			add(importer.Unchanged, "user", u.username, u.existing, "already in the database")
		} else {
			add(importer.Created, "user", u.username, w.ids[u.key], "")
		}
	}
	for _, t := range w.d.teams {
		if t.existing != 0 {
			add(importer.Unchanged, "team", t.code, t.existing, "already in the database")
		} else {
			add(importer.Created, "team", t.code, w.ids[t.key], "")
		}
	}
}

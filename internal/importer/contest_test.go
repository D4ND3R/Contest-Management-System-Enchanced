// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// demoTasks returns the tasks of a contest, by name.
func demoTasks(t testing.TB, names ...string) map[string]*Task {
	dir := t.TempDir()
	out := make(map[string]*Task, len(names))
	for _, n := range names {
		out[n] = batchTask(t, dir, n, 2)
	}
	return out
}

func TestWriteContestRoundTrip(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	c := demoContest("demo", "a", "b")
	c.Timezone = Ptr("Europe/Rome")
	c.AllowedLocalizations = []string{"en", "it"}
	c.Tokens = Tokens{Mode: TokenModeFinite, MaxNumber: Ptr(int32(5)), GenInitial: 0, GenNumber: 1,
		GenInterval: 10 * time.Minute, GenMax: Ptr(int32(3))}
	c.MaxSubmissionNumber = Ptr(int32(100))
	c.MinSubmissionIntervalGracePeriod = Ptr(30 * time.Second)
	c.ScorePrecision = 1
	c.AllowRegistration = true
	c.Participations[0].IP = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.7/32")}
	c.Participations[0].DelayTime = Ptr(10 * time.Minute)
	c.Participations[0].ExtraTime = Ptr(90 * time.Second)
	c.Participations[0].Unrestricted = Ptr(true)
	c.Users[1].Email = Ptr("bob@example.com")
	c.Users[1].PreferredLanguages = []string{"it", "en"}
	c.Users[2].Password = "" // a random one

	res, err := w.WriteContest(ctx, c, demoTasks(t, "a", "b"), ContestOptions{ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	id := res.ContestID
	if ch := change(res, "contest", "demo"); ch.Action != Created || ch.ID != id || ch.Details != "2 groups" {
		t.Errorf("changes:\n%s", changes(res))
	}
	for _, k := range []string{"task", "user", "participation"} {
		if n := res.Count(k, Created); n != map[string]int{"task": 2, "user": 3, "participation": 3}[k] {
			t.Errorf("%d %ss created:\n%s", n, k, changes(res))
		}
	}
	if res.Count("team", Created) != 1 || len(res.TaskIDs) != 2 {
		t.Errorf("changes:\n%s", changes(res))
	}
	if want := (judging.Event{Kind: judging.EventSweep, ContestID: id}); !sameEvents(res.Events, want) {
		t.Errorf("events %+v", res.Events)
	}

	wantRows(t, pool, []string{
		`Contest demo|{en,it}|{"C++17 / g++","Python 3 / CPython"}|t|t|t|f|f|t|t|t|f|finite|5|00:00:00|0|1|00:10:00|3|Europe/Rome|100||00:00:30|1|main`,
	}, `SELECT concat_ws('|', c.description, allowed_localizations, languages, submissions_download_allowed, allow_questions,
		allow_user_tests, allow_unofficial_submission_before_analysis_mode, block_hidden_participations,
		allow_password_authentication, allow_registration, ip_restriction, ip_autologin, token_mode, token_max_number,
		token_min_interval, token_gen_initial, token_gen_number, token_gen_interval, token_gen_max, timezone,
		max_submission_number, coalesce(min_submission_interval::text, ''), min_submission_interval_grace_period,
		score_precision, g.name)
		FROM contests c JOIN groups g ON g.id = c.main_group_id WHERE c.id = $1`, id)
	wantRows(t, pool, []string{
		"late|2026-03-01 10:00:00+00|2026-03-01 15:00:00+00|f|2100-01-01 00:00:00+00|03:00:00",
		"main|2026-03-01 09:00:00+00|2026-03-01 14:00:00+00|f|2100-01-01 00:00:00+00|",
	}, `SELECT concat_ws('|', name, start AT TIME ZONE 'UTC' || '+00', stop AT TIME ZONE 'UTC' || '+00', analysis_enabled,
		analysis_start AT TIME ZONE 'UTC' || '+00', coalesce(per_user_time::text, ''))
		FROM groups WHERE contest_id = $1 ORDER BY name`, id)
	wantRows(t, pool, []string{"a 0", "b 1"}, `SELECT name || ' ' || num FROM tasks WHERE contest_id = $1 ORDER BY num`, id)
	users := texts(t, pool, `SELECT concat_ws('|', username, first_name, last_name, password, coalesce(email, ''),
		preferred_languages) FROM users ORDER BY username`)
	if len(users) != 3 || users[0] != "alice|Alice|Test|plaintext:pw-alice||{}" ||
		users[1] != "bob|Bob|Test|plaintext:pw-bob|bob@example.com|{it,en}" ||
		!strings.HasPrefix(users[2], "carol|Carol|Test|plaintext:") || len(users[2]) != len("carol|Carol|Test|plaintext:abcdef||{}") {
		t.Errorf("users %q", users)
	}
	wantRows(t, pool, []string{"ita Italy"}, `SELECT code || ' ' || name FROM teams`)
	wantRows(t, pool, []string{
		"alice|main||plaintext:pw-alice|{10.0.0.0/8,192.168.1.7/32}|f|t|00:10:00|00:01:30",
		"bob|late||plaintext:pw-bob||f|f|00:00:00|00:00:00",
		"carol|main|ita|plaintext:pw-carol||t|f|00:00:00|00:00:00",
	}, `SELECT concat_ws('|', u.username, g.name, coalesce(tm.code, ''), p.password, coalesce(p.ip::text, ''), p.hidden,
		p.unrestricted, p.delay_time, p.extra_time)
		FROM participations p JOIN users u ON u.id = p.user_id JOIN groups g ON g.id = p.group_id
		LEFT JOIN teams tm ON tm.id = p.team_id WHERE p.contest_id = $1 ORDER BY u.username`, id)

	// The same package again: nothing changes (networks, durations and
	// times compare by value).
	again := *c
	res, err = w.WriteContest(ctx, &again, nil, ContestOptions{UpdateContest: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	if change(res, "contest", "demo").Action != Unchanged || res.Count("participation", Updated) != 0 ||
		res.Count("user", Created) != 0 || len(res.Report.Issues()) != 0 {
		t.Errorf("changes:\n%s\nissues:\n%s", changes(res), issues(res.Report))
	}

	// -z: every group starts and stops at the epoch.
	z := demoContest("zero")
	z.Participations, z.Users, z.Teams = nil, nil, nil
	res, err = w.WriteContest(ctx, z, nil, ContestOptions{ZeroTime: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	wantRows(t, pool, []string{"1970-01-01 00:00:00 1970-01-01 00:00:00", "1970-01-01 00:00:00 1970-01-01 00:00:00"},
		`SELECT (start AT TIME ZONE 'UTC') || ' ' || (stop AT TIME ZONE 'UTC') FROM groups WHERE contest_id = $1`, res.ContestID)
	if z.Groups[0].Start.Year() != 2026 {
		t.Error("-z changed the model")
	}
}

func TestWriteContestUpdate(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	res, err := w.WriteContest(ctx, demoContest("demo", "a", "b"), demoTasks(t, "a", "b"),
		ContestOptions{ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	id := res.ContestID
	groupIDs := map[string]string{}
	for _, r := range texts(t, pool, `SELECT name || ' ' || id FROM groups WHERE contest_id = $1`, id) {
		n, gid, _ := strings.Cut(r, " ")
		groupIDs[n] = gid
	}

	// Without an update flag an existing contest is an error.
	_, err = w.WriteContest(ctx, demoContest("demo", "a", "b"), nil, ContestOptions{})
	if !errors.Is(err, ErrExists) {
		t.Errorf("existing contest: %v", err)
	}
	// The same package with -u: nothing changes.
	res, err = w.WriteContest(ctx, demoContest("demo", "a", "b"), nil, ContestOptions{UpdateContest: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	if ch := change(res, "contest", "demo"); ch.Action != Unchanged || res.Count("task", Skipped) != 2 ||
		res.Count("participation", Updated) != 0 || len(res.Report.Issues()) != 0 {
		t.Errorf("changes:\n%s\nissues:\n%s", changes(res), issues(res.Report))
	}

	// A new version: new description and times, a new main group, the task
	// list changed (b first, a new task c, a dropped), carol dropped, dave
	// added, alice in the new main group, bob without his team change.
	c := demoContest("demo", "b", "c")
	c.Description = "Demo v2"
	c.Groups[0].Stop = c.Groups[0].Stop.Add(time.Hour)
	extra := NewGroup("extra")
	c.Groups = []Group{c.Groups[0], extra} // "late" is not in the package: kept
	c.MainGroup = "extra"
	c.Users = append(c.Users[:2], User{Username: "dave", FirstName: "Dave", LastName: "Test"})
	c.Participations = []Participation{
		{Username: "alice"},
		{Username: "bob", Group: "late", Team: Ptr("ita"), Hidden: Ptr(true)},
		{Username: "dave", Group: "main"},
	}
	res, err = w.WriteContest(ctx, c, demoTasks(t, "c"), ContestOptions{UpdateContest: true, ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	if ch := change(res, "contest", "demo"); ch.Action != Updated || ch.Details != "settings changed; 1 group added, 1 group changed" {
		t.Errorf("changes:\n%s", changes(res))
	}
	if !hasIssue(res.Report, SeverityWarning, "contest", "1 participation of the contest not in the package kept: carol") {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
	if res.Count("participation", Updated) != 2 || res.Count("participation", Created) != 1 || res.Count("user", Created) != 1 ||
		change(res, "task", "b").Action != Skipped || change(res, "task", "c").Action != Created {
		t.Errorf("changes:\n%s", changes(res))
	}
	wantRows(t, pool, []string{"Demo v2 extra"}, `SELECT c.description || ' ' || g.name FROM contests c
		JOIN groups g ON g.id = c.main_group_id WHERE c.id = $1`, id)
	wantRows(t, pool, []string{
		"extra " + "2000-01-01 00:00:00",
		"late " + groupIDs["late"] + " 2026-03-01 10:00:00",
		"main " + groupIDs["main"] + " 2026-03-01 09:00:00 2026-03-01 15:00:00",
	}, `SELECT CASE name WHEN 'extra' THEN name || ' ' || (start AT TIME ZONE 'UTC')
		WHEN 'late' THEN concat_ws(' ', name, id, start AT TIME ZONE 'UTC')
		ELSE concat_ws(' ', name, id, start AT TIME ZONE 'UTC', stop AT TIME ZONE 'UTC') END
		FROM groups WHERE contest_id = $1 ORDER BY name`, id)
	wantRows(t, pool, []string{"a  ", fmt.Sprintf("b %d 0", id), fmt.Sprintf("c %d 1", id)},
		`SELECT name || ' ' || coalesce(contest_id::text, '') || ' ' || coalesce(num::text, '') FROM tasks ORDER BY name`)
	wantRows(t, pool, []string{
		"alice|extra||plaintext:pw-alice|f", // the password stays: not given
		"bob|late|ita|plaintext:pw-bob|t",
		"carol|main|ita|plaintext:pw-carol|t",
		"dave|main|||f",
	}, `SELECT concat_ws('|', u.username, g.name, coalesce(tm.code, ''), coalesce(p.password, ''), p.hidden)
		FROM participations p JOIN users u ON u.id = p.user_id JOIN groups g ON g.id = p.group_id
		LEFT JOIN teams tm ON tm.id = p.team_id WHERE p.contest_id = $1 ORDER BY u.username`, id)

	// -U: the tasks are updated; without -u the contest is not.
	tasks := demoTasks(t, "b", "c")
	tasks["b"].Title = "B v2"
	c2 := demoContest("demo", "b", "c")
	c2.Description = "ignored"
	c2.Participations = nil
	res, err = w.WriteContest(ctx, c2, tasks, ContestOptions{UpdateTasks: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	if change(res, "task", "b").Action != Updated || change(res, "task", "c").Action != Unchanged ||
		change(res, "contest", "demo").Action != Unchanged {
		t.Errorf("changes:\n%s", changes(res))
	}
	if n := len(res.Events); n != 3 || res.Events[2].Kind != judging.EventSweep {
		t.Errorf("events %+v", res.Events)
	}
	wantRows(t, pool, []string{"Demo v2"}, `SELECT description FROM contests WHERE id = $1`, id)
	wantRows(t, pool, []string{"B v2"}, `SELECT title FROM tasks WHERE name = 'b'`)
	if n := count(t, pool, `SELECT count(*) FROM participations WHERE contest_id = $1`, id); n != 4 {
		t.Errorf("%d participations, want 4 (none given, none deleted)", n)
	}

	// --delete-stale-participations deletes carol's, with its submissions.
	if _, err := pool.Exec(ctx, `INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp)
		SELECT 7, p.id, $2, now() FROM participations p JOIN users u ON u.id = p.user_id
		WHERE p.contest_id = $1 AND u.username = 'carol'`, id, res.TaskIDs["b"]); err != nil {
		t.Fatal(err)
	}
	res, err = w.WriteContest(ctx, c, nil, ContestOptions{UpdateContest: true, DeleteStaleParticipations: true})
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	if ch := change(res, "participation", "carol"); ch.Action != Deleted {
		t.Errorf("changes:\n%s", changes(res))
	}
	wantRows(t, pool, []string{"alice", "bob", "dave"}, `SELECT u.username FROM participations p JOIN users u ON u.id = p.user_id
		WHERE p.contest_id = $1 ORDER BY 1`, id)
	if n := count(t, pool, `SELECT count(*) FROM users WHERE username = 'carol'`); n != 1 {
		t.Error("the user of a stale participation was deleted")
	}
	if n := count(t, pool, `SELECT count(*) FROM submissions WHERE opaque_id = 7`); n != 0 {
		t.Error("the submissions of a stale participation were kept")
	}
}

func TestWriteContestAllOrNothing(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	other := contestWithGroup(t, w, "other")
	if _, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "x", 2), TaskOptions{ContestID: other}); err != nil {
		t.Fatal(err)
	}
	nothing := func(t *testing.T) {
		t.Helper()
		if n := count(t, pool, `SELECT (SELECT count(*) FROM contests WHERE name = 'demo') + (SELECT count(*) FROM tasks WHERE name <> 'x')
			+ (SELECT count(*) FROM users) + (SELECT count(*) FROM teams) + (SELECT count(*) FROM participations)`); n != 0 {
			t.Errorf("%d rows written by a failed import", n)
		}
	}

	t.Run("task of another contest", func(t *testing.T) {
		// Task a is created, then x fails: a is rolled back with the
		// contest, the users and the teams.
		_, err := w.WriteContest(ctx, demoContest("demo", "a", "x"), demoTasks(t, "a"), ContestOptions{ImportTasks: true, ImportUsers: true})
		if err == nil || !strings.Contains(err.Error(), `task "x" is already tied to contest "other"`) {
			t.Errorf("err %v", err)
		}
		nothing(t)
		if n := count(t, pool, `SELECT count(*) FROM tasks WHERE name = 'x' AND contest_id = $1`, other); n != 1 {
			t.Error("task x moved")
		}
	})
	t.Run("missing users", func(t *testing.T) {
		_, err := w.WriteContest(ctx, demoContest("demo", "a"), demoTasks(t, "a"), ContestOptions{ImportTasks: true})
		if err == nil || !strings.Contains(err.Error(), "users not found in the database: alice, bob, carol") {
			t.Errorf("err %v", err)
		}
		nothing(t)
	})
	t.Run("missing task", func(t *testing.T) {
		_, err := w.WriteContest(ctx, demoContest("demo", "a", "b"), demoTasks(t, "a"), ContestOptions{ImportTasks: true, ImportUsers: true})
		if err == nil || !strings.Contains(err.Error(), `task "b" not found in the database`) {
			t.Errorf("err %v", err)
		}
		nothing(t)
	})
	t.Run("upload", func(t *testing.T) {
		tasks := demoTasks(t, "a", "b")
		tasks["b"].Datasets[0].Testcases[1].Input = FileFromFunc("broken", func() (io.ReadCloser, error) {
			return nil, errors.New("disk on fire")
		})
		_, err := w.WriteContest(ctx, demoContest("demo", "a", "b"), tasks, ContestOptions{ImportTasks: true, ImportUsers: true})
		if err == nil || !strings.Contains(err.Error(), "broken: disk on fire") {
			t.Errorf("err %v", err)
		}
		nothing(t)
	})
	t.Run("invalid task", func(t *testing.T) {
		tasks := demoTasks(t, "a", "b")
		tasks["b"].Datasets[0].ScoreType = "Nope"
		res, err := w.WriteContest(ctx, demoContest("demo", "a", "b"), tasks, ContestOptions{ImportTasks: true, ImportUsers: true})
		var ve *ValidationError
		if !errors.As(err, &ve) || !hasIssue(res.Report, SeverityError, `task b: dataset "Default"`, `unknown score type "Nope"`) {
			t.Errorf("err %v\n%s", err, issues(res.Report))
		}
		nothing(t)
	})
	t.Run("task name", func(t *testing.T) {
		tasks := demoTasks(t, "a")
		tasks["b"] = tasks["a"]
		res, err := w.WriteContest(ctx, demoContest("demo", "a", "b"), tasks, ContestOptions{ImportTasks: true, ImportUsers: true})
		if err == nil || !hasIssue(res.Report, SeverityError, "task b", `the package of task "b" contains task "a"`) {
			t.Errorf("err %v\n%s", err, issues(res.Report))
		}
		nothing(t)
	})
	t.Run("unknown team", func(t *testing.T) {
		c := demoContest("demo", "a")
		c.Teams = nil
		_, err := w.WriteContest(ctx, c, demoTasks(t, "a"), ContestOptions{ImportTasks: true, ImportUsers: true})
		if err == nil || !strings.Contains(err.Error(), "teams not found in the database: ita") {
			t.Errorf("err %v", err)
		}
		nothing(t)
	})
}

func TestWriteUsersAndTeams(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	bcrypt := "bcrypt:$2b$12$" + strings.Repeat("x", 53)
	users := []User{
		{Username: "alice", FirstName: "Alice", LastName: "A"},
		{Username: "bob", FirstName: "Bob", LastName: "B", Password: bcrypt, Timezone: Ptr("Europe/Rome")},
	}
	res, err := w.WriteUsers(ctx, users, UserOptions{})
	if err != nil {
		t.Fatalf("WriteUsers: %v\n%s", err, issues(res.Report))
	}
	if res.Count("user", Created) != 2 {
		t.Errorf("changes:\n%s", changes(res))
	}
	alicePW := text(t, pool, `SELECT password FROM users WHERE username = 'alice'`)
	if !strings.HasPrefix(alicePW, "plaintext:") || len(alicePW) != len("plaintext:")+6 {
		t.Errorf("random password %q", alicePW)
	}
	wantRows(t, pool, []string{"bob " + bcrypt + " Europe/Rome"}, `SELECT concat_ws(' ', username, password, timezone) FROM users WHERE username = 'bob'`)

	// Existing users are left alone without Update.
	users[0].FirstName = "Alicia"
	res, err = w.WriteUsers(ctx, users, UserOptions{})
	if err != nil || res.Count("user", Skipped) != 2 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	// With Update they change; a user without password keeps the stored one.
	users[1].Email = Ptr("bob@example.com")
	res, err = w.WriteUsers(ctx, append(users, User{Username: "carol", Password: "plaintext:c"}), UserOptions{Update: true})
	if err != nil || res.Count("user", Updated) != 2 || res.Count("user", Created) != 1 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	wantRows(t, pool, []string{"alice Alicia " + alicePW, "bob Bob " + bcrypt + " bob@example.com", "carol  plaintext:c"},
		`SELECT concat_ws(' ', username, first_name, password, email) FROM users ORDER BY username`)
	res, err = w.WriteUsers(ctx, users, UserOptions{Update: true})
	if err != nil || res.Count("user", Unchanged) != 2 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}

	// -c: a participation in the main group of the contest for those
	// without one.
	cid := contestWithGroup(t, w, "c")
	res, err = w.WriteUsers(ctx, users[:1], UserOptions{ContestID: cid})
	if err != nil || res.Count("participation", Created) != 1 || res.ContestID != cid ||
		len(res.Events) != 1 || res.Events[0].Kind != judging.EventSweep {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	res, err = w.WriteUsers(ctx, users, UserOptions{ContestID: cid})
	if err != nil || res.Count("participation", Created) != 1 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	wantRows(t, pool, []string{"alice main", "bob main"}, `SELECT u.username || ' ' || g.name FROM participations p
		JOIN users u ON u.id = p.user_id JOIN groups g ON g.id = p.group_id ORDER BY 1`)
	if _, err := w.WriteUsers(ctx, users, UserOptions{ContestID: 999}); err == nil {
		t.Error("unknown contest accepted")
	}

	// Invalid users: nothing is written.
	res, err = w.WriteUsers(ctx, []User{{Username: "dave"}, {Username: "e e"}}, UserOptions{})
	if err == nil || !hasIssue(res.Report, SeverityError, "users", `invalid username "e e"`) {
		t.Errorf("err %v\n%s", err, issues(res.Report))
	}
	if n := count(t, pool, `SELECT count(*) FROM users WHERE username = 'dave'`); n != 0 {
		t.Error("a user of an invalid list was written")
	}

	// Teams, by code.
	teams := []Team{{Code: "ita", Name: "Italy"}, {Code: "esp", Name: "Spain"}}
	if res, err = w.WriteTeams(ctx, teams, TeamOptions{}); err != nil || res.Count("team", Created) != 2 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	teams[0].Name = "Italia"
	if res, err = w.WriteTeams(ctx, teams, TeamOptions{}); err != nil || res.Count("team", Skipped) != 2 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	res, err = w.WriteTeams(ctx, teams, TeamOptions{Update: true})
	if ch := change(res, "team", "ita"); err != nil || ch.Action != Updated || change(res, "team", "esp").Action != Unchanged {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	wantRows(t, pool, []string{"esp Spain", "ita Italia"}, `SELECT code || ' ' || name FROM teams ORDER BY code`)
}

// TestWriteContestQueries checks that importing a contest takes the same
// number of round trips whatever the number of participants (no N+1).
func TestWriteContestQueries(t *testing.T) {
	ctx := context.Background()
	w, qc := testWriter(t)
	contest := func(name string, n int) *Contest {
		c := demoContest(name, name+"-task")
		c.Users, c.Participations = nil, nil
		c.Teams = []Team{{Code: name + "-team", Name: name}}
		for i := range n {
			u := fmt.Sprintf("%s-u%03d", name, i)
			c.Users = append(c.Users, User{Username: u, FirstName: "F", LastName: "L", Password: "plaintext:p"})
			p := Participation{Username: u, Password: Ptr("plaintext:q"), Hidden: Ptr(i%2 == 0)}
			if i%3 == 0 {
				p.Group, p.Team = "late", Ptr(name+"-team")
			}
			c.Participations = append(c.Participations, p)
		}
		return c
	}
	measure := func(name string, n int, opts ContestOptions) int64 {
		t.Helper()
		c := contest(name, n)
		if opts.UpdateContest {
			for i := range c.Participations {
				c.Participations[i].Hidden = Ptr(i%2 == 1)
			}
		}
		tasks := demoTasks(t, name+"-task")
		qc.Reset()
		res, err := w.WriteContest(ctx, c, tasks, opts)
		if err != nil {
			t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
		}
		return qc.Count()
	}
	create := ContestOptions{ImportTasks: true, ImportUsers: true}
	small, big := measure("small", 3, create), measure("big", 150, create)
	if small != big {
		t.Errorf("creating a contest: %d round trips with 3 participants, %d with 150", small, big)
	}
	update := ContestOptions{UpdateContest: true, UpdateTasks: true, ImportUsers: true}
	small, big = measure("small", 3, update), measure("big", 150, update)
	if small != big {
		t.Errorf("updating a contest: %d round trips with 3 participants, %d with 150", small, big)
	}
	if n := count(t, w.Pool, `SELECT count(*) FROM participations p JOIN contests c ON c.id = p.contest_id
		WHERE c.name = 'big' AND p.hidden`); n != 75 {
		t.Errorf("%d hidden participations after the update, want 75", n)
	}
	users := make([]User, 200)
	for i := range users {
		users[i] = User{Username: fmt.Sprintf("x%03d", i), Password: "plaintext:x"}
	}
	qc.Reset()
	if _, err := w.WriteUsers(ctx, users[:2], UserOptions{}); err != nil {
		t.Fatal(err)
	}
	few := qc.Count()
	qc.Reset()
	if _, err := w.WriteUsers(ctx, users[2:], UserOptions{}); err != nil {
		t.Fatal(err)
	}
	if many := qc.Count(); few != many {
		t.Errorf("users: %d round trips for 2, %d for 198", few, many)
	}
}

func TestDryRun(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	blobs := func() int {
		n := 0
		if err := w.Blobs.Walk(ctx, func(string, int64) error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	c, tasks := demoContest("demo", "a", "b"), demoTasks(t, "a", "b")
	opts := ContestOptions{ImportTasks: true, ImportUsers: true}
	w.DryRun = true
	res, err := w.WriteContest(ctx, c, tasks, opts)
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, issues(res.Report))
	}
	if res.Count("task", Created) != 2 || res.Count("participation", Created) != 3 || len(res.Events) != 0 {
		t.Errorf("changes:\n%s\nevents %+v", changes(res), res.Events)
	}
	if n := count(t, pool, `SELECT (SELECT count(*) FROM contests) + (SELECT count(*) FROM tasks) + (SELECT count(*) FROM users)`); n != 0 || blobs() != 0 {
		t.Errorf("a dry run wrote %d rows and %d blobs", n, blobs())
	}
	if d := tasks["a"].Datasets[0].Testcases[0].Input.Digest(); d != sha("0 0\n") {
		t.Errorf("digest after a dry run %q", d)
	}
	// The same model, for real: the hashed files are stored now.
	w.DryRun = false
	res, err = w.WriteContest(ctx, c, tasks, opts)
	if err != nil {
		t.Fatalf("WriteContest: %v\n%s", err, issues(res.Report))
	}
	if len(res.Events) != 1 || count(t, pool, `SELECT count(*) FROM tasks`) != 2 {
		t.Errorf("events %+v", res.Events)
	}
	for _, tk := range tasks {
		for _, f := range tk.Files() {
			if _, err := w.Blobs.Stat(ctx, f.Digest()); err != nil {
				t.Errorf("%s: %v", f.Name(), err)
			}
		}
	}
	// A dry run of an update says what would change.
	w.DryRun = true
	tasks = demoTasks(t, "a", "b")
	tasks["a"].Title = "New"
	res, err = w.WriteContest(ctx, demoContest("demo", "a", "b"), tasks, ContestOptions{UpdateTasks: true})
	if err != nil || change(res, "task", "a").Action != Updated || change(res, "task", "b").Action != Unchanged {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	wantRows(t, pool, []string{"Task a"}, `SELECT title FROM tasks WHERE name = 'a'`)
}

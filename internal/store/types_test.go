// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"encoding/json"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// The type overrides of sqlc.yaml rely on pgx encoding and scanning these
// Go types natively; these tests check each round trip and how NULL maps.

func TestIntervalRoundTrip(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q
	c, g := f.contest("ioi")

	for _, d := range []time.Duration{0, time.Microsecond, 90 * time.Minute, 36 * time.Hour, 400 * 24 * time.Hour} {
		arg := groupParams(c.ID, "g")
		upd := q.UpdateGroupParams{ID: g.ID, Name: "default", Start: arg.Start, Stop: arg.Stop,
			AnalysisStart: arg.AnalysisStart, AnalysisStop: arg.AnalysisStop, PerUserTime: &d}
		got := must[q.Group](t)(qs.UpdateGroup(ctx, upd))
		if got.PerUserTime == nil || *got.PerUserTime != d {
			t.Fatalf("interval %v came back as %v", d, got.PerUserTime)
		}
		var us int64
		check(t, f.s.Pool.QueryRow(ctx, `SELECT (extract(epoch FROM per_user_time) * 1000000)::bigint FROM groups WHERE id = $1`, g.ID).Scan(&us))
		if us != d.Microseconds() {
			t.Fatalf("interval %v stored as %dµs", d, us)
		}
	}
	// NULL is nil.
	got := must[q.Group](t)(qs.GetGroup(ctx, must[q.Group](t)(qs.CreateGroup(ctx, groupParams(c.ID, "other"))).ID))
	if got.PerUserTime != nil {
		t.Fatalf("NULL interval = %v", *got.PerUserTime)
	}
	// Intervals written by hand with days and months are read as durations
	// (a day is 24 hours, a month 30 days, as pgx does).
	_, err := f.s.Pool.Exec(ctx, `UPDATE contests SET token_gen_interval = '1 day 2 hours 3.5 seconds', token_min_interval = '1 mon' WHERE id = $1`, c.ID)
	check(t, err)
	cc := must[q.Contest](t)(qs.GetContest(ctx, c.ID))
	if want := 26*time.Hour + 3500*time.Millisecond; cc.TokenGenInterval != want {
		t.Fatalf("'1 day 2 hours 3.5 seconds' = %v, want %v", cc.TokenGenInterval, want)
	}
	if cc.TokenMinInterval != 30*24*time.Hour {
		t.Fatalf("'1 mon' = %v, want 720h", cc.TokenMinInterval)
	}
	// Below a microsecond is truncated (PostgreSQL's resolution).
	upd := contestUpdate(cc)
	upd.TokenMinInterval = 1500 * time.Nanosecond
	if got := must[q.Contest](t)(qs.UpdateContest(ctx, upd)); got.TokenMinInterval != time.Microsecond {
		t.Fatalf("1.5µs = %v, want 1µs", got.TokenMinInterval)
	}
}

// contestUpdate returns the parameters that rewrite c unchanged.
func contestUpdate(c q.Contest) q.UpdateContestParams {
	return q.UpdateContestParams{
		ID: c.ID, Name: c.Name, Description: c.Description, AllowedLocalizations: c.AllowedLocalizations,
		Languages: c.Languages, SubmissionsDownloadAllowed: c.SubmissionsDownloadAllowed,
		AllowQuestions: c.AllowQuestions, AllowUserTests: c.AllowUserTests,
		AllowUnofficialSubmissionBeforeAnalysisMode: c.AllowUnofficialSubmissionBeforeAnalysisMode,
		BlockHiddenParticipations:                   c.BlockHiddenParticipations,
		AllowPasswordAuthentication:                 c.AllowPasswordAuthentication,
		AllowRegistration:                           c.AllowRegistration, IpRestriction: c.IpRestriction, IpAutologin: c.IpAutologin,
		TokenMode: c.TokenMode, TokenMaxNumber: c.TokenMaxNumber, TokenMinInterval: c.TokenMinInterval,
		TokenGenInitial: c.TokenGenInitial, TokenGenNumber: c.TokenGenNumber, TokenGenInterval: c.TokenGenInterval,
		TokenGenMax: c.TokenGenMax, Timezone: c.Timezone, MaxSubmissionNumber: c.MaxSubmissionNumber,
		MaxUserTestNumber: c.MaxUserTestNumber, MinSubmissionInterval: c.MinSubmissionInterval,
		MinSubmissionIntervalGracePeriod: c.MinSubmissionIntervalGracePeriod,
		MinUserTestInterval:              c.MinUserTestInterval, ScorePrecision: c.ScorePrecision, MainGroupID: c.MainGroupID,
	}
}

func TestNetworkRoundTrip(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q
	c, g := f.contest("ioi")
	p := f.participation(c, g, f.user("alice"), nil)

	update := func(ip []netip.Prefix) q.Participation {
		t.Helper()
		return must[q.Participation](t)(qs.UpdateParticipation(ctx, q.UpdateParticipationParams{ID: p.ID, GroupID: g.ID, Ip: ip}))
	}
	isNull := func() (null, empty bool) {
		t.Helper()
		check(t, f.s.Pool.QueryRow(ctx, `SELECT ip IS NULL, coalesce(ip = '{}', false) FROM participations WHERE id = $1`, p.ID).Scan(&null, &empty))
		return null, empty
	}

	// cidr[]: values, both families, single hosts.
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.7/32"),
		netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001:db8::1/128"),
	}
	if got := update(want).Ip; !slices.Equal(got, want) {
		t.Fatalf("cidr[] %v came back as %v", want, got)
	}
	// A nil slice is NULL (no IP lock), an empty one is '{}' (no address
	// allowed), and they come back as such.
	if got := update(nil).Ip; got != nil {
		t.Fatalf("NULL cidr[] came back as %#v", got)
	}
	if null, _ := isNull(); !null {
		t.Fatal("a nil []netip.Prefix was not stored as NULL")
	}
	if got := update([]netip.Prefix{}).Ip; got == nil || len(got) != 0 {
		t.Fatalf("empty cidr[] came back as %#v", got)
	}
	if null, empty := isNull(); null || !empty {
		t.Fatal("an empty []netip.Prefix was not stored as '{}'")
	}
	// cidr rejects host bits: callers store Prefix.Masked().
	if _, err := qs.UpdateParticipation(ctx, q.UpdateParticipationParams{
		ID: p.ID, GroupID: g.ID, Ip: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/8")},
	}); err == nil {
		t.Fatal("cidr accepted 10.0.0.1/8")
	}

	// inet: both families and NULL.
	for _, s := range []string{"192.0.2.10", "2001:db8::10"} {
		addr := netip.MustParseAddr(s)
		e := must[q.AuditLog](t)(qs.CreateAuditEntry(ctx, q.CreateAuditEntryParams{At: t0, Action: "a", TargetType: "t", Ip: &addr}))
		if got := must[q.AuditLog](t)(qs.GetAuditEntry(ctx, e.ID)); got.Ip == nil || *got.Ip != addr {
			t.Fatalf("inet %v came back as %v", addr, got.Ip)
		}
	}
	e := must[q.AuditLog](t)(qs.CreateAuditEntry(ctx, q.CreateAuditEntryParams{At: t0, Action: "a", TargetType: "t"}))
	if e.Ip != nil {
		t.Fatalf("NULL inet = %v", *e.Ip)
	}
}

func TestJSONAndArrayRoundTrip(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q
	c, g := f.contest("ioi")
	tk, ds := f.task(c, 0, "aaa")

	// jsonb keeps the value (PostgreSQL normalises spaces and key order).
	params := json.RawMessage(`{"b": [1, 2.5, "x"], "a": {"nested": null}}`)
	upd := q.UpdateDatasetParams{ID: ds.ID, Description: "d", TaskType: "Batch", TaskTypeParameters: params,
		ScoreType: "Sum", ScoreTypeParameters: json.RawMessage(`[]`)}
	got := must[q.Dataset](t)(qs.UpdateDataset(ctx, upd))
	var a, b any
	check(t, json.Unmarshal(params, &a))
	check(t, json.Unmarshal(got.TaskTypeParameters, &b))
	if ja, jb := mustJSON(t, a), mustJSON(t, b); ja != jb {
		t.Fatalf("jsonb %s came back as %s", params, got.TaskTypeParameters)
	}
	// A nil json.RawMessage is NULL in a nullable column and comes back nil.
	s := f.submission(f.participation(c, g, f.user("alice"), nil), tk, t0)
	f.compile(s, ds, "ok")
	r := must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, q.GetSubmissionResultParams{SubmissionID: s.ID, DatasetID: ds.ID}))
	if r.ScoreDetails != nil || r.PublicScoreDetails != nil {
		t.Fatalf("NULL jsonb = %q, %q", r.ScoreDetails, r.PublicScoreDetails)
	}
	// In a NOT NULL jsonb column nil is refused.
	upd.ScoreTypeParameters = nil
	if _, err := qs.UpdateDataset(ctx, upd); err == nil {
		t.Fatal("NULL accepted in datasets.score_type_parameters")
	}

	// text[]: NOT NULL columns turn nil into '{}'; nullable ones keep NULL
	// and '{}' apart.
	task := must[q.Task](t)(qs.CreateTask(ctx, q.CreateTaskParams{
		Name: "zzz", Title: "Z", TokenMode: "disabled", TokenGenInterval: time.Minute,
		FeedbackLevel: "full", ScoreMode: "max", AllowedLanguages: []string{},
	}))
	if task.SubmissionFormat == nil || len(task.SubmissionFormat) != 0 || task.AllowedLanguages == nil || len(task.AllowedLanguages) != 0 {
		t.Fatalf("CreateTask arrays = %#v, %#v", task.SubmissionFormat, task.AllowedLanguages)
	}
	langs := []string{"C++20 / g++", `with "quotes", commas and \ backslash`, ""}
	task = must[q.Task](t)(qs.CreateTask(ctx, q.CreateTaskParams{
		Name: "yyy", Title: "Y", TokenMode: "disabled", TokenGenInterval: time.Minute,
		FeedbackLevel: "full", ScoreMode: "max", AllowedLanguages: langs,
	}))
	if !slices.Equal(task.AllowedLanguages, langs) {
		t.Fatalf("text[] %q came back as %q", langs, task.AllowedLanguages)
	}
	if got := must[q.Task](t)(qs.GetTask(ctx, tk.ID)); got.AllowedLanguages != nil {
		t.Fatalf("NULL text[] = %#v", got.AllowedLanguages)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	check(t, err)
	return string(b)
}

func TestTimestampRoundTrip(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q
	c, _ := f.contest("ioi")

	// Any location is stored as the same instant, with microseconds.
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	at := time.Date(2026, 8, 10, 14, 30, 15, 123456789, rome)
	a := must[q.Announcement](t)(qs.CreateAnnouncement(ctx, q.CreateAnnouncementParams{ContestID: c.ID, Timestamp: at, Subject: "s", Text: "t"}))
	got := must[q.Announcement](t)(qs.GetAnnouncement(ctx, a.ID))
	if want := at.Truncate(time.Microsecond); !got.Timestamp.Equal(want) {
		t.Fatalf("timestamptz %v came back as %v", want, got.Timestamp)
	}
	var utc string
	check(t, f.s.Pool.QueryRow(ctx, `SELECT timestamp::text FROM announcements WHERE id = $1`, a.ID).Scan(&utc))
	if utc != "2026-08-10 12:30:15.123456+00" {
		t.Fatalf("stored as %s, want the UTC instant (the connection runs in UTC)", utc)
	}
}

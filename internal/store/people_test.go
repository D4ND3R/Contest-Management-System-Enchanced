// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

func TestAdmins(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	a := must[q.Admin](t)(qs.CreateAdmin(ctx, q.CreateAdminParams{
		Name: "Root", Username: "root", Authentication: "plaintext:x", Enabled: true, PermissionAll: true,
	}))
	b := must[q.Admin](t)(qs.CreateAdmin(ctx, q.CreateAdminParams{
		Name: "Ann", Username: "ann", Authentication: "plaintext:y", Enabled: true, PermissionMessaging: true,
	}))
	if got := must[q.Admin](t)(qs.GetAdmin(ctx, a.ID)); got != a {
		t.Fatalf("GetAdmin = %+v, want %+v", got, a)
	}
	if got := must[q.Admin](t)(qs.GetAdminByUsername(ctx, "ann")); got.ID != b.ID {
		t.Fatalf("GetAdminByUsername = %+v", got)
	}
	list := must[[]q.Admin](t)(qs.ListAdmins(ctx))
	if len(list) != 2 || list[0].Username != "ann" || list[1].Username != "root" {
		t.Fatalf("ListAdmins = %+v, want ann, root", list)
	}
	if n := must[int64](t)(qs.CountSuperAdmins(ctx)); n != 1 {
		t.Fatalf("CountSuperAdmins = %d, want 1", n)
	}
	b = must[q.Admin](t)(qs.UpdateAdmin(ctx, q.UpdateAdminParams{
		ID: b.ID, Name: "Ann B", Username: "annb", Authentication: "plaintext:z",
		Enabled: true, PermissionAll: true, PermissionTasks: true,
	}))
	if b.Username != "annb" || !b.PermissionTasks || b.PermissionMessaging {
		t.Fatalf("UpdateAdmin = %+v", b)
	}
	if n := must[int64](t)(qs.CountSuperAdmins(ctx)); n != 2 {
		t.Fatalf("CountSuperAdmins = %d, want 2", n)
	}
	_, err := qs.CreateAdmin(ctx, q.CreateAdminParams{Name: "x", Username: "root", Authentication: "plaintext:"})
	if !store.IsUniqueViolation(err, "admins_username_key") {
		t.Fatalf("duplicate username: %v, want admins_username_key violation", err)
	}
	n, err := qs.DeleteAdmin(ctx, b.ID)
	wantRows(t, "DeleteAdmin", n, err, 1)
	n, err = qs.DeleteAdmin(ctx, b.ID)
	wantRows(t, "DeleteAdmin again", n, err, 0)
	_, err = qs.GetAdmin(ctx, b.ID)
	wantNoRows(t, "GetAdmin after delete", err)
	_, err = qs.UpdateAdmin(ctx, q.UpdateAdminParams{ID: b.ID, Name: "n", Username: "n", Authentication: "x"})
	wantNoRows(t, "UpdateAdmin of a deleted admin", err)
}

func TestContestsAndGroups(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, main := f.contest("ioi")
	other, _ := f.contest("apio")
	got := must[q.Contest](t)(qs.GetContest(ctx, c.ID))
	if got.Name != "ioi" || *got.MainGroupID != main.ID || got.TokenGenInterval != 30*time.Minute ||
		len(got.AllowedLocalizations) != 0 || got.AllowedLocalizations == nil {
		t.Fatalf("GetContest = %+v", got)
	}
	if got := must[q.Contest](t)(qs.GetContestByName(ctx, "apio")); got.ID != other.ID {
		t.Fatalf("GetContestByName = %+v", got)
	}
	_, err := qs.GetContestByName(ctx, "nope")
	wantNoRows(t, "GetContestByName", err)
	list := must[[]q.Contest](t)(qs.ListContests(ctx))
	if len(list) != 2 || list[0].Name != "apio" || list[1].Name != "ioi" {
		t.Fatalf("ListContests = %v", list)
	}

	late := must[q.Group](t)(qs.CreateGroup(ctx, groupParams(c.ID, "late")))
	late = must[q.Group](t)(qs.UpdateGroup(ctx, q.UpdateGroupParams{
		ID: late.ID, Name: "late-start", Start: t0.Add(time.Hour), Stop: t0.Add(6 * time.Hour),
		AnalysisEnabled: true, AnalysisStart: t0.Add(7 * time.Hour), AnalysisStop: t0.Add(9 * time.Hour),
		PerUserTime: ptr(90 * time.Minute),
	}))
	if late.Name != "late-start" || *late.PerUserTime != 90*time.Minute || !late.Start.Equal(t0.Add(time.Hour)) {
		t.Fatalf("UpdateGroup = %+v", late)
	}
	if got := must[q.Group](t)(qs.GetGroup(ctx, late.ID)); got.Name != late.Name || !got.AnalysisEnabled ||
		*got.PerUserTime != *late.PerUserTime || !got.AnalysisStop.Equal(late.AnalysisStop) {
		t.Fatalf("GetGroup = %+v, want %+v", got, late)
	}
	if got := must[q.Group](t)(qs.GetMainGroup(ctx, c.ID)); got.ID != main.ID {
		t.Fatalf("GetMainGroup = %+v", got)
	}
	groups := must[[]q.Group](t)(qs.ListGroupsByContest(ctx, c.ID))
	if len(groups) != 2 || groups[0].Name != "default" || groups[1].Name != "late-start" {
		t.Fatalf("ListGroupsByContest = %+v", groups)
	}

	upd := contestParams("ioi2026")
	c = must[q.Contest](t)(qs.UpdateContest(ctx, q.UpdateContestParams{
		ID: c.ID, Name: upd.Name, Description: "IOI", AllowedLocalizations: []string{"es", "en"},
		Languages: nil, TokenMode: "finite", TokenMaxNumber: ptr(int32(3)),
		TokenMinInterval: time.Minute, TokenGenInitial: 1, TokenGenNumber: 1, TokenGenInterval: time.Hour,
		TokenGenMax: ptr(int32(5)), Timezone: ptr("Europe/Rome"), MaxSubmissionNumber: ptr(int32(50)),
		MinSubmissionInterval: ptr(30 * time.Second), MinSubmissionIntervalGracePeriod: ptr(5 * time.Second),
		MinUserTestInterval: ptr(10 * time.Second), ScorePrecision: 2, MainGroupID: &late.ID,
	}))
	if c.Name != "ioi2026" || c.Languages == nil || len(c.Languages) != 0 || c.TokenMode != "finite" ||
		*c.MinSubmissionIntervalGracePeriod != 5*time.Second || *c.MainGroupID != late.ID ||
		len(c.AllowedLocalizations) != 2 {
		t.Fatalf("UpdateContest = %+v", c)
	}

	n, err := qs.DeleteGroup(ctx, late.ID)
	wantRows(t, "DeleteGroup", n, err, 1)
	if got := must[q.Contest](t)(qs.GetContest(ctx, c.ID)); got.MainGroupID != nil {
		t.Fatalf("deleting the main group left main_group_id = %d", *got.MainGroupID)
	}
	n, err = qs.DeleteContest(ctx, c.ID)
	wantRows(t, "DeleteContest", n, err, 1)
	_, err = qs.GetGroup(ctx, main.ID)
	wantNoRows(t, "GetGroup after the contest is deleted", err)
}

func TestUsersTeamsParticipations(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, g := f.contest("ioi")
	other, og := f.contest("apio")
	alice, bob, carol := f.user("alice"), f.user("bob"), f.user("carol")

	if got := must[q.User](t)(qs.GetUser(ctx, alice.ID)); got.Username != "alice" || got.PreferredLanguages == nil {
		t.Fatalf("GetUser = %+v", got)
	}
	if got := must[q.User](t)(qs.GetUserByUsername(ctx, "bob")); got.ID != bob.ID {
		t.Fatalf("GetUserByUsername = %+v", got)
	}
	carol = must[q.User](t)(qs.UpdateUser(ctx, q.UpdateUserParams{
		ID: carol.ID, FirstName: "Carol", LastName: "C", Username: "carol", Password: "bcrypt:x",
		Email: ptr("carol@example.org"), PreferredLanguages: []string{"es", "it"},
	}))
	if *carol.Email != "carol@example.org" || len(carol.PreferredLanguages) != 2 {
		t.Fatalf("UpdateUser = %+v", carol)
	}
	if users := must[[]q.User](t)(qs.ListUsers(ctx)); len(users) != 3 || users[0].Username != "alice" {
		t.Fatalf("ListUsers = %+v", users)
	}

	arg := must[q.Team](t)(qs.CreateTeam(ctx, q.CreateTeamParams{Code: "ARG", Name: "Argentina"}))
	ita := must[q.Team](t)(qs.CreateTeam(ctx, q.CreateTeamParams{Code: "ITA", Name: "Italy"}))
	if got := must[q.Team](t)(qs.GetTeam(ctx, arg.ID)); got != arg {
		t.Fatalf("GetTeam = %+v", got)
	}
	if got := must[q.Team](t)(qs.GetTeamByCode(ctx, "ITA")); got.ID != ita.ID {
		t.Fatalf("GetTeamByCode = %+v", got)
	}
	ita = must[q.Team](t)(qs.UpdateTeam(ctx, q.UpdateTeamParams{ID: ita.ID, Code: "ITA", Name: "Italia"}))
	if teams := must[[]q.Team](t)(qs.ListTeams(ctx)); len(teams) != 2 || teams[1].Name != "Italia" {
		t.Fatalf("ListTeams = %+v", teams)
	}

	pa := f.participation(c, g, alice, &arg.ID)
	pb := f.participation(c, g, bob, nil)
	f.participation(other, og, alice, nil)

	if got := must[q.Participation](t)(qs.GetParticipation(ctx, pa.ID)); got.UserID != alice.ID || got.Ip != nil {
		t.Fatalf("GetParticipation = %+v", got)
	}
	if got := must[q.Participation](t)(qs.GetParticipationByContestUser(ctx, q.GetParticipationByContestUserParams{
		ContestID: c.ID, UserID: bob.ID,
	})); got.ID != pb.ID {
		t.Fatalf("GetParticipationByContestUser = %+v", got)
	}
	cu := must[q.GetContestantRow](t)(qs.GetContestant(ctx, pa.ID))
	if cu.User.Username != "alice" || cu.Group.ID != g.ID || cu.Participation.ID != pa.ID {
		t.Fatalf("GetContestant = %+v", cu)
	}
	cb := must[q.GetContestantByUsernameRow](t)(qs.GetContestantByUsername(ctx, q.GetContestantByUsernameParams{
		ContestID: c.ID, Username: "bob",
	}))
	if cb.Participation.ID != pb.ID || cb.Group.Name != "default" {
		t.Fatalf("GetContestantByUsername = %+v", cb)
	}
	_, err := qs.GetContestantByUsername(ctx, q.GetContestantByUsernameParams{ContestID: c.ID, Username: "carol"})
	wantNoRows(t, "GetContestantByUsername of a non participant", err)

	// IP lock: bob may connect from 10.0.0.0/24 or 2001:db8::/32.
	pb = must[q.Participation](t)(qs.UpdateParticipation(ctx, q.UpdateParticipationParams{
		ID: pb.ID, GroupID: g.ID, TeamID: &ita.ID,
		Ip:        []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("2001:db8::/32")},
		DelayTime: time.Minute, ExtraTime: 10 * time.Minute, Password: ptr("plaintext:p"), Hidden: true,
	}))
	if len(pb.Ip) != 2 || pb.ExtraTime != 10*time.Minute || !pb.Hidden {
		t.Fatalf("UpdateParticipation = %+v", pb)
	}
	for addr, want := range map[string]int{"10.0.0.7": 1, "10.0.1.7": 0, "2001:db8::1": 1} {
		rows := must[[]q.ListContestantsByIPRow](t)(qs.ListContestantsByIP(ctx, q.ListContestantsByIPParams{
			ContestID: c.ID, Ip: netip.MustParseAddr(addr),
		}))
		if len(rows) != want || (want == 1 && rows[0].User.Username != "bob") {
			t.Fatalf("ListContestantsByIP(%s) = %+v, want %d rows", addr, rows, want)
		}
	}

	list := must[[]q.ListParticipationsByContestRow](t)(qs.ListParticipationsByContest(ctx, c.ID))
	if len(list) != 2 || list[0].User.Username != "alice" || *list[0].TeamCode != "ARG" ||
		*list[1].TeamName != "Italia" || list[1].GroupName != "default" {
		t.Fatalf("ListParticipationsByContest = %+v", list)
	}
	byUser := must[[]q.ListParticipationsByUserRow](t)(qs.ListParticipationsByUser(ctx, alice.ID))
	if len(byUser) != 2 || byUser[0].ContestName != "apio" || byUser[1].ContestName != "ioi" {
		t.Fatalf("ListParticipationsByUser = %+v", byUser)
	}
	notIn := must[[]q.User](t)(qs.ListUsersNotInContest(ctx, c.ID))
	if len(notIn) != 1 || notIn[0].Username != "carol" {
		t.Fatalf("ListUsersNotInContest = %+v", notIn)
	}

	// The window starts once.
	first := must[*time.Time](t)(qs.StartParticipation(ctx, q.StartParticipationParams{ID: pa.ID, StartingTime: t0}))
	again := must[*time.Time](t)(qs.StartParticipation(ctx, q.StartParticipationParams{ID: pa.ID, StartingTime: t0.Add(time.Hour)}))
	if !first.Equal(t0) || !again.Equal(t0) {
		t.Fatalf("StartParticipation = %v then %v, want %v twice", first, again, t0)
	}

	n, err := qs.DeleteTeam(ctx, arg.ID)
	wantRows(t, "DeleteTeam", n, err, 1)
	if got := must[q.Participation](t)(qs.GetParticipation(ctx, pa.ID)); got.TeamID != nil {
		t.Fatalf("deleting a team left team_id = %d", *got.TeamID)
	}
	n, err = qs.DeleteParticipation(ctx, pb.ID)
	wantRows(t, "DeleteParticipation", n, err, 1)
	n, err = qs.DeleteUser(ctx, alice.ID)
	wantRows(t, "DeleteUser", n, err, 1)
	_, err = qs.GetParticipation(ctx, pa.ID)
	wantNoRows(t, "participation of a deleted user", err)
}

func TestCommunication(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, g := f.contest("ioi")
	admin := must[q.Admin](t)(qs.CreateAdmin(ctx, q.CreateAdminParams{
		Name: "Root", Username: "root", Authentication: "plaintext:x", Enabled: true, PermissionAll: true,
	}))
	pa := f.participation(c, g, f.user("alice"), nil)
	pb := f.participation(c, g, f.user("bob"), nil)

	// Announcements.
	a1 := must[q.Announcement](t)(qs.CreateAnnouncement(ctx, q.CreateAnnouncementParams{
		ContestID: c.ID, AdminID: &admin.ID, Timestamp: t0, Subject: "Welcome", Text: "Good luck",
	}))
	a2 := must[q.Announcement](t)(qs.CreateAnnouncement(ctx, q.CreateAnnouncementParams{
		ContestID: c.ID, Timestamp: t0.Add(time.Hour), Subject: "Fix", Text: "Task A fixed",
	}))
	if got := must[q.Announcement](t)(qs.GetAnnouncement(ctx, a1.ID)); got.Subject != "Welcome" {
		t.Fatalf("GetAnnouncement = %+v", got)
	}
	ann := must[[]q.ListAnnouncementsByContestRow](t)(qs.ListAnnouncementsByContest(ctx, c.ID))
	if len(ann) != 2 || ann[0].Announcement.ID != a2.ID || ann[0].AdminUsername != nil || *ann[1].AdminUsername != "root" {
		t.Fatalf("ListAnnouncementsByContest = %+v", ann)
	}
	since := must[[]q.Announcement](t)(qs.ListAnnouncementsSince(ctx, q.ListAnnouncementsSinceParams{
		ContestID: c.ID, Until: t0.Add(2 * time.Hour),
	}))
	if len(since) != 2 || since[0].ID != a1.ID {
		t.Fatalf("ListAnnouncementsSince(nil) = %+v", since)
	}
	since = must[[]q.Announcement](t)(qs.ListAnnouncementsSince(ctx, q.ListAnnouncementsSinceParams{
		ContestID: c.ID, After: ptr(t0), Until: t0.Add(2 * time.Hour),
	}))
	if len(since) != 1 || since[0].ID != a2.ID {
		t.Fatalf("ListAnnouncementsSince(t0) = %+v", since)
	}
	a2 = must[q.Announcement](t)(qs.UpdateAnnouncement(ctx, q.UpdateAnnouncementParams{
		ID: a2.ID, Subject: "Fixed", Text: "Task A is fixed", AdminID: &admin.ID,
	}))
	if a2.Subject != "Fixed" || *a2.AdminID != admin.ID {
		t.Fatalf("UpdateAnnouncement = %+v", a2)
	}
	n, err := qs.DeleteAnnouncement(ctx, a1.ID)
	wantRows(t, "DeleteAnnouncement", n, err, 1)

	// Messages.
	m1 := must[q.Message](t)(qs.CreateMessage(ctx, q.CreateMessageParams{
		ParticipationID: pa.ID, AdminID: &admin.ID, Timestamp: t0, Subject: "Hi", Text: "Your screen",
	}))
	m2 := must[q.Message](t)(qs.CreateMessage(ctx, q.CreateMessageParams{
		ParticipationID: pa.ID, Timestamp: t0.Add(time.Minute), Subject: "Again", Text: "...",
	}))
	if got := must[q.Message](t)(qs.GetMessage(ctx, m1.ID)); got.Text != "Your screen" {
		t.Fatalf("GetMessage = %+v", got)
	}
	if ms := must[[]q.Message](t)(qs.ListMessagesByParticipation(ctx, pa.ID)); len(ms) != 2 || ms[0].ID != m2.ID {
		t.Fatalf("ListMessagesByParticipation = %+v", ms)
	}
	if ms := must[[]q.Message](t)(qs.ListMessagesSince(ctx, q.ListMessagesSinceParams{
		ParticipationID: pa.ID, After: ptr(t0), Until: t0.Add(time.Hour),
	})); len(ms) != 1 || ms[0].ID != m2.ID {
		t.Fatalf("ListMessagesSince = %+v", ms)
	}
	m2 = must[q.Message](t)(qs.UpdateMessage(ctx, q.UpdateMessageParams{ID: m2.ID, Subject: "Again!", Text: "x"}))
	if m2.Subject != "Again!" {
		t.Fatalf("UpdateMessage = %+v", m2)
	}
	n, err = qs.DeleteMessage(ctx, m1.ID)
	wantRows(t, "DeleteMessage", n, err, 1)

	// Questions.
	q1 := must[q.Question](t)(qs.CreateQuestion(ctx, q.CreateQuestionParams{
		ParticipationID: pa.ID, QuestionTimestamp: t0.Add(time.Minute), Subject: "A", Text: "Is n > 0?",
	}))
	q2 := must[q.Question](t)(qs.CreateQuestion(ctx, q.CreateQuestionParams{
		ParticipationID: pb.ID, QuestionTimestamp: t0.Add(2 * time.Minute), Subject: "B", Text: "?",
	}))
	if n := must[int64](t)(qs.CountUnansweredQuestions(ctx, c.ID)); n != 2 {
		t.Fatalf("CountUnansweredQuestions = %d, want 2", n)
	}
	q1 = must[q.Question](t)(qs.ReplyQuestion(ctx, q.ReplyQuestionParams{
		ID: q1.ID, ReplySubject: "Yes", ReplyText: "n >= 1", ReplyTimestamp: t0.Add(3 * time.Minute), AdminID: &admin.ID,
	}))
	if *q1.ReplyText != "n >= 1" || q1.Ignored {
		t.Fatalf("ReplyQuestion = %+v", q1)
	}
	q2 = must[q.Question](t)(qs.SetQuestionIgnored(ctx, q.SetQuestionIgnoredParams{ID: q2.ID, Ignored: true, AdminID: &admin.ID}))
	if !q2.Ignored {
		t.Fatalf("SetQuestionIgnored = %+v", q2)
	}
	if n := must[int64](t)(qs.CountUnansweredQuestions(ctx, c.ID)); n != 0 {
		t.Fatalf("CountUnansweredQuestions = %d, want 0", n)
	}
	if got := must[q.Question](t)(qs.GetQuestion(ctx, q2.ID)); !got.Ignored {
		t.Fatalf("GetQuestion = %+v", got)
	}
	if qq := must[[]q.Question](t)(qs.ListQuestionsByParticipation(ctx, pa.ID)); len(qq) != 1 || qq[0].ID != q1.ID {
		t.Fatalf("ListQuestionsByParticipation = %+v", qq)
	}
	all := must[[]q.ListQuestionsByContestRow](t)(qs.ListQuestionsByContest(ctx, c.ID))
	if len(all) != 2 || all[0].Question.ID != q2.ID || all[0].User.Username != "bob" ||
		all[1].Participation.ID != pa.ID || *all[1].AdminUsername != "root" {
		t.Fatalf("ListQuestionsByContest = %+v", all)
	}
	replies := must[[]q.Question](t)(qs.ListRepliesSince(ctx, q.ListRepliesSinceParams{
		ParticipationID: pa.ID, Until: t0.Add(time.Hour),
	}))
	if len(replies) != 1 || replies[0].ID != q1.ID {
		t.Fatalf("ListRepliesSince = %+v", replies)
	}
	replies = must[[]q.Question](t)(qs.ListRepliesSince(ctx, q.ListRepliesSinceParams{
		ParticipationID: pa.ID, After: ptr(t0.Add(3 * time.Minute)), Until: t0.Add(time.Hour),
	}))
	if len(replies) != 0 {
		t.Fatalf("ListRepliesSince after the reply = %+v", replies)
	}
	n, err = qs.DeleteQuestion(ctx, q2.ID)
	wantRows(t, "DeleteQuestion", n, err, 1)

	// Deleting the admin keeps their rows, without author.
	n, err = qs.DeleteAdmin(ctx, admin.ID)
	wantRows(t, "DeleteAdmin", n, err, 1)
	if got := must[q.Question](t)(qs.GetQuestion(ctx, q1.ID)); got.AdminID != nil {
		t.Fatalf("question admin_id = %d after deleting the admin", *got.AdminID)
	}
}

func TestServerSettings(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	s := must[q.ServerSetting](t)(qs.SetServerSetting(ctx, q.SetServerSettingParams{Key: "timezone", Value: []byte(`"UTC"`)}))
	s2 := must[q.ServerSetting](t)(qs.SetServerSetting(ctx, q.SetServerSettingParams{Key: "timezone", Value: []byte(`"America/Argentina/Buenos_Aires"`)}))
	if string(s2.Value) != `"America/Argentina/Buenos_Aires"` || s2.UpdatedAt.Before(s.UpdatedAt) {
		t.Fatalf("SetServerSetting = %+v then %+v", s, s2)
	}
	must[q.ServerSetting](t)(qs.SetServerSetting(ctx, q.SetServerSettingParams{Key: "contest_web.url", Value: []byte(`":8888"`)}))
	if got := must[q.ServerSetting](t)(qs.GetServerSetting(ctx, "timezone")); string(got.Value) != string(s2.Value) {
		t.Fatalf("GetServerSetting = %+v", got)
	}
	list := must[[]q.ServerSetting](t)(qs.ListServerSettings(ctx))
	if len(list) != 2 || list[0].Key != "contest_web.url" {
		t.Fatalf("ListServerSettings = %+v", list)
	}
	n, err := qs.DeleteServerSetting(ctx, "timezone")
	wantRows(t, "DeleteServerSetting", n, err, 1)
}

func TestAuditLog(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, _ := f.contest("ioi")
	admin := must[q.Admin](t)(qs.CreateAdmin(ctx, q.CreateAdminParams{
		Name: "Root", Username: "root", Authentication: "plaintext:x", Enabled: true, PermissionAll: true,
	}))
	ip := netip.MustParseAddr("192.0.2.10")
	var ids []int64
	for i := range 5 {
		e := must[q.AuditLog](t)(qs.CreateAuditEntry(ctx, q.CreateAuditEntryParams{
			// Two entries share a time: the id breaks the tie.
			At:      t0.Add(time.Duration(i/2) * time.Minute),
			AdminID: &admin.ID, Action: "task.update", TargetType: "task", TargetID: ptr(int64(7)),
			ContestID: &c.ID, Details: json.RawMessage(fmt.Sprintf(`{"admin":"root","i":%d}`, i)), Ip: &ip,
		}))
		ids = append(ids, e.ID)
	}
	// nil details store {}.
	other := must[q.AuditLog](t)(qs.CreateAuditEntry(ctx, q.CreateAuditEntryParams{
		At: t0.Add(time.Hour), Action: "server.update", TargetType: "server",
	}))
	if string(other.Details) != "{}" || other.Ip != nil || other.AdminID != nil {
		t.Fatalf("CreateAuditEntry with nil details = %+v", other)
	}
	if got := must[q.AuditLog](t)(qs.GetAuditEntry(ctx, ids[0])); *got.Ip != ip || *got.TargetID != 7 {
		t.Fatalf("GetAuditEntry = %+v", got)
	}

	// Pages of two, newest first.
	var seen []int64
	var before *q.AuditLog
	for {
		arg := q.ListAuditLogByContestParams{ContestID: c.ID, PageSize: 2}
		if before != nil {
			arg.BeforeAt, arg.BeforeID = &before.At, &before.ID
		}
		page := must[[]q.ListAuditLogByContestRow](t)(qs.ListAuditLogByContest(ctx, arg))
		if len(page) == 0 {
			break
		}
		for _, r := range page {
			if *r.AdminUsername != "root" {
				t.Fatalf("admin username = %v", r.AdminUsername)
			}
			seen = append(seen, r.AuditLog.ID)
		}
		before = &page[len(page)-1].AuditLog
	}
	if len(seen) != 5 {
		t.Fatalf("ListAuditLogByContest pages = %v, want 5 entries", seen)
	}
	for i, id := range seen {
		if id != ids[4-i] {
			t.Fatalf("ListAuditLogByContest order = %v, want %v reversed", seen, ids)
		}
	}
	all := must[[]q.ListAuditLogRow](t)(qs.ListAuditLog(ctx, q.ListAuditLogParams{PageSize: 10}))
	if len(all) != 6 || all[0].AuditLog.ID != other.ID || all[0].AdminUsername != nil {
		t.Fatalf("ListAuditLog = %+v", all)
	}
	rest := must[[]q.ListAuditLogRow](t)(qs.ListAuditLog(ctx, q.ListAuditLogParams{
		BeforeAt: &all[2].AuditLog.At, BeforeID: &all[2].AuditLog.ID, PageSize: 10,
	}))
	if len(rest) != 3 || rest[0].AuditLog.ID != all[3].AuditLog.ID {
		t.Fatalf("ListAuditLog after the third = %+v", rest)
	}
	byTarget := must[[]q.ListAuditLogByTargetRow](t)(qs.ListAuditLogByTarget(ctx, q.ListAuditLogByTargetParams{
		TargetType: "task", TargetID: 7, PageSize: 3,
	}))
	if len(byTarget) != 3 || byTarget[0].AuditLog.ID != ids[4] {
		t.Fatalf("ListAuditLogByTarget = %+v", byTarget)
	}
	byAdmin := must[[]q.ListAuditLogByAdminRow](t)(qs.ListAuditLogByAdmin(ctx, q.ListAuditLogByAdminParams{
		AdminID: admin.ID, PageSize: 100,
	}))
	if len(byAdmin) != 5 {
		t.Fatalf("ListAuditLogByAdmin = %d rows, want 5", len(byAdmin))
	}

	// Deleting the contest and the admin keeps the log.
	_, err := qs.DeleteContest(ctx, c.ID)
	check(t, err)
	_, err = qs.DeleteAdmin(ctx, admin.ID)
	check(t, err)
	if got := must[q.AuditLog](t)(qs.GetAuditEntry(ctx, ids[0])); got.ContestID != nil || got.AdminID != nil {
		t.Fatalf("audit entry after deletes = %+v", got)
	}
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/contestuser.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2017 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Peyman Jabbarzade Ganje <peyman.jabarzade@gmail.com>
// Copyright © 2017 Valentin Rosca <rosca.valentin2012@gmail.com>
// Copyright © 2021 Manuel Gundlach <manuel.gundlach@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Chuyang Wang <mail@chuyang-wang.de>

package adminweb

// The users of a contest and the participation page (work A): add a user
// to a contest (in a group), remove a participation, and edit one (group,
// team, password, hidden, unrestricted, IP, starting time, delay, extra
// time); the page also shows the participation's submissions, questions
// and private messages (the message form is in communication.go).

import (
	"errors"
	"html/template"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Bounds of the participation page and of the selectors of the users
// page.
const (
	participationQuestionsMax = 500
	participationMessagesMax  = 500
	selectorMax               = 5000
)

// participationSubmissionsHook renders the submissions table of the
// participation page (CMS macro_submission.rows over the submissions of
// the participation, paginated by ?page=): submissions are work C's
// (submissions.go sets it). Nil: the page shows only the count and the
// reevaluation buttons.
var participationSubmissionsHook func(c *Ctx, contestID, participationID int32) (template.HTML, error)

// contestUsersData is the data of contest_users.html.
type contestUsersData struct {
	*Page
	Users      []q.AdminContestParticipationsPageRow
	Unassigned []q.AdminUsersNotInContestRow
	Groups     []groupRow
	Pager      *Pager
}

// contestUsersPage is GET /contest/<id>/users (CMS ContestUsersHandler).
func (s *Server) contestUsersPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	ctx, cid := c.Context(), c.Contest.ID
	n, err := s.st.Q.AdminContestParticipationsCount(ctx, cid)
	if err != nil {
		c.Fail(err)
		return
	}
	pg := c.Pager(n, peoplePageSize)
	users, err := s.st.Q.AdminContestParticipationsPage(ctx, q.AdminContestParticipationsPageParams{
		ContestID: cid, Lim: int32(pg.Size), Off: int32(pg.Offset())})
	if err != nil {
		c.Fail(err)
		return
	}
	free, err := s.st.Q.AdminUsersNotInContest(ctx, q.AdminUsersNotInContestParams{ContestID: cid, Lim: selectorMax})
	if err != nil {
		c.Fail(err)
		return
	}
	groups, err := s.st.Q.AdminContestGroups(ctx, cid)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("contest_users.html", contestUsersData{Page: p, Users: users, Unassigned: free, Groups: c.groupRows(groups), Pager: pg})
}

// contestUsersPost is POST /contest/<id>/users: "Remove from contest"
// opens the confirmation page.
func (s *Server) contestUsersPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	uid, err := strconv.ParseInt(c.ArgDefault("user_id", ""), 10, 32)
	op, _ := c.Arg("operation")
	if err != nil || !c.isOp(op, opRemoveFromContest) {
		msg := "Please select a valid operation"
		if err != nil {
			msg = "Please select a valid user"
		}
		c.NotifyError(N("Invalid field(s)"), msg)
		c.Redirect(c.URL("contest", cid, "users"))
		return
	}
	c.Redirect(c.URL("contest", cid, "user", uid, "remove"))
}

// addContestUserPost is POST /contest/<id>/users/add (CMS
// AddContestUserHandler): the selected user in the selected group.
func (s *Server) addContestUserPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	fallback := c.URL("contest", cid, "users")
	uid, err1 := strconv.ParseInt(c.ArgDefault("user_id", ""), 10, 32)
	gid, err2 := strconv.ParseInt(c.ArgDefault("group_id", ""), 10, 32)
	if err1 != nil || err2 != nil {
		msg := "Please select a valid user"
		if err1 == nil {
			msg = "Please select a valid group"
		}
		c.NotifyError(N("Invalid field(s)"), msg)
		c.Redirect(fallback)
		return
	}
	err := c.Write(func(tx *Tx) error {
		if _, err := tx.Q.GetContest(tx.Ctx, cid); err != nil {
			return err
		}
		if _, err := tx.Q.GetUser(tx.Ctx, int32(uid)); err != nil {
			return err
		}
		g, err := tx.Q.GetGroup(tx.Ctx, int32(gid))
		if err != nil {
			return err
		}
		if g.ContestID != cid {
			return Invalid("The group does not belong to the contest.")
		}
		return createParticipation(tx, q.CreateParticipationParams{ContestID: cid, UserID: int32(uid), GroupID: g.ID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(fallback)
}

// removeParticipationData is the data of participation_remove.html.
type removeParticipationData struct {
	*Page
	User        q.User
	Submissions int64
}

// removeParticipationPage is GET /contest/<id>/user/<id>/remove (CMS
// RemoveParticipationHandler).
func (s *Server) removeParticipationPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	uid, _ := c.PathID("user_id")
	row, err := s.st.Q.AdminParticipation(c.Context(), q.AdminParticipationParams{ContestID: c.Contest.ID, UserID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("participation_remove.html", removeParticipationData{Page: p, User: row.User, Submissions: row.Submissions})
}

// removeParticipationDelete is DELETE /contest/<id>/user/<id>/remove.
func (s *Server) removeParticipationDelete(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	uid, _ := c.PathID("user_id")
	err := c.Write(func(tx *Tx) error { return deleteParticipation(tx, cid, uid) })
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.WriteText("../../users")
}

// participationView is a participation as participation.html shows it.
type participationView struct {
	q.Participation
}

// IPs is the list of networks as the form shows it ("" for none).
func (v participationView) IPs() string {
	if v.Ip == nil {
		return ""
	}
	out := make([]string, len(v.Ip))
	for i, p := range v.Ip {
		out[i] = p.String()
	}
	return strings.Join(out, ", ")
}

// DelaySec is delay_time in whole seconds.
func (v participationView) DelaySec() string { return secs(v.DelayTime) }

// ExtraSec is extra_time in whole seconds.
func (v participationView) ExtraSec() string { return secs(v.ExtraTime) }

// participationData is the data of participation.html.
type participationData struct {
	*Page
	P         participationView
	User      q.User
	TeamCode  string
	Groups    []q.AdminContestGroupsRow
	TeamCodes []string
	// SubmissionCount and SubmissionsHTML are the submissions section.
	SubmissionCount int64
	SubmissionsHTML template.HTML
	Questions       []questionView
	Messages        []q.AdminParticipationMessagesRow
	QuickAnswers    []quickAnswer
	TZ              string
}

// participationPage is GET /contest/<id>/user/<id>/edit (CMS
// ParticipationHandler.get).
func (s *Server) participationPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	ctx, cid := c.Context(), c.Contest.ID
	uid, _ := c.PathID("user_id")
	row, err := s.st.Q.AdminParticipation(ctx, q.AdminParticipationParams{ContestID: cid, UserID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	groups, err := s.st.Q.AdminContestGroups(ctx, cid)
	if err != nil {
		c.Fail(err)
		return
	}
	teams, err := s.st.Q.AdminTeamCodes(ctx, selectorMax)
	if err != nil {
		c.Fail(err)
		return
	}
	qs, err := s.st.Q.AdminParticipationQuestions(ctx, q.AdminParticipationQuestionsParams{
		ParticipationID: row.Participation.ID, Lim: participationQuestionsMax})
	if err != nil {
		c.Fail(err)
		return
	}
	msgs, err := s.st.Q.AdminParticipationMessages(ctx, q.AdminParticipationMessagesParams{
		ParticipationID: row.Participation.ID, Lim: participationMessagesMax})
	if err != nil {
		c.Fail(err)
		return
	}
	var subs template.HTML
	if participationSubmissionsHook != nil && row.Submissions > 0 {
		if subs, err = participationSubmissionsHook(c, cid, row.Participation.ID); err != nil {
			c.Fail(err)
			return
		}
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	views := make([]questionView, len(qs))
	for i, r := range qs {
		views[i] = questionView{Question: r.Question, AdminName: r.AdminName}
	}
	c.Render("participation.html", participationData{Page: p, P: participationView{row.Participation}, User: row.User,
		TeamCode: deref(row.TeamCode), Groups: groups, TeamCodes: teams, SubmissionCount: row.Submissions,
		SubmissionsHTML: subs, Questions: views, Messages: msgs, QuickAnswers: c.quickAnswers(),
		TZ: c.RC.TZ.String()})
}

// participationPost is POST /contest/<id>/user/<id>/edit (CMS
// ParticipationHandler.post).
func (s *Server) participationPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	uid, _ := c.PathID("user_id")
	page := c.URL("contest", cid, "user", uid, "edit")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetParticipationByContestUser(tx.Ctx, q.GetParticipationByContestUserParams{ContestID: cid, UserID: uid})
		if err != nil {
			return err
		}
		p := old
		f := c.Form()
		f.Password(&p.Password, deref(old.Password), true)
		f.IPNetworks(&p.Ip, "ip")
		f.OptDatetime(&p.StartingTime, "starting_time")
		f.Duration(&p.DelayTime, "delay_time")
		f.Duration(&p.ExtraTime, "extra_time")
		f.Bool(&p.Hidden, "hidden")
		f.Bool(&p.Unrestricted, "unrestricted")
		gid, gerr := strconv.ParseInt(c.ArgDefault("group_id", ""), 10, 32)
		if gerr != nil {
			f.Fail("Please select a valid group")
		}
		if err := f.Err(); err != nil {
			return &FieldError{Msg: err.Error()}
		}
		g, err := tx.Q.GetGroup(tx.Ctx, int32(gid))
		if errors.Is(err, pgx.ErrNoRows) || err == nil && g.ContestID != cid {
			return Invalid("Group %d does not exist in this contest", gid)
		}
		if err != nil {
			return err
		}
		p.GroupID = g.ID
		p.TeamID = nil
		if code := c.ArgDefault("team", ""); code != "" {
			t, err := tx.Q.GetTeamByCode(tx.Ctx, code)
			if errors.Is(err, pgx.ErrNoRows) {
				return Invalid("Team with code '%s' does not exist", code)
			}
			if err != nil {
				return err
			}
			p.TeamID = &t.ID
		}
		upd, err := tx.Q.UpdateParticipation(tx.Ctx, q.UpdateParticipationParams{ID: p.ID, GroupID: p.GroupID,
			TeamID: p.TeamID, Ip: p.Ip, StartingTime: p.StartingTime, DelayTime: p.DelayTime, ExtraTime: p.ExtraTime,
			Password: p.Password, Hidden: p.Hidden, Unrestricted: p.Unrestricted})
		if err != nil {
			return err
		}
		tx.Publish(live.ParticipationChange{Contest: int64(cid), Participation: int64(p.ID)}.Event(),
			live.ScoreboardChange{Contest: int64(cid), All: old.Hidden != upd.Hidden || !ptrEq(old.TeamID, upd.TeamID),
				Participations: []int64{int64(p.ID)}}.Event())
		return tx.Audit(AuditEntry{Action: "participation.update", TargetType: "participation", TargetID: int64(p.ID),
			ContestID: cid, Before: old, After: upd})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		c.NotifyError(N("Invalid field(s)"), fe.Msg)
		c.Redirect(page)
		return
	}
	c.Commit(err)
	c.Redirect(page)
}

// ptrEq reports whether two optional values are equal.
func ptrEq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

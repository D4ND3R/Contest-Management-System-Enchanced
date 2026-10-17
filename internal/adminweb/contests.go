// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/contest.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>
// Copyright © 2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Chuyang Wang <mail@chuyang-wang.de>
// Copyright © 2026 Jonathan Baumann <Jonathan.Baumann@edu.ruhr-uni-bochum.de>

package adminweb

// Contests (work A): the list, creation, removal and the settings page of
// a contest with its main group. Groups are in contests_groups.go and the
// task list of a contest in contests_tasks.go.
//
// Every change publishes live.ContestChange (contestant pages reload what
// depends on the settings; the admin pages of the contest too) and, when
// it can change the scoreboard (participations, tasks, hidden flags),
// live.ScoreboardChange{All: true}: the hook where CMS calls
// ProxyService.reinitialize (the ranking push is F7's).

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// isOp reports whether the "operation" of a form (the value of the
// submit button the admin pressed) is msgid: the buttons show it
// translated, CMS compares it in English.
func (c *Ctx) isOp(op, msgid string) bool { return op == msgid || op == c.T(msgid) }

// contestSettingsEvents are the live events of a change of the settings of a
// contest.
func contestSettingsEvents(contestID int32) []live.Event {
	return []live.Event{
		live.ContestChange{Contest: int64(contestID)}.Event(),
		live.ScoreboardChange{Contest: int64(contestID), All: true}.Event(),
	}
}

// secs is a duration as CMS's forms show total_seconds()|int.
func secs(d time.Duration) string { return strconv.FormatInt(int64(d/time.Second), 10) }

// mins is a duration in minutes as the forms show it: CMS's integer
// division for whole minutes, the fraction otherwise (an imported
// interval of 30 seconds would show as 0 and the form would not save).
func mins(d time.Duration) string {
	if d%time.Minute == 0 {
		return strconv.FormatInt(int64(d/time.Minute), 10)
	}
	return strconv.FormatFloat(d.Minutes(), 'f', -1, 64)
}

// optSecs is secs of a nullable duration, "" for nil.
func optSecs(d *time.Duration) string {
	if d == nil {
		return ""
	}
	return secs(*d)
}

// optInt is a nullable integer as the forms show it ("" for nil).
func optInt(n *int32) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(int(*n))
}

// contestsData is the data of contests.html.
type contestsData struct {
	*Page
	Contests []q.AdminContestsListRow
}

// contestsPage is GET /contests (CMS ContestListHandler).
func (s *Server) contestsPage(c *Ctx) {
	rows, err := s.st.Q.AdminContestsList(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("contests.html", contestsData{Page: p, Contests: rows})
}

// contestsPost is POST /contests: the Remove button of the list and of
// the contest page opens the confirmation page.
func (s *Server) contestsPost(c *Ctx) {
	id, _ := c.Arg("contest_id")
	op, _ := c.Arg("operation")
	if c.isOp(op, "Remove") {
		if _, err := strconv.ParseInt(id, 10, 32); err != nil {
			c.BadRequest()
			return
		}
		c.Redirect(c.URL("contests", id, "remove"))
		return
	}
	c.NotifyError(N("Invalid operation"), op)
	c.Redirect(c.URL("contests"))
}

// removeContestData is the data of contest_remove.html.
type removeContestData struct {
	*Page
	Participations int64
	Submissions    int64
}

// removeContestPage is GET /contests/<id>/remove (CMS RemoveContestHandler).
func (s *Server) removeContestPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	n, err := s.st.Q.AdminContestRemoveCounts(c.Context(), c.Contest.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("contest_remove.html", removeContestData{Page: p, Participations: n.Participations, Submissions: n.Submissions})
}

// removeContestDelete is DELETE /contests/<id>/remove: the contest and
// everything in it (groups, participations, tasks, submissions...).
func (s *Server) removeContestDelete(c *Ctx) {
	id, _ := c.PathID("contest_id")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetContest(tx.Ctx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Q.DeleteContest(tx.Ctx, id); err != nil {
			return err
		}
		tx.Publish(live.ScoreboardChange{Contest: int64(id), All: true}.Event())
		return tx.Audit(AuditEntry{Action: "contest.delete", TargetType: "contest", TargetID: int64(id),
			Before: old})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.WriteText("../../contests")
}

// addContestPage is GET /contests/add.
func (s *Server) addContestPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("add_contest.html", p)
}

// addContestPost is POST /contests/add (CMS AddContestHandler): a contest
// named and described by the name, with a main group "default".
func (s *Server) addContestPost(c *Ctx) {
	fallback := c.URL("contests", "add")
	f := c.Form()
	name := f.Required("name", "contest name")
	if err := f.Err(); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	var created q.Contest
	err := c.Write(func(tx *Tx) error {
		ct, err := tx.Q.AdminCreateContest(tx.Ctx, name)
		if err != nil {
			return err
		}
		g, err := tx.Q.AdminCreateDefaultGroup(tx.Ctx, ct.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Q.SetContestMainGroup(tx.Ctx, q.SetContestMainGroupParams{ID: ct.ID, MainGroupID: &g.ID}); err != nil {
			return err
		}
		ct.MainGroupID = &g.ID
		created = ct
		return tx.Audit(AuditEntry{Action: "contest.create", TargetType: "contest", TargetID: int64(ct.ID),
			ContestID: ct.ID, After: ct})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("contest", created.ID))
	} else {
		c.Redirect(fallback)
	}
}

// contestView is a contest as contest.html shows it.
type contestView struct {
	q.Contest
}

// HasLanguage reports whether the contest allows a programming language.
func (v contestView) HasLanguage(name string) bool { return slices.Contains(v.Languages, name) }

// TokenMinIntervalSec and the methods below are the values of the form
// (CMS total_seconds()|int and "" for None).
func (v contestView) TokenMinIntervalSec() string { return secs(v.TokenMinInterval) }

// TokenGenIntervalMin is token_gen_interval in minutes.
func (v contestView) TokenGenIntervalMin() string { return mins(v.TokenGenInterval) }

// TokenMaxNumberStr is token_max_number ("" for none).
func (v contestView) TokenMaxNumberStr() string { return optInt(v.TokenMaxNumber) }

// TokenGenMaxStr is token_gen_max ("" for none).
func (v contestView) TokenGenMaxStr() string { return optInt(v.TokenGenMax) }

// MaxSubmissionNumberStr is max_submission_number ("" for none).
func (v contestView) MaxSubmissionNumberStr() string { return optInt(v.MaxSubmissionNumber) }

// MaxUserTestNumberStr is max_user_test_number ("" for none).
func (v contestView) MaxUserTestNumberStr() string { return optInt(v.MaxUserTestNumber) }

// MinSubmissionIntervalSec is min_submission_interval ("" for none).
func (v contestView) MinSubmissionIntervalSec() string { return optSecs(v.MinSubmissionInterval) }

// MinSubmissionIntervalGracePeriodSec is the grace period ("" for none).
func (v contestView) MinSubmissionIntervalGracePeriodSec() string {
	return optSecs(v.MinSubmissionIntervalGracePeriod)
}

// MinUserTestIntervalSec is min_user_test_interval ("" for none).
func (v contestView) MinUserTestIntervalSec() string { return optSecs(v.MinUserTestInterval) }

// TimezoneStr is the time zone of the contest ("" for none).
func (v contestView) TimezoneStr() string { return deref(v.Timezone) }

// contestData is the data of contest.html.
type contestData struct {
	*Page
	C         contestView
	Languages []string
	// Group is the main group (fragments/group_settings.html), nil if
	// the contest has none.
	Group *q.Group
	// TZ names the server time zone (the "(in UTC)" of CMS's labels).
	TZ string
}

// contestPage is GET /contest/<id> (CMS ContestHandler.get).
func (s *Server) contestPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	langs := slices.Clone(s.langs.Names())
	slices.Sort(langs)
	c.Render("contest.html", contestData{Page: p, C: contestView{c.Contest.Contest}, Languages: langs,
		Group: c.Contest.MainGroup, TZ: c.RC.TZ.String()})
}

// groupSettings reads the times of a group (CMS get_group_settings) over
// g.
func groupSettings(f *Form, g *q.Group) {
	if v, _ := f.Get("start"); v == "" {
		f.Fail("No main group start time specified.")
	}
	f.Datetime(&g.Start, "start")
	if v, _ := f.Get("stop"); v == "" {
		f.Fail("No main group stop time specified.")
	}
	f.Datetime(&g.Stop, "stop")
	f.OptDuration(&g.PerUserTime, "per_user_time")
	f.Bool(&g.AnalysisEnabled, "analysis_enabled")
	f.Datetime(&g.AnalysisStart, "analysis_start")
	f.Datetime(&g.AnalysisStop, "analysis_stop")
}

// contestAttrs reads the form of contest.html over ct (CMS
// ContestHandler.post).
func (c *Ctx) contestAttrs(f *Form, ct *q.Contest) {
	name := f.Required("name", "contest name")
	ct.Name = name
	f.String(&ct.Description, "description")
	ct.AllowedLocalizations = ParseStringList(c.ArgDefault("allowed_localizations", ""))
	ct.Languages = web.Args(c.R, "languages")
	if ct.Languages == nil {
		ct.Languages = []string{}
	}
	f.Bool(&ct.SubmissionsDownloadAllowed, "submissions_download_allowed")
	f.Bool(&ct.AllowQuestions, "allow_questions")
	f.Bool(&ct.AllowUserTests, "allow_user_tests")
	f.Bool(&ct.AllowUnofficialSubmissionBeforeAnalysisMode, "allow_unofficial_submission_before_analysis_mode")
	f.Bool(&ct.BlockHiddenParticipations, "block_hidden_participations")
	f.Bool(&ct.AllowPasswordAuthentication, "allow_password_authentication")
	f.Bool(&ct.AllowRegistration, "allow_registration")
	f.Bool(&ct.IpRestriction, "ip_restriction")
	f.Bool(&ct.IpAutologin, "ip_autologin")

	f.String(&ct.TokenMode, "token_mode")
	f.OptInt32(&ct.TokenMaxNumber, "token_max_number")
	f.Duration(&ct.TokenMinInterval, "token_min_interval")
	f.Int32(&ct.TokenGenInitial, "token_gen_initial")
	f.Int32(&ct.TokenGenNumber, "token_gen_number")
	f.DurationMin(&ct.TokenGenInterval, "token_gen_interval")
	f.OptInt32(&ct.TokenGenMax, "token_gen_max")

	f.OptInt32(&ct.MaxSubmissionNumber, "max_submission_number")
	f.OptInt32(&ct.MaxUserTestNumber, "max_user_test_number")
	f.OptDuration(&ct.MinSubmissionInterval, "min_submission_interval")
	f.OptDuration(&ct.MinSubmissionIntervalGracePeriod, "min_submission_interval_grace_period")
	f.OptDuration(&ct.MinUserTestInterval, "min_user_test_interval")

	f.OptString(&ct.Timezone, "timezone")
	f.Int32(&ct.ScorePrecision, "score_precision")
	switch ct.TokenMode {
	case "disabled", "finite", "infinite":
	default:
		f.Fail("Invalid token mode %s.", ct.TokenMode)
	}
	if ct.Timezone != nil {
		if _, err := time.LoadLocation(*ct.Timezone); err != nil {
			f.Fail("Unknown time zone %s.", *ct.Timezone)
		}
	}
}

// contestPost is POST /contest/<id> (CMS ContestHandler.post): the
// settings of the contest and the times of its main group.
func (s *Server) contestPost(c *Ctx) {
	id, _ := c.PathID("contest_id")
	page := c.URL("contest", id)
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetContest(tx.Ctx, id)
		if err != nil {
			return err
		}
		ct := old
		f := c.Form()
		c.contestAttrs(f, &ct)
		var oldGroup, group q.Group
		if ct.MainGroupID != nil {
			if oldGroup, err = tx.Q.GetGroup(tx.Ctx, *ct.MainGroupID); err != nil {
				return err
			}
			group = oldGroup
			groupSettings(f, &group)
		}
		if err := f.Err(); err != nil {
			return &FieldError{Msg: err.Error()}
		}
		upd, err := tx.Q.UpdateContest(tx.Ctx, updateContestParams(ct))
		if err != nil {
			return err
		}
		if ct.MainGroupID != nil {
			g, err := tx.Q.UpdateGroup(tx.Ctx, updateGroupParams(group))
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(g, oldGroup) {
				if err := tx.Audit(AuditEntry{Action: "group.update", TargetType: "group", TargetID: int64(g.ID),
					ContestID: id, Before: oldGroup, After: g}); err != nil {
					return err
				}
			}
		}
		tx.Publish(contestSettingsEvents(id)...)
		return tx.Audit(AuditEntry{Action: "contest.update", TargetType: "contest", TargetID: int64(id),
			ContestID: id, Before: old, After: upd})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		c.NotifyError(N("Invalid field(s)."), fe.Msg)
		c.Redirect(page)
		return
	}
	c.Commit(err)
	c.Redirect(page)
}

// updateContestParams are the parameters that write ct whole.
func updateContestParams(ct q.Contest) q.UpdateContestParams {
	return q.UpdateContestParams{
		ID:                         ct.ID,
		Name:                       ct.Name,
		Description:                ct.Description,
		AllowedLocalizations:       ct.AllowedLocalizations,
		Languages:                  ct.Languages,
		SubmissionsDownloadAllowed: ct.SubmissionsDownloadAllowed,
		AllowQuestions:             ct.AllowQuestions,
		AllowUserTests:             ct.AllowUserTests,
		AllowUnofficialSubmissionBeforeAnalysisMode: ct.AllowUnofficialSubmissionBeforeAnalysisMode,
		BlockHiddenParticipations:                   ct.BlockHiddenParticipations,
		AllowPasswordAuthentication:                 ct.AllowPasswordAuthentication,
		AllowRegistration:                           ct.AllowRegistration,
		IpRestriction:                               ct.IpRestriction,
		IpAutologin:                                 ct.IpAutologin,
		TokenMode:                                   ct.TokenMode,
		TokenMaxNumber:                              ct.TokenMaxNumber,
		TokenMinInterval:                            ct.TokenMinInterval,
		TokenGenInitial:                             ct.TokenGenInitial,
		TokenGenNumber:                              ct.TokenGenNumber,
		TokenGenInterval:                            ct.TokenGenInterval,
		TokenGenMax:                                 ct.TokenGenMax,
		Timezone:                                    ct.Timezone,
		MaxSubmissionNumber:                         ct.MaxSubmissionNumber,
		MaxUserTestNumber:                           ct.MaxUserTestNumber,
		MinSubmissionInterval:                       ct.MinSubmissionInterval,
		MinSubmissionIntervalGracePeriod:            ct.MinSubmissionIntervalGracePeriod,
		MinUserTestInterval:                         ct.MinUserTestInterval,
		ScorePrecision:                              ct.ScorePrecision,
		MainGroupID:                                 ct.MainGroupID,
	}
}

// updateGroupParams are the parameters that write g whole.
func updateGroupParams(g q.Group) q.UpdateGroupParams {
	return q.UpdateGroupParams{ID: g.ID, Name: g.Name, Start: g.Start, Stop: g.Stop,
		AnalysisEnabled: g.AnalysisEnabled, AnalysisStart: g.AnalysisStart, AnalysisStop: g.AnalysisStop,
		PerUserTime: g.PerUserTime}
}

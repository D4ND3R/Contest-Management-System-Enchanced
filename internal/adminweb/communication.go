// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/contestannouncement.py,
// contestquestion.py and MessageHandler in contestuser.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>

package adminweb

// Communication by contest (work A, R6.3): announcements, questions
// (reply with a quick answer or a text, claim and unclaim, ignore and
// unignore) and private messages. Every change is audited and published
// live: contestants get "announcement" (c:<contest>), "message" and
// "answer" (p:<participation>); the admin pages of the contest get
// "announcement", "message" and "question" (a:<contest>), which updates
// the unanswered questions badge of the menu and the open question pages.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Bounds of the communication pages.
const (
	announcementsMax = 1000
	questionsPageMax = 1000
)

// quickAnswer is one of CMS's Question.QUICK_ANSWERS.
type quickAnswer struct {
	Key, Text string
}

// quickAnswers are CMS's Question.QUICK_ANSWERS in their order; the text
// is stored as the reply subject (in English, as CMS).
var quickAnswerList = []quickAnswer{
	{"yes", N("Yes")},
	{"no", N("No")},
	{"invalid", N("Invalid Question (not a Yes/No Question)")},
	{"nocomment", N("No Comment/Please refer to task statement")},
}

// quickAnswers returns the quick answers with their texts translated for
// the selector.
func (c *Ctx) quickAnswers() []quickAnswer {
	out := make([]quickAnswer, len(quickAnswerList))
	for i, a := range quickAnswerList {
		out[i] = quickAnswer{Key: a.Key, Text: c.T(a.Text)}
	}
	return out
}

// questionView is a question as macro/question.html shows it.
type questionView struct {
	Question q.Question
	// AdminName is the admin who answered, ignored or claimed it.
	AdminName *string
	// The user who asked (the contest questions page only).
	UserID                        int32
	Username, FirstName, LastName string
}

// Class is the CSS class of the question ("answered", "ignored", "").
func (v questionView) Class() string {
	switch {
	case v.Question.ReplyTimestamp != nil:
		return "answered"
	case v.Question.Ignored:
		return "ignored"
	}
	return ""
}

// announcementsData is the data of announcements.html.
type announcementsData struct {
	*Page
	Announcements []q.AdminContestAnnouncementsRow
	Tasks         []q.AdminContestTasksRow
	TZ            string
}

// announcementsPage is GET /contest/<id>/announcements.
func (s *Server) announcementsPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	rows, err := s.st.Q.AdminContestAnnouncements(c.Context(), q.AdminContestAnnouncementsParams{
		ContestID: c.Contest.ID, Lim: announcementsMax})
	if err != nil {
		c.Fail(err)
		return
	}
	tasks, err := s.st.Q.AdminContestTasks(c.Context(), &c.Contest.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("announcements.html", announcementsData{Page: p, Announcements: rows, Tasks: tasks, TZ: c.RC.TZ.String()})
}

// addAnnouncementPost is POST /contest/<id>/announcements/add (CMS
// AddAnnouncementHandler): the subject is mandatory, the text Markdown.
func (s *Server) addAnnouncementPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	page := c.URL("contest", cid, "announcements")
	subject := c.ArgDefault("subject", "")
	text := c.ArgDefault("text", "")
	if subject == "" {
		c.NotifyError(N("Subject is mandatory."), "")
		c.Redirect(page)
		return
	}
	err := c.Write(func(tx *Tx) error {
		if _, err := tx.Q.GetContest(tx.Ctx, cid); err != nil {
			return err
		}
		a, err := tx.Q.CreateAnnouncement(tx.Ctx, q.CreateAnnouncementParams{ContestID: cid, AdminID: &c.Admin.ID,
			Timestamp: c.Now, Subject: subject, Text: text})
		if err != nil {
			return err
		}
		tx.Publish(live.Announcement{Contest: int64(cid), ID: int64(a.ID)}.Event())
		return tx.Audit(AuditEntry{Action: "announcement.create", TargetType: "announcement", TargetID: int64(a.ID),
			ContestID: cid, After: a})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(page)
}

// announcementDelete is DELETE /contest/<id>/announcement/<id> (CMS
// AnnouncementHandler.delete).
func (s *Server) announcementDelete(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	aid, _ := c.PathID("announcement_id")
	err := c.Write(func(tx *Tx) error {
		a, err := tx.Q.GetAnnouncement(tx.Ctx, aid)
		if err != nil {
			return err
		}
		if a.ContestID != cid {
			return pgx.ErrNoRows
		}
		if _, err := tx.Q.DeleteAnnouncement(tx.Ctx, aid); err != nil {
			return err
		}
		tx.Publish(live.Announcement{Contest: int64(cid), ID: int64(aid)}.Event())
		return tx.Audit(AuditEntry{Action: "announcement.delete", TargetType: "announcement", TargetID: int64(aid),
			ContestID: cid, Before: a})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.WriteText("announcements")
}

// questionsData is the data of questions.html.
type questionsData struct {
	*Page
	Questions    []questionView
	QuickAnswers []quickAnswer
	TZ           string
}

// questionsPage is GET /contest/<id>/questions (CMS QuestionsHandler).
func (s *Server) questionsPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	rows, err := s.st.Q.AdminContestQuestions(c.Context(), q.AdminContestQuestionsParams{
		ContestID: c.Contest.ID, Lim: questionsPageMax})
	if err != nil {
		c.Fail(err)
		return
	}
	views := make([]questionView, len(rows))
	for i, r := range rows {
		views[i] = questionView{Question: r.Question, AdminName: r.AdminName, UserID: r.UserID,
			Username: r.Username, FirstName: r.FirstName, LastName: r.LastName}
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("questions.html", questionsData{Page: p, Questions: views, QuickAnswers: c.quickAnswers(), TZ: c.RC.TZ.String()})
}

// errNotAllowed: a claim on an answered or ignored question (CMS 405).
var errNotAllowed = errors.New("adminweb: action not allowed on the question")

// questionAction runs an action on the question of the path (CMS
// QuestionActionHandler.post): fn changes the locked question and
// returns the audit action and the live change; the page goes back to
// the participation page (user_id) or the questions page.
func (s *Server) questionAction(c *Ctx, fn func(tx *Tx, row q.AdminQuestionForUpdateRow) (q.Question, string, string, error)) {
	cid, _ := c.PathID("contest_id")
	qid, _ := c.PathID("question_id")
	ref := c.URL("contest", cid, "questions")
	if uid, ok := c.Arg("user_id"); ok {
		if _, err := strconv.ParseInt(uid, 10, 32); err == nil {
			ref = c.URL("contest", cid, "user", uid, "edit")
		}
	}
	err := c.Write(func(tx *Tx) error {
		row, err := tx.Q.AdminQuestionForUpdate(tx.Ctx, qid)
		if err != nil {
			return err
		}
		if row.ContestID != cid {
			return pgx.ErrNoRows
		}
		upd, action, change, err := fn(tx, row)
		if err != nil {
			return err
		}
		ev := live.Question{Contest: int64(cid), Participation: int64(row.Question.ParticipationID),
			ID: int64(qid), Change: change}
		tx.Publish(ev.Event())
		if change == live.QuestionAnswered {
			tx.Publish(live.Answer{Contest: int64(cid), Participation: int64(row.Question.ParticipationID),
				Question: int64(qid)}.Event())
		}
		return tx.Audit(AuditEntry{Action: action, TargetType: "question", TargetID: int64(qid), ContestID: cid,
			Before: row.Question, After: upd})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.NotFound()
		return
	case errors.Is(err, errNotAllowed):
		c.Error(http.StatusMethodNotAllowed)
		return
	}
	if c.Commit(err) {
		s.log.Info("question handled", "question", qid, "contest", cid, "by", c.Admin.Username)
	}
	c.Redirect(ref)
}

// questionReplyPost is POST /contest/<id>/question/<id>/reply (CMS
// QuestionReplyHandler): a quick answer is the subject (and no text);
// "other" is the text (Markdown) without a subject.
func (s *Server) questionReplyPost(c *Ctx) {
	code := c.ArgDefault("reply_question_quick_answer", "")
	text := c.ArgDefault("reply_question_text", "")
	subject := ""
	for _, a := range quickAnswerList {
		if a.Key == code {
			subject, text = a.Text, ""
		}
	}
	s.questionAction(c, func(tx *Tx, row q.AdminQuestionForUpdateRow) (q.Question, string, string, error) {
		upd, err := tx.Q.AdminReplyQuestion(tx.Ctx, q.AdminReplyQuestionParams{ID: row.Question.ID,
			ReplySubject: subject, ReplyText: text, ReplyTimestamp: c.Now, AdminID: &c.Admin.ID})
		return upd, "question.reply", live.QuestionAnswered, err
	})
}

// questionIgnorePost is POST /contest/<id>/question/<id>/ignore (CMS
// QuestionIgnoreHandler): ignore=yes ignores, anything else unignores.
func (s *Server) questionIgnorePost(c *Ctx) {
	ignore := c.ArgDefault("ignore", "no") == "yes"
	s.questionAction(c, func(tx *Tx, row q.AdminQuestionForUpdateRow) (q.Question, string, string, error) {
		upd, err := tx.Q.SetQuestionIgnored(tx.Ctx, q.SetQuestionIgnoredParams{ID: row.Question.ID, Ignored: ignore,
			AdminID: &c.Admin.ID})
		if ignore {
			return upd, "question.ignore", live.QuestionIgnored, err
		}
		return upd, "question.unignore", live.QuestionReleased, err
	})
}

// questionClaimPost is POST /contest/<id>/question/<id>/claim (CMS
// QuestionClaimHandler): claim=yes makes the admin the owner of the
// question, anything else releases it; only questions neither answered
// nor ignored (405 otherwise).
func (s *Server) questionClaimPost(c *Ctx) {
	claim := c.ArgDefault("claim", "no") == "yes"
	s.questionAction(c, func(tx *Tx, row q.AdminQuestionForUpdateRow) (q.Question, string, string, error) {
		if row.Question.Ignored || row.Question.ReplyTimestamp != nil {
			return q.Question{}, "", "", errNotAllowed
		}
		var admin *int32
		action, change := "question.unclaim", live.QuestionReleased
		if claim {
			admin, action, change = &c.Admin.ID, "question.claim", live.QuestionClaimed
		}
		upd, err := tx.Q.AdminSetQuestionAdmin(tx.Ctx, q.AdminSetQuestionAdminParams{ID: row.Question.ID, AdminID: admin})
		return upd, action, change, err
	})
}

// messagePost is POST /contest/<id>/user/<id>/message (CMS
// MessageHandler): a private message (Markdown) to the participant.
func (s *Server) messagePost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	uid, _ := c.PathID("user_id")
	subject := c.ArgDefault("message_subject", "")
	text := c.ArgDefault("message_text", "")
	err := c.Write(func(tx *Tx) error {
		p, err := tx.Q.GetParticipationByContestUser(tx.Ctx, q.GetParticipationByContestUserParams{ContestID: cid, UserID: uid})
		if err != nil {
			return err
		}
		m, err := tx.Q.CreateMessage(tx.Ctx, q.CreateMessageParams{ParticipationID: p.ID, AdminID: &c.Admin.ID,
			Timestamp: c.Now, Subject: subject, Text: text})
		if err != nil {
			return err
		}
		tx.Publish(live.PrivateMessage{Contest: int64(cid), Participation: int64(p.ID), ID: int64(m.ID)}.Event())
		return tx.Audit(AuditEntry{Action: "message.create", TargetType: "message", TargetID: int64(m.ID),
			ContestID: cid, After: m})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if c.Commit(err) {
		s.log.Info("message sent", "user", uid, "contest", cid, "by", c.Admin.Username)
	}
	c.Redirect(c.URL("contest", cid, "user", uid, "edit"))
}

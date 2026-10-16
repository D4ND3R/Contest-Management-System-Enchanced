// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/communication.py and
// accept_question (cms/server/contest/communication.py) of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2015-2016 William Di Luigi <williamdiluigi@gmail.com>

package contestweb

// Communication (R5.5): the page with the announcements of the contest,
// the questions of the participant with their answers and the private
// messages of the administrators (communication.html), and the questions
// the participant asks (POST question). The notifications of news are
// the JSON of <contest>/notifications (notifications.go, Communications)
// that cws_utils.js polls and counts as unread; the live events
// announcement (c:), message and answer (p:) make contest.html fetch them
// at once and refresh the lists of this page, which are live regions
// (data-live), so news appears without reloading. A new question is
// published as the event question (a:<contest>) for the administrators.

import (
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Limits of a question (CMS Question.MAX_SUBJECT_LENGTH and
// MAX_TEXT_LENGTH), in characters; the form has the same maxlength.
const (
	MaxQuestionSubjectLength = 50
	MaxQuestionTextLength    = 2000
)

// Question rate limit (new; CMS accepts any number of questions): a
// participant may ask questionBurst questions at once, then one every
// questionInterval. The limit is per process of the site.
const (
	questionBurst    = 10
	questionInterval = 20 * time.Second
)

// communicationItem is an announcement, a message, or a question with its
// answer, as communication.html shows them.
type communicationItem struct {
	ID int32
	// Timestamp is the time of the announcement or message, or of the
	// question (CMS question_timestamp).
	Timestamp time.Time
	Subject   string
	Text      string
	// The answer of a question: ReplyTimestamp is nil while it has none
	// (CMS "no answer yet"). A NULL subject or text shows as empty.
	ReplyTimestamp *time.Time
	ReplySubject   string
	ReplyText      string
}

// communicationData is the data of communication.html: the three lists,
// newest first (CMS reverses contest.announcements, participation.questions
// and participation.messages).
type communicationData struct {
	*Page
	Announcements []communicationItem
	Questions     []communicationItem
	Messages      []communicationItem
}

// communicationPage is GET <contest>/communication (CMS
// CommunicationHandler): one query for the three lists, whatever their
// length, besides the queries of every page.
func (s *Server) communicationPage(c *Ctx) {
	p, err := c.Page("communication")
	if err != nil {
		c.Fail(err)
		return
	}
	rows, err := s.st.Q.CWSCommunicationPage(c.Context(), q.CWSCommunicationPageParams{
		ContestID:       c.Contest.ID,
		ParticipationID: c.Participation.ID,
	})
	if err != nil {
		c.Fail(err)
		return
	}
	d := communicationData{Page: p}
	for _, r := range rows {
		it := communicationItem{ID: r.ID, Timestamp: r.Timestamp, Subject: r.Subject, Text: r.Text}
		switch r.Kind {
		case "announcement":
			d.Announcements = append(d.Announcements, it)
		case "message":
			d.Messages = append(d.Messages, it)
		case "question":
			it.ReplyTimestamp = r.ReplyTimestamp
			it.ReplySubject = deref(r.ReplySubject)
			it.ReplyText = deref(r.ReplyText)
			d.Questions = append(d.Questions, it)
		}
	}
	c.Render(http.StatusOK, "communication.html", d)
}

// questionPost is POST <contest>/question (CMS QuestionHandler and
// accept_question): it stores the question of the participant, or
// explains with a notification why it was not accepted, and goes back to
// the communication page. Questions are refused with 404 when the
// contest does not allow them, as in CMS.
func (s *Server) questionPost(c *Ctx) {
	if !c.Contest.AllowQuestions {
		c.Error(http.StatusNotFound)
		return
	}
	back := c.URL.Join("communication")
	p := c.Participation
	subject := c.ArgDefault("question_subject", "")
	text := c.ArgDefault("question_text", "")

	// Python's len() counts code points, as RuneCountInString (the
	// arguments are valid UTF-8 after Tornado's cleaning).
	sl, tl := utf8.RuneCountInString(subject), utf8.RuneCountInString(text)
	if sl > MaxQuestionSubjectLength || tl > MaxQuestionTextLength {
		s.log.Warn("long question dropped", "subject_length", sl, "text_length", tl,
			"username", p.User.Username, "contest", c.Contest.Name)
		c.NotifyError(steps.N("Question too long!"),
			steps.N("Subject must be at most %(max_subject_length)d characters, content at most %(max_text_length)d."),
			map[string]any{"max_subject_length": MaxQuestionSubjectLength, "max_text_length": MaxQuestionTextLength})
		c.Redirect(back)
		return
	}
	if ok, _ := s.questions.Allow(c.Contest.Name + "\x00" + p.User.Username); !ok {
		s.log.Warn("too many questions", "username", p.User.Username, "contest", c.Contest.Name)
		c.NotifyError(steps.N("Too many questions!"),
			steps.N("Please wait a few seconds before asking another question."), nil)
		c.Redirect(back)
		return
	}

	qn, err := s.st.Q.CreateQuestion(c.Context(), q.CreateQuestionParams{
		ParticipationID:   p.ID,
		QuestionTimestamp: c.Now,
		Subject:           subject,
		Text:              text,
	})
	if err != nil {
		c.Fail(err)
		return
	}
	s.log.Info("question submitted", "username", p.User.Username, "contest", c.Contest.Name, "question", qn.ID)
	s.pub.Publish(live.Question{Contest: int64(c.Contest.ID), Participation: int64(p.ID),
		ID: int64(qn.ID), Change: live.QuestionNew}.Event())
	c.NotifySuccess(steps.N("Question received"),
		steps.N("Your question has been received, you will be notified when it is answered."), nil)
	c.Redirect(back)
}

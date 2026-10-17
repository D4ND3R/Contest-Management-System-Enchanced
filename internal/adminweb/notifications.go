// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from NotificationsHandler in cms/server/admin/handlers/main.py and
// AdminWebServer.add_notification in cms/server/admin/server.py of CMS:
// Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

// Notifications: the bubbles of aws_utils.js (update_notifications polls
// /notifications every 15 s). CMS keeps the "Operation successful." kind
// in the memory of its process and gives them to whichever admin polls
// first; here each admin has a short list in Valkey (every replica sees
// it, a restart keeps it), taken whole by their next poll. New questions
// come from the database, as in CMS.

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// Bounds of the pending notifications of an admin.
const (
	notesMax = 50
	notesTTL = time.Hour
	// questionsMax bounds the new questions of one poll.
	questionsMax = 100
)

// notification is one element of the answer of /notifications (CMS's
// dicts: type, timestamp, subject, text, contest_id for questions).
type notification struct {
	Type      string        `json:"type"`
	Timestamp web.Timestamp `json:"timestamp"`
	Subject   string        `json:"subject"`
	Text      string        `json:"text"`
	ContestID *int32        `json:"contest_id,omitempty"`
	Level     string        `json:"level,omitempty"`
}

// notifications stores the pending notifications of the admins.
type notifications struct{ kv *kv.Client }

func newNotifications(c *kv.Client) *notifications { return &notifications{kv: c} }

func (n *notifications) key(admin int32) string {
	return n.kv.Key("admin-notes", strconv.Itoa(int(admin)))
}

// push queues a notification (best effort: a Valkey failure loses it).
func (n *notifications) push(ctx context.Context, admin int32, note notification) {
	note.Type = "notification"
	b, err := json.Marshal(note)
	if err != nil {
		return
	}
	k := n.key(admin)
	pipe := n.kv.Redis().TxPipeline()
	pipe.RPush(ctx, k, b)
	pipe.LTrim(ctx, k, -notesMax, -1)
	pipe.Expire(ctx, k, notesTTL)
	_, _ = pipe.Exec(ctx)
}

// take returns and removes the pending notifications of an admin.
func (n *notifications) take(ctx context.Context, admin int32) ([]notification, error) {
	k := n.key(admin)
	pipe := n.kv.Redis().TxPipeline()
	lr := pipe.LRange(ctx, k, 0, -1)
	pipe.Del(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	var out []notification
	for _, s := range lr.Val() {
		var note notification
		if json.Unmarshal([]byte(s), &note) == nil {
			out = append(out, note)
		}
	}
	return out, nil
}

// notificationsPage is GET /notifications?last_notification=<timestamp>
// (CMS NotificationsHandler): the questions without an answer asked
// after last_notification and the pending notifications of the admin, as
// JSON.
func (s *Server) notificationsPage(c *Ctx) {
	last, err := strconv.ParseFloat(c.ArgDefault("last_notification", "0"), 64)
	if err != nil {
		c.BadRequest()
		return
	}
	after := time.UnixMicro(int64(last * 1e6)).UTC()
	rows, err := s.st.Q.AdminCoreNewQuestions(c.Context(), q.AdminCoreNewQuestionsParams{After: after, Lim: questionsMax})
	if err != nil {
		c.Fail(err)
		return
	}
	res := []notification{}
	for _, r := range rows {
		cid := r.ContestID
		res = append(res, notification{
			Type:      "new_question",
			Timestamp: web.Timestamp(float64(r.QuestionTimestamp.UnixMicro()) / 1e6),
			Subject:   r.Subject,
			Text:      r.Text,
			ContestID: &cid,
		})
	}
	notes, err := s.notes.take(c.Context(), c.Admin.ID)
	if err != nil {
		s.log.Warn("reading the notifications of an admin", "err", err)
	}
	for _, n := range notes {
		n.Level = ""
		res = append(res, n)
	}
	b, err := json.Marshal(res)
	if err != nil {
		c.Fail(err)
		return
	}
	// CMS writes the JSON with Tornado's default Content-Type.
	c.W.Header().Set("Content-Type", "text/html; charset=UTF-8")
	_, _ = c.W.Write(b)
}

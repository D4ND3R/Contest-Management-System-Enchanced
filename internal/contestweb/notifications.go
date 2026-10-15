// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from NotificationsHandler (cms/server/contest/handlers/main.py),
// get_communications (cms/server/contest/communication.py) and
// add_notification (cms/server/contest/server.py) of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// Communication is one item of the notifications JSON of CMS
// (get_communications): an announcement, a message or an answer, or (with
// Level) a notification of the site such as "Submission received".
type Communication struct {
	// Type is "announcement", "message", "question" (an answer) or
	// "notification".
	Type      string        `json:"type"`
	Timestamp web.Timestamp `json:"timestamp"`
	Subject   string        `json:"subject"`
	Text      string        `json:"text"`
	// Level is the level of a notification ("success", "warning",
	// "error"); absent for the others.
	Level string `json:"level,omitempty"`
}

// Communications returns the announcements of the participant's contest,
// the messages to them and the answers to their questions in (after,
// until] (after nil: from the beginning), in that order, oldest first
// within each kind (CMS get_communications), with one query.
func (s *Server) Communications(ctx context.Context, p *Participation, until time.Time, after *time.Time) ([]Communication, error) {
	rows, err := s.st.Q.CWSCommunications(ctx, q.CWSCommunicationsParams{
		ContestID:       p.ContestID,
		ParticipationID: p.ID,
		Until:           until,
		After:           after,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Communication, len(rows))
	for i, r := range rows {
		out[i] = Communication{
			Type:      r.Kind,
			Timestamp: timestamp(r.Timestamp),
			Subject:   r.Subject,
			Text:      r.Text,
		}
	}
	return out, nil
}

// timestamp is CMS make_timestamp: seconds since the epoch, with
// microseconds.
func timestamp(t time.Time) web.Timestamp {
	return web.Timestamp(float64(t.UnixMicro()) / 1e6)
}

// fromTimestamp is CMS make_datetime: the time of a number of seconds
// since the epoch, to the microsecond.
func fromTimestamp(f float64) (time.Time, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e14 {
		return time.Time{}, false
	}
	return time.UnixMicro(int64(math.Round(f * 1e6))).UTC(), true
}

// notificationsPage is GET <contest>/notifications (CMS
// NotificationsHandler): the communications since last_notification and
// the pending notifications of the participant, which it consumes.
func (s *Server) notificationsPage(c *Ctx) {
	var after *time.Time
	if v, ok := c.Arg("last_notification"); ok {
		f, err := strconv.ParseFloat(v, 64)
		t, valid := fromTimestamp(f)
		if err != nil || !valid {
			c.Error(http.StatusBadRequest)
			return
		}
		after = &t
	}
	res, err := s.Communications(c.Context(), c.Participation, c.Now, after)
	if err != nil {
		c.Fail(err)
		return
	}
	for _, n := range s.notes.pop(c.Context(), c.Participation.ID) {
		res = append(res, Communication{Type: "notification", Timestamp: n.Timestamp,
			Subject: n.Subject, Text: n.Text, Level: n.Level})
	}
	if res == nil {
		res = []Communication{}
	}
	c.JSON(http.StatusOK, res)
}

// notification is a message of the site to a participant (CMS
// add_notification): already translated; Text is HTML (cws_utils.js
// inserts it as such).
type notification struct {
	Timestamp web.Timestamp `json:"t"`
	Subject   string        `json:"s"`
	Text      string        `json:"x"`
	Level     string        `json:"l"`
}

// notifications keeps the pending notifications of each participant in
// Valkey (CMS kept them in the memory of its only process; here every
// replica of the site sees them): a list per participation, bounded and
// expiring, consumed by the next poll.
type notifications struct {
	kv *kv.Client
}

const (
	// maxNotifications bounds the pending notifications of a
	// participant (the oldest are dropped).
	maxNotifications = 50
	// notificationTTL drops the notifications nobody fetched.
	notificationTTL = time.Hour
)

func newNotifications(c *kv.Client) *notifications { return &notifications{kv: c} }

func (n *notifications) key(participation int32) string {
	return n.kv.Key("cws", "notifications", strconv.FormatInt(int64(participation), 10))
}

// push queues a notification (one round trip). Failures are logged by
// the client and the notification is lost, as CMS loses them on restart.
func (n *notifications) push(ctx context.Context, participation int32, m notification) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	key := n.key(participation)
	pipe := n.kv.Redis().TxPipeline()
	pipe.RPush(ctx, key, b)
	pipe.LTrim(ctx, key, -maxNotifications, -1)
	pipe.Expire(ctx, key, notificationTTL)
	_, _ = pipe.Exec(ctx)
}

// pop returns and removes the pending notifications (one round trip).
func (n *notifications) pop(ctx context.Context, participation int32) []notification {
	key := n.key(participation)
	pipe := n.kv.Redis().TxPipeline()
	lr := pipe.LRange(ctx, key, 0, -1)
	pipe.Del(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil
	}
	var out []notification
	for _, v := range lr.Val() {
		var m notification
		if json.Unmarshal([]byte(v), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

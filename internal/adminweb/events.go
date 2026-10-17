// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// The live events of the admin pages (R6.9; new, CMS polls): see
// internal/live (the stream and its browser contract) and
// internal/web/static/live.js (the indicator next to the admin name,
// the live regions and the fallback to polling).

import (
	"net/http"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// eventsPage is GET /events and /contest/<id>/events: the events of every
// admin page (topic "a": judging counters, workers) and, for a contest,
// those of the contest ("a:<id>": submissions, questions, messages,
// announcements, changes). Without a hub it answers 503 and the pages
// poll.
func (s *Server) eventsPage(c *Ctx) {
	if s.hub == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Live updates are not available"})
		return
	}
	topics := []live.Topic{live.AdminTopic}
	if c.R.PathValue("contest_id") != "" {
		ct, err := c.LoadContest(mustID(c, "contest_id"))
		if err != nil {
			c.Fail(err)
			return
		}
		if ct == nil {
			c.JSON(http.StatusNotFound, map[string]string{"error": "No such contest"})
			return
		}
		topics = append(topics, live.AdminContestTopic(int64(ct.ID)))
	}
	s.hub.Stream(c.W, c.R, live.StreamOptions{Topics: topics})
}

// mustID returns a path id the route validated.
func mustID(c *Ctx, name string) int32 {
	id, _ := c.PathID(name)
	return id
}

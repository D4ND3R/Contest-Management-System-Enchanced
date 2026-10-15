// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// The live events of the contestant pages (R5.8; new, CMS polls): see
// internal/live (the stream and its browser contract) and
// internal/web/static/live.js (the indicator, the live regions and the
// fallback to polling).

import (
	"net/http"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// eventsPage is GET <contest>/events: the live events of the participant
// (p:) and of the contest (c:) as server-sent events (R5.8, see
// internal/live). Without a hub it answers 503 and the pages poll.
func (s *Server) eventsPage(c *Ctx) {
	if s.hub == nil {
		c.JSONError(http.StatusServiceUnavailable, "Live updates are not available")
		return
	}
	s.hub.Stream(c.W, c.R, live.StreamOptions{Topics: s.eventTopics(c)})
}

// eventTopics are the live topics a participant may see: their own
// (submissions, user tests, scores, messages, answers, changes of their
// participation) and their contest's (announcements, changes of the
// contest). The scoreboard topic (s:) joins them when contests get a
// scoreboard for contestants (R5.6).
func (s *Server) eventTopics(c *Ctx) []live.Topic {
	return []live.Topic{
		live.ParticipationTopic(int64(c.Participation.ID)),
		live.ContestTopic(int64(c.Contest.ID)),
	}
}

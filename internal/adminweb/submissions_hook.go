// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"bytes"
	"html/template"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
)

// The submissions table of the participation page (users_participations.go)
// is rendered here: CMS render_params_for_submissions over the
// submissions of the participation and macro_submission.rows.
func init() {
	participationSubmissionsHook = func(c *Ctx, contestID, participationID int32) (template.HTML, error) {
		return c.S.renderSubmissionTable(c, store.SubmissionFilter{ContestID: contestID, ParticipationID: participationID},
			participationID)
	}
}

// renderSubmissionTable renders a page of submissions (?page=) as
// fragments/submission_table.html; matchParticipation limits its live
// region to the events of a participation (0: any).
func (s *Server) renderSubmissionTable(c *Ctx, f store.SubmissionFilter, matchParticipation int32) (template.HTML, error) {
	tab, _, err := s.submissionTable(c, f)
	if err != nil {
		return "", err
	}
	tab.MatchParticipation = matchParticipation
	var b bytes.Buffer
	if err := s.site.Engine.Execute(&b, "fragments/submission_table.html", c.RC, tab); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil
}

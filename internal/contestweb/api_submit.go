// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from ApiSubmitHandler (cms/server/contest/handlers/api.py) of
// CMS: Copyright © the CMS contributors of the JSON API.

package contestweb

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/contestweb/submission"
)

// apiRefusal is the answer to a refused submission: CMS's subject and
// formatted text, in English.
type apiRefusal struct {
	Error   string `json:"error"`
	Details string `json:"details"`
}

// apiSubmit is POST api/<task>/submit (CMS ApiSubmitHandler): the files
// of the submission format as multipart form fields named after the
// format ("sum.%l", or one archive in "submission"), and optionally
// "language". It runs the workflow of the submit form
// (Server.AcceptSubmission, submissions.go) and answers {"id": <opaque
// id>}, or 422 {"error": subject, "details": text} with CMS's English
// messages when the submission is refused. The submission is official
// while the participant competes; an impersonating administrator may set
// override_official, override_max_number and override_min_interval.
func (s *Server) apiSubmit(c *Ctx) {
	task, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.apiFail(err)
		return
	}
	if task == nil {
		c.JSONError(http.StatusNotFound, "Task not found")
		return
	}
	// Official only while competing (not in analysis mode).
	o := SubmitOptions{Official: c.Actual.Phase == PhaseCompeting}
	if c.Impersonated {
		var ok bool
		if o.Official, ok = c.boolArg("override_official", o.Official); !ok {
			return
		}
		if o.OverrideMaxNumber, ok = c.boolArg("override_max_number", false); !ok {
			return
		}
		if o.OverrideMinInterval, ok = c.boolArg("override_min_interval", false); !ok {
			return
		}
	}
	sub, err := s.AcceptSubmission(c, task, o)
	var refused *submission.Unacceptable
	var status errStatus
	switch {
	case errors.As(err, &refused):
		s.log.Info("API submission rejected", "contest", c.Contest.Name, "username", c.Participation.User.Username,
			"task", task.Name, "subject", refused.Subject, "details", refused.FormattedText(), "err", refused.Err)
		c.JSON(http.StatusUnprocessableEntity, apiRefusal{Error: refused.Subject, Details: refused.FormattedText()})
	case errors.As(err, &status):
		c.JSONError(int(status), http.StatusText(int(status)))
	case err != nil:
		c.apiFail(err)
	default:
		c.JSON(http.StatusOK, map[string]string{"id": strconv.FormatInt(sub.OpaqueID, 10)})
	}
}

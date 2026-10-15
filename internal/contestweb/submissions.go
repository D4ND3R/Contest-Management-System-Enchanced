// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// Submissions and tokens (CMS handlers/tasksubmission.py). Stubs until
// the tasks work replaces them (see routes.go).

// submitPost is POST tasks/{task}/submit.
func (s *Server) submitPost(c *Ctx) { c.notImplemented() }

// taskSubmissionsPage is GET tasks/{task}/submissions.
func (s *Server) taskSubmissionsPage(c *Ctx) { c.notImplemented() }

// submissionStatusPage is GET tasks/{task}/submissions/{id} (JSON).
func (s *Server) submissionStatusPage(c *Ctx) { c.notImplemented() }

// submissionDetailsPage is GET tasks/{task}/submissions/{id}/details.
func (s *Server) submissionDetailsPage(c *Ctx) { c.notImplemented() }

// submissionFilePage is GET tasks/{task}/submissions/{id}/files/{filename...}.
func (s *Server) submissionFilePage(c *Ctx) { c.notImplemented() }

// useTokenPost is POST tasks/{task}/submissions/{id}/token.
func (s *Server) useTokenPost(c *Ctx) { c.notImplemented() }

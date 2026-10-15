// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// The JSON API (CMS handlers/api.py, docs/API.rst). Stubs until the API
// work replaces them (see routes.go); login primitives are in auth.go
// (Server.ValidateLogin, Ctx.Participation, Ctx.Impersonated).

// apiLogin is POST api/login.
func (s *Server) apiLogin(c *Ctx) { c.notImplemented() }

// apiTaskList is GET api/task_list.
func (s *Server) apiTaskList(c *Ctx) { c.notImplemented() }

// apiSubmit is POST api/{task}/submit.
func (s *Server) apiSubmit(c *Ctx) { c.notImplemented() }

// apiSubmissionList is GET api/{task}/submission_list.
func (s *Server) apiSubmissionList(c *Ctx) { c.notImplemented() }

// apiSubmissionDetails is GET api/{task}/submissions/{id}/details.
func (s *Server) apiSubmissionDetails(c *Ctx) { c.notImplemented() }

// apiSubmissionFullDetails is GET api/{task}/submissions/{id}/full_details.
func (s *Server) apiSubmissionFullDetails(c *Ctx) { c.notImplemented() }

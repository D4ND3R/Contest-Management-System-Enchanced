// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// User tests (CMS handlers/taskusertest.py). Stubs until the tasks work
// replaces them (see routes.go).

// testInterfacePage is GET testing.
func (s *Server) testInterfacePage(c *Ctx) { c.notImplemented() }

// userTestPost is POST tasks/{task}/test.
func (s *Server) userTestPost(c *Ctx) { c.notImplemented() }

// userTestStatusPage is GET tasks/{task}/tests/{id} (JSON).
func (s *Server) userTestStatusPage(c *Ctx) { c.notImplemented() }

// userTestDetailsPage is GET tasks/{task}/tests/{id}/details.
func (s *Server) userTestDetailsPage(c *Ctx) { c.notImplemented() }

// userTestIOPage is GET tasks/{task}/tests/{id}/{io} (input or output).
func (s *Server) userTestIOPage(c *Ctx) { c.notImplemented() }

// userTestFilePage is GET tasks/{task}/tests/{id}/files/{filename...}.
func (s *Server) userTestFilePage(c *Ctx) { c.notImplemented() }

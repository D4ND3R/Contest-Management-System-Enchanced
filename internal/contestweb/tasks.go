// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// Task pages (CMS handlers/task.py): description, statements and
// attachments. Stubs until the tasks work replaces them (see routes.go).

// taskDescriptionPage is GET tasks/{task}/description.
func (s *Server) taskDescriptionPage(c *Ctx) { c.notImplemented() }

// taskStatementPage is GET tasks/{task}/statements/{lang}[/…].
func (s *Server) taskStatementPage(c *Ctx) { c.notImplemented() }

// taskAttachmentPage is GET tasks/{task}/attachments/{filename...}.
func (s *Server) taskAttachmentPage(c *Ctx) { c.notImplemented() }

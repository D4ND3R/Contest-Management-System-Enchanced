// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Communication by contest (work A): announcements, questions (reply,
// ignore, claim) and private messages, the pages of
// cms/server/admin/handlers/contestannouncement.py, contestquestion.py and
// MessageHandler of contestuser.py (R6.3). Until they are written their
// handlers answer 501 (routes.go lists the routes).

func (s *Server) messagePost(c *Ctx) { c.NotImplemented() }

func (s *Server) announcementsPage(c *Ctx) { c.NotImplemented() }

func (s *Server) addAnnouncementPost(c *Ctx) { c.NotImplemented() }

func (s *Server) announcementDelete(c *Ctx) { c.NotImplemented() }

func (s *Server) questionsPage(c *Ctx) { c.NotImplemented() }

func (s *Server) questionReplyPost(c *Ctx) { c.NotImplemented() }

func (s *Server) questionIgnorePost(c *Ctx) { c.NotImplemented() }

func (s *Server) questionClaimPost(c *Ctx) { c.NotImplemented() }

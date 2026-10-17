// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from BaseHandler.render_params in
// cms/server/admin/handlers/base.py of CMS:
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

import (
	"encoding/json"
	"time"
)

// Page is the context of base.html (CMS render_params): pages put it in
// their data by embedding it,
//
//	type usersData struct {
//		*Page
//		Users []q.User
//	}
//	p, err := c.Page()
//	if err != nil { c.Fail(err); return }
//	c.Render("users.html", usersData{Page: p, Users: users})
//
// so templates read .Admin, .Contest... directly.
type Page struct {
	// Admin is the logged-in admin (nil: the bare header of base.html).
	Admin *Admin
	// Contest is the contest of the page: the sidebar shows its menu,
	// its countdown and its unanswered questions. Nil: the global menu.
	Contest *Contest
	// Sidebar holds the lists of the global menu (nil with a contest).
	Sidebar *Sidebar
	// Timestamp is the time of the request (CMS timestamp).
	Timestamp time.Time
	// LiveURL and LiveSince configure live.js: the event stream of the
	// page (topics "a" and, with a contest, "a:<contest>") and the id of
	// the last event before the page read its data.
	LiveURL   string
	LiveSince string
}

// Sidebar is what the global menu of base.html shows: the first five
// contests and tasks (and whether there are more) and the numbers of
// users and teams. CMS loads every contest, task, user and team on every
// page; one bounded query here.
type Sidebar struct {
	Contests     []SidebarItem
	MoreContests bool
	Tasks        []SidebarItem
	MoreTasks    bool
	Users        int64
	Teams        int64
}

// SidebarItem is a contest (Title: its description) or a task (Title:
// its title) of the sidebar.
type SidebarItem struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

// sidebarSize is how many contests and tasks the menu shows.
const sidebarSize = 5

// Page returns the context of base.html for the request: with c.Contest
// set it shows the contest menu, otherwise it loads the global lists (one
// query). Logged-out requests (error pages) get the bare header.
func (c *Ctx) Page() (*Page, error) {
	p := &Page{Admin: c.Admin, Contest: c.Contest, Timestamp: c.Now}
	if c.Admin == nil {
		return p, nil
	}
	if c.S.hub != nil {
		p.LiveSince = c.LiveSince
		if c.Contest != nil {
			p.LiveURL = c.URL("contest", c.Contest.ID, "events")
		} else {
			p.LiveURL = c.URL("events")
		}
	}
	if c.Contest != nil {
		return p, nil
	}
	row, err := c.S.st.Q.AdminCoreSidebar(c.Context(), sidebarSize+1)
	if err != nil {
		return nil, err
	}
	sb := &Sidebar{Users: row.Users, Teams: row.Teams}
	if err := json.Unmarshal(row.Contests, &sb.Contests); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(row.Tasks, &sb.Tasks); err != nil {
		return nil, err
	}
	if len(sb.Contests) > sidebarSize {
		sb.Contests, sb.MoreContests = sb.Contests[:sidebarSize], true
	}
	if len(sb.Tasks) > sidebarSize {
		sb.Tasks, sb.MoreTasks = sb.Tasks[:sidebarSize], true
	}
	p.Sidebar = sb
	return p, nil
}

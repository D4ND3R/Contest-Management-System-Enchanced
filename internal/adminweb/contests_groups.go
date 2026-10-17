// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from GroupListHandler, AddGroupHandler and GroupHandler in
// cms/server/admin/handlers/user.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Chuyang Wang <mail@chuyang-wang.de>

package adminweb

// Groups of a contest (R6.1): each group has its own start and stop
// times, per-user time and analysis mode; the main group is the default
// of new participations.

import (
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// groupUsersMax bounds the users the group page lists.
const groupUsersMax = 1000

// groupsData is the data of groups.html.
type groupsData struct {
	*Page
	Groups []groupRow
	TZ     string
}

// groupRow is a group of the list; Main marks the main group.
type groupRow struct {
	q.AdminContestGroupsRow
	Main bool
}

// groupsPage is GET /contest/<id>/groups (CMS GroupListHandler).
func (s *Server) groupsPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	rows, err := s.st.Q.AdminContestGroups(c.Context(), c.Contest.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("groups.html", groupsData{Page: p, Groups: c.groupRows(rows), TZ: c.RC.TZ.String()})
}

// groupRows marks the main group of the contest of the page.
func (c *Ctx) groupRows(rows []q.AdminContestGroupsRow) []groupRow {
	out := make([]groupRow, len(rows))
	for i, r := range rows {
		out[i] = groupRow{AdminContestGroupsRow: r, Main: ptrEq(c.Contest.MainGroupID, &r.Group.ID)}
	}
	return out
}

// groupsPost is POST /contest/<id>/groups: "Make main group" or "Delete
// selected group" on the selected group.
func (s *Server) groupsPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	fallback := c.URL("contest", cid, "groups")
	gid, err := strconv.ParseInt(c.ArgDefault("group_id", ""), 10, 32)
	op, _ := c.Arg("operation")
	if err != nil {
		c.NotifyError(N("Invalid field(s)"), "Please select a valid group")
		c.Redirect(fallback)
		return
	}
	var makeMain bool
	switch {
	case c.isOp(op, "Make main group"):
		makeMain = true
	case c.isOp(op, "Delete selected group"):
	default:
		c.NotifyError(N("Invalid operation"), "I do not understand the operation `"+op+"'")
		c.Redirect(fallback)
		return
	}
	type refusal struct{ subject, text string }
	var refused *refusal
	err = c.Write(func(tx *Tx) error {
		refused = nil
		ct, err := tx.Q.GetContest(tx.Ctx, cid)
		if err != nil {
			return err
		}
		g, err := tx.Q.GetGroup(tx.Ctx, int32(gid))
		if err != nil {
			return err
		}
		if g.ContestID != cid {
			return pgx.ErrNoRows
		}
		if makeMain {
			if _, err := tx.Q.SetContestMainGroup(tx.Ctx, q.SetContestMainGroupParams{ID: cid, MainGroupID: &g.ID}); err != nil {
				return err
			}
			tx.Publish(contestSettingsEvents(cid)...)
			return tx.Audit(AuditEntry{Action: "contest.update", TargetType: "contest", TargetID: int64(cid),
				ContestID: cid, Before: map[string]any{"main_group_id": ct.MainGroupID},
				After: map[string]any{"main_group_id": g.ID}})
		}
		n, err := tx.Q.AdminGroupParticipationCount(tx.Ctx, g.ID)
		if err != nil {
			return err
		}
		if n != 0 {
			refused = &refusal{N("Cannot delete group containing users"),
				"The group `" + g.Name + "' contains " + strconv.FormatInt(n, 10) + " users"}
			return errRefused
		}
		if ct.MainGroupID != nil && *ct.MainGroupID == g.ID {
			refused = &refusal{N("Cannot delete a contest's main group."),
				"To delete '" + g.Name + "', change the main group of the contest first to another group."}
			return errRefused
		}
		if _, err := tx.Q.DeleteGroup(tx.Ctx, g.ID); err != nil {
			return err
		}
		tx.Publish(contestSettingsEvents(cid)...)
		return tx.Audit(AuditEntry{Action: "group.delete", TargetType: "group", TargetID: int64(g.ID),
			ContestID: cid, Before: g})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.NotFound()
		return
	case errors.Is(err, errRefused):
		c.NotifyError(refused.subject, refused.text)
	default:
		c.Commit(err)
	}
	c.Redirect(fallback)
}

// defaultGroupTimes are the times of a group created without a main
// group to copy (the defaults of the schema).
func defaultGroupTimes() q.Group {
	far := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	return q.Group{Start: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Stop: far, AnalysisStart: far, AnalysisStop: far}
}

// errRefused rolls back a Write whose change was refused with a
// notification of its own.
var errRefused = errors.New("adminweb: change refused")

// addGroupPost is POST /contest/<id>/groups/add (CMS AddGroupHandler): a
// group with the times of the main group, then its page.
func (s *Server) addGroupPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	fallback := c.URL("contest", cid, "groups")
	f := c.Form()
	name := f.Required("name", "name")
	if err := f.Err(); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	var created q.Group
	err := c.Write(func(tx *Tx) error {
		main, err := tx.Q.GetMainGroup(tx.Ctx, cid)
		if errors.Is(err, pgx.ErrNoRows) {
			// A contest without a main group (an import): the defaults.
			if _, err := tx.Q.GetContest(tx.Ctx, cid); err != nil {
				return err
			}
			main, err = defaultGroupTimes(), nil
		}
		if err != nil {
			return err
		}
		created, err = tx.Q.CreateGroup(tx.Ctx, q.CreateGroupParams{ContestID: cid, Name: name,
			Start: main.Start, Stop: main.Stop, AnalysisEnabled: main.AnalysisEnabled,
			AnalysisStart: main.AnalysisStart, AnalysisStop: main.AnalysisStop, PerUserTime: main.PerUserTime})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "group.create", TargetType: "group", TargetID: int64(created.ID),
			ContestID: cid, After: created})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if c.Commit(err) {
		c.Redirect(c.URL("contest", cid, "group", created.ID, "edit"))
	} else {
		c.Redirect(fallback)
	}
}

// groupData is the data of group.html.
type groupData struct {
	*Page
	Group *q.Group
	Users []q.AdminGroupParticipantsRow
	TZ    string
}

// contestGroup loads the group of the path, which must belong to the
// contest of the page (404 otherwise).
func (c *Ctx) contestGroup() (*q.Group, bool) {
	gid, _ := c.PathID("group_id")
	g, err := c.S.st.Q.GetGroup(c.Context(), gid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && g.ContestID != c.Contest.ID {
		c.NotFound()
		return nil, false
	}
	if err != nil {
		c.Fail(err)
		return nil, false
	}
	return &g, true
}

// groupPage is GET /contest/<id>/group/<id>/edit (CMS GroupHandler.get).
func (s *Server) groupPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	g, ok := c.contestGroup()
	if !ok {
		return
	}
	users, err := s.st.Q.AdminGroupParticipants(c.Context(), q.AdminGroupParticipantsParams{GroupID: g.ID, Lim: groupUsersMax})
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("group.html", groupData{Page: p, Group: g, Users: users, TZ: c.RC.TZ.String()})
}

// groupPost is POST /contest/<id>/group/<id>/edit (CMS GroupHandler.post).
func (s *Server) groupPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	gid, _ := c.PathID("group_id")
	page := c.URL("contest", cid, "group", gid, "edit")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetGroup(tx.Ctx, gid)
		if err != nil {
			return err
		}
		if old.ContestID != cid {
			return pgx.ErrNoRows
		}
		g := old
		f := c.Form()
		g.Name = f.Required("name", "group name")
		groupSettings(f, &g)
		if err := f.Err(); err != nil {
			return &FieldError{Msg: err.Error()}
		}
		upd, err := tx.Q.UpdateGroup(tx.Ctx, updateGroupParams(g))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(upd, old) {
			tx.Publish(contestSettingsEvents(cid)...)
		}
		return tx.Audit(AuditEntry{Action: "group.update", TargetType: "group", TargetID: int64(gid),
			ContestID: cid, Before: old, After: upd})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		c.NotifyError(N("Invalid field(s)"), fe.Msg)
		c.Redirect(page)
		return
	}
	c.Commit(err)
	c.Redirect(page)
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/contesttask.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>

package adminweb

// The task list of a contest (work A, CMS ContestTasksHandler and
// AddContestTaskHandler): add a task without a contest, remove a task
// from the contest, move it. Changing the tasks of a contest changes the
// contest: permission_all (routes.go).

import (
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// contestTasksData is the data of contest_tasks.html.
type contestTasksData struct {
	*Page
	Tasks      []q.AdminContestTasksRow
	Unassigned []q.AdminUnassignedTasksRow
}

// contestTasksPage is GET /contest/<id>/tasks.
func (s *Server) contestTasksPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	tasks, err := s.st.Q.AdminContestTasks(c.Context(), &c.Contest.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	free, err := s.st.Q.AdminUnassignedTasks(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("contest_tasks.html", contestTasksData{Page: p, Tasks: tasks, Unassigned: free})
}

// The operations of the task list (the values of its buttons).
const (
	opRemoveFromContest = "Remove from contest"
	opMoveUp            = "up by 1"
	opMoveDown          = "down by 1"
	opMoveTop           = "to the top"
	opMoveBottom        = "to the bottom"
)

// contestTasksPost is POST /contest/<id>/tasks: remove the selected task
// from the contest or move it.
func (s *Server) contestTasksPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	fallback := c.URL("contest", cid, "tasks")
	op, _ := c.Arg("operation")
	var opName string
	for _, o := range []string{opRemoveFromContest, opMoveUp, opMoveDown, opMoveTop, opMoveBottom} {
		if c.isOp(op, o) {
			opName = o
		}
	}
	tid, err := strconv.ParseInt(c.ArgDefault("task_id", ""), 10, 32)
	if err != nil || opName == "" {
		msg := "Please select a valid operation"
		if err != nil {
			msg = "Please select a valid task"
		}
		c.NotifyError(N("Invalid field(s)"), msg)
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.GetContest(tx.Ctx, cid); err != nil {
			return err
		}
		rows, err := tx.Q.AdminContestTasksForUpdate(tx.Ctx, &cid)
		if err != nil {
			return err
		}
		order := make([]int32, len(rows))
		for i, r := range rows {
			order[i] = r.ID
		}
		pos := slices.Index(order, int32(tid))
		if pos < 0 {
			return pgx.ErrNoRows
		}
		before := slices.Clone(order)
		id := order[pos]
		switch opName {
		case opRemoveFromContest:
			order = slices.Delete(order, pos, pos+1)
		case opMoveUp:
			if pos > 0 {
				order[pos-1], order[pos] = order[pos], order[pos-1]
			}
		case opMoveDown:
			if pos+1 < len(order) {
				order[pos+1], order[pos] = order[pos], order[pos+1]
			}
		case opMoveTop:
			order = append([]int32{id}, slices.Delete(order, pos, pos+1)...)
		case opMoveBottom:
			order = append(slices.Delete(order, pos, pos+1), id)
		}
		if err := tx.Q.AdminClearTaskNums(tx.Ctx, &cid); err != nil {
			return err
		}
		if opName == opRemoveFromContest {
			if _, err := tx.Q.SetTaskPosition(tx.Ctx, q.SetTaskPositionParams{ID: id}); err != nil {
				return err
			}
		}
		if err := setTaskOrder(tx, cid, order); err != nil {
			return err
		}
		tx.Publish(contestSettingsEvents(cid)...)
		action := "contest.tasks.reorder"
		if opName == opRemoveFromContest {
			action = "contest.tasks.remove"
		}
		return tx.Audit(AuditEntry{Action: action, TargetType: "contest", TargetID: int64(cid), ContestID: cid,
			Before: map[string]any{"tasks": before}, After: map[string]any{"tasks": order},
			Details: map[string]any{"task_id": id, "operation": opName}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(fallback)
}

// setTaskOrder numbers the tasks of a contest 0, 1, ... in order (their
// numbers must have been cleared).
func setTaskOrder(tx *Tx, cid int32, order []int32) error {
	nums := make([]int32, len(order))
	for i := range nums {
		nums[i] = int32(i)
	}
	return tx.Q.AdminSetTaskNums(tx.Ctx, q.AdminSetTaskNumsParams{ContestID: &cid, Ids: order, Nums: nums})
}

// addContestTaskPost is POST /contest/<id>/tasks/add: the selected task
// (without a contest) goes at the end of the contest.
func (s *Server) addContestTaskPost(c *Ctx) {
	cid, _ := c.PathID("contest_id")
	fallback := c.URL("contest", cid, "tasks")
	tid, err := strconv.ParseInt(c.ArgDefault("task_id", ""), 10, 32)
	if err != nil {
		c.NotifyError(N("Invalid field(s)"), "Please select a valid task")
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.GetContest(tx.Ctx, cid); err != nil {
			return err
		}
		t, err := tx.Q.GetTask(tx.Ctx, int32(tid))
		if err != nil {
			return err
		}
		if t.ContestID != nil {
			return Invalid("Task %s already belongs to a contest.", t.Name)
		}
		rows, err := tx.Q.AdminContestTasksForUpdate(tx.Ctx, &cid)
		if err != nil {
			return err
		}
		order := make([]int32, 0, len(rows)+1)
		for _, r := range rows {
			order = append(order, r.ID)
		}
		order = append(order, t.ID)
		if err := tx.Q.AdminClearTaskNums(tx.Ctx, &cid); err != nil {
			return err
		}
		if err := setTaskOrder(tx, cid, order); err != nil {
			return err
		}
		tx.Publish(contestSettingsEvents(cid)...)
		return tx.Audit(AuditEntry{Action: "contest.tasks.add", TargetType: "contest", TargetID: int64(cid),
			ContestID: cid, Details: map[string]any{"task_id": t.ID, "task": t.Name, "num": len(order) - 1}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(fallback)
}

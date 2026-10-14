// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The rules are those of cmscontrib/ImportContest.py and importing.py of
// CMS:
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// ContestOptions are the options of a contest import (cmsImportContest).
type ContestOptions struct {
	// ImportTasks creates the tasks that are not in the database (-i);
	// without it a missing task is an error.
	ImportTasks bool
	// UpdateContest updates an existing contest and its groups (-u).
	UpdateContest bool
	// UpdateTasks updates the existing tasks (-U, see WriteTask).
	UpdateTasks bool
	// SkipStatements leaves the statements alone (-S).
	SkipStatements bool
	// ImportUsers creates the users and teams of the package that are not
	// in the database (--users; upstream needs cmsImportUser first).
	// Existing ones are only linked.
	ImportUsers bool
	// ZeroTime sets the start and stop of every group to 1970-01-01 (-z).
	ZeroTime bool
	// DeleteStaleParticipations deletes the participations of users that
	// are not in the package, with their submissions; without it they are
	// kept (and the report warns).
	DeleteStaleParticipations bool
	// Report, if set, receives the issues.
	Report *Report
}

// ZeroTime is the start and stop of the groups with ContestOptions.ZeroTime.
var ZeroTime = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// WriteContest stores a contest (cmsImportContest) with the tasks it needs:
// tasks holds, by name, the tasks of c.Tasks that were loaded (those to
// create with ImportTasks, those to update with UpdateTasks); the others
// must be in the database.
//
// A new contest is created with its groups. An existing contest is an
// error unless UpdateContest or UpdateTasks; UpdateContest updates its
// columns and its groups by name (groups not in the package are kept).
// The tasks of the contest become c.Tasks in that order: missing tasks are
// created (ImportTasks), existing ones updated (UpdateTasks), tasks of the
// contest not in the list are detached, and a task of another contest is
// an error. Participations are created or updated (only the fields given
// change; the group is always set); their users and teams must exist or be
// created with ImportUsers.
func (w *Writer) WriteContest(ctx context.Context, c *Contest, tasks map[string]*Task, opts ContestOptions) (*Result, error) {
	res := newResult(opts.Report)
	v := newValidator(w.langs(), res.Report)
	v.skipStatements = opts.SkipStatements
	v.contest(c)
	var loaded []*Task
	for _, name := range c.Tasks {
		t := tasks[name]
		if t == nil {
			continue
		}
		if t.Name != name {
			res.Report.Errorf(taskWhere(name), "the package of task %q contains task %q", name, t.Name)
		}
		v.task(t)
		v.taskInContest(c, t)
		loaded = append(loaded, t)
	}
	if err := res.Report.Err(); err != nil {
		return res, err
	}
	if err := w.upload(ctx, loaded, opts.SkipStatements); err != nil {
		return res, err
	}
	groups := c.Groups
	if opts.ZeroTime {
		groups = append([]Group(nil), groups...)
		for i := range groups {
			groups[i].Start, groups[i].Stop = ZeroTime, ZeroTime
		}
	}
	var out *Result
	err := w.inTx(ctx, func(tx pgx.Tx, qq *q.Queries) error {
		x := newTxWriter(ctx, tx, qq, opts.SkipStatements)
		if err := x.writeContest(c, groups, tasks, opts); err != nil {
			return err
		}
		out = x.res
		return nil
	})
	if err != nil {
		return res, err
	}
	w.done(res, out)
	return res, nil
}

func (x *txWriter) writeContest(c *Contest, groups []Group, tasks map[string]*Task, opts ContestOptions) error {
	ctx, qq := x.ctx, x.qq
	want := contestParams(c)
	row, err := qq.ImportLockContest(ctx, c.Name)
	created := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row, err = qq.CreateContest(ctx, q.CreateContestParams{
			Name: want.Name, Description: want.Description, AllowedLocalizations: want.AllowedLocalizations,
			Languages: want.Languages, SubmissionsDownloadAllowed: want.SubmissionsDownloadAllowed,
			AllowQuestions: want.AllowQuestions, AllowUserTests: want.AllowUserTests,
			AllowUnofficialSubmissionBeforeAnalysisMode: want.AllowUnofficialSubmissionBeforeAnalysisMode,
			BlockHiddenParticipations:                   want.BlockHiddenParticipations,
			AllowPasswordAuthentication:                 want.AllowPasswordAuthentication,
			AllowRegistration:                           want.AllowRegistration, IpRestriction: want.IpRestriction,
			IpAutologin: want.IpAutologin, TokenMode: want.TokenMode, TokenMaxNumber: want.TokenMaxNumber,
			TokenMinInterval: want.TokenMinInterval, TokenGenInitial: want.TokenGenInitial,
			TokenGenNumber: want.TokenGenNumber, TokenGenInterval: want.TokenGenInterval, TokenGenMax: want.TokenGenMax,
			Timezone: want.Timezone, MaxSubmissionNumber: want.MaxSubmissionNumber,
			MaxUserTestNumber: want.MaxUserTestNumber, MinSubmissionInterval: want.MinSubmissionInterval,
			MinSubmissionIntervalGracePeriod: want.MinSubmissionIntervalGracePeriod,
			MinUserTestInterval:              want.MinUserTestInterval, ScorePrecision: want.ScorePrecision,
		})
		if err != nil {
			return fmt.Errorf("contest %s: %w", c.Name, err)
		}
		created = true
	case err != nil:
		return err
	case !opts.UpdateContest && !opts.UpdateTasks:
		return errContestExists(c.Name)
	}
	x.res.ContestID = row.ID

	// The contest and its groups.
	var groupIDs map[string]int32
	var mainGroup int32
	if created || opts.UpdateContest {
		var gd delta
		if groupIDs, gd, err = x.syncGroups(row.ID, groups); err != nil {
			return err
		}
		main, ok := groupIDs[c.MainGroupName()]
		if !ok {
			return fmt.Errorf("contest %s: the main group %q does not exist", c.Name, c.MainGroupName())
		}
		mainGroup = main
		want.ID, want.MainGroupID = row.ID, &main
		cur := currentContestParams(row)
		switch {
		case created:
			if _, err := qq.SetContestMainGroup(ctx, q.SetContestMainGroupParams{ID: row.ID, MainGroupID: &main}); err != nil {
				return err
			}
			x.res.add(Created, "contest", c.Name, row.ID, nonZero(len(groups), "group"))
		case !reflect.DeepEqual(cur, want):
			if _, err := qq.UpdateContest(ctx, want); err != nil {
				return fmt.Errorf("contest %s: %w", c.Name, err)
			}
			x.res.add(Updated, "contest", c.Name, row.ID, joinDetails("settings changed", gd.describe("group")))
		case !gd.zero():
			x.res.add(Updated, "contest", c.Name, row.ID, gd.describe("group"))
		default:
			x.res.add(Unchanged, "contest", c.Name, row.ID, "")
		}
	} else {
		x.res.add(Unchanged, "contest", c.Name, row.ID, "not updated (use --update-contest to update it)")
		gs, err := qq.ListGroupsByContest(ctx, row.ID)
		if err != nil {
			return err
		}
		groupIDs = make(map[string]int32, len(gs))
		for _, g := range gs {
			groupIDs[g.Name] = g.ID
		}
		if row.MainGroupID == nil {
			return fmt.Errorf("contest %s has no main group", c.Name)
		}
		mainGroup = *row.MainGroupID
	}

	// The tasks, in order.
	rows, err := qq.ImportTasksByName(ctx, c.Tasks)
	if err != nil {
		return err
	}
	existing := make(map[string]q.ImportTasksByNameRow, len(rows))
	for _, r := range rows {
		existing[r.Task.Name] = r
	}
	ids := make([]int32, len(c.Tasks))
	nums := make([]int32, len(c.Tasks))
	for i, name := range c.Tasks {
		nums[i] = int32(i)
		r, ok := existing[name]
		t := tasks[name]
		switch {
		case !ok && (!opts.ImportTasks || t == nil):
			return fmt.Errorf("task %q not found in the database: use --import-tasks to import it", name)
		case !ok:
			task, err := x.createTask(t, nil, nil)
			if err != nil {
				return err
			}
			ids[i] = task.ID
			continue
		}
		if r.Task.ContestID != nil && *r.Task.ContestID != row.ID {
			return fmt.Errorf("task %q is already tied to contest %q", name, deref(r.ContestName))
		}
		ids[i] = r.Task.ID
		x.res.TaskIDs[name] = r.Task.ID
		switch {
		case opts.UpdateTasks && t != nil:
			if err := x.updateTask(r.Task, t); err != nil {
				return err
			}
			x.res.Events = append(x.res.Events, judging.Event{Kind: judging.EventDataset, ContestID: row.ID, TaskID: r.Task.ID})
		case opts.UpdateTasks:
			x.res.Report.Warnf(taskWhere(name), "not loaded, not updated")
		default:
			x.res.add(Skipped, "task", name, r.Task.ID, "exists (use --update-tasks to update it)")
		}
	}
	if !created {
		if _, err := qq.ImportDetachContestTasks(ctx, row.ID); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		if _, err := qq.ImportAttachTasks(ctx, q.ImportAttachTasksParams{ContestID: row.ID, Ids: ids, Nums: nums}); err != nil {
			return fmt.Errorf("contest %s: attaching the tasks: %w", c.Name, err)
		}
	}

	// People.
	tg := participationTargets{contestID: row.ID, groups: groupIDs, mainGroup: mainGroup}
	if opts.ImportUsers {
		if tg.users, err = x.writeUsers(c.Users, false, false); err != nil {
			return err
		}
		if tg.teams, err = x.writeTeams(c.Teams, false, false); err != nil {
			return err
		}
	}
	if len(c.Participations) > 0 || opts.DeleteStaleParticipations {
		if err := x.writeParticipations(tg, c.Participations, opts.DeleteStaleParticipations); err != nil {
			return err
		}
	}
	x.res.Events = append(x.res.Events, judging.Event{Kind: judging.EventSweep, ContestID: row.ID})
	return nil
}

// groupRow is a group as the JSON of ImportUpsertGroups.
type groupRow struct {
	Name            string    `json:"name"`
	Start           time.Time `json:"start"`
	Stop            time.Time `json:"stop"`
	AnalysisEnabled bool      `json:"analysis_enabled"`
	AnalysisStart   time.Time `json:"analysis_start"`
	AnalysisStop    time.Time `json:"analysis_stop"`
	PerUserTimeUS   *int64    `json:"per_user_time_us"`
}

func groupRowOf(g Group) groupRow {
	return groupRow{
		Name: g.Name, Start: usecTime(g.Start), Stop: usecTime(g.Stop), AnalysisEnabled: g.AnalysisEnabled,
		AnalysisStart: usecTime(g.AnalysisStart), AnalysisStop: usecTime(g.AnalysisStop),
		PerUserTimeUS: microseconds(g.PerUserTime),
	}
}

// sameGroup compares groups, the times as instants.
func sameGroup(a, b groupRow) bool {
	return a.Name == b.Name && a.Start.Equal(b.Start) && a.Stop.Equal(b.Stop) &&
		a.AnalysisEnabled == b.AnalysisEnabled && a.AnalysisStart.Equal(b.AnalysisStart) &&
		a.AnalysisStop.Equal(b.AnalysisStop) && reflect.DeepEqual(a.PerUserTimeUS, b.PerUserTimeUS)
}

// syncGroups creates the new groups of a contest and updates those that
// changed (upstream update_contest: groups not in the package are kept),
// and returns the ids of all its groups by name.
func (x *txWriter) syncGroups(contestID int32, groups []Group) (map[string]int32, delta, error) {
	var d delta
	existing, err := x.qq.ListGroupsByContest(x.ctx, contestID)
	if err != nil {
		return nil, d, err
	}
	byName := make(map[string]q.Group, len(existing))
	for _, g := range existing {
		byName[g.Name] = g
	}
	var upsert []groupRow
	for _, g := range groups {
		want := groupRowOf(g)
		ex, ok := byName[g.Name]
		if ok {
			cur := groupRow{
				Name: ex.Name, Start: ex.Start.UTC(), Stop: ex.Stop.UTC(), AnalysisEnabled: ex.AnalysisEnabled,
				AnalysisStart: ex.AnalysisStart.UTC(), AnalysisStop: ex.AnalysisStop.UTC(),
				PerUserTimeUS: microseconds(ex.PerUserTime),
			}
			if sameGroup(cur, want) {
				continue
			}
			d.changed++
		} else {
			d.added++
		}
		upsert = append(upsert, want)
	}
	ids := make(map[string]int32, len(existing)+len(upsert))
	for _, g := range existing {
		ids[g.Name] = g.ID
	}
	if len(upsert) > 0 {
		b, err := json.Marshal(upsert)
		if err != nil {
			return nil, d, err
		}
		written, err := x.qq.ImportUpsertGroups(x.ctx, q.ImportUpsertGroupsParams{ContestID: contestID, Groups: b})
		if err != nil {
			return nil, d, fmt.Errorf("groups: %w", err)
		}
		for _, g := range written {
			ids[g.Name] = g.ID
		}
	}
	return ids, d, nil
}

// contestParams returns the columns of c as an update (without id and
// main group).
func contestParams(c *Contest) q.UpdateContestParams {
	return q.UpdateContestParams{
		Name:                       c.Name,
		Description:                c.Description,
		AllowedLocalizations:       nonNil(c.AllowedLocalizations),
		Languages:                  nonNil(c.Languages),
		SubmissionsDownloadAllowed: c.SubmissionsDownloadAllowed,
		AllowQuestions:             c.AllowQuestions,
		AllowUserTests:             c.AllowUserTests,
		AllowUnofficialSubmissionBeforeAnalysisMode: c.AllowUnofficialSubmissionBeforeAnalysisMode,
		BlockHiddenParticipations:                   c.BlockHiddenParticipations,
		AllowPasswordAuthentication:                 c.AllowPasswordAuthentication,
		AllowRegistration:                           c.AllowRegistration,
		IpRestriction:                               c.IPRestriction,
		IpAutologin:                                 c.IPAutologin,
		TokenMode:                                   c.Tokens.Mode,
		TokenMaxNumber:                              c.Tokens.MaxNumber,
		TokenMinInterval:                            usec(c.Tokens.MinInterval),
		TokenGenInitial:                             c.Tokens.GenInitial,
		TokenGenNumber:                              c.Tokens.GenNumber,
		TokenGenInterval:                            usec(c.Tokens.GenInterval),
		TokenGenMax:                                 c.Tokens.GenMax,
		Timezone:                                    c.Timezone,
		MaxSubmissionNumber:                         c.MaxSubmissionNumber,
		MaxUserTestNumber:                           c.MaxUserTestNumber,
		MinSubmissionInterval:                       usecPtr(c.MinSubmissionInterval),
		MinSubmissionIntervalGracePeriod:            usecPtr(c.MinSubmissionIntervalGracePeriod),
		MinUserTestInterval:                         usecPtr(c.MinUserTestInterval),
		ScorePrecision:                              c.ScorePrecision,
	}
}

func currentContestParams(c q.Contest) q.UpdateContestParams {
	return q.UpdateContestParams{
		ID:                         c.ID,
		Name:                       c.Name,
		Description:                c.Description,
		AllowedLocalizations:       nonNil(c.AllowedLocalizations),
		Languages:                  nonNil(c.Languages),
		SubmissionsDownloadAllowed: c.SubmissionsDownloadAllowed,
		AllowQuestions:             c.AllowQuestions,
		AllowUserTests:             c.AllowUserTests,
		AllowUnofficialSubmissionBeforeAnalysisMode: c.AllowUnofficialSubmissionBeforeAnalysisMode,
		BlockHiddenParticipations:                   c.BlockHiddenParticipations,
		AllowPasswordAuthentication:                 c.AllowPasswordAuthentication,
		AllowRegistration:                           c.AllowRegistration,
		IpRestriction:                               c.IpRestriction,
		IpAutologin:                                 c.IpAutologin,
		TokenMode:                                   c.TokenMode,
		TokenMaxNumber:                              c.TokenMaxNumber,
		TokenMinInterval:                            c.TokenMinInterval,
		TokenGenInitial:                             c.TokenGenInitial,
		TokenGenNumber:                              c.TokenGenNumber,
		TokenGenInterval:                            c.TokenGenInterval,
		TokenGenMax:                                 c.TokenGenMax,
		Timezone:                                    c.Timezone,
		MaxSubmissionNumber:                         c.MaxSubmissionNumber,
		MaxUserTestNumber:                           c.MaxUserTestNumber,
		MinSubmissionInterval:                       c.MinSubmissionInterval,
		MinSubmissionIntervalGracePeriod:            c.MinSubmissionIntervalGracePeriod,
		MinUserTestInterval:                         c.MinUserTestInterval,
		ScorePrecision:                              c.ScorePrecision,
		MainGroupID:                                 c.MainGroupID,
	}
}

// ErrExists is returned (wrapped) when an import finds in the database a
// contest or task that it was not asked to update.
var ErrExists = errors.New("already exists in the database")

func errContestExists(name string) error {
	return fmt.Errorf("contest %q %w: use --update-contest (or --update-tasks) to update it", name, ErrExists)
}

// usecTime rounds a time to what PostgreSQL stores (microseconds), in UTC.
func usecTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

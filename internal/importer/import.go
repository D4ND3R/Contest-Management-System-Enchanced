// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/ImportContest.py, ImportTask.py, ImportUser.py,
// ImportTeam.py and ImportDataset.py of CMS:
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>

package importer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// loadOptions are the options of the loaders of an import.
func (w *Writer) loadOptions(rep *Report, skipStatements bool) *Options {
	return &Options{Languages: w.langs(), SkipStatements: skipStatements, Report: rep}
}

// ImportContest loads the contest at dir with l and writes it
// (cmsImportContest): the tasks that are not in the database are loaded
// with opts.ImportTasks, the existing ones with opts.UpdateTasks, the
// others are not read at all. See WriteContest.
func (w *Writer) ImportContest(ctx context.Context, l ContestLoader, dir string, opts ContestOptions) (*Result, error) {
	if opts.Report == nil {
		opts.Report = &Report{}
	}
	lo := w.loadOptions(opts.Report, opts.SkipStatements)
	c, err := l.LoadContest(dir, lo)
	if err != nil {
		return newResult(opts.Report), fmt.Errorf("loading the contest at %s: %w", dir, err)
	}
	if c == nil {
		return newResult(opts.Report), fmt.Errorf("loading the contest at %s: %w", dir, opts.Report.Err())
	}
	// An existing contest without an update flag is an error: say it
	// before reading the tasks (the transaction checks again).
	if !opts.UpdateContest && !opts.UpdateTasks {
		if _, err := q.New(w.Pool).ImportLockContest(ctx, c.Name); err == nil {
			return newResult(opts.Report), errContestExists(c.Name)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return newResult(opts.Report), err
		}
	}
	// Which tasks exist decides which ones to read; the transaction looks
	// again (a task created meanwhile is an error, nothing is written).
	rows, err := q.New(w.Pool).ImportTasksByName(ctx, c.Tasks)
	if err != nil {
		return newResult(opts.Report), err
	}
	exists := make(map[string]bool, len(rows))
	for _, r := range rows {
		exists[r.Task.Name] = true
	}
	tasks := make(map[string]*Task)
	for _, name := range c.Tasks {
		if exists[name] && !opts.UpdateTasks || !exists[name] && !opts.ImportTasks {
			continue
		}
		if _, ok := tasks[name]; ok {
			continue // a duplicate: the validation reports it
		}
		t, err := loadTaskOf(l, dir, name, lo)
		if err != nil {
			return newResult(opts.Report), err
		}
		tasks[name] = t
	}
	return w.WriteContest(ctx, c, tasks, opts)
}

// loadTaskOf loads a task of a contest.
func loadTaskOf(l ContestLoader, dir, name string, lo *Options) (*Task, error) {
	tl, tdir := l.TaskLoader(dir, name)
	if tl == nil {
		return nil, fmt.Errorf("task %s: the contest loader %s has no loader for it", name, l.Name())
	}
	t, err := tl.LoadTask(tdir, lo)
	if err != nil {
		return nil, fmt.Errorf("loading task %s at %s: %w", name, tdir, err)
	}
	if t == nil {
		return nil, fmt.Errorf("loading task %s at %s: %w", name, tdir, lo.Report.Err())
	}
	return t, nil
}

// TaskImport are the options of ImportTask besides those of WriteTask.
type TaskImport struct {
	TaskOptions
	// Name replaces the name of the task (-n); Prefix is added before it
	// (-p).
	Name, Prefix string
}

// ImportTask loads the task at dir with l and writes it (cmsImportTask).
func (w *Writer) ImportTask(ctx context.Context, l TaskLoader, dir string, opts TaskImport) (*Result, error) {
	if opts.Report == nil {
		opts.Report = &Report{}
	}
	t, err := l.LoadTask(dir, w.loadOptions(opts.Report, opts.SkipStatements))
	if err == nil && t == nil {
		err = opts.Report.Err()
	}
	if err != nil {
		return newResult(opts.Report), fmt.Errorf("loading the task at %s: %w", dir, err)
	}
	if opts.Name != "" {
		t.Name = opts.Name
	}
	t.Name = opts.Prefix + t.Name
	return w.WriteTask(ctx, t, opts.TaskOptions)
}

// ImportUsers loads the users of dir with l and writes them
// (cmsImportUser --all).
func (w *Writer) ImportUsers(ctx context.Context, l UserLoader, dir string, opts UserOptions) (*Result, error) {
	if opts.Report == nil {
		opts.Report = &Report{}
	}
	users, err := l.LoadUsers(dir, w.loadOptions(opts.Report, false))
	if err != nil {
		return newResult(opts.Report), fmt.Errorf("loading the users at %s: %w", dir, err)
	}
	return w.WriteUsers(ctx, users, opts)
}

// ImportTeams loads the teams of dir with l and writes them
// (cmsImportTeam --all).
func (w *Writer) ImportTeams(ctx context.Context, l TeamLoader, dir string, opts TeamOptions) (*Result, error) {
	if opts.Report == nil {
		opts.Report = &Report{}
	}
	teams, err := l.LoadTeams(dir, w.loadOptions(opts.Report, false))
	if err != nil {
		return newResult(opts.Report), fmt.Errorf("loading the teams at %s: %w", dir, err)
	}
	return w.WriteTeams(ctx, teams, opts)
}

// ImportDataset loads the task at dir with l and adds its active dataset,
// with a new description, to the task of the same name in the database
// (cmsImportDataset). The new dataset is not active.
func (w *Writer) ImportDataset(ctx context.Context, l TaskLoader, dir, description string, rep *Report) (*Result, error) {
	if rep == nil {
		rep = &Report{}
	}
	res := newResult(rep)
	t, err := l.LoadTask(dir, w.loadOptions(rep, true))
	if err == nil && t == nil {
		err = rep.Err()
	}
	if err != nil {
		return res, fmt.Errorf("loading the task at %s: %w", dir, err)
	}
	d := t.Active()
	if d == nil {
		return res, fmt.Errorf("the task at %s has no dataset", dir)
	}
	if description == "" {
		return res, errors.New("the new dataset needs a description")
	}
	d.Description = description
	// Only the dataset is stored: its files are the only ones read.
	t.Datasets, t.ActiveDataset = []*Dataset{d}, ""
	t.Statements, t.PrimaryStatements, t.Attachments = nil, nil, nil
	v := newValidator(w.langs(), rep)
	v.skipStatements = true
	v.task(t)
	if err := rep.Err(); err != nil {
		return res, err
	}
	if err := w.upload(ctx, []*Task{t}, true); err != nil {
		return res, err
	}
	var out *Result
	err = w.inTx(ctx, func(tx pgx.Tx, qq *q.Queries) error {
		x := newTxWriter(ctx, tx, qq, true)
		rows, err := qq.ImportTasksByName(ctx, []string{t.Name})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("task %q does not exist in the database", t.Name)
		}
		task := rows[0].Task
		existing, err := qq.ListDatasetsByTask(ctx, task.ID)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.Description == description {
				return fmt.Errorf("task %s already has a dataset %q", t.Name, description)
			}
		}
		ds, err := x.createDataset(t, task.ID, d)
		if err != nil {
			return err
		}
		x.res.TaskIDs[t.Name] = task.ID
		x.res.add(Created, "dataset", fmt.Sprintf("%s %q", t.Name, description), ds.ID, plural(len(d.Testcases), "testcase")+", not active")
		if task.ContestID != nil {
			x.res.ContestID = *task.ContestID
			x.res.Events = append(x.res.Events, judging.Event{Kind: judging.EventDataset, ContestID: *task.ContestID, TaskID: task.ID, DatasetID: ds.ID})
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

// ValidateTaskDir loads the task at dir with l and validates it, without
// a database (`cms ctl validate-task`). The error is that of the loader;
// the problems of the task are in the report.
func ValidateTaskDir(l TaskLoader, dir string, langs *language.Registry) (*Task, *Report, error) {
	if langs == nil {
		langs = language.Builtin()
	}
	rep := &Report{}
	t, err := l.LoadTask(dir, &Options{Languages: langs, Report: rep})
	if err != nil {
		return nil, rep, err
	}
	if t == nil {
		return nil, rep, rep.Err()
	}
	ValidateTask(t, langs, rep)
	return t, rep, nil
}

// ValidateContestDir loads the contest at dir and all its tasks with l and
// validates them, without a database (`cms ctl validate-contest`).
func ValidateContestDir(l ContestLoader, dir string, langs *language.Registry) (*Contest, map[string]*Task, *Report, error) {
	if langs == nil {
		langs = language.Builtin()
	}
	rep := &Report{}
	lo := &Options{Languages: langs, Report: rep}
	c, err := l.LoadContest(dir, lo)
	if err != nil {
		return nil, nil, rep, err
	}
	if c == nil {
		return nil, nil, rep, rep.Err()
	}
	ValidateContest(c, langs, rep)
	tasks := make(map[string]*Task, len(c.Tasks))
	for _, name := range c.Tasks {
		if _, ok := tasks[name]; ok {
			continue
		}
		t, err := loadTaskOf(l, dir, name, lo)
		if err != nil {
			rep.Errorf(taskWhere(name), "%v", err)
			continue
		}
		if t.Name != name {
			rep.Errorf(taskWhere(name), "the package of task %q contains task %q", name, t.Name)
		}
		ValidateTask(t, langs, rep)
		ValidateTaskInContest(c, t, langs, rep)
		tasks[name] = t
	}
	return c, tasks, rep, nil
}

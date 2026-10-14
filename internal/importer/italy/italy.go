// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/italy_yaml.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2015-2019 Luca Chiodini <luca@chiodini.org>
// Copyright © 2016 Andrea Cracco <guilucand@gmail.com>
// Copyright © 2018 Edoardo Morassutto <edoardo.morassutto@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Jonathan Baumann <jonathan.baumann@edu.ruhr-uni-bochum.de>

// Package italy reads the Italian YAML format of CMS (italy_yaml): a
// contest directory with contest.yaml (contest settings, groups, users,
// teams and the list of tasks) and one directory per task with task.yaml,
// statement/ (or testo/), att/, input/, output/, gen/GEN, sol/ (graders,
// stubs and headers) and check/ (or cor/: checker, manager, controller).
//
// It is a port of cmscontrib/loaders/italy_yaml.py: the same keys (with
// their Italian aliases), the same defaults and the same deductions (the
// task type from the files present, the subtasks from the #ST: lines of
// gen/GEN, the deprecated token format...). Differences:
//
//   - Nothing is written to the package (.itime, .import_error): the
//     importer compares the package with the database.
//   - What upstream reports with logger.critical and sys.exit, or by
//     crashing (an assert, a missing key), is an error in the report, and
//     the load goes on so that one run lists every problem.
//   - analysis_start is read (upstream ignores it in make_group, so a
//     contest with an analysis window failed to import); analysis_end is
//     accepted as an alias of analysis_stop.
//   - YAML timestamps without a zone are UTC (upstream: the local time of
//     the machine), and times may also be strings such as
//     "2025-01-01 10:00:00".
//   - A user without a password gets no participation password (upstream
//     crashes); the writer gives new users a random one.
//   - The output-only testcases of BatchAndOutput are sorted (upstream
//     joins a Python set, in no particular order).
//   - Unknown values of feedback_level, score_mode and user_io are
//     reported as warnings (upstream ignores them silently, except
//     user_io).
package italy

import (
	"path/filepath"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// Name is the name of the loader (-L italy_yaml).
const Name = "italy_yaml"

// Loader reads contests, tasks, users and teams in the Italian YAML
// format (upstream YamlLoader). It is stateless.
type Loader struct{}

func init() { importer.Register(Loader{}) }

var (
	_ importer.ContestLoader = Loader{}
	_ importer.TaskLoader    = Loader{}
	_ importer.UserLoader    = Loader{}
	_ importer.TeamLoader    = Loader{}
)

// Name implements importer.Loader.
func (Loader) Name() string { return Name }

// Description implements importer.Loader.
func (Loader) Description() string { return "Italian YAML-based format" }

// Detect implements importer.Loader (upstream detect, by kind): a contest
// has contest.yaml; a task has task.yaml, or ../<name>.yaml (the
// deprecated location); users and teams are listed in contest.yaml, given
// as the contest directory or, as upstream, as <contest>/<username>.
func (Loader) Detect(dir string, kind importer.Kind) bool {
	switch kind {
	case importer.KindContest:
		return isFile(filepath.Join(dir, "contest.yaml"))
	case importer.KindTask:
		return isFile(filepath.Join(dir, "task.yaml")) ||
			isFile(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+".yaml"))
	case importer.KindUsers, importer.KindTeams:
		return isFile(filepath.Join(dir, "contest.yaml")) || isFile(filepath.Join(filepath.Dir(dir), "contest.yaml"))
	}
	return false
}

// TaskLoader implements importer.ContestLoader: the tasks are the
// subdirectories of the contest named after them (upstream
// get_task_loader).
func (l Loader) TaskLoader(dir, name string) (importer.TaskLoader, string) {
	return l, filepath.Join(dir, name)
}

// options fills what a caller outside the importer may leave out.
func options(opts *importer.Options) *importer.Options {
	if opts == nil {
		opts = &importer.Options{}
	}
	o := *opts
	if o.Languages == nil {
		o.Languages = language.Builtin()
	}
	if o.Report == nil {
		o.Report = &importer.Report{}
	}
	return &o
}

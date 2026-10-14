// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// check links every row to its parents (dropping the rows whose parent is
// left out by the options), and validates the dump as the other imports
// do: the constraints of the database, and what the judge can run (task
// and score types with their parameters, managers, languages). Problems
// go to the report.
func (d *Dump) check(langs *language.Registry) {
	for _, cls := range sortedKeys(d.unknown) {
		d.rep.Warnf(modelName, "%s of the unknown class %s ignored", plural(d.unknown[cls], "object"), cls)
	}
	d.link()
	d.validate(langs)
}

// resolve checks a reference of row i (named name) to an object of class
// cls. It returns false if the row must be left out: its parent is left
// out (silently), or the reference is wrong (an error).
func (d *Dump) resolve(i int32, name, field string, r int32, cls string, required bool) bool {
	switch {
	case r == none:
		if required {
			d.rep.Errorf(d.where(i, name), "no %s", field)
			return false
		}
		return true
	case d.class[r] == "":
		d.rep.Errorf(d.where(i, name), "%s: object %s is not in the dump", field, d.keys[r])
		return false
	case d.class[r] != cls:
		d.rep.Errorf(d.where(i, name), "%s: object %s is a %s, not a %s", field, d.keys[r], d.class[r], cls)
		return false
	case d.skip[r]:
		return false
	}
	return true
}

// keep filters rows: those for which ok returns false are marked as
// skipped and removed.
func keep[T any](d *Dump, rows []*T, key func(*T) int32, ok func(*T) bool) []*T {
	out := rows[:0]
	for _, r := range rows {
		if d.skip[key(r)] {
			continue
		}
		if !ok(r) {
			d.skip[key(r)] = true
			continue
		}
		out = append(out, r)
	}
	clear(rows[len(out):])
	return out
}

func (d *Dump) link() {
	if d.opts.SkipUsers {
		// cmsDumpImporter imports what the roots reach once the objects
		// left out are gone: without the users, the contests that are in
		// the dump only through the participations of the contestants of
		// the exported ones (cmsDumpExporter -c) are not reached.
		exported := d.exportedContests()
		d.contests = keep(d, d.contests, func(c *contest) int32 { return c.key }, func(c *contest) bool {
			if !exported[c.key] {
				d.rep.Warnf(d.where(c.key, c.name), "left out with its tasks: the dump has the contest only because its "+
					"contestants also took part in an exported contest (the dump was written with cmsDumpExporter -c), "+
					"and the users are left out")
			}
			return exported[c.key]
		})
	}
	groupContest := map[int32]int32{}
	d.groups = keep(d, d.groups, func(g *group) int32 { return g.key }, func(g *group) bool {
		if !d.resolve(g.key, g.name, "contest", g.contest, "Contest", true) {
			return false
		}
		groupContest[g.key] = g.contest
		return true
	})
	for _, c := range d.contests {
		if c.mainGroup == none {
			continue
		}
		if !d.resolve(c.key, c.name, "main_group", c.mainGroup, "Group", false) {
			c.mainGroup = none
		} else if groupContest[c.mainGroup] != c.key {
			d.rep.Errorf(d.where(c.key, c.name), "main_group: group %s belongs to another contest", d.keys[c.mainGroup])
		}
	}
	adminUsed := func(i, a int32) bool {
		if !d.resolve(i, "", "admin", a, "Admin", false) {
			return false
		}
		if a != none {
			d.markAdmin(a)
		}
		return true
	}
	d.participations = keep(d, d.participations, func(p *participation) int32 { return p.key }, func(p *participation) bool {
		ok := d.resolve(p.key, "", "contest", p.contest, "Contest", true) &&
			d.resolve(p.key, "", "user", p.user, "User", true) &&
			d.resolve(p.key, "", "group", p.group, "Group", true) &&
			d.resolve(p.key, "", "team", p.team, "Team", false)
		if ok && groupContest[p.group] != p.contest {
			d.rep.Errorf(d.where(p.key, ""), "group: group %s belongs to another contest", d.keys[p.group])
		}
		return ok
	})
	d.announcements = keep(d, d.announcements, func(a *announcement) int32 { return a.key }, func(a *announcement) bool {
		return d.resolve(a.key, "", "contest", a.contest, "Contest", true) && adminUsed(a.key, a.admin)
	})
	d.messages = keep(d, d.messages, func(m *message) int32 { return m.key }, func(m *message) bool {
		return d.resolve(m.key, "", "participation", m.participation, "Participation", true) && adminUsed(m.key, m.admin)
	})
	d.questions = keep(d, d.questions, func(q *question) int32 { return q.key }, func(q *question) bool {
		return d.resolve(q.key, "", "participation", q.participation, "Participation", true) && adminUsed(q.key, q.admin)
	})
	d.admins = keep(d, d.admins, func(a *admin) int32 { return a.key }, func(a *admin) bool { return a.used })

	d.tasks = keep(d, d.tasks, func(t *task) int32 { return t.key }, func(t *task) bool {
		return d.resolve(t.key, t.name, "contest", t.contest, "Contest", false)
	})
	byTask := func(rows []*fileRow) []*fileRow {
		return keep(d, rows, func(f *fileRow) int32 { return f.key }, func(f *fileRow) bool {
			return d.resolve(f.key, f.name, "task", f.parent, "Task", true)
		})
	}
	d.statements = byTask(d.statements)
	d.attachments = byTask(d.attachments)
	datasetTask := map[int32]int32{}
	d.datasets = keep(d, d.datasets, func(x *dataset) int32 { return x.key }, func(x *dataset) bool {
		if !d.resolve(x.key, x.description, "task", x.task, "Task", true) {
			return false
		}
		datasetTask[x.key] = x.task
		return true
	})
	for _, t := range d.tasks {
		if t.activeDataset == none {
			continue
		}
		if !d.resolve(t.key, t.name, "active_dataset", t.activeDataset, "Dataset", false) {
			t.activeDataset = none
		} else if datasetTask[t.activeDataset] != t.key {
			d.rep.Errorf(d.where(t.key, t.name), "active_dataset: dataset %s belongs to another task", d.keys[t.activeDataset])
		}
	}
	d.managers = keep(d, d.managers, func(f *fileRow) int32 { return f.key }, func(f *fileRow) bool {
		return d.resolve(f.key, f.name, "dataset", f.parent, "Dataset", true)
	})
	d.testcases = keep(d, d.testcases, func(t *testcase) int32 { return t.key }, func(t *testcase) bool {
		return d.resolve(t.key, t.codename, "dataset", t.dataset, "Dataset", true)
	})

	d.submissions = keep(d, d.submissions, func(s *submission) int32 { return s.key }, func(s *submission) bool {
		return d.resolve(s.key, "", "participation", s.participation, "Participation", true) &&
			d.resolve(s.key, "", "task", s.task, "Task", true)
	})
	bySubmission := func(rows []*fileRow) []*fileRow {
		return keep(d, rows, func(f *fileRow) int32 { return f.key }, func(f *fileRow) bool {
			return d.resolve(f.key, f.name, "submission", f.parent, "Submission", true)
		})
	}
	d.files = bySubmission(d.files)
	d.tokens = keep(d, d.tokens, func(t *token) int32 { return t.key }, func(t *token) bool {
		return d.resolve(t.key, "", "submission", t.submission, "Submission", true)
	})
	type pair struct{ object, dataset int32 }
	results := map[int32]pair{}
	resultKeys := map[pair]bool{}
	d.results = keep(d, d.results, func(r *submissionResult) int32 { return r.key }, func(r *submissionResult) bool {
		if !d.resolve(r.key, "", "submission", r.submission, "Submission", true) ||
			!d.resolve(r.key, "", "dataset", r.dataset, "Dataset", true) {
			return false
		}
		results[r.key] = pair{r.submission, r.dataset}
		resultKeys[pair{r.submission, r.dataset}] = true
		return true
	})
	// fromResult takes the submission (or user test) and dataset of a row
	// from its result, which must be imported.
	fromResult := func(i int32, name string, result int32, cls, field string, results map[int32]pair, keys map[pair]bool,
		object, dataset *int32, objectClass, objectField string) bool {
		if result != none {
			if !d.resolve(i, name, field, result, cls, true) {
				return false
			}
			p := results[result]
			*object, *dataset = p.object, p.dataset
			return true
		}
		if !d.resolve(i, name, objectField, *object, objectClass, true) || !d.resolve(i, name, "dataset", *dataset, "Dataset", true) {
			return false
		}
		if !keys[pair{*object, *dataset}] {
			// The result is not in the dump (or left out).
			if !d.opts.SkipGenerated {
				d.rep.Errorf(d.where(i, name), "no result of %s %s on dataset %s", strings.ToLower(objectClass), d.keys[*object], d.keys[*dataset])
			}
			return false
		}
		return true
	}
	d.executables = keep(d, d.executables, func(f *fileRow) int32 { return f.key }, func(f *fileRow) bool {
		return fromResult(f.key, f.name, f.result, "SubmissionResult", "submission_result", results, resultKeys, &f.parent, &f.dataset, "Submission", "submission")
	})
	d.evaluations = keep(d, d.evaluations, func(e *evaluation) int32 { return e.key }, func(e *evaluation) bool {
		return fromResult(e.key, "", e.result, "SubmissionResult", "submission_result", results, resultKeys, &e.submission, &e.dataset, "Submission", "submission") &&
			d.resolve(e.key, "", "testcase", e.testcase, "Testcase", true)
	})

	d.userTests = keep(d, d.userTests, func(u *userTest) int32 { return u.key }, func(u *userTest) bool {
		return d.resolve(u.key, "", "participation", u.participation, "Participation", true) &&
			d.resolve(u.key, "", "task", u.task, "Task", true)
	})
	byUserTest := func(rows []*fileRow) []*fileRow {
		return keep(d, rows, func(f *fileRow) int32 { return f.key }, func(f *fileRow) bool {
			return d.resolve(f.key, f.name, "user_test", f.parent, "UserTest", true)
		})
	}
	d.userTestFiles = byUserTest(d.userTestFiles)
	d.userTestManager = byUserTest(d.userTestManager)
	utResults := map[int32]pair{}
	utResultKeys := map[pair]bool{}
	d.userTestResults = keep(d, d.userTestResults, func(r *userTestResult) int32 { return r.key }, func(r *userTestResult) bool {
		if !d.resolve(r.key, "", "user_test", r.userTest, "UserTest", true) ||
			!d.resolve(r.key, "", "dataset", r.dataset, "Dataset", true) {
			return false
		}
		utResults[r.key] = pair{r.userTest, r.dataset}
		utResultKeys[pair{r.userTest, r.dataset}] = true
		return true
	})
	d.userTestExecs = keep(d, d.userTestExecs, func(f *fileRow) int32 { return f.key }, func(f *fileRow) bool {
		return fromResult(f.key, f.name, f.result, "UserTestResult", "user_test_result", utResults, utResultKeys, &f.parent, &f.dataset, "UserTest", "user_test")
	})

	d.collectFiles()
}

// markAdmin notes that an imported row names an admin.
func (d *Dump) markAdmin(key int32) {
	for _, a := range d.admins {
		if a.key == key {
			a.used = true
			return
		}
	}
}

// exportedContests returns the contests whose files the dump has: those
// of _objects. cmsDumpExporter -c writes the objects of the contests given
// and of everything they reach, which includes the other contests their
// contestants took part in (through the participations of the users), but
// the files of the contests given only (enumerate_files); these are the
// roots of the dump. Without -c every contest is a root. A dump whose
// roots name none of its contests was not written by cmsDumpExporter: all
// its contests count, and their files are required.
func (d *Dump) exportedContests() map[int32]bool {
	out := make(map[int32]bool, len(d.contests))
	for _, k := range d.roots {
		if i, ok := d.index[k]; ok && d.class[i] == "Contest" {
			out[i] = true
		}
	}
	if len(out) == 0 {
		for _, c := range d.contests {
			out[c.key] = true
		}
	}
	return out
}

// collectFiles notes the files the rows kept need, by SHA-1, with what a
// missing one means: the files of tasks in no contest, or in a contest
// whose files were not exported, may be missing (outside).
func (d *Dump) collectFiles() {
	d.need = map[string]*use{}
	d.exported = d.exportedContests()
	inContest := make(map[int32]bool, len(d.tasks))
	for _, t := range d.tasks {
		inContest[t.key] = t.contest != none && d.exported[t.contest]
	}
	taskOf := map[int32]int32{} // dataset, submission or user test → task
	for _, x := range d.datasets {
		taskOf[x.key] = x.task
	}
	for _, s := range d.submissions {
		taskOf[s.key] = s.task
	}
	for _, u := range d.userTests {
		taskOf[u.key] = u.task
	}
	note := func(digest string, kind fileKind, task int32, row int32, name, field string) {
		if digest == "" || digest == tombstone {
			return
		}
		if kind == required && !inContest[task] {
			kind = outside
		}
		if u := d.need[digest]; u != nil && u.kind >= kind {
			return
		}
		d.need[digest] = &use{kind: kind, where: d.where(row, name) + ": " + field, task: task}
	}
	for _, f := range d.statements {
		note(f.digest, required, f.parent, f.key, f.name, "digest")
	}
	for _, f := range d.attachments {
		note(f.digest, required, f.parent, f.key, f.name, "digest")
	}
	for _, f := range d.managers {
		note(f.digest, required, taskOf[f.parent], f.key, f.name, "digest")
	}
	for _, t := range d.testcases {
		note(t.input, required, taskOf[t.dataset], t.key, t.codename, "input")
		note(t.output, required, taskOf[t.dataset], t.key, t.codename, "output")
	}
	for _, f := range d.files {
		note(f.digest, required, taskOf[f.parent], f.key, f.name, "digest")
	}
	for _, f := range d.executables {
		note(f.digest, generated, taskOf[f.parent], f.key, f.name, "digest")
	}
	for _, u := range d.userTests {
		note(u.input, required, u.task, u.key, "", "input")
	}
	for _, rows := range [][]*fileRow{d.userTestFiles, d.userTestManager} {
		for _, f := range rows {
			note(f.digest, required, taskOf[f.parent], f.key, f.name, "digest")
		}
	}
	for _, f := range d.userTestExecs {
		note(f.digest, generated, taskOf[f.parent], f.key, f.name, "digest")
	}
	sandboxes := func(row int32, field string, l []string) {
		for _, s := range l {
			note(s, optional, none, row, "", field)
		}
	}
	for _, r := range d.results {
		sandboxes(r.key, "compilation_sandbox_digests", r.digests)
	}
	for _, e := range d.evaluations {
		sandboxes(e.key, "evaluation_sandbox_digests", e.digests)
	}
	for _, r := range d.userTestResults {
		if r.output != nil {
			note(*r.output, generated, taskOf[r.userTest], r.key, "", "output")
		}
		sandboxes(r.key, "compilation_sandbox_digests", r.compilation.digests)
		sandboxes(r.key, "evaluation_sandbox_digests", r.execution.digests)
	}
}

// validate checks the rows as the other imports do.
func (d *Dump) validate(langs *language.Registry) {
	if langs == nil {
		langs = language.Builtin()
	}
	unique(d, "contest", d.contests, func(c *contest) (int32, string) { return c.key, c.name })
	unique(d, "task", d.tasks, func(t *task) (int32, string) { return t.key, t.name })
	unique(d, "admin", d.admins, func(a *admin) (int32, string) { return a.key, a.username })
	for _, a := range d.admins {
		if !importer.ValidCodename(a.username) {
			d.rep.Errorf(d.where(a.key, a.username), "invalid username %q", a.username)
		}
		if _, _, err := importer.ParseAuthentication(a.auth); err != nil {
			d.rep.Errorf(d.where(a.key, a.username), "%v", err)
		}
	}

	// Users and teams.
	users := make([]importer.User, len(d.users))
	userName := make(map[int32]string, len(d.users))
	for i, u := range d.users {
		users[i] = importer.User{Username: u.username, FirstName: u.firstName, LastName: u.lastName,
			Password: u.passwd, Email: u.email, Timezone: u.timezone, PreferredLanguages: u.preferredLanguages}
		userName[u.key] = u.username
	}
	importer.ValidateUsers(users, d.rep)
	teams := make([]importer.Team, len(d.teams))
	teamCode := make(map[int32]string, len(d.teams))
	for i, t := range d.teams {
		teams[i] = importer.Team{Code: t.code, Name: t.name}
		teamCode[t.key] = t.code
	}
	importer.ValidateTeams(teams, d.rep)

	// Tasks, with their files and datasets.
	placeholder := importer.FileFromBytes(modelName, nil)
	models := make(map[int32]*importer.Task, len(d.tasks))
	for _, t := range d.tasks {
		m := importer.NewTask(t.name, t.title)
		m.SubmissionFormat, m.PrimaryStatements, m.AllowedLanguages = t.submissionFormat, t.primaryStatements, t.allowedLanguages
		m.Tokens = modelTokens(t.tokens)
		m.MaxSubmissionNumber, m.MaxUserTestNumber = t.maxSubmissionNumber, t.maxUserTestNumber
		m.MinSubmissionInterval, m.MinUserTestInterval = t.minSubmissionInterval, t.minUserTestInterval
		m.FeedbackLevel, m.ScoreMode, m.ScorePrecision = t.feedbackLevel, t.scoreMode, t.scorePrecision
		models[t.key] = m
	}
	for _, s := range d.statements {
		m := models[s.parent]
		m.Statements = append(m.Statements, importer.Statement{Language: s.name, File: placeholder})
	}
	for _, a := range d.attachments {
		m := models[a.parent]
		m.Attachments = append(m.Attachments, importer.Attachment{Filename: a.name, File: placeholder})
	}
	datasets := make(map[int32]*importer.Dataset, len(d.datasets))
	for _, x := range d.datasets {
		ds := &importer.Dataset{Description: x.description, Autojudge: x.autojudge, TimeLimit: x.timeLimit,
			MemoryLimit: x.memoryLimit, TaskType: x.taskType, TaskTypeParameters: x.taskParams,
			ScoreType: x.scoreType, ScoreTypeParameters: x.scoreParams}
		datasets[x.key] = ds
		models[x.task].Datasets = append(models[x.task].Datasets, ds)
	}
	for _, f := range d.managers {
		ds := datasets[f.parent]
		ds.Managers = append(ds.Managers, importer.Manager{Filename: f.name, File: placeholder})
	}
	for _, t := range d.testcases {
		ds := datasets[t.dataset]
		ds.Testcases = append(ds.Testcases, importer.Testcase{Codename: t.codename, Public: t.public,
			Input: placeholder, Output: placeholder})
	}
	for _, t := range d.tasks {
		if t.activeDataset != none {
			models[t.key].ActiveDataset = datasets[t.activeDataset].Description
		}
		importer.ValidateTask(models[t.key], langs, d.rep)
	}

	// Contests, with their groups, tasks and participations.
	contests := make(map[int32]*importer.Contest, len(d.contests))
	for _, c := range d.contests {
		m := importer.NewContest(c.name)
		m.Description, m.AllowedLocalizations, m.Languages = c.description, c.allowedLocalizations, c.languages
		m.SubmissionsDownloadAllowed, m.AllowQuestions, m.AllowUserTests = c.downloadAllowed, c.allowQuestions, c.allowUserTests
		m.AllowUnofficialSubmissionBeforeAnalysisMode, m.BlockHiddenParticipations = c.allowUnofficial, c.blockHidden
		m.AllowPasswordAuthentication, m.AllowRegistration = c.allowPassword, c.allowRegistration
		m.IPRestriction, m.IPAutologin, m.Tokens, m.Timezone = c.ipRestriction, c.ipAutologin, modelTokens(c.tokens), c.timezone
		m.MaxSubmissionNumber, m.MaxUserTestNumber = c.maxSubmissionNumber, c.maxUserTestNumber
		m.MinSubmissionInterval, m.MinSubmissionIntervalGracePeriod = c.minSubmissionInterval, c.minSubmissionIntervalGrace
		m.MinUserTestInterval, m.ScorePrecision = c.minUserTestInterval, c.scorePrecision
		contests[c.key] = m
	}
	groupName := make(map[int32]string, len(d.groups))
	for _, g := range d.groups {
		groupName[g.key] = g.name
		m := contests[g.contest]
		m.Groups = append(m.Groups, importer.Group{Name: g.name, Start: g.start, Stop: g.stop,
			AnalysisEnabled: g.analysisEnabled, AnalysisStart: g.analysisStart, AnalysisStop: g.analysisStop,
			PerUserTime: g.perUserTime})
	}
	inContest := d.contestTasks()
	for _, t := range inContest {
		contests[t.contest].Tasks = append(contests[t.contest].Tasks, t.name)
	}
	for _, p := range d.participations {
		h, u, dt, et := p.hidden, p.unrestricted, p.delayTime, p.extraTime
		mp := importer.Participation{Username: userName[p.user], Group: groupName[p.group], Password: p.password,
			IP: p.ip, Hidden: &h, Unrestricted: &u, DelayTime: &dt, ExtraTime: &et}
		if p.team != none {
			code := teamCode[p.team]
			mp.Team = &code
		}
		contests[p.contest].Participations = append(contests[p.contest].Participations, mp)
	}
	for _, c := range d.contests {
		m := contests[c.key]
		if c.mainGroup != none {
			m.MainGroup = groupName[c.mainGroup]
		} else if len(m.Groups) > 0 {
			d.rep.Warnf(d.where(c.key, c.name), "no main group")
		}
		if len(m.Groups) == 0 {
			// Every contest has a main group since CMS data model 48.
			m.Groups = []importer.Group{importer.NewGroup("default")}
			d.rep.Errorf(d.where(c.key, c.name), "the contest has no group")
			continue
		}
		importer.ValidateContest(m, langs, d.rep)
	}
	for _, t := range inContest {
		importer.ValidateTaskInContest(contests[t.contest], models[t.key], langs, d.rep)
	}

	// Submissions and user tests.
	unknown := map[string]int{}
	type opaque struct {
		participation int32
		id            int64
	}
	opaques := make(map[opaque]bool, len(d.submissions))
	for _, s := range d.submissions {
		if s.language != nil {
			if _, ok := langs.Lookup(*s.language); !ok {
				unknown[*s.language]++
			}
		}
		k := opaque{s.participation, s.opaqueID}
		if opaques[k] {
			d.rep.Errorf(d.where(s.key, ""), "opaque_id %d is used twice by participation %s", s.opaqueID, d.keys[s.participation])
		}
		opaques[k] = true
	}
	for _, u := range d.userTests {
		if u.language != nil {
			if _, ok := langs.Lookup(*u.language); !ok {
				unknown[*u.language]++
			}
		}
	}
	for _, l := range sortedKeys(unknown) {
		d.rep.Errorf(modelName, "unknown language %q, used by %s: it cannot be judged (known: %s; "+
			"add it to languages_dir)", l, plural(unknown[l], "submission or user test"), strings.Join(langs.Names(), ", "))
	}
	for _, r := range d.results {
		if o := r.outcome; o != nil && *o != "ok" && *o != "fail" {
			d.rep.Errorf(d.where(r.key, ""), "invalid compilation_outcome %q", *o)
		}
		if o := r.evaluation; o != nil && *o != "ok" {
			d.rep.Errorf(d.where(r.key, ""), "invalid evaluation_outcome %q", *o)
		}
	}
	d.checkFilenames()
}

// checkFilenames checks the file names of the rows that are not checked
// by the validation of tasks.
func (d *Dump) checkFilenames() {
	for _, rows := range []struct {
		rows   []*fileRow
		schema bool
	}{{d.files, true}, {d.userTestFiles, true}, {d.executables, false}, {d.userTestManager, false}, {d.userTestExecs, false}} {
		for _, f := range rows.rows {
			ok := importer.ValidFilename(f.name)
			if rows.schema {
				ok = importer.ValidFilenameSchema(f.name)
			}
			if !ok {
				d.rep.Errorf(d.where(f.key, ""), "invalid file name %q", f.name)
			}
		}
	}
}

// unique reports duplicate names.
func unique[T any](d *Dump, what string, rows []*T, name func(*T) (int32, string)) {
	seen := make(map[string]int32, len(rows))
	for _, r := range rows {
		i, n := name(r)
		if j, ok := seen[n]; ok {
			d.rep.Errorf(d.where(i, n), "%s %q is also object %s", what, n, d.keys[j])
			continue
		}
		seen[n] = i
	}
}

// modelTokens converts token rules for the validation.
func modelTokens(t tokens) importer.Tokens {
	return importer.Tokens{Mode: t.mode, MaxNumber: t.maxNumber, MinInterval: t.minInterval, GenInitial: t.genInitial,
		GenNumber: t.genNumber, GenInterval: t.genInterval, GenMax: t.genMax}
}

// contestTasks returns the tasks of contests in the order of their
// contest and position.
func (d *Dump) contestTasks() []*task {
	var out []*task
	for _, t := range d.tasks {
		if t.contest != none {
			out = append(out, t)
		}
	}
	slices.SortStableFunc(out, func(a, b *task) int {
		if a.contest != b.contest {
			return int(a.contest - b.contest)
		}
		return cmpNum(a.num, b.num)
	})
	return out
}

func cmpNum(a, b *int32) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return int(*a - *b)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// plural returns "1 thing" or "n things".
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

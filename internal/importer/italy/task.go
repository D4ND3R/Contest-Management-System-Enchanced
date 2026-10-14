// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/italy_yaml.py of CMS (get_task):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2015-2019 Luca Chiodini <luca@chiodini.org>
// Copyright © 2016 Andrea Cracco <guilucand@gmail.com>
// Copyright © 2018 Edoardo Morassutto <edoardo.morassutto@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>

package italy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// maxTestcases bounds n_input: a typo must not allocate millions of
// testcases.
const maxTestcases = 100000

// LoadTask implements importer.TaskLoader (upstream get_task).
func (Loader) LoadTask(dir string, opts *importer.Options) (*importer.Task, error) {
	opts = options(opts)
	rep := opts.Report
	dirName := filepath.Base(dir)

	// task.yaml in the task directory, or (deprecated) <name>.yaml next
	// to it.
	path := filepath.Join(dir, "task.yaml")
	m, err := readYAML(path)
	if notExist(err) {
		deprecated := filepath.Join(filepath.Dir(dir), dirName+".yaml")
		var err2 error
		if m, err2 = readYAML(deprecated); err2 == nil {
			rep.Warnf(deprecated, "you're using a deprecated location for the task.yaml file; "+
				"you're advised to move %s to %s", deprecated, path)
			path, err = deprecated, nil
		} else if !notExist(err2) {
			return nil, err2
		}
	}
	if notExist(err) {
		return nil, fmt.Errorf("file missing: %s", path)
	}
	if err != nil {
		return nil, err
	}
	c := conf{m: m, where: path, rep: rep}

	name, okName := c.str("name", "nome_breve")
	if !okName {
		rep.Errorf(path, "the task has no name (name or nome_breve)")
	}
	title, okTitle := c.str("title", "nome")
	if !okTitle {
		rep.Errorf(path, "the task has no title (title or nome)")
	}
	if !okName || !okTitle {
		return nil, nil
	}
	if name == title {
		rep.Warnf(path, "short name equals long name (title); please check")
	}
	t := importer.NewTask(name, title)
	l := &taskLoader{dir: dir, c: c, rep: rep, langs: opts.Languages, t: t}

	if !opts.SkipStatements {
		l.statements()
	}
	t.SubmissionFormat = []string{name + ".%l"}

	// The feedback level and the score mode, when set explicitly.
	if s, ok := c.str("feedback_level"); ok {
		switch s {
		case importer.FeedbackFull, importer.FeedbackRestricted, importer.FeedbackOIRestricted:
			t.FeedbackLevel = s
		default:
			rep.Warnf(path, "feedback_level: unknown value %q ignored", s)
		}
	}
	if s, ok := c.str("score_mode"); ok {
		switch s {
		case importer.ScoreModeMax, importer.ScoreModeMaxSubtask, importer.ScoreModeMaxTokenedLast:
			t.ScoreMode = s
		default:
			rep.Warnf(path, "score_mode: unknown value %q ignored", s)
		}
	}
	loadTokens(c, &t.Tokens, "task.yaml")
	if n, ok := c.int32("max_submission_number"); ok {
		t.MaxSubmissionNumber = &n
	}
	if n, ok := c.int32("max_user_test_number"); ok {
		t.MaxUserTestNumber = &n
	}
	if d, ok := c.seconds("min_submission_interval"); ok {
		t.MinSubmissionInterval = &d
	}
	if d, ok := c.seconds("min_user_test_interval"); ok {
		t.MinUserTestInterval = &d
	}
	l.attachments()
	if n, ok := c.int32("score_precision"); ok {
		t.ScorePrecision = n
	}

	d := importer.NewDataset("Default")
	if s, ok := c.str("version"); ok {
		d.Description = s
	}
	if f, ok := c.number("time_limit", "timeout"); ok {
		d.TimeLimit = &f
	}
	// The Italian YAML format specifies memory limits in MiB.
	if f, ok := c.number("memory_limit", "memlimit"); ok {
		b := int64(f * importer.MiB)
		if float64(b) != f*importer.MiB {
			rep.Errorf(path, "memory_limit: %v MiB is not a whole number of bytes", f)
		}
		d.MemoryLimit = &b
	}
	l.d = d
	t.Datasets = []*importer.Dataset{d}
	if !l.dataset() {
		return nil, nil
	}
	return t, nil
}

// taskLoader holds the state of one get_task.
type taskLoader struct {
	dir   string
	c     conf
	rep   *importer.Report
	langs *language.Registry
	t     *importer.Task
	d     *importer.Dataset
}

func (l *taskLoader) path(elem ...string) string {
	return filepath.Join(append([]string{l.dir}, elem...)...)
}

// statements reads statement/ or testo/: one <language>.pdf per language
// (the names of importer.LanguageCodes), or a single statement.pdf
// (testo.pdf) in primary_language (default "it").
func (l *taskLoader) statements() {
	primary := "it"
	if s, ok := l.c.str("primary_language"); ok {
		primary = s
	}
	folder := ""
	for _, f := range []string{"statement", "testo"} {
		if exists(l.path(f)) {
			if folder != "" {
				l.rep.Errorf(l.dir, "both testo/ and statement/ are present; this is likely an error")
				return
			}
			folder = f
		}
	}
	if folder == "" {
		l.rep.Errorf(l.dir, "statement folder not found (statement/ or testo/)")
		return
	}
	single := l.path(folder, folder+".pdf")
	if !exists(single) {
		single = ""
	}
	multi := map[string]string{}
	for name, code := range importer.LanguageCodes {
		p := l.path(folder, name+".pdf")
		if exists(p) {
			multi[code] = p
		}
	}
	statements := multi
	if len(multi) > 0 {
		// Either statement.pdf or <language>.pdf files, unless
		// statement.pdf is a link to one of them.
		if single != "" {
			if st, err := os.Lstat(single); err == nil && st.Mode()&os.ModeSymlink == 0 {
				l.rep.Warnf(single, "a statement (not a symlink!) is present at %s but %d more multi-language "+
					"statements were found; this is likely an error; proceeding with importing the "+
					"multi-language files only", single, len(multi))
			}
		}
	} else if single != "" {
		statements = map[string]string{primary: single}
	}
	if _, ok := statements[primary]; !ok {
		l.rep.Errorf(l.dir, "couldn't find the statement for the primary language %s (%s/%s.pdf or %s/<language>.pdf)",
			primary, folder, folder, folder)
		return
	}
	codes := make([]string, 0, len(statements))
	for code := range statements {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		l.t.Statements = append(l.t.Statements, importer.Statement{Language: code, File: importer.FileFromPath(statements[code])})
	}
	l.t.PrimaryStatements = []string{primary}
}

// attachments reads every file of att/.
func (l *taskLoader) attachments() {
	entries, err := os.ReadDir(l.path("att"))
	if err != nil {
		if !notExist(err) {
			l.rep.Errorf(l.path("att"), "%v", err)
		}
		return
	}
	for _, e := range entries {
		l.setAttachment(e.Name(), importer.FileFromPath(l.path("att", e.Name())))
	}
}

// setAttachment adds an attachment or replaces the one with that name
// (upstream task.attachments.set).
func (l *taskLoader) setAttachment(filename string, f *importer.File) {
	for i := range l.t.Attachments {
		if l.t.Attachments[i].Filename == filename {
			l.t.Attachments[i].File = f
			return
		}
	}
	l.t.Attachments = append(l.t.Attachments, importer.Attachment{Filename: filename, File: f})
}

// dataset deduces the task type and its parameters, the score type and
// the testcases. It returns false when the task cannot be built at all.
func (l *taskLoader) dataset() bool {
	c, d, rep := l.c, l.d, l.rep
	where := c.where

	infile, outfile := "input.txt", "output.txt"
	for _, io := range []struct {
		key string
		dst *string
	}{{"infile", &infile}, {"outfile", &outfile}} {
		if !c.has(io.key) {
			continue
		}
		if s, ok := c.str(io.key); ok {
			*io.dst = s
		} else if _, v, _ := c.get(io.key); v == nil {
			rep.Errorf(where, "%s is null: write %s: \"\" for the standard stream", io.key, io.key)
		}
	}

	// Graders (sol/grader.<ext>) or legacy stubs (sol/stub.<ext>, stored
	// as graders), for the source extension of each language.
	graders, stubs := map[string]string{}, map[string]string{}
	var exts []string
	extLangs := map[string][]string{}
	for _, lang := range l.langs.All() {
		ext := lang.SourceExtension()
		if _, ok := extLangs[ext]; !ok {
			exts = append(exts, ext)
		}
		extLangs[ext] = append(extLangs[ext], lang.Name)
		if p := l.path("sol", "grader"+ext); isFile(p) {
			graders[ext] = p
		}
		if p := l.path("sol", "stub"+ext); isFile(p) {
			stubs[ext] = p
		}
	}
	compilation := "alone"
	if len(graders) > 0 && len(stubs) > 0 {
		rep.Errorf(l.path("sol"), "the task contains both sol/grader and sol/stub")
		return false
	}
	if len(graders) > 0 || len(stubs) > 0 {
		found, kind := graders, "grader"
		if len(stubs) > 0 {
			found, kind = stubs, "stub"
		}
		var missing []string
		for _, ext := range exts {
			if p, ok := found[ext]; ok {
				d.Managers = append(d.Managers, importer.Manager{Filename: "grader" + ext, File: importer.FileFromPath(p)})
			} else {
				missing = append(missing, extLangs[ext]...)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			rep.Warnf(l.path("sol"), "%s not found for: %s", kind, strings.Join(missing, ", "))
		}
		compilation = "grader"
		// Headers (any file of sol/ ending with a header extension).
		entries, _ := os.ReadDir(l.path("sol"))
		headers := l.langs.HeaderExtensions()
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			for _, h := range headers {
				if strings.HasSuffix(e.Name(), h) {
					d.Managers = append(d.Managers, importer.Manager{Filename: e.Name(), File: importer.FileFromPath(l.path("sol", e.Name()))})
					break
				}
			}
		}
	}

	// A checker (check/checker or cor/correttore) makes the evaluation a
	// comparator.
	evaluation := tasktype.OutputEvalDiff
	for _, p := range []string{l.path("check", "checker"), l.path("cor", "correttore")} {
		if exists(p) {
			d.Managers = append(d.Managers, importer.Manager{Filename: "checker", File: importer.FileFromPath(p)})
			evaluation = tasktype.OutputEvalComparator
			break
		}
	}

	nInput, ok := l.scoreType()
	if !ok {
		return false
	}
	if nInput < 0 {
		nInput = 0
	}
	if nInput > maxTestcases {
		rep.Errorf(where, "n_input: %d testcases are too many (at most %d)", nInput, maxTestcases)
		return false
	}

	// The task type: Interactive with a controller, Communication with a
	// manager, OutputOnly with output_only, else Batch or
	// BatchAndOutput.
	outputOnly, _ := c.boolean("output_only")
	controller := firstExisting(l.path("check", "controller"), l.path("cor", "controller"))
	manager := firstExisting(l.path("check", "manager"), l.path("cor", "manager"))
	n := 0
	for _, b := range []bool{controller != "", manager != "", outputOnly} {
		if b {
			n++
		}
	}
	if n > 1 {
		rep.Errorf(l.dir, "the task can have at most one of a manager, a controller and output_only")
		return false
	}
	var outputCodenames map[string]bool
	switch {
	case controller != "":
		d.TaskType = tasktype.NameInteractive
		processLimit := any(200)
		if _, v, ok := c.get("controller_process_limit"); ok {
			processLimit = v
		}
		concurrent := true
		if b, ok := c.boolean("interactive_concurrent"); ok {
			concurrent = b
		}
		var missing []string
		limits := make([]any, 3)
		for i, k := range []string{"controller_memory_limit", "controller_time_limit", "controller_wall_time_limit"} {
			_, limits[i], _ = c.get(k)
			if limits[i] == nil {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			rep.Errorf(where, "the controller (Interactive task) needs %s: CMS cannot judge it without them",
				strings.Join(missing, ", "))
		}
		d.TaskTypeParameters = l.params(processLimit, compilation, concurrent, limits[0], limits[1], limits[2])
		d.Managers = append(d.Managers, importer.Manager{Filename: "controller", File: importer.FileFromPath(controller)})
	case manager != "":
		var numProcesses any = 1
		if _, v, ok := c.value("num_processes"); ok {
			numProcesses = v
		}
		var userIO string
		if s, ok := c.str("user_io"); ok {
			if s != "std_io" && s != "fifo_io" {
				rep.Warnf(where, "user_io incorrect; valid options are 'std_io' and 'fifo_io'; ignored")
			} else {
				userIO = s
			}
		}
		if userIO == "" {
			userIO = "std_io"
			if compilation == "grader" {
				userIO = "fifo_io"
			}
		}
		d.TaskType = tasktype.NameCommunication
		d.TaskTypeParameters = l.params(numProcesses, compilation, userIO)
		d.Managers = append(d.Managers, importer.Manager{Filename: "manager", File: importer.FileFromPath(manager)})
	case outputOnly:
		d.TaskType = tasktype.NameOutputOnly
		d.TimeLimit, d.MemoryLimit = nil, nil
		d.TaskTypeParameters = l.params(evaluation)
		l.t.SubmissionFormat = make([]string, nInput)
		for i := range nInput {
			l.t.SubmissionFormat[i] = fmt.Sprintf("output_%03d.txt", i)
		}
	default:
		d.TaskType = tasktype.NameBatch
		params := []any{compilation, []any{infile, outfile}, evaluation}
		only, okOnly := l.indexList("output_only_testcases")
		optional, okOptional := l.indexList("output_optional_testcases")
		if okOnly && len(only) > 0 || okOptional && len(optional) > 0 {
			d.TaskType = tasktype.NameBatchAndOutput
			outputCodenames = map[string]bool{}
			var onlyCodenames []string
			for _, i := range only {
				cn := fmt.Sprintf("%03d", i)
				if !outputCodenames[cn] {
					onlyCodenames = append(onlyCodenames, cn)
				}
				outputCodenames[cn] = true
			}
			sort.Strings(onlyCodenames)
			params = append(params, strings.Join(onlyCodenames, ","))
			for _, i := range optional {
				outputCodenames[fmt.Sprintf("%03d", i)] = true
			}
			var all []string
			for cn := range outputCodenames {
				all = append(all, cn)
			}
			sort.Strings(all)
			for _, cn := range all {
				l.t.SubmissionFormat = append(l.t.SubmissionFormat, fmt.Sprintf("output_%s.txt", cn))
			}
		}
		d.TaskTypeParameters = l.params(params...)
	}

	// The testcases: input/input<i>.txt and output/output<i>.txt.
	for i := range nInput {
		codename := fmt.Sprintf("%03d", i)
		in := importer.FileFromPath(l.path("input", fmt.Sprintf("input%d.txt", i)))
		d.Testcases = append(d.Testcases, importer.Testcase{
			Codename: codename,
			Input:    in,
			Output:   importer.FileFromPath(l.path("output", fmt.Sprintf("output%d.txt", i))),
		})
		if d.TaskType == tasktype.NameOutputOnly || outputCodenames[codename] {
			l.setAttachment("input_"+codename+".txt", in)
		}
	}
	l.publicTestcases()
	sort.Slice(d.Managers, func(i, j int) bool { return d.Managers[i].Filename < d.Managers[j].Filename })
	d.Managers = slices.CompactFunc(d.Managers, func(a, b importer.Manager) bool { return a.Filename == b.Filename })
	sort.Slice(l.t.Attachments, func(i, j int) bool { return l.t.Attachments[i].Filename < l.t.Attachments[j].Filename })
	return true
}

// params encodes task type parameters.
func (l *taskLoader) params(values ...any) json.RawMessage {
	raw, err := toJSON(values)
	if err != nil {
		l.rep.Errorf(l.c.where, "task type parameters: %v", err)
		return json.RawMessage("null")
	}
	return raw
}

// firstExisting returns the first path that exists, or "".
func firstExisting(paths ...string) string {
	for _, p := range paths {
		if exists(p) {
			return p
		}
	}
	return ""
}

// indexList reads a list of testcase numbers: "0, 3,5" (upstream), also a
// single number or a YAML list.
func (l *taskLoader) indexList(keys ...string) ([]int, bool) {
	k, v, ok := l.c.value(keys...)
	if !ok {
		return nil, false
	}
	var items []string
	switch v := v.(type) {
	case string:
		if v == "" {
			return nil, true
		}
		items = strings.Split(v, ",")
	case []any:
		for _, e := range v {
			items = append(items, fmt.Sprint(e))
		}
	default:
		if n, ok := toInt(v); ok {
			return []int{int(n)}, true
		}
		l.c.bad(k, v, "a comma-separated list of testcase numbers")
		return nil, false
	}
	out := make([]int, 0, len(items))
	for _, s := range items {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			l.rep.Errorf(l.c.where, "%s: %q is not a testcase number", k, strings.TrimSpace(s))
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// publicTestcases marks the testcases of public_testcases (risultati):
// "all", or their numbers.
func (l *taskLoader) publicTestcases() {
	tcs := l.d.Testcases
	if _, v, ok := l.c.value("public_testcases", "risultati"); ok && v == "all" {
		for i := range tcs {
			tcs[i].Public = true
		}
		return
	}
	indexes, ok := l.indexList("public_testcases", "risultati")
	if !ok {
		return
	}
	for _, i := range indexes {
		j := i
		if j < 0 { // Python's negative indexes
			j += len(tcs)
		}
		if j < 0 || j >= len(tcs) {
			l.rep.Errorf(l.c.where, "public_testcases: testcase %d does not exist (the task has %d)", i, len(tcs))
			continue
		}
		tcs[j].Public = true
	}
}

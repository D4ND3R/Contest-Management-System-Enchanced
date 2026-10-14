// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// Name is the name of the loader (-L problem_yaml, also -L package).
const Name = "problem_yaml"

// Files and directories of a package.
const (
	ProblemFile    = "problem.yaml"
	StatementDir   = "statement"
	TestsDir       = "tests"
	GradersDir     = "graders"
	AttachmentsDir = "attachments"
	SolutionsDir   = "solutions"

	// maxProblemYAML bounds the size of problem.yaml.
	maxProblemYAML = 1 << 20
	// maxTests bounds the number of tests of a package.
	maxTests = 100000
)

// Loader reads task packages of this format (a directory or a zip file).
// It is stateless.
type Loader struct{}

func init() { importer.Register(Loader{}, "package") }

var _ importer.TaskLoader = Loader{}

// Name implements importer.Loader.
func (Loader) Name() string { return Name }

// Description implements importer.Loader.
func (Loader) Description() string {
	return "task package of this fork: problem.yaml, tests/, graders/... in a directory or a zip"
}

// Detect implements importer.Loader: a task with problem.yaml at its root
// (for a zip, at its root or in its only directory).
func (Loader) Detect(p string, kind importer.Kind) bool {
	return kind == importer.KindTask && Detect(p)
}

// Detect reports whether p is a package of this format. An unsafe zip
// is one (no other format reads zip files): Load says what is wrong.
func Detect(p string) bool {
	src, err := openSource(p)
	if errors.Is(err, ErrUnsafeArchive) {
		return true
	}
	return err == nil && src.isFile(ProblemFile)
}

// LoadTask implements importer.TaskLoader.
func (Loader) LoadTask(p string, opts *importer.Options) (*importer.Task, error) {
	pk, err := Load(p, opts)
	if pk == nil || err != nil {
		return nil, err
	}
	return pk.Task, nil
}

// Package is a package as read by Load.
type Package struct {
	// Path is the directory or zip file.
	Path string
	// Task is the task, with one dataset; nil if the package has errors
	// that prevent building it (they are in the report).
	Task *importer.Task
	// Solutions are the files of solutions/ with the verdict their name
	// expects.
	Solutions []Solution
}

// Expected verdicts of solutions, the second extension of their names
// (correct.ac.cpp).
const (
	VerdictAccepted    = "ac"  // full score
	VerdictPartial     = "pa"  // some points, not all
	VerdictWrongAnswer = "wa"  // wrong answer on some test
	VerdictTimeLimit   = "tle" // too slow on some test
	VerdictMemoryLimit = "mle" // too much memory on some test
	VerdictRuntime     = "re"  // crashes on some test
	VerdictCompilation = "ce"  // does not compile
)

// Verdicts are the expected verdicts a solution can have.
var Verdicts = []string{VerdictAccepted, VerdictPartial, VerdictWrongAnswer, VerdictTimeLimit,
	VerdictMemoryLimit, VerdictRuntime, VerdictCompilation}

// Solution is a file of solutions/.
type Solution struct {
	// Name is its path in the package ("solutions/correct.ac.cpp").
	Name string
	// Verdict is the expected verdict (Verdicts); "" if the name has
	// none.
	Verdict string
	// Language is the programming language of a source; "" if unknown.
	Language string
	File     *importer.File
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

// Load reads the package at p (a directory, or a zip file with the
// package at its root or in its only directory). Problems of the package
// go to opts.Report; the error is for a package that cannot be read at
// all (no problem.yaml, invalid YAML, an unsafe zip). When the report has
// errors that prevent building the task, Package.Task is nil.
func Load(p string, opts *importer.Options) (*Package, error) {
	opts = options(opts)
	src, err := openSource(p)
	if err != nil {
		return nil, err
	}
	data, err := src.read(ProblemFile, maxProblemYAML)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("file missing: %s", src.where(ProblemFile))
	}
	if err != nil {
		return nil, err
	}
	pr, err := parseProblem(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.where(ProblemFile), err)
	}
	l := &loader{src: src, p: pr, opts: opts, rep: opts.Report, where: src.where(ProblemFile)}
	pk := &Package{Path: p}
	pk.Task = l.task()
	pk.Solutions = l.solutions(pk.Task)
	return pk, nil
}

// loader holds the state of one Load.
type loader struct {
	src   source
	p     *problem
	opts  *importer.Options
	rep   *importer.Report
	where string

	tests    []string            // test names, sorted
	managers []importer.Manager  // graders/
	sources  map[string][]string // graders/: sources of programs (checker.cpp...)
	compiled []string            // programs compiled from their sources
}

func (l *loader) errorf(format string, args ...any) { l.rep.Errorf(l.where, format, args...) }
func (l *loader) warnf(format string, args ...any)  { l.rep.Warnf(l.where, format, args...) }

// taskTypes maps the names of problem.yaml (and those of CMS) to task
// types.
var taskTypes = map[string]string{
	"batch":          tasktype.NameBatch,
	"batchandoutput": tasktype.NameBatchAndOutput,
	"outputonly":     tasktype.NameOutputOnly,
	"communication":  tasktype.NameCommunication,
	"interactive":    tasktype.NameInteractive,
	"twosteps":       tasktype.NameTwoSteps,
}

// typeNames are the names problem.yaml uses, by task type.
var typeNames = map[string]string{
	tasktype.NameBatch:          "batch",
	tasktype.NameBatchAndOutput: "batch_and_output",
	tasktype.NameOutputOnly:     "output_only",
	tasktype.NameCommunication:  "communication",
	tasktype.NameInteractive:    "interactive",
	tasktype.NameTwoSteps:       "two_steps",
}

func taskTypeOf(s string) (string, bool) {
	if s == "" {
		return tasktype.NameBatch, true
	}
	t, ok := taskTypes[strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(s))]
	return t, ok
}

// checkers maps the checker names of problem.yaml to "output_eval".
var checkers = map[string]string{
	"whitediff":  tasktype.OutputEvalDiff,
	"white_diff": tasktype.OutputEvalDiff,
	"diff":       tasktype.OutputEvalDiff,
	"exact":      tasktype.OutputEvalExact,
	"float":      tasktype.OutputEvalReal,
	"real":       tasktype.OutputEvalReal,
	"comparator": tasktype.OutputEvalComparator,
	"testlib":    tasktype.OutputEvalTestlib,
}

// checkerNames are the names problem.yaml uses, by "output_eval".
var checkerNames = map[string]string{
	tasktype.OutputEvalDiff:       "whitediff",
	tasktype.OutputEvalExact:      "exact",
	tasktype.OutputEvalReal:       "float",
	tasktype.OutputEvalComparator: "comparator",
	tasktype.OutputEvalTestlib:    "testlib",
}

// Programs of graders/ that the task types run.
const (
	checkerFile    = "checker"
	managerFile    = "manager"
	controllerFile = "controller"
)

// task builds the task, or returns nil.
func (l *loader) task() *importer.Task {
	p := l.p
	if p.Name == "" {
		l.errorf("name is missing")
	}
	if strings.TrimSpace(p.Title) == "" {
		l.errorf("title is missing")
	}
	taskType, ok := taskTypeOf(p.Type)
	if !ok {
		l.errorf("unknown type %q (batch, batch_and_output, output_only, communication, interactive or two_steps)", p.Type)
		return nil
	}
	if taskType == tasktype.NameBatch && (!p.OutputOnlyTests.empty() || !p.OutputTests.empty()) {
		taskType = tasktype.NameBatchAndOutput
	}
	t := importer.NewTask(p.Name, p.Title)
	d := importer.NewDataset("Default")
	if p.Dataset != "" {
		d.Description = p.Dataset
	}
	d.Autojudge = p.Autojudge
	d.TaskType = taskType
	t.Datasets = []*importer.Dataset{d}

	// Limits.
	d.TimeLimit = p.TimeLimit
	if p.MemoryLimit != nil {
		if *p.MemoryLimit <= 0 || *p.MemoryLimit > math.MaxInt64/importer.MiB {
			l.errorf("memory_limit: %d MiB is not a valid limit", *p.MemoryLimit)
		} else {
			d.MemoryLimit = importer.Ptr(*p.MemoryLimit * importer.MiB)
		}
	}

	l.loadTests(d)
	l.loadGraders(d)
	l.taskType(t, d)
	l.scoring(t, d)
	if p.PublicTests != nil {
		public, bad := p.PublicTests.resolve(l.tests)
		l.badTests("public_tests", bad)
		for i := range d.Testcases {
			d.Testcases[i].Public = slices.Contains(public, d.Testcases[i].Codename)
		}
	}
	if p.SubmissionFormat != nil {
		t.SubmissionFormat = *p.SubmissionFormat
	} else if taskType == tasktype.NameTwoSteps {
		l.errorf("submission_format is needed with type two_steps (e.g. [encoder.%%l, decoder.%%l])")
	}

	// The settings of the task.
	if p.Languages != nil {
		t.AllowedLanguages = *p.Languages
	}
	if p.FeedbackLevel != "" {
		t.FeedbackLevel = p.FeedbackLevel
	}
	if p.ScoreMode != "" {
		t.ScoreMode = p.ScoreMode
	}
	t.ScorePrecision = p.ScorePrecision
	t.MaxSubmissionNumber = p.MaxSubmissions
	t.MaxUserTestNumber = p.MaxUserTests
	t.MinSubmissionInterval = l.seconds("min_submission_interval", p.MinSubmissionInterval)
	t.MinUserTestInterval = l.seconds("min_user_test_interval", p.MinUserTestInterval)
	if tk := p.Tokens; tk != nil {
		if tk.Mode != "" {
			t.Tokens.Mode = tk.Mode
		}
		t.Tokens.MaxNumber = tk.MaxNumber
		if d := l.seconds("tokens.min_interval", tk.MinInterval); d != nil {
			t.Tokens.MinInterval = *d
		}
		if tk.GenInitial != nil {
			t.Tokens.GenInitial = *tk.GenInitial
		}
		if tk.GenNumber != nil {
			t.Tokens.GenNumber = *tk.GenNumber
		}
		if d := l.seconds("tokens.gen_interval", tk.GenInterval); d != nil {
			t.Tokens.GenInterval = *d
		}
		t.Tokens.GenMax = tk.GenMax
	}

	if !l.opts.SkipStatements {
		l.statements(t)
	}
	l.attachments(t, d)
	if p.Name == "" || strings.TrimSpace(p.Title) == "" {
		return nil
	}
	return t
}

// seconds converts a duration in seconds (rounded to microseconds).
func (l *loader) seconds(key string, s *float64) *time.Duration {
	if s == nil {
		return nil
	}
	if math.IsNaN(*s) || math.IsInf(*s, 0) || *s < 0 || *s > 1e12 {
		l.errorf("%s: %v is not a valid number of seconds", key, *s)
		return nil
	}
	d := time.Duration(math.Round(*s*1e6)) * time.Microsecond
	return &d
}

// visible returns names without the hidden files (.gitkeep, .DS_Store),
// leaving names alone.
func visible(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	return out
}

// loadTests reads tests/: <name>.in with <name>.out.
func (l *loader) loadTests(d *importer.Dataset) {
	files, dirs, err := l.src.list(TestsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		l.errorf("%s: %v", TestsDir, err)
		return
	}
	for _, dir := range visible(dirs) {
		l.rep.Warnf(l.src.where(TestsDir+"/"+dir), "directories in %s/ are ignored", TestsDir)
	}
	have := map[string]bool{}
	for _, f := range visible(files) {
		have[f] = true
	}
	for _, f := range visible(files) {
		name, ext := splitExt(f)
		switch ext {
		case ".in":
			if !have[name+".out"] {
				l.rep.Errorf(l.src.where(TestsDir+"/"+f), "test %s has no output (%s/%s.out)", name, TestsDir, name)
				continue
			}
			if !importer.ValidCodename(name) {
				l.rep.Errorf(l.src.where(TestsDir+"/"+f), "invalid test name %q: only letters, digits, _ and - are allowed", name)
				continue
			}
			l.tests = append(l.tests, name)
		case ".out":
			if !have[name+".in"] {
				l.rep.Errorf(l.src.where(TestsDir+"/"+f), "test %s has no input (%s/%s.in)", name, TestsDir, name)
			}
		default:
			l.rep.Warnf(l.src.where(TestsDir+"/"+f), "ignored: tests are <name>.in with <name>.out")
		}
	}
	if len(l.tests) == 0 {
		l.errorf("the package has no tests (%s/<name>.in and %s/<name>.out)", TestsDir, TestsDir)
	}
	if len(l.tests) > maxTests {
		l.errorf("%d tests are too many (at most %d)", len(l.tests), maxTests)
		l.tests = l.tests[:maxTests]
	}
	slices.Sort(l.tests)
	for _, name := range l.tests {
		d.Testcases = append(d.Testcases, importer.Testcase{
			Codename: name,
			Input:    l.src.file(TestsDir + "/" + name + ".in"),
			Output:   l.src.file(TestsDir + "/" + name + ".out"),
		})
	}
}

// splitExt splits "a.b.c" into "a.b" and ".c".
func splitExt(name string) (string, string) {
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext), ext
}

// loadGraders reads graders/: every file is a manager with its name
// (grader.<ext>, headers, checker, manager, controller, manager.<ext> of
// two_steps...), except the sources of the programs (checker.cpp,
// controller.cpp, manager.cpp but for two_steps, testlib.h), which are
// kept for reference. stub.<ext> is stored as grader.<ext>, as italy_yaml
// does.
func (l *loader) loadGraders(d *importer.Dataset) {
	files, dirs, err := l.src.list(GradersDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			l.errorf("%s: %v", GradersDir, err)
		}
		return
	}
	for _, dir := range visible(dirs) {
		l.rep.Warnf(l.src.where(GradersDir+"/"+dir), "directories in %s/ are ignored", GradersDir)
	}
	sourceExts := l.opts.Languages.SourceExtensions()
	l.sources = map[string][]string{}
	byName := map[string]bool{}
	for _, f := range visible(files) {
		base, ext := splitExt(f)
		isSource := slices.Contains(sourceExts, ext)
		switch {
		case f == "testlib.h":
			continue
		case isSource && (base == checkerFile || base == controllerFile ||
			base == managerFile && d.TaskType != tasktype.NameTwoSteps):
			l.sources[base] = append(l.sources[base], f)
			continue
		}
		name := f
		if base == "stub" && isSource {
			name = "grader" + ext
			if slices.Contains(files, name) {
				l.rep.Errorf(l.src.where(GradersDir+"/"+f), "both %s and %s: keep one", f, name)
				continue
			}
		}
		if byName[name] {
			continue
		}
		byName[name] = true
		l.managers = append(l.managers, importer.Manager{Filename: name, File: l.src.file(GradersDir + "/" + f)})
	}
	slices.SortFunc(l.managers, func(a, b importer.Manager) int { return strings.Compare(a.Filename, b.Filename) })
	d.Managers = l.managers
}

// hasManager reports whether graders/ has a manager of that name.
func (l *loader) hasManager(name string) bool {
	return slices.ContainsFunc(l.managers, func(m importer.Manager) bool { return m.Filename == name })
}

// graders returns the grader.<ext> managers.
func (l *loader) graders() []string {
	var out []string
	for _, m := range l.managers {
		if base, ext := splitExt(m.Filename); base == "grader" && ext != "" {
			out = append(out, m.Filename)
		}
	}
	return out
}

// unused warns about keys that do not apply to the task type.
func (l *loader) unused(taskType string, keys ...keySet) {
	for _, k := range keys {
		if k.set {
			l.warnf("%s does not apply to type %s: ignored", k.key, typeNames[taskType])
		}
	}
}

// keySet says whether a key of problem.yaml is set where it does not
// apply.
type keySet struct {
	key string
	set bool
}

// taskType builds the parameters of the task type.
func (l *loader) taskType(t *importer.Task, d *importer.Dataset) {
	p := l.p
	tt := d.TaskType
	name := p.Name

	// The checker, for the types that compare outputs.
	outputEval := tasktype.OutputEvalDiff
	comparesOutputs := tt != tasktype.NameCommunication && tt != tasktype.NameInteractive
	if p.Checker != nil {
		if !comparesOutputs {
			l.errorf("checker does not apply to type %s: its %s decides the outcome", typeNames[tt],
				map[bool]string{true: "manager", false: "controller"}[tt == tasktype.NameCommunication])
		} else if ev, ok := checkers[strings.ToLower(p.Checker.Type)]; !ok {
			l.errorf("unknown checker %q (whitediff, exact, float, comparator or testlib)", p.Checker.Type)
		} else {
			outputEval = ev
			if p.Checker.Tolerance != nil && (ev != tasktype.OutputEvalReal || *p.Checker.Tolerance != tasktype.RealTolerance) {
				l.errorf("checker: the tolerance can only be %g, with type float (use a comparator for another one)", tasktype.RealTolerance)
			}
		}
	}
	needsChecker := outputEval == tasktype.OutputEvalComparator || outputEval == tasktype.OutputEvalTestlib
	switch {
	case needsChecker:
		l.program(d, checkerFile, compileMode{cms: outputEval == tasktype.OutputEvalComparator},
			fmt.Sprintf("checker: %s needs %s/%s (an executable) or its C++ source %s/%s.cpp",
				checkerNames[outputEval], GradersDir, checkerFile, GradersDir, checkerFile))
	case l.hasManager(checkerFile):
		l.warnf("%s/%s is stored but not used: set checker: comparator (or testlib) to use it", GradersDir, checkerFile)
	}
	switch tt {
	case tasktype.NameCommunication:
		l.program(d, managerFile, compileMode{}, fmt.Sprintf("type communication needs %s/%s (an executable) or its C++ source %s/%s.cpp",
			GradersDir, managerFile, GradersDir, managerFile))
	case tasktype.NameInteractive:
		l.program(d, controllerFile, compileMode{}, fmt.Sprintf("type interactive needs %s/%s (an executable) or its C++ source %s/%s.cpp",
			GradersDir, controllerFile, GradersDir, controllerFile))
	}
	for _, prog := range []string{checkerFile, managerFile, controllerFile} {
		l.checkExecutable(prog)
	}

	// The grader.
	graders := l.graders()
	grader := len(graders) > 0
	if p.Grader != nil {
		grader = *p.Grader
	}
	compilation := "alone"
	if grader {
		compilation = "grader"
	}
	if p.Grader != nil && !*p.Grader && len(graders) > 0 && tt != tasktype.NameTwoSteps {
		l.warnf("grader is false: %s are stored but not used", strings.Join(graders, ", "))
	}
	usesGrader := tt == tasktype.NameBatch || tt == tasktype.NameBatchAndOutput ||
		tt == tasktype.NameCommunication || tt == tasktype.NameInteractive
	l.canonicalGraders(graders)

	isBatch := tt == tasktype.NameBatch || tt == tasktype.NameBatchAndOutput
	l.unused(tt,
		keySet{"input", !isBatch && p.Input != ""},
		keySet{"output", !isBatch && p.Output != ""},
		keySet{"grader", !usesGrader && p.Grader != nil},
		keySet{"processes", tt != tasktype.NameCommunication && p.Processes != nil},
		keySet{"user_io", tt != tasktype.NameCommunication && p.UserIO != ""},
		keySet{"process_limit", tt != tasktype.NameInteractive && p.ProcessLimit != nil},
		keySet{"concurrent", tt != tasktype.NameInteractive && p.Concurrent != nil},
		keySet{"controller_memory_limit", tt != tasktype.NameInteractive && p.ControllerMemoryLimit != nil},
		keySet{"controller_time_limit", tt != tasktype.NameInteractive && p.ControllerTimeLimit != nil},
		keySet{"controller_wall_time_limit", tt != tasktype.NameInteractive && p.ControllerWallTimeLimit != nil},
		keySet{"attach_inputs", tt != tasktype.NameOutputOnly && tt != tasktype.NameBatchAndOutput && p.AttachInputs != nil},
	)
	if tt != tasktype.NameBatchAndOutput && (!p.OutputOnlyTests.empty() || !p.OutputTests.empty()) {
		l.errorf("output_only_tests and output_tests need type batch_and_output")
	}

	var params []any
	t.SubmissionFormat = []string{name + ".%l"}
	switch tt {
	case tasktype.NameBatch, tasktype.NameBatchAndOutput:
		params = []any{compilation, []any{p.Input, p.Output}, outputEval}
		if tt == tasktype.NameBatchAndOutput {
			only, bad := p.OutputOnlyTests.resolve(l.tests)
			l.badTests("output_only_tests", bad)
			optional, bad := p.OutputTests.resolve(l.tests)
			l.badTests("output_tests", bad)
			params = append(params, strings.Join(only, ","))
			for _, c := range l.tests {
				if slices.Contains(only, c) || slices.Contains(optional, c) {
					t.SubmissionFormat = append(t.SubmissionFormat, tasktype.UserOutputFilename(c))
				}
			}
		}
	case tasktype.NameOutputOnly:
		params = []any{outputEval}
		t.SubmissionFormat = nil
		for _, c := range l.tests {
			t.SubmissionFormat = append(t.SubmissionFormat, tasktype.UserOutputFilename(c))
		}
	case tasktype.NameCommunication:
		processes := 1
		if p.Processes != nil {
			processes = *p.Processes
		}
		userIO := p.UserIO
		if userIO == "" {
			userIO = "std_io"
			if grader {
				userIO = "fifo_io"
			}
		}
		params = []any{processes, compilation, userIO}
	case tasktype.NameInteractive:
		processLimit, concurrent := 1, true
		if p.ProcessLimit != nil {
			processLimit = *p.ProcessLimit
		}
		if p.Concurrent != nil {
			concurrent = *p.Concurrent
		}
		var missing []string
		limit := func(key string, v *number) any {
			if v == nil {
				missing = append(missing, key)
				return nil
			}
			return v
		}
		params = []any{processLimit, compilation, concurrent,
			limit("controller_memory_limit", p.ControllerMemoryLimit),
			limit("controller_time_limit", p.ControllerTimeLimit),
			limit("controller_wall_time_limit", p.ControllerWallTimeLimit)}
		if len(missing) > 0 {
			l.errorf("type interactive needs %s (the limits of the controller, in MiB and seconds)", strings.Join(missing, ", "))
		}
	case tasktype.NameTwoSteps:
		params = []any{outputEval}
		t.SubmissionFormat = nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		l.errorf("task type parameters: %v", err)
	}
	d.TaskTypeParameters = raw
}

// program makes sure the dataset has the program called name (checker,
// manager, controller): graders/<name> as it is, or else compiled from
// its C++ source graders/<name>.cpp (.cc, .cxx). missing is the error when
// there is neither.
func (l *loader) program(d *importer.Dataset, name string, mode compileMode, missing string) {
	if l.hasManager(name) {
		return
	}
	var cpp string
	for _, s := range l.sources[name] {
		if _, ext := splitExt(s); slices.Contains(cppExtensions, ext) {
			cpp = s
			break
		}
	}
	if cpp == "" {
		if len(l.sources[name]) > 0 {
			l.errorf("%s/%s: only C++ sources are compiled at import; put the executable (or a script with a #! line) in %s/%s",
				GradersDir, l.sources[name][0], GradersDir, name)
		} else {
			l.errorf("%s", missing)
		}
		return
	}
	where := l.src.where(GradersDir + "/" + cpp)
	bin, err := compileProgram(l.src, cpp, mode)
	if err != nil {
		l.rep.Errorf(where, "%v", err)
		return
	}
	l.managers = append(l.managers, importer.Manager{Filename: name, File: importer.FileFromBytes(where+" (compiled)", bin)})
	slices.SortFunc(l.managers, func(a, b importer.Manager) int { return strings.Compare(a.Filename, b.Filename) })
	d.Managers = l.managers
	l.compiled = append(l.compiled, name)
}

// checkExecutable warns about a program of graders/ that is not one: no
// ELF header nor #! line (a source saved under the name of the program).
func (l *loader) checkExecutable(name string) {
	if !l.hasManager(name) || slices.Contains(l.compiled, name) {
		return
	}
	head, err := l.src.head(GradersDir+"/"+name, 4)
	if err != nil {
		return
	}
	if !bytes.HasPrefix(head, []byte("\x7fELF")) && !bytes.HasPrefix(head, []byte("#!")) {
		l.warnf("%s/%s does not look like an executable (no ELF header nor #! line): the judges run it as it is",
			GradersDir, name)
	}
}

// canonicalGraders warns about graders whose extension is not the one CMS
// looks for (grader.cc for C++: it uses grader.cpp).
func (l *loader) canonicalGraders(graders []string) {
	canonical := map[string]bool{}
	for _, lang := range l.opts.Languages.All() {
		canonical[lang.SourceExtension()] = true
	}
	for _, g := range graders {
		if _, ext := splitExt(g); !canonical[ext] {
			l.warnf("%s/%s is never used: graders are named grader + the first source extension of the language (grader.cpp, grader.py...)",
				GradersDir, g)
		}
	}
}

// badTests reports the items of a selection that name no test.
func (l *loader) badTests(key string, bad []string) {
	for _, b := range bad {
		l.errorf("%s: %s", key, b)
	}
}

// scoring builds the score type.
func (l *loader) scoring(t *importer.Task, d *importer.Dataset) {
	s := l.p.Scoring
	if s == nil {
		s = &scoring{}
	}
	typ := s.Type
	if typ == "" {
		typ = scoretype.NameSum
	}
	for _, n := range scoretype.Names() {
		if strings.EqualFold(n, typ) {
			typ = n
		}
	}
	if !scoretype.Valid(typ) {
		l.errorf("scoring: unknown type %q (%s)", s.Type, strings.Join(scoretype.Names(), ", "))
		return
	}
	d.ScoreType = typ
	if typ == scoretype.NameSum {
		if len(s.Subtasks) > 0 {
			l.errorf("scoring: subtasks need a group type (GroupMin, GroupMul or GroupThreshold)")
		}
		var value any
		switch {
		case s.Points != nil && s.PerTest != nil:
			l.errorf("scoring: give points (the total) or per_test, not both")
			return
		case s.PerTest != nil:
			value = s.PerTest
		default:
			total := 100.0
			if s.Points != nil {
				total = s.Points.f
			}
			v := 0.0
			if len(l.tests) > 0 {
				v = total / float64(len(l.tests))
			}
			value = floatNumber(v) // a Python float, as CMS
		}
		d.ScoreTypeParameters, _ = json.Marshal(value)
		return
	}

	if s.PerTest != nil {
		l.errorf("scoring: per_test is for type Sum")
	}
	if len(s.Subtasks) == 0 {
		l.errorf("scoring: type %s needs subtasks", typ)
		return
	}
	threshold := typ == scoretype.NameGroupThreshold
	params := make([]any, len(s.Subtasks))
	covered := map[string]bool{}
	selector := ""
	total := 0.0
	next := 0 // the next test of a count
	for i, st := range s.Subtasks {
		key := fmt.Sprintf("scoring.subtasks[%d]", i)
		if st.Points == nil {
			l.errorf("%s: points is missing", key)
			return
		}
		total += st.Points.f
		var tests any
		kinds := 0
		kind := ""
		if st.Tests != nil {
			kinds, kind = kinds+1, "tests"
			names, bad := st.Tests.resolve(l.tests)
			l.badTests(key+".tests", bad)
			if len(names) == 0 && len(bad) == 0 {
				l.errorf("%s.tests: no test", key)
			}
			for _, n := range names {
				covered[n] = true
			}
			tests = nonNil(names)
		}
		if st.Regex != nil {
			kinds, kind = kinds+1, "regex"
			tests = *st.Regex
		}
		if st.Count != nil {
			kinds, kind = kinds+1, "count"
			if *st.Count < 0 || next+*st.Count > len(l.tests) {
				l.errorf("%s.count: %d tests from test %d, but there are %d tests", key, *st.Count, next+1, len(l.tests))
			} else {
				for _, n := range l.tests[next : next+*st.Count] {
					covered[n] = true
				}
				next += *st.Count
			}
			tests = *st.Count
		}
		if kinds != 1 {
			l.errorf("%s: give exactly one of tests, regex and count", key)
			return
		}
		if selector == "" {
			selector = kind
		} else if kind != selector {
			l.errorf("%s: every subtask must use the same of tests, regex and count (CMS needs it)", key)
		}
		switch {
		case threshold && st.Threshold == nil:
			l.errorf("%s: threshold is missing (type GroupThreshold)", key)
			return
		case !threshold && st.Threshold != nil:
			l.errorf("%s: threshold is for type GroupThreshold", key)
		}
		if st.AlwaysShowTests != nil {
			o := map[string]any{"max_score": st.Points, "testcases": tests, "always_show_testcases": *st.AlwaysShowTests}
			if threshold {
				o["threshold"] = st.Threshold
			}
			params[i] = o
		} else if threshold {
			params[i] = []any{st.Points, tests, st.Threshold}
		} else {
			params[i] = []any{st.Points, tests}
		}
	}
	d.ScoreTypeParameters, _ = json.Marshal(params)

	// Points that do not add up; tests that count in no subtask.
	if s.Points != nil {
		if math.Abs(total-s.Points.f) > 1e-9 {
			l.errorf("scoring: the points of the subtasks add up to %g, not %g", total, s.Points.f)
		}
	} else if math.Abs(total-100) > 1e-9 {
		l.warnf("scoring: the points of the subtasks add up to %g, not 100 (set scoring.points to say it is intended)", total)
	}
	if selector != "regex" {
		var missing []string
		for _, n := range l.tests {
			if !covered[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			l.warnf("scoring: tests in no subtask (they do not count): %s", selectionOf(missing, l.tests))
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// statements reads statement/<language>.pdf or .md (a language code such
// as "en" or "es_MX", or a name such as "english").
func (l *loader) statements(t *importer.Task) {
	files, _, err := l.src.list(StatementDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		l.errorf("%s: %v", StatementDir, err)
		return
	}
	for _, f := range visible(files) {
		base, ext := splitExt(f)
		where := l.src.where(StatementDir + "/" + f)
		if ext != ".pdf" && ext != ".md" {
			l.rep.Warnf(where, "ignored: statements are %s/<language>.pdf or .md", StatementDir)
			continue
		}
		lang := base
		if code, ok := importer.LanguageCodes[strings.ToLower(base)]; ok {
			lang = code
		}
		if !importer.ValidCodename(lang) {
			l.rep.Errorf(where, "invalid language %q (a code such as en or es_MX)", base)
			continue
		}
		if slices.ContainsFunc(t.Statements, func(s importer.Statement) bool { return s.Language == lang }) {
			l.rep.Errorf(where, "a second statement in %s", lang)
			continue
		}
		t.Statements = append(t.Statements, importer.Statement{Language: lang, File: l.src.file(StatementDir + "/" + f)})
	}
	slices.SortFunc(t.Statements, func(a, b importer.Statement) int { return strings.Compare(a.Language, b.Language) })
	switch {
	case l.p.PrimaryStatements != nil:
		t.PrimaryStatements = *l.p.PrimaryStatements
	case len(t.Statements) == 1:
		t.PrimaryStatements = []string{t.Statements[0].Language}
	}
	if len(t.Statements) == 0 {
		l.warnf("the task has no statement (%s/<language>.pdf or .md)", StatementDir)
	}
}

// attachments reads attachments/ and, for the tests answered with an
// output file (output_only, batch_and_output), adds their inputs as
// input_<test>.txt unless attach_inputs is false.
func (l *loader) attachments(t *importer.Task, d *importer.Dataset) {
	files, dirs, err := l.src.list(AttachmentsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		l.errorf("%s: %v", AttachmentsDir, err)
	}
	for _, dir := range visible(dirs) {
		l.rep.Errorf(l.src.where(AttachmentsDir+"/"+dir), "attachments are files: zip the directory")
	}
	for _, f := range visible(files) {
		t.Attachments = append(t.Attachments, importer.Attachment{Filename: f, File: l.src.file(AttachmentsDir + "/" + f)})
	}
	if l.p.AttachInputs == nil || *l.p.AttachInputs {
		for _, tc := range d.Testcases {
			if !slices.Contains(t.SubmissionFormat, tasktype.UserOutputFilename(tc.Codename)) {
				continue
			}
			name := inputAttachment(tc.Codename)
			if !slices.ContainsFunc(t.Attachments, func(a importer.Attachment) bool { return a.Filename == name }) {
				t.Attachments = append(t.Attachments, importer.Attachment{Filename: name, File: tc.Input})
			}
		}
	}
	slices.SortFunc(t.Attachments, func(a, b importer.Attachment) int { return strings.Compare(a.Filename, b.Filename) })
}

// inputAttachment is the attachment with the input of a test answered
// with an output file.
func inputAttachment(codename string) string { return "input_" + codename + ".txt" }

// solutions reads solutions/: <name>.<verdict>.<extension> (sources),
// and for output_only tasks also <name>.<verdict> directories or
// <name>.<verdict>.zip archives of outputs.
func (l *loader) solutions(t *importer.Task) []Solution {
	if t == nil {
		return nil
	}
	outputOnly := t.Datasets[0].TaskType == tasktype.NameOutputOnly
	files, dirs, err := l.src.list(SolutionsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		l.errorf("%s: %v", SolutionsDir, err)
	}
	var out []Solution
	accepted := false
	add := func(f string, isDir bool) {
		name := SolutionsDir + "/" + f
		where := l.src.where(name)
		s := Solution{Name: name}
		base := f
		if !isDir {
			s.File = l.src.file(name)
			var ext string
			base, ext = splitExt(f)
			if !outputOnly {
				if lang := l.opts.Languages.FilenameToLanguage(f, nil); lang == nil {
					l.rep.Warnf(where, "unknown language of %q", ext)
				} else {
					s.Language = lang.Name
					if t.AllowedLanguages != nil && l.opts.Languages.FilenameToLanguage(f, t.AllowedLanguages) == nil {
						l.rep.Warnf(where, "%s is not among the languages of the task", lang.Name)
					}
				}
			}
		}
		_, verdict := splitExt(base)
		verdict = strings.ToLower(strings.TrimPrefix(verdict, "."))
		switch {
		case slices.Contains(Verdicts, verdict):
			s.Verdict = verdict
		case verdict == "":
			l.rep.Warnf(where, "no expected verdict in the name (e.g. %s.ac%s; one of %s)",
				base, strings.TrimPrefix(f, base), strings.Join(Verdicts, ", "))
		default:
			l.rep.Warnf(where, "unknown expected verdict %q (one of %s)", verdict, strings.Join(Verdicts, ", "))
		}
		accepted = accepted || s.Verdict == VerdictAccepted
		out = append(out, s)
	}
	for _, f := range visible(files) {
		add(f, false)
	}
	for _, d := range visible(dirs) {
		if !outputOnly {
			l.rep.Warnf(l.src.where(SolutionsDir+"/"+d), "ignored: solutions are source files")
			continue
		}
		add(d, true)
	}
	switch {
	case outputOnly:
	case len(out) == 0:
		l.warnf("the package has no solutions to check it with (%s/<name>.<verdict>.<extension>, e.g. %s/correct.ac.cpp)",
			SolutionsDir, SolutionsDir)
	case !accepted:
		l.warnf("no solution is expected to be accepted (%s/<name>.ac.<extension>)", SolutionsDir)
	}
	return out
}

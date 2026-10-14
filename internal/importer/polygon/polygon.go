// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/polygon.py of CMS:
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014-2017 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2018 Edoardo Morassutto <edoardo.morassutto@gmail.com>
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2021 Grace Hawkins <amoomajid99@gmail.com>

// Package polygon reads tasks and contests made with Codeforces Polygon
// (the "Polygon (XML-based)" loaders of CMS: polygon_task,
// polygon_contest and polygon_user), with the rules of upstream:
//
//   - a task is the directory of a Polygon package (problem.xml); its
//     name is the name of the directory, its title the first name in
//     problem.xml, its statements statements/.pdf/<language>/problem.pdf
//     (all of them primary), its submission format <name>.%l;
//   - every testset of problem.xml is a dataset named after it, not
//     autojudged, with the time limit (ms) and memory limit (bytes) of
//     the testset, task type Batch with the input and output files of
//     problem.xml, score type Sum with 100/n points per test (Polygon
//     points and groups are not used), testcases 000, 001... from
//     <testset>/01, 01.a... all public; the testset "tests" is active;
//   - the checker, files/check.cpp or check.cpp, is compiled with g++
//     at import time against the testlib.h of CMS (testlib-cms patch)
//     and stored as the "checker" manager (comparator); without one the
//     output is compared with the white diff;
//   - files/cms_conf.py may set columns of the task (general) and of
//     each dataset (datasets, by testset name), e.g. GroupMin subtasks;
//   - a contest is a directory with contest.xml and problems/<task>; its
//     name is the name of the directory, its description the first name
//     in contest.xml; contestants.txt (username;password;first
//     name;last name;hidden) lists users and participations.
//
// Differences with upstream: cms_conf.py is read as Python literals, not
// run (see pyliteral.go); test files are converted as dos2unix does while
// they are read, instead of running dos2unix on the package (whether the
// program is installed or not); the paths of the tests follow the
// patterns of problem.xml (upstream assumes <testset>/%02d, which is what
// Polygon writes); nothing is written to the package (upstream leaves
// .itime and .import_error files); without a testset "tests" the first
// one is active (upstream leaves the task without an active dataset);
// time limits are milliseconds divided by 1000 (upstream multiplies by
// 0.001: 0.7000000000000001 s for 700 ms);
// the contest gets one group "default" with start and stop at 1970-01-01
// (upstream sets the start and stop of the contest, which no longer has
// them, and fails); the hidden field of contestants.txt applies to the
// participation (upstream passes it to the user, which has no such
// column, and fails).
package polygon

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// Loader reads Polygon tasks, contests and users. It is registered as
// "polygon" and under the names of upstream (polygon_task,
// polygon_contest, polygon_user).
type Loader struct{}

func init() {
	importer.Register(Loader{}, "polygon_task", "polygon_contest", "polygon_user")
}

var (
	_ importer.TaskLoader    = Loader{}
	_ importer.ContestLoader = Loader{}
	_ importer.UserLoader    = Loader{}
)

// Name implements importer.Loader.
func (Loader) Name() string { return "polygon" }

// Description implements importer.Loader.
func (Loader) Description() string { return "Polygon (XML-based) format" }

// Detect implements importer.Loader as upstream: a task has problem.xml,
// a contest contest.xml and problems/, users are in contestants.txt (in
// the directory, or next to it for one user as upstream).
func (Loader) Detect(dir string, kind importer.Kind) bool {
	switch kind {
	case importer.KindTask:
		return exists(filepath.Join(dir, "problem.xml"))
	case importer.KindContest:
		return exists(filepath.Join(dir, "contest.xml")) && exists(filepath.Join(dir, "problems"))
	case importer.KindUsers:
		return exists(filepath.Join(dir, contestantsFile)) || exists(filepath.Join(filepath.Dir(filepath.Clean(dir)), contestantsFile))
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// languageOrder are the language names of upstream LANGUAGE_MAP in its
// order, which is the order of the primary statements.
var languageOrder = []string{
	"afrikaans", "arabic", "armenian", "azerbaijani", "belarusian", "bengali",
	"bosnian", "bulgarian", "catalan", "chinese", "croatian", "czech", "danish",
	"dutch", "english", "estonian", "filipino", "finnish", "french", "georgian",
	"german", "greek", "hebrew", "hindi", "hungarian", "icelandic", "indonesian",
	"irish", "italian", "japanese", "kazakh", "korean", "kyrgyz", "latvian",
	"lithuanian", "luxembourgish", "macedonian", "malay", "mongolian",
	"norwegian", "persian", "polish", "portuguese", "romanian", "russian",
	"serbian", "sinhala", "slovak", "slovene", "spanish", "swedish", "tajik",
	"tamil", "thai", "turkish", "turkmen", "ukrainian", "urdu", "uzbek",
	"vietnamese", "other",
}

// problemXML is what the loader reads of problem.xml.
type problemXML struct {
	Names   []nameXML   `xml:"names>name"`
	Judging *judgingXML `xml:"judging"`
}

type nameXML struct {
	Language string `xml:"language,attr"`
	Value    string `xml:"value,attr"`
}

type judgingXML struct {
	InputFile  string       `xml:"input-file,attr"`
	OutputFile string       `xml:"output-file,attr"`
	Testsets   []testsetXML `xml:"testset"`
}

type testsetXML struct {
	Name          string     `xml:"name,attr"`
	TimeLimit     *string    `xml:"time-limit"`
	MemoryLimit   *string    `xml:"memory-limit"`
	TestCount     *string    `xml:"test-count"`
	InputPattern  string     `xml:"input-path-pattern"`
	AnswerPattern string     `xml:"answer-path-pattern"`
	Tests         []testXML  `xml:"tests>test"`
	Groups        []groupXML `xml:"groups>group"`
}

type testXML struct {
	Points *string `xml:"points,attr"`
	Group  *string `xml:"group,attr"`
}

type groupXML struct {
	Name string `xml:"name,attr"`
}

func readXML(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := xml.NewDecoder(f)
	d.CharsetReader = func(charset string, r io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") || strings.EqualFold(charset, "us-ascii") {
			return r, nil
		}
		return nil, fmt.Errorf("unsupported encoding %q (Polygon writes UTF-8)", charset)
	}
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// options returns opts with what the importer always sets, for direct
// callers.
func options(opts *importer.Options) *importer.Options {
	if opts == nil {
		opts = &importer.Options{}
	}
	if opts.Report == nil {
		o := *opts
		o.Report = &importer.Report{}
		opts = &o
	}
	return opts
}

// baseName is the name of the directory, as upstream
// os.path.basename(os.path.abspath(path)).
func baseName(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Base(dir)
}

// LoadTask implements importer.TaskLoader (upstream
// PolygonTaskLoader.get_task).
func (Loader) LoadTask(dir string, opts *importer.Options) (*importer.Task, error) {
	opts = options(opts)
	rep := opts.Report
	name := baseName(dir)
	where := "task " + name
	var p problemXML
	if err := readXML(filepath.Join(dir, "problem.xml"), &p); err != nil {
		return nil, err
	}
	if len(p.Names) == 0 {
		return nil, fmt.Errorf("%s: no <names><name> element: the task has no title", filepath.Join(dir, "problem.xml"))
	}
	t := importer.NewTask(name, p.Names[0].Value)

	if !opts.SkipStatements {
		for _, language := range languageOrder {
			path := filepath.Join(dir, "statements", ".pdf", language, "problem.pdf")
			if !exists(path) {
				continue
			}
			code := importer.LanguageCodes[language]
			t.Statements = append(t.Statements, importer.Statement{Language: code, File: importer.FileFromPath(path)})
			t.PrimaryStatements = append(t.PrimaryStatements, code)
		}
	}

	t.SubmissionFormat = []string{name + ".%l"}

	conf, err := readConf(filepath.Join(dir, "files", "cms_conf.py"))
	if err != nil {
		return nil, err
	}
	if conf != nil && conf.general != nil {
		if err := applyGeneral(t, conf.general); err != nil {
			rep.Errorf(conf.path, "general: %v", err)
		}
	}

	if p.Judging == nil {
		return nil, fmt.Errorf("%s: no <judging> element", filepath.Join(dir, "problem.xml"))
	}

	// The checker is the same for every testset: compiled once (upstream
	// compiles it again for each one).
	evaluation := "diff"
	var checkerFile *importer.File
	checkerSrc := filepath.Join(dir, "files", "check.cpp")
	if !exists(checkerSrc) {
		checkerSrc = filepath.Join(dir, "check.cpp")
	}
	if exists(checkerSrc) && len(p.Judging.Testsets) > 0 {
		evaluation = "comparator"
		bin, err := compileChecker(checkerSrc)
		if err != nil {
			rep.Errorf(checkerSrc, "%v", err)
		} else {
			checkerFile = importer.FileFromBytes("checker compiled from "+checkerSrc, bin)
		}
	}

	var testsets []string
	active := ""
	for _, ts := range p.Judging.Testsets {
		testsets = append(testsets, ts.Name)
		o, confErr := conf.dataset(ts.Name)
		d, err := loadTestset(dir, name, ts, o, rep)
		if err != nil {
			return nil, err
		}
		if checkerFile != nil {
			d.Managers = append(d.Managers, importer.Manager{Filename: "checker", File: checkerFile})
		}
		d.TaskType = "Batch"
		d.TaskTypeParameters = taskTypeParameters(p.Judging.InputFile, p.Judging.OutputFile, evaluation)

		if confErr != nil {
			rep.Errorf(conf.path, "%v", confErr)
		} else if o != nil {
			if err := applyDataset(d, o); err != nil {
				rep.Errorf(conf.path, "datasets[%q]: %v", ts.Name, err)
			}
		}
		t.Datasets = append(t.Datasets, d)
		if ts.Name == "tests" {
			active = d.Description
		}
	}
	if unused := conf.unusedDatasets(testsets); len(unused) > 0 {
		rep.Warnf(conf.path, "datasets names testsets that problem.xml does not have, ignored: %s", strings.Join(unused, ", "))
	}
	switch {
	case len(t.Datasets) == 0:
		rep.Errorf(where, "problem.xml has no testset")
	case active == "" && !hasTestset(testsets, "tests"):
		rep.Warnf(where, "no testset is called \"tests\": the first one, %q, is the active dataset", t.Datasets[0].Description)
	}
	t.ActiveDataset = active
	return t, nil
}

func hasTestset(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// patternRE accepts a path pattern with one integer verb (%d, %02d...).
var patternRE = regexp.MustCompile(`^[^%]*%0?[1-9]?d[^%]*$`)

// testPath returns the file of test number i (from 1) of a testset: the
// pattern of problem.xml if it is a plain one inside the package, else
// <testset>/%02d as upstream (plus suffix).
func testPath(dir, testset, pattern, suffix string, i int) string {
	if patternRE.MatchString(pattern) {
		rel := fmt.Sprintf(pattern, i)
		if filepath.IsLocal(rel) {
			return filepath.Join(dir, rel)
		}
	}
	return filepath.Join(dir, testset, fmt.Sprintf("%02d", i)+suffix)
}

// loadTestset reads one testset as a dataset (without the task type);
// conf are its options in cms_conf.py (nil if none), applied later.
func loadTestset(dir, task string, ts testsetXML, conf *pyValue, rep *importer.Report) (*importer.Dataset, error) {
	where := fmt.Sprintf("task %s: testset %q", task, ts.Name)
	d := importer.NewDataset(ts.Name)
	d.Autojudge = false

	if ts.TimeLimit == nil || ts.MemoryLimit == nil || ts.TestCount == nil {
		return nil, fmt.Errorf("%s: problem.xml needs time-limit, memory-limit and test-count", where)
	}
	tl, err := strconv.ParseFloat(strings.TrimSpace(*ts.TimeLimit), 64)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid time-limit %q", where, *ts.TimeLimit)
	}
	// Polygon gives milliseconds. Upstream multiplies by 0.001, which
	// gives 0.7000000000000001 s for 700 ms; dividing gives 0.7.
	d.TimeLimit = importer.Ptr(tl / 1000)
	ml, err := strconv.ParseInt(strings.TrimSpace(*ts.MemoryLimit), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid memory-limit %q", where, *ts.MemoryLimit)
	}
	// Polygon gives bytes, which is what CMS stores.
	d.MemoryLimit = &ml
	n, err := strconv.Atoi(strings.TrimSpace(*ts.TestCount))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("%s: invalid test-count %q", where, *ts.TestCount)
	}

	d.ScoreType = "Sum"
	value := 0.0
	if n != 0 {
		value = 100.0 / float64(n)
	}
	d.ScoreTypeParameters = []byte(pyFloatRepr(value))

	for i := 0; i < n; i++ {
		in := testPath(dir, ts.Name, ts.InputPattern, "", i+1)
		out := testPath(dir, ts.Name, ts.AnswerPattern, ".a", i+1)
		for _, f := range []string{in, out} {
			if !isFile(f) {
				rep.Errorf(where, "test %d: %s is missing (generate the tests in Polygon and download the full package)", i+1, f)
			}
		}
		d.Testcases = append(d.Testcases, importer.Testcase{
			Codename: fmt.Sprintf("%03d", i),
			Public:   true,
			Input:    unixFile(in),
			Output:   unixFile(out),
		})
	}

	hasPoints := len(ts.Groups) > 0
	for _, tc := range ts.Tests {
		if tc.Points != nil || tc.Group != nil {
			hasPoints = true
		}
	}
	// No warning when cms_conf.py sets the score type of the testset.
	if conf != nil {
		if _, ok := conf.get(pyStrValue("score_type")); ok {
			hasPoints = false
		}
	}
	if hasPoints {
		rep.Warnf(where, "the points and groups of the tests are not used, as in CMS: every test is worth 100/%d points (Sum); set score_type and score_type_parameters (e.g. GroupMin) in files/cms_conf.py", max(n, 1))
	}
	return d, nil
}

// taskTypeParameters are those of Batch: ["alone", [input, output],
// evaluation].
func taskTypeParameters(input, output, evaluation string) []byte {
	b, err := pyJSON(pyValue{kind: pyList, items: []pyValue{
		pyStrValue("alone"),
		{kind: pyList, items: []pyValue{pyStrValue(input), pyStrValue(output)}},
		pyStrValue(evaluation),
	}})
	if err != nil {
		panic(err) // strings always encode
	}
	return b
}

// contestantsFile lists users (not a standard Polygon file).
const contestantsFile = "contestants.txt"

// contestant is a line of contestants.txt:
// username;password;first_name;last_name;hidden.
type contestant struct {
	line                int
	username, password  string
	firstName, lastName string
	hidden              *bool
	// names: the line has the first and last names.
	names bool
}

// readContestants reads contestants.txt.
func readContestants(path string) ([]contestant, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\uFEFF")
	var out []contestant
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, ";")
		for j := range f {
			f[j] = strings.TrimSpace(f[j])
		}
		if len(f) < 2 {
			return nil, fmt.Errorf("%s:%d: expected username;password;first name;last name;hidden", path, i+1)
		}
		c := contestant{line: i + 1, username: f[0], password: f[1]}
		if len(f) > 3 {
			c.firstName, c.lastName, c.names = f[2], f[3], true
		} else if len(f) > 2 {
			c.firstName = f[2]
		}
		if len(f) > 4 {
			// Upstream hands the text to PostgreSQL, which reads it as a
			// boolean.
			b, ok := pgBool(f[4])
			if !ok {
				return nil, fmt.Errorf("%s:%d: invalid hidden value %q (0 or 1)", path, i+1, f[4])
			}
			c.hidden = &b
		}
		out = append(out, c)
	}
	return out, nil
}

// pgBool parses a boolean as PostgreSQL does.
func pgBool(s string) (bool, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return false, false
	case s == "1" || strings.HasPrefix("true", s) || strings.HasPrefix("yes", s) || s == "on":
		return true, true
	case s == "0" || strings.HasPrefix("false", s) || strings.HasPrefix("no", s) || s == "of" || s == "off":
		return false, true
	}
	return false, false
}

func (c contestant) user() importer.User {
	return importer.User{
		Username:  c.username,
		FirstName: c.firstName,
		LastName:  c.lastName,
		Password:  importer.BuildPassword(c.password, importer.PasswordPlaintext),
	}
}

// LoadUsers implements importer.UserLoader (upstream PolygonUserLoader):
// every user of dir/contestants.txt or, as upstream, the user called
// like dir in the contestants.txt next to it.
func (Loader) LoadUsers(dir string, opts *importer.Options) ([]importer.User, error) {
	path := filepath.Join(dir, contestantsFile)
	only := ""
	if !exists(path) {
		path = filepath.Join(filepath.Dir(filepath.Clean(dir)), contestantsFile)
		only = baseName(dir)
	}
	cs, err := readContestants(path)
	if err != nil {
		return nil, err
	}
	var users []importer.User
	for _, c := range cs {
		if only == "" || c.username == only {
			users = append(users, c.user())
		}
	}
	if only != "" && len(users) == 0 {
		return nil, fmt.Errorf("user %s not found in %s", only, path)
	}
	return users, nil
}

// contestXML is what the loader reads of contest.xml.
type contestXML struct {
	Names    []nameXML `xml:"names>name"`
	Problems []struct {
		URL string `xml:"url,attr"`
	} `xml:"problems>problem"`
}

// TaskLoader implements importer.ContestLoader: the tasks are in
// problems/<name>.
func (Loader) TaskLoader(dir, name string) (importer.TaskLoader, string) {
	return Loader{}, filepath.Join(dir, "problems", name)
}

// LoadContest implements importer.ContestLoader (upstream
// PolygonContestLoader.get_contest).
func (Loader) LoadContest(dir string, opts *importer.Options) (*importer.Contest, error) {
	opts = options(opts)
	path := filepath.Join(dir, "contest.xml")
	var x contestXML
	if err := readXML(path, &x); err != nil {
		return nil, err
	}
	c := importer.NewContest(baseName(dir))
	if len(x.Names) == 0 {
		return nil, fmt.Errorf("%s: no <names><name> element", path)
	}
	// The description is the name in the primary language, the language
	// of the first name: the first name.
	c.Description = x.Names[0].Value
	// Polygon has no contest-wide settings: the times are those upstream
	// hard-codes.
	g := importer.NewGroup("default")
	g.Start, g.Stop = importer.ZeroTime, importer.ZeroTime
	c.Groups = []importer.Group{g}

	for _, pr := range x.Problems {
		name := pr.URL[strings.LastIndex(pr.URL, "/")+1:]
		if name == "" {
			return nil, fmt.Errorf("%s: a problem has no name at the end of its url %q", path, pr.URL)
		}
		c.Tasks = append(c.Tasks, name)
	}

	cpath := filepath.Join(dir, contestantsFile)
	if exists(cpath) {
		cs, err := readContestants(cpath)
		if err != nil {
			return nil, err
		}
		for _, ct := range cs {
			pw := importer.BuildPassword(ct.password, importer.PasswordPlaintext)
			c.Participations = append(c.Participations, importer.Participation{
				Username: ct.username,
				Password: &pw,
				Hidden:   ct.hidden,
			})
			c.Users = append(c.Users, ct.user())
			if !ct.names {
				opts.Report.Warnf(fmt.Sprintf("%s:%d", cpath, ct.line), "user %s has no first or last name", ct.username)
			}
		}
	}
	return c, nil
}

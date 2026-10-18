// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/tps.py of CMS:
// Copyright © 2017 Kiarash Golezardi <kiarashgolezardi@gmail.com>
// Copyright © 2017 Amir Keivan Mohtashami <akmohtashami97@gmail.com>
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

// Package tps reads tasks exported for CMS by TPS, the Task Preparation
// System of the IOI (`tps export CMS`), with the rules of the tps_task
// loader of CMS:
//
//   - problem.json gives the name (code), the title (name), the task type
//     (task_type, its first letter capitalized), its parameters
//     (task_type_params), the limits (time_limit in seconds, memory_limit
//     in bytes), score_precision (default 2) and feedback_level;
//   - the task gets score mode max_subtask, at most 50 submissions and 50
//     user tests (100 for OutputOnly), one minute between them;
//   - statements/<language>.pdf are the statements (en_US is the primary
//     one), attachments/* the attachments;
//   - tests/<codename>.in and .out are the testcases, all public;
//   - subtasks/*.json ({"score": 30, "testcases": [...]}, in the order of
//     their names) make a GroupMin score type, whose groups match their
//     testcases by name (the first one is named "Samples" and the others
//     "Subtask N" when the first is worth 0); without subtasks the score
//     type is Sum, 100/n per testcase;
//   - checker/checker.cpp and graders/manager.cpp are compiled with g++
//     at import time and stored as the "checker" and "manager" managers
//     (with a checker the output is checked by it, else by the white
//     diff); every other file of graders/ is a manager (for Communication
//     tasks stub.<ext> is stored as grader.<ext>);
//   - one dataset, "Default", autojudged and active.
//
// Differences with upstream: nothing is written to the package (upstream
// leaves the compiled checker and manager in it); the output-only
// testcases of BatchAndOutput are listed sorted (upstream joins a set, in
// no particular order); problem.json files of protocol 2, whose
// task_type_params are the list of parameters, are read too (upstream
// fails on them; "stub" compilation is "grader" in CMS); and the problems
// upstream only finds when it crashes (a missing field, a subtask file
// that is not JSON) are errors that name the file.
package tps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// Loader reads TPS tasks. It is registered as "tps" and as "tps_task",
// the name of upstream.
type Loader struct{}

func init() { importer.Register(Loader{}, "tps_task") }

var _ importer.TaskLoader = Loader{}

// Name implements importer.Loader.
func (Loader) Name() string { return "tps" }

// Description implements importer.Loader.
func (Loader) Description() string { return "TPS task format" }

// Detect implements importer.Loader: a task has problem.json.
func (Loader) Detect(dir string, kind importer.Kind) bool {
	return kind == importer.KindTask && exists(filepath.Join(dir, "problem.json"))
}

// Expects says what Detect looks for (importer.Expecter).
func (Loader) Expects(kind importer.Kind) string {
	if kind == importer.KindTask {
		return "problem.json"
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The task types whose names the loader knows.
const (
	batch          = "Batch"
	batchAndOutput = "BatchAndOutput"
	communication  = "Communication"
	outputOnly     = "OutputOnly"
	twoSteps       = "TwoSteps"
	notice         = "Notice"
)

// problem is problem.json.
type problem map[string]json.RawMessage

func (p problem) has(key string) bool {
	v, ok := p[key]
	return ok && string(v) != "null"
}

func (p problem) str(key string) (string, error) {
	v, ok := p[key]
	if !ok {
		return "", fmt.Errorf("problem.json has no %q", key)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", fmt.Errorf("problem.json: %q must be a string, not %s", key, v)
	}
	return s, nil
}

// LoadTask implements importer.TaskLoader (upstream TpsTaskLoader.get_task).
func (Loader) LoadTask(dir string, opts *importer.Options) (*importer.Task, error) {
	if opts == nil {
		opts = &importer.Options{}
	}
	rep := opts.Report
	if rep == nil {
		rep = &importer.Report{}
	}
	jsonPath := filepath.Join(dir, "problem.json")
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var data problem
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("%s: %w", jsonPath, err)
	}
	if !data.has("code") && data.has("type") && data.has("name") {
		return nil, fmt.Errorf("%s is the problem.json of a TPS repository, not of an export: export the task with `tps export CMS 1` (or 2) and import the export", jsonPath)
	}
	name, err := data.str("code")
	if err != nil {
		return nil, err
	}
	title, err := data.str("name")
	if err != nil {
		return nil, err
	}
	where := "task " + name
	t := importer.NewTask(name, title)
	t.ScoreMode = importer.ScoreModeMaxSubtask
	if data.has("score_mode") {
		rep.Warnf(jsonPath, "score_mode is not used, as in CMS: the score mode of TPS tasks is %s", importer.ScoreModeMaxSubtask)
	}

	if raw, ok := data["feedback_level"]; ok {
		var fl any
		if err := json.Unmarshal(raw, &fl); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonPath, err)
		}
		// Upstream ignores a false value (null, "", 0, false).
		if pyTruth(fl) {
			s, _ := fl.(string)
			switch s {
			case importer.FeedbackFull, importer.FeedbackRestricted, importer.FeedbackOIRestricted:
				t.FeedbackLevel = s
			default:
				return nil, fmt.Errorf("%s: invalid feedback_level %s (want %q, %q or %q)", jsonPath, raw,
					importer.FeedbackFull, importer.FeedbackRestricted, importer.FeedbackOIRestricted)
			}
		}
	}

	// Statements.
	if !opts.SkipStatements {
		files, err := listDir(filepath.Join(dir, "statements"), rep)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			language, ok := strings.CutSuffix(f, ".pdf")
			if !ok {
				continue
			}
			if language == "en_US" {
				t.PrimaryStatements = []string{"en_US"}
			}
			t.Statements = append(t.Statements, importer.Statement{
				Language: language, File: importer.FileFromPath(filepath.Join(dir, "statements", f)),
			})
		}
	}

	// Attachments.
	files, err := listDir(filepath.Join(dir, "attachments"), rep)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		t.Attachments = append(t.Attachments, importer.Attachment{
			Filename: f, File: importer.FileFromPath(filepath.Join(dir, "attachments", f)),
		})
	}

	taskType, err := data.str("task_type")
	if err != nil {
		return nil, err
	}
	if taskType == "" {
		return nil, fmt.Errorf("%s: task_type is empty", jsonPath)
	}
	first, size := utf8.DecodeRuneInString(taskType)
	taskType = strings.ToUpper(string(first)) + taskType[size:]

	// Subtasks, in the order of their file names.
	subtasksDir := filepath.Join(dir, "subtasks")
	var subtasks []subtask
	if !exists(subtasksDir) {
		rep.Warnf(where, "subtask folder was not found")
	} else {
		names, err := listDir(subtasksDir, rep)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			st, err := readSubtask(filepath.Join(subtasksDir, n))
			if err != nil {
				return nil, err
			}
			subtasks = append(subtasks, st)
		}
	}

	// Testcases' codenames.
	testsDir := filepath.Join(dir, "tests")
	var codenames []string
	if !exists(testsDir) {
		rep.Warnf(where, "testcase folder was not found")
	} else {
		names, err := listDir(testsDir, rep)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if c, ok := strings.CutSuffix(n, ".in"); ok {
				codenames = append(codenames, c)
			}
		}
		// Upstream sorts the codenames, not the file names ("a" comes
		// before "a-b", but "a-b.in" before "a.in"): the order of the
		// submission format of OutputOnly tasks.
		sort.Strings(codenames)
	}

	var outputOnlyCases, outputCases []string
	if taskType == batchAndOutput {
		only, optional := map[string]bool{}, map[string]bool{}
		for _, st := range subtasks {
			switch {
			case st.outputOnly:
				for _, c := range st.testcases {
					only[c] = true
				}
			case st.outputOptional:
				for _, c := range st.testcases {
					optional[c] = true
				}
			}
		}
		outputOnlyCases = sortedKeys(only)
		for c := range only {
			optional[c] = true
		}
		outputCases = sortedKeys(optional)
	}

	// Submission format.
	switch taskType {
	case outputOnly:
		t.SubmissionFormat = []string{}
		for _, c := range codenames {
			t.SubmissionFormat = append(t.SubmissionFormat, "output_"+c+".txt")
		}
	case notice:
		t.SubmissionFormat = []string{}
	case batchAndOutput:
		t.SubmissionFormat = []string{name + ".%l"}
		for _, c := range outputCases {
			t.SubmissionFormat = append(t.SubmissionFormat, "output_"+c+".txt")
		}
	default:
		t.SubmissionFormat = []string{name + ".%l"}
	}

	t.ScorePrecision = 2
	if data.has("score_precision") {
		p, err := pyInt(data["score_precision"])
		if err != nil || p < math.MinInt32 || p > math.MaxInt32 {
			return nil, fmt.Errorf("%s: invalid score_precision %s", jsonPath, data["score_precision"])
		}
		t.ScorePrecision = int32(p)
	}
	limit := int32(50)
	if taskType == outputOnly {
		limit = 100
	}
	t.MaxSubmissionNumber, t.MaxUserTestNumber = importer.Ptr(limit), importer.Ptr(limit)
	t.MinSubmissionInterval, t.MinUserTestInterval = importer.Ptr(time.Minute), importer.Ptr(time.Minute)

	d := importer.NewDataset("Default")
	d.Autojudge = true
	if taskType != outputOnly && taskType != notice {
		for _, k := range []string{"time_limit", "memory_limit"} {
			if !data.has(k) {
				return nil, fmt.Errorf("%s has no %s", jsonPath, k)
			}
		}
		tl, err := pyFloat(data["time_limit"])
		if err != nil {
			return nil, fmt.Errorf("%s: invalid time_limit %s", jsonPath, data["time_limit"])
		}
		d.TimeLimit = &tl
		ml, err := pyInt(data["memory_limit"])
		if err != nil {
			return nil, fmt.Errorf("%s: invalid memory_limit %s", jsonPath, data["memory_limit"])
		}
		d.MemoryLimit = &ml
		if ml > 0 && ml < importer.MiB {
			rep.Warnf(jsonPath, "memory_limit is in bytes in the exports for CMS: %d bytes is less than 1 MiB (%d MiB would be %d)", ml, ml, ml*importer.MiB)
		}
	}

	var managers managerSet

	// Checker.
	evaluation := "diff"
	checkerSrc := filepath.Join(dir, "checker", "checker.cpp")
	if exists(checkerSrc) {
		evaluation = "comparator"
		if bin, err := compile(checkerSrc, "checker"); err != nil {
			rep.Errorf(checkerSrc, "%v", err)
		} else {
			managers.add("checker", importer.FileFromBytes("checker compiled from "+checkerSrc, bin))
		}
	}

	d.TaskType = taskType
	if d.TaskTypeParameters, err = taskTypeParameters(data, taskType, evaluation, outputOnlyCases); err != nil {
		return nil, fmt.Errorf("%s: %w", jsonPath, err)
	}

	// Graders.
	gradersDir := filepath.Join(dir, "graders")
	if taskType == twoSteps {
		pas := name + "lib.pas"
		if !exists(filepath.Join(gradersDir, pas)) {
			managers.add(pas, importer.FileFromBytes("empty Pascal manager", []byte{}))
		}
	}
	var graders []string
	if !exists(gradersDir) {
		rep.Warnf(where, "grader folder was not found")
	} else {
		names, err := listDir(gradersDir, rep)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n != "manager.cpp" {
				graders = append(graders, n)
			}
		}
	}
	if taskType == communication {
		var stubs, grs bool
		for _, g := range graders {
			switch stem(g) {
			case "stub":
				stubs = true
			case "grader":
				grs = true
			}
		}
		if stubs && grs {
			return nil, fmt.Errorf("%s has both a stub and a grader", gradersDir)
		}
	}
	for _, g := range graders {
		filename := g
		if taskType == communication && stem(g) == "stub" {
			filename = "grader" + filepath.Ext(g)
		}
		managers.add(filename, importer.FileFromPath(filepath.Join(gradersDir, g)))
	}

	// Manager.
	managerSrc := filepath.Join(gradersDir, "manager.cpp")
	if exists(managerSrc) {
		if bin, err := compile(managerSrc, "manager"); err != nil {
			rep.Errorf(managerSrc, "%v", err)
		} else {
			managers.add("manager", importer.FileFromBytes("manager compiled from "+managerSrc, bin))
		}
	}
	d.Managers = managers.list

	// Testcases.
	known := make(map[string]bool, len(codenames))
	for _, c := range codenames {
		known[c] = true
		out := filepath.Join(testsDir, c+".out")
		if !exists(out) {
			return nil, fmt.Errorf("could not find the output file for testcase %s: %s", c, out)
		}
		d.Testcases = append(d.Testcases, importer.Testcase{
			Codename: c, Public: true,
			Input:  importer.FileFromPath(filepath.Join(testsDir, c+".in")),
			Output: importer.FileFromPath(out),
		})
	}

	// Score type.
	if len(subtasks) == 0 {
		d.ScoreType = "Sum"
		d.ScoreTypeParameters = []byte(pyFloatRepr(100 / float64(max(len(codenames), 1))))
	} else {
		d.ScoreType = "GroupMin"
		if d.ScoreTypeParameters, err = groupMinParameters(subtasks, known, rep); err != nil {
			return nil, err
		}
	}

	t.Datasets = []*importer.Dataset{d}
	return t, nil
}

// listDir returns the names of the regular files of dir sorted ([] if dir
// does not exist); subdirectories are skipped with a warning (upstream
// fails on them).
func listDir(dir string, rep *importer.Report) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			rep.Warnf(dir, "%s is a directory: skipped", e.Name())
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// stem is os.path.splitext(name)[0].
func stem(name string) string {
	ext := filepath.Ext(name)
	if ext == name || strings.TrimLeft(name, ".") == strings.TrimPrefix(ext, ".") {
		// ".bashrc" has no extension for Python.
		return name
	}
	return strings.TrimSuffix(name, ext)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// managerSet is the dict of managers of upstream: a name added again
// replaces the file and keeps its place.
type managerSet struct {
	list []importer.Manager
}

func (m *managerSet) add(name string, f *importer.File) {
	for i := range m.list {
		if m.list[i].Filename == name {
			m.list[i].File = f
			return
		}
	}
	m.list = append(m.list, importer.Manager{Filename: name, File: f})
}

// subtask is a file of subtasks/.
type subtask struct {
	path           string
	score          int64
	testcases      []string
	outputOnly     bool
	outputOptional bool
}

func readSubtask(path string) (subtask, error) {
	st := subtask{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return st, fmt.Errorf("%s: %w", path, err)
	}
	if _, ok := m["score"]; !ok {
		return st, fmt.Errorf("%s has no score", path)
	}
	if st.score, err = pyInt(m["score"]); err != nil {
		return st, fmt.Errorf("%s: invalid score %s", path, m["score"])
	}
	if v, ok := m["testcases"]; ok {
		if err := json.Unmarshal(v, &st.testcases); err != nil {
			return st, fmt.Errorf("%s: testcases must be a list of codenames", path)
		}
	} else {
		// Upstream reads cur_subtask_data["testcases"] of every subtask.
		return st, fmt.Errorf("%s has no testcases", path)
	}
	for k, dst := range map[string]*bool{"output_only": &st.outputOnly, "output_optional": &st.outputOptional} {
		if v, ok := m[k]; ok {
			var x any
			if err := json.Unmarshal(v, &x); err != nil {
				return st, err
			}
			*dst = pyTruth(x)
		}
	}
	return st, nil
}

// groupMinParameters are the parameters of GroupMin for the subtasks:
// [score, regex] or, when the first subtask is worth 0, [score, regex,
// name] with the names "Samples", "Subtask 1"...
func groupMinParameters(subtasks []subtask, known map[string]bool, rep *importer.Report) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	named := false
	for i, st := range subtasks {
		if i > 0 {
			b.WriteString(", ")
		}
		var parts []string
		for _, c := range st.testcases {
			parts = append(parts, reEscape(c))
			if !known[c] {
				rep.Warnf(st.path, "testcase %s is not in tests/", c)
			}
		}
		label := fmt.Sprintf("Subtask %d", i)
		if i == 0 && st.score == 0 {
			named = true
			label = "Samples"
		}
		fmt.Fprintf(&b, "[%d, %s", st.score, jsonString(strings.Join(parts, "|")))
		if named {
			fmt.Fprintf(&b, ", %s", jsonString(label))
		}
		b.WriteByte(']')
	}
	b.WriteByte(']')
	return b.Bytes(), nil
}

func jsonString(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

// reEscape is re.escape of Python 3.7 and later: only the characters that
// can be special in a pattern are escaped.
func reEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("()[]{}?*+-|^$\\.&~# \t\n\r\v\f", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// taskTypeParameters builds the parameters of the task type as upstream
// _get_task_type_parameters does from task_type_params, a string with a
// JSON object of TPS parameters (protocol 1). A list is taken as the
// parameters themselves (protocol 2).
func taskTypeParameters(data problem, taskType, evaluation string, outputOnlyCases []string) (json.RawMessage, error) {
	raw := data["task_type_params"]
	params := map[string]json.RawMessage{}
	switch {
	case len(raw) == 0 || string(raw) == "null":
	case raw[0] == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if s == "" {
			s = "{}"
		}
		if err := json.Unmarshal([]byte(s), &params); err != nil {
			return nil, fmt.Errorf("task_type_params is not a JSON object: %v", err)
		}
	case raw[0] == '[':
		return protocol2(raw, taskType)
	case raw[0] == '{':
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid task_type_params %s", raw)
	}
	prefix := "task_type_parameters_" + taskType
	get := func(key string, def any) json.RawMessage {
		if v, ok := params[prefix+"_"+key]; ok {
			return v
		}
		b, _ := json.Marshal(def)
		return b
	}
	str := func(s string) json.RawMessage { return json.RawMessage(jsonString(s)) }
	var list []json.RawMessage
	switch taskType {
	case batch, batchAndOutput:
		files := marshalList([]json.RawMessage{get("io_0_inputfile", ""), get("io_1_outputfile", "")})
		list = []json.RawMessage{get("compilation", "grader"), files, str(evaluation)}
		if taskType == batchAndOutput {
			list = append(list, str(strings.Join(outputOnlyCases, ",")))
		}
	case communication:
		list = []json.RawMessage{get("num_processes", 1), str("grader"), str("std_io")}
	case twoSteps, outputOnly:
		list = []json.RawMessage{str(evaluation)}
	default:
		list = []json.RawMessage{}
	}
	return marshalList(list), nil
}

// protocol2 takes the parameters as they are, but for the "stub"
// compilation of Communication, which is "grader" in CMS.
func protocol2(raw json.RawMessage, taskType string) (json.RawMessage, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	if taskType == communication && len(list) > 1 && string(bytes.TrimSpace(list[1])) == `"stub"` {
		list[1] = json.RawMessage(`"grader"`)
	}
	return marshalList(list), nil
}

// marshalList writes a list as Python's json.dumps: ", " between items
// and ": " after keys, also inside the items.
func marshalList(list []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range list {
		if i > 0 {
			b.WriteString(", ")
		}
		if err := pyFormat(&b, v); err != nil {
			b.Write(v) // not JSON: the validation reports it
		}
	}
	b.WriteByte(']')
	return b.Bytes()
}

// pyFormat writes the JSON value raw with the separators of Python's
// json.dumps, keeping the order of keys and the text of numbers.
func pyFormat(b *bytes.Buffer, raw json.RawMessage) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	type level struct {
		object bool
		n      int
	}
	var stack []level
	out := bytes.Buffer{}
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if delim, ok := tok.(json.Delim); ok && (delim == ']' || delim == '}') {
			stack = stack[:len(stack)-1]
			out.WriteByte(byte(delim))
			continue
		}
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			switch {
			case top.object && top.n%2 == 1:
				out.WriteString(": ")
			case top.n > 0:
				out.WriteString(", ")
			}
			top.n++
		}
		switch tok := tok.(type) {
		case json.Delim:
			out.WriteByte(byte(tok))
			stack = append(stack, level{object: tok == '{'})
		case string:
			out.WriteString(jsonString(tok))
		case json.Number:
			out.WriteString(tok.String())
		case bool:
			out.WriteString(strconv.FormatBool(tok))
		case nil:
			out.WriteString("null")
		}
	}
	b.Write(out.Bytes())
	return nil
}

// pyInt is Python's int() of a JSON value: integers, floats (truncated),
// booleans and strings of an integer.
func pyInt(raw json.RawMessage) (int64, error) {
	var x any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&x); err != nil {
		return 0, err
	}
	switch x := x.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, nil
		}
		f, err := x.Float64()
		if err != nil || math.IsInf(f, 0) || math.Abs(f) >= 1<<63 {
			return 0, fmt.Errorf("invalid integer %s", x)
		}
		return int64(f), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case string:
		return strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(x), "_", ""), 10, 64)
	}
	return 0, fmt.Errorf("not a number: %s", raw)
}

// pyFloat is Python's float() of a JSON value.
func pyFloat(raw json.RawMessage) (float64, error) {
	var x any
	if err := json.Unmarshal(raw, &x); err != nil {
		return 0, err
	}
	switch x := x.(type) {
	case float64:
		return x, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case string:
		return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), "_", ""), 64)
	}
	return 0, fmt.Errorf("not a number: %s", raw)
}

// pyTruth is Python's bool() of a JSON value.
func pyTruth(x any) bool {
	switch x := x.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// pyFloatRepr formats a float as Python's repr (how json.dumps writes
// it): the shortest digits, with a decimal point, in fixed notation
// between 1e-4 and 1e16.
func pyFloatRepr(f float64) string {
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	if f != 0 && (exp < -4 || exp >= 16) {
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		return fmt.Sprintf("%se%s%02d", mant, sign, exp)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

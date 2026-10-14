// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Encoded is a task as a package: problem.yaml and the files, ready to
// be written to a directory or a zip.
type Encoded struct {
	// Task and Dataset name what was encoded.
	Task, Dataset string
	// Problem is the content of problem.yaml.
	Problem []byte
	// Files are the other files, in the order to write them.
	Files []PackageFile
	// Warnings say what the package cannot carry (other datasets,
	// managers the loader would not import back).
	Warnings []string
}

// PackageFile is a file of a package.
type PackageFile struct {
	// Path is its path in the package ("tests/01.in").
	Path string
	// File is its content (by digest when exported from the database).
	File *importer.File
	// Executable: a program run by the judges (checker, manager,
	// controller), written with the executable bit.
	Executable bool
}

// Opener returns the content of a file of the model: f.Open for files on
// disk, the blob store for files known by digest (BlobOpener).
type Opener func(ctx context.Context, f *importer.File) (io.ReadCloser, error)

// BlobOpener opens the files known by digest in a blob store, the others
// with File.Open.
func BlobOpener(store blob.Store) Opener {
	return func(ctx context.Context, f *importer.File) (io.ReadCloser, error) {
		if f.Path() == "" && f.Digest() != "" {
			if !blob.ValidDigest(f.Digest()) {
				return nil, fmt.Errorf("%s: the content is gone (digest %q)", f.Name(), f.Digest())
			}
			return store.Open(ctx, f.Digest())
		}
		return f.Open()
	}
}

// ReadTask reads the task with that id and one of its datasets (the one
// with that description, or the active one if description is "") from the
// database, with a constant number of queries. Files are given by digest.
func ReadTask(ctx context.Context, db q.DBTX, taskID int32, description string) (*importer.Task, *importer.Dataset, []string, error) {
	qq := q.New(db)
	row, err := qq.GetTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, fmt.Errorf("task %d does not exist", taskID)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	t := &importer.Task{
		Name: row.Name, Title: row.Title,
		SubmissionFormat: row.SubmissionFormat, PrimaryStatements: row.PrimaryStatements, AllowedLanguages: row.AllowedLanguages,
		Tokens: importer.Tokens{Mode: row.TokenMode, MaxNumber: row.TokenMaxNumber, MinInterval: row.TokenMinInterval,
			GenInitial: row.TokenGenInitial, GenNumber: row.TokenGenNumber, GenInterval: row.TokenGenInterval, GenMax: row.TokenGenMax},
		MaxSubmissionNumber: row.MaxSubmissionNumber, MaxUserTestNumber: row.MaxUserTestNumber,
		MinSubmissionInterval: row.MinSubmissionInterval, MinUserTestInterval: row.MinUserTestInterval,
		FeedbackLevel: row.FeedbackLevel, ScorePrecision: row.ScorePrecision, ScoreMode: row.ScoreMode,
	}
	where := func(kind, name string) string { return fmt.Sprintf("task %s: %s %s", row.Name, kind, name) }
	statements, err := qq.ListStatementsByTask(ctx, taskID)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, s := range statements {
		t.Statements = append(t.Statements, importer.Statement{Language: s.Language, File: importer.FileFromDigest(where("statement", s.Language), s.Digest)})
	}
	attachments, err := qq.ListAttachmentsByTask(ctx, taskID)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, a := range attachments {
		t.Attachments = append(t.Attachments, importer.Attachment{Filename: a.Filename, File: importer.FileFromDigest(where("attachment", a.Filename), a.Digest)})
	}
	datasets, err := qq.ListDatasetsByTask(ctx, taskID)
	if err != nil {
		return nil, nil, nil, err
	}
	var ds *q.Dataset
	var others []string
	for i := range datasets {
		d := &datasets[i]
		if description == "" && row.ActiveDatasetID != nil && d.ID == *row.ActiveDatasetID || description != "" && d.Description == description {
			ds = d
		} else {
			others = append(others, d.Description)
		}
	}
	if ds == nil {
		if description == "" {
			return nil, nil, nil, fmt.Errorf("task %s has no active dataset", row.Name)
		}
		return nil, nil, nil, fmt.Errorf("task %s has no dataset %q", row.Name, description)
	}
	d := &importer.Dataset{
		Description: ds.Description, Autojudge: ds.Autojudge, TimeLimit: ds.TimeLimit, MemoryLimit: ds.MemoryLimit,
		TaskType: ds.TaskType, TaskTypeParameters: ds.TaskTypeParameters,
		ScoreType: ds.ScoreType, ScoreTypeParameters: ds.ScoreTypeParameters,
	}
	managers, err := qq.ListManagersByDataset(ctx, ds.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, m := range managers {
		d.Managers = append(d.Managers, importer.Manager{Filename: m.Filename, File: importer.FileFromDigest(where("manager", m.Filename), m.Digest)})
	}
	testcases, err := qq.ListTestcasesByDataset(ctx, ds.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, tc := range testcases {
		d.Testcases = append(d.Testcases, importer.Testcase{Codename: tc.Codename, Public: tc.Public,
			Input:  importer.FileFromDigest(where("input of testcase", tc.Codename), tc.Input),
			Output: importer.FileFromDigest(where("output of testcase", tc.Codename), tc.Output)})
	}
	t.Datasets = []*importer.Dataset{d}
	t.ActiveDataset = d.Description
	return t, d, others, nil
}

// sameContent reports whether two files have the same content: the same
// digest, or the same file.
func sameContent(a, b *importer.File) bool {
	if a == b {
		return true
	}
	return a != nil && b != nil && a.Digest() != "" && a.Digest() == b.Digest()
}

// Encode returns the package of task t with its dataset d: the
// problem.yaml that Load reads back into the same rows, and the files. The
// format of the statements is guessed from their content (PDF or
// Markdown), which open reads.
func Encode(ctx context.Context, t *importer.Task, d *importer.Dataset, langs *language.Registry, open Opener) (*Encoded, error) {
	if langs == nil {
		langs = language.Builtin()
	}
	enc := &Encoded{Task: t.Name, Dataset: d.Description}
	fail := func(format string, args ...any) (*Encoded, error) {
		return nil, fmt.Errorf("task %s: "+format, append([]any{t.Name}, args...)...)
	}
	if _, err := tasktype.Get(d.TaskType, d.TaskTypeParameters); err != nil {
		return fail("dataset %q: %v", d.Description, err)
	}
	var params []json.RawMessage
	if err := json.Unmarshal(d.TaskTypeParameters, &params); err != nil {
		return fail("task type parameters: %v", err)
	}
	str := func(i int) string {
		var s string
		_ = json.Unmarshal(params[i], &s)
		return s
	}

	p := &problem{Name: t.Name, Title: t.Title, Dataset: d.Description, Autojudge: d.Autojudge,
		TimeLimit: d.TimeLimit, ScorePrecision: t.ScorePrecision,
		MaxSubmissions: t.MaxSubmissionNumber, MaxUserTests: t.MaxUserTestNumber,
		MinSubmissionInterval: secondsOf(t.MinSubmissionInterval), MinUserTestInterval: secondsOf(t.MinUserTestInterval)}
	if d.TaskType != tasktype.NameBatch {
		p.Type = typeNames[d.TaskType]
	}
	// Settings that differ from the defaults of a new task.
	def := importer.NewTask(t.Name, t.Title)
	if t.FeedbackLevel != def.FeedbackLevel {
		p.FeedbackLevel = t.FeedbackLevel
	}
	if t.ScoreMode != def.ScoreMode {
		p.ScoreMode = t.ScoreMode
	}
	if d.MemoryLimit != nil {
		if *d.MemoryLimit%importer.MiB != 0 {
			return fail("the memory limit (%d bytes) is not a whole number of MiB", *d.MemoryLimit)
		}
		p.MemoryLimit = importer.Ptr(*d.MemoryLimit / importer.MiB)
	}
	if t.AllowedLanguages != nil {
		p.Languages = &t.AllowedLanguages
	}

	// Tests, sorted by name as the loader sorts them.
	tests := make([]string, len(d.Testcases))
	byCodename := map[string]importer.Testcase{}
	for i, tc := range d.Testcases {
		if !importer.ValidCodename(tc.Codename) {
			return fail("invalid testcase codename %q", tc.Codename)
		}
		tests[i] = tc.Codename
		byCodename[tc.Codename] = tc
	}
	slices.Sort(tests)
	var public []string
	for _, c := range tests {
		if byCodename[c].Public {
			public = append(public, c)
		}
	}
	p.PublicTests = selectionOf(public, tests)

	// The task type.
	hasGraders := false
	for _, m := range d.Managers {
		if base, ext := splitExt(m.Filename); base == "grader" && ext != "" {
			hasGraders = true
		}
	}
	setGrader := func(compilation string) {
		if g := compilation == "grader"; g != hasGraders {
			p.Grader = &g
		}
	}
	setChecker := func(outputEval string) {
		if outputEval != tasktype.OutputEvalDiff {
			p.Checker = &checkerSpec{Type: checkerNames[outputEval]}
		}
	}
	defaultFormat := []string{t.Name + ".%l"}
	outputTests := map[string]bool{} // tests answered with an output file
	switch d.TaskType {
	case tasktype.NameBatch, tasktype.NameBatchAndOutput:
		setGrader(str(0))
		var io []string
		_ = json.Unmarshal(params[1], &io)
		p.Input, p.Output = io[0], io[1]
		setChecker(str(2))
		if d.TaskType == tasktype.NameBatchAndOutput {
			var only []string
			for _, c := range strings.Split(str(3), ",") {
				if c == "" {
					continue
				}
				if _, ok := byCodename[c]; !ok {
					return fail("output-only testcase %q does not exist", c)
				}
				only = append(only, c)
				outputTests[c] = true
			}
			slices.Sort(only)
			p.OutputOnlyTests = selectionOf(only, tests)
			var optional []string
			for _, c := range tests {
				if !outputTests[c] && slices.Contains(t.SubmissionFormat, tasktype.UserOutputFilename(c)) {
					optional = append(optional, c)
					outputTests[c] = true
				}
			}
			p.OutputTests = selectionOf(optional, tests)
			for _, c := range tests {
				if outputTests[c] {
					defaultFormat = append(defaultFormat, tasktype.UserOutputFilename(c))
				}
			}
			if p.OutputOnlyTests == nil && p.OutputTests == nil {
				p.Type = typeNames[d.TaskType] // no output test: say it
			}
		}
	case tasktype.NameOutputOnly:
		setChecker(str(0))
		defaultFormat = nil
		for _, c := range tests {
			defaultFormat = append(defaultFormat, tasktype.UserOutputFilename(c))
			outputTests[c] = true
		}
	case tasktype.NameCommunication:
		var n int
		_ = json.Unmarshal(params[0], &n)
		if n != 1 {
			p.Processes = &n
		}
		setGrader(str(1))
		userIO := "std_io"
		if str(1) == "grader" {
			userIO = "fifo_io"
		}
		if str(2) != userIO {
			p.UserIO = str(2)
		}
	case tasktype.NameInteractive:
		var n int
		_ = json.Unmarshal(params[0], &n)
		if n != 1 {
			p.ProcessLimit = &n
		}
		setGrader(str(1))
		var concurrent bool
		_ = json.Unmarshal(params[2], &concurrent)
		if !concurrent {
			p.Concurrent = &concurrent
		}
		limits := make([]*number, 3)
		for i := range limits {
			n, err := jsonNumber(params[3+i])
			if err != nil {
				return fail("controller limits: %v", err)
			}
			limits[i] = n
		}
		p.ControllerMemoryLimit, p.ControllerTimeLimit, p.ControllerWallTimeLimit = limits[0], limits[1], limits[2]
	case tasktype.NameTwoSteps:
		setChecker(str(0))
		defaultFormat = nil
	}
	if !slices.Equal(t.SubmissionFormat, defaultFormat) || defaultFormat == nil {
		sf := nonNil(t.SubmissionFormat)
		p.SubmissionFormat = &sf
	}

	// The score type.
	sc, err := encodeScoring(d, tests, t.ScorePrecision)
	if err != nil {
		return fail("%v", err)
	}
	p.Scoring = sc

	// The tokens, where they differ from the defaults.
	p.Tokens = encodeTokens(t.Tokens)

	// Statements (the primary ones where they differ from the default).
	var files []PackageFile
	for _, s := range t.Statements {
		if !importer.ValidCodename(s.Language) {
			return fail("statement language %q cannot be a file name", s.Language)
		}
		ext, err := statementExt(ctx, s.File, open)
		if err != nil {
			return fail("statement %s: %v", s.Language, err)
		}
		files = append(files, PackageFile{Path: StatementDir + "/" + s.Language + ext, File: s.File})
	}
	var defaultPrimary []string
	if len(t.Statements) == 1 {
		defaultPrimary = []string{t.Statements[0].Language}
	}
	if !slices.Equal(t.PrimaryStatements, defaultPrimary) {
		ps := nonNil(t.PrimaryStatements)
		p.PrimaryStatements = &ps
	}

	// Attachments; the inputs of the output tests are added back by the
	// loader if all of them are there.
	auto := map[string]bool{}
	if d.TaskType == tasktype.NameOutputOnly || d.TaskType == tasktype.NameBatchAndOutput {
		attached := 0
		for c := range outputTests {
			name := inputAttachment(c)
			if i := slices.IndexFunc(t.Attachments, func(a importer.Attachment) bool { return a.Filename == name }); i >= 0 &&
				sameContent(t.Attachments[i].File, byCodename[c].Input) {
				attached++
			}
		}
		if attached == len(outputTests) {
			for c := range outputTests {
				auto[inputAttachment(c)] = true
			}
		} else if len(outputTests) > 0 {
			p.AttachInputs = importer.Ptr(false)
		}
	}
	for _, a := range t.Attachments {
		if auto[a.Filename] {
			continue
		}
		if !importer.ValidFilename(a.Filename) {
			return fail("invalid attachment file name %q", a.Filename)
		}
		if strings.HasPrefix(a.Filename, ".") {
			enc.Warnings = append(enc.Warnings, fmt.Sprintf("attachment %s is a hidden file: it will not be imported back", a.Filename))
		}
		files = append(files, PackageFile{Path: AttachmentsDir + "/" + a.Filename, File: a.File})
	}

	// Tests and graders.
	for _, c := range tests {
		tc := byCodename[c]
		files = append(files,
			PackageFile{Path: TestsDir + "/" + c + ".in", File: tc.Input},
			PackageFile{Path: TestsDir + "/" + c + ".out", File: tc.Output})
	}
	sourceExts := langs.SourceExtensions()
	for _, m := range d.Managers {
		if !importer.ValidFilename(m.Filename) {
			return fail("invalid manager file name %q", m.Filename)
		}
		base, ext := splitExt(m.Filename)
		isSource := slices.Contains(sourceExts, ext)
		if m.Filename == "testlib.h" || strings.HasPrefix(m.Filename, ".") || isSource && (base == checkerFile ||
			base == controllerFile || base == "stub" || base == managerFile && d.TaskType != tasktype.NameTwoSteps) {
			enc.Warnings = append(enc.Warnings, fmt.Sprintf("manager %s will not be imported back from the package", m.Filename))
		}
		files = append(files, PackageFile{Path: GradersDir + "/" + m.Filename, File: m.File,
			Executable: m.Filename == checkerFile || m.Filename == managerFile || m.Filename == controllerFile})
	}
	enc.Files = files

	var b bytes.Buffer
	fmt.Fprintf(&b, "# Task %s, dataset %q.\n# Format: docs/en/package-format.md\n", t.Name, d.Description)
	ye := yaml.NewEncoder(&b)
	ye.SetIndent(2)
	if err := ye.Encode(p); err != nil {
		return nil, err
	}
	if err := ye.Close(); err != nil {
		return nil, err
	}
	enc.Problem = b.Bytes()
	return enc, nil
}

// secondsOf converts a duration to seconds.
func secondsOf(d *time.Duration) *float64 {
	if d == nil {
		return nil
	}
	return importer.Ptr(d.Seconds())
}

// encodeTokens returns the token rules that differ from the defaults.
func encodeTokens(tk importer.Tokens) *tokens {
	def := importer.NewTask("", "").Tokens
	out := &tokens{}
	if tk.Mode != def.Mode {
		out.Mode = tk.Mode
	}
	out.MaxNumber = tk.MaxNumber
	if tk.MinInterval != def.MinInterval {
		out.MinInterval = importer.Ptr(tk.MinInterval.Seconds())
	}
	if tk.GenInitial != def.GenInitial {
		out.GenInitial = &tk.GenInitial
	}
	if tk.GenNumber != def.GenNumber {
		out.GenNumber = &tk.GenNumber
	}
	if tk.GenInterval != def.GenInterval {
		out.GenInterval = importer.Ptr(tk.GenInterval.Seconds())
	}
	out.GenMax = tk.GenMax
	if *out == (tokens{}) {
		return nil
	}
	return out
}

// jsonNumber decodes a JSON number keeping integers as integers.
func jsonNumber(raw json.RawMessage) (*number, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("%s is not a number", raw)
	}
	if i, err := n.Int64(); err == nil && !strings.ContainsAny(string(n), ".eE") {
		return intNumber(i), nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil, err
	}
	return floatNumber(f), nil
}

// encodeScoring returns the scoring of a dataset (nil for the default:
// Sum, 100 points shared among the tests), checked with the score
// precision of its task.
func encodeScoring(d *importer.Dataset, tests []string, precision int32) (*scoring, error) {
	public := map[string]bool{}
	for _, c := range tests {
		public[c] = false
	}
	if _, err := scoretype.New(d.ScoreType, d.ScoreTypeParameters, public, int(max(precision, 0))); err != nil {
		return nil, fmt.Errorf("dataset %q: %v", d.Description, err)
	}
	if d.ScoreType == scoretype.NameSum {
		n, err := jsonNumber(d.ScoreTypeParameters)
		if err != nil {
			return nil, err
		}
		// The default is a float (as CMS computes it): 25 is not 25.0.
		if !n.isInt && (len(tests) > 0 && n.f == 100/float64(len(tests)) || len(tests) == 0 && n.f == 0) {
			return nil, nil
		}
		return &scoring{PerTest: n}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(d.ScoreTypeParameters, &items); err != nil {
		return nil, err
	}
	s := &scoring{Type: d.ScoreType}
	total := 0.0
	for i, item := range items {
		var maxScore, testcases, threshold json.RawMessage
		var always *bool
		switch item = bytes.TrimSpace(item); item[0] {
		case '[':
			var a []json.RawMessage
			if err := json.Unmarshal(item, &a); err != nil {
				return nil, err
			}
			maxScore, testcases = a[0], a[1]
			if len(a) > 2 {
				threshold = a[2]
			}
		default:
			var o struct {
				MaxScore   json.RawMessage `json:"max_score"`
				Testcases  json.RawMessage `json:"testcases"`
				Threshold  json.RawMessage `json:"threshold"`
				AlwaysShow *bool           `json:"always_show_testcases"`
			}
			if err := json.Unmarshal(item, &o); err != nil {
				return nil, err
			}
			// Without always_show_testcases the object is the same as
			// the list form, which the loader writes back.
			maxScore, testcases, threshold, always = o.MaxScore, o.Testcases, o.Threshold, o.AlwaysShow
		}
		st := subtask{AlwaysShowTests: always}
		var err error
		if st.Points, err = jsonNumber(maxScore); err != nil {
			return nil, fmt.Errorf("subtask %d: %v", i, err)
		}
		total += st.Points.f
		if d.ScoreType == scoretype.NameGroupThreshold && threshold != nil {
			if st.Threshold, err = jsonNumber(threshold); err != nil {
				return nil, fmt.Errorf("subtask %d: %v", i, err)
			}
		}
		testcases = bytes.TrimSpace(testcases)
		switch testcases[0] {
		case '"':
			var re string
			_ = json.Unmarshal(testcases, &re)
			st.Regex = &re
		case '[':
			var names []string
			if err := json.Unmarshal(testcases, &names); err != nil {
				return nil, fmt.Errorf("subtask %d: %v", i, err)
			}
			var chosen []string
			for _, c := range tests {
				if slices.Contains(names, c) {
					chosen = append(chosen, c)
				}
			}
			if len(chosen) != len(slices.Compact(slices.Sorted(slices.Values(names)))) {
				return nil, fmt.Errorf("subtask %d names testcases that do not exist", i)
			}
			st.Tests = selectionOf(chosen, tests)
			if st.Tests == nil {
				st.Tests = &selection{}
			}
		default:
			var n int
			if err := json.Unmarshal(testcases, &n); err != nil {
				return nil, fmt.Errorf("subtask %d: %v", i, err)
			}
			st.Count = &n
		}
		s.Subtasks = append(s.Subtasks, st)
	}
	if math.Abs(total-100) > 1e-9 {
		s.Points = floatNumber(total)
		if total == math.Trunc(total) && math.Abs(total) < 1<<53 {
			s.Points = intNumber(int64(total))
		}
	}
	return s, nil
}

// statementExt guesses the format of a statement: ".pdf" for a PDF,
// ".md" for anything else (Markdown).
func statementExt(ctx context.Context, f *importer.File, open Opener) (string, error) {
	rc, err := open(ctx, f)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	head, err := readHead(rc, 5)
	if err != nil {
		return "", err
	}
	if bytes.Equal(head, []byte("%PDF-")) {
		return ".pdf", nil
	}
	return ".md", nil
}

// WriteZip writes an encoded package as a zip.
func WriteZip(ctx context.Context, enc *Encoded, w io.Writer, open Opener) error {
	zw := zip.NewWriter(w)
	now := time.Now()
	add := func(name string, exec bool, r io.Reader) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}
		h.SetMode(0o644)
		if exec {
			h.SetMode(0o755)
		}
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, r)
		return err
	}
	if err := add(ProblemFile, false, bytes.NewReader(enc.Problem)); err != nil {
		return err
	}
	for _, f := range enc.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyFile(ctx, f, open, func(r io.Reader) error { return add(f.Path, f.Executable, r) }); err != nil {
			return err
		}
	}
	return zw.Close()
}

// copyFile opens a file of the package and passes its content to write.
func copyFile(ctx context.Context, f PackageFile, open Opener, write func(io.Reader) error) error {
	rc, err := open(ctx, f.File)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	defer rc.Close()
	if err := write(rc); err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	return nil
}

// WriteDir writes an encoded package to dir, which must not exist (or be
// empty). The package is written next to it and renamed at the end, so
// dir never holds half a package.
func WriteDir(ctx context.Context, enc *Encoded, dir string, open Opener) (err error) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".tmp-")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	write := func(name string, exec bool, r io.Reader) error {
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if exec {
			mode = 0o755
		}
		out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, r); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	if err := write(ProblemFile, false, bytes.NewReader(enc.Problem)); err != nil {
		return err
	}
	for _, f := range enc.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !safeZipName(f.Path) || path.Clean(f.Path) != f.Path {
			return fmt.Errorf("unsafe path %q", f.Path)
		}
		if err := copyFile(ctx, f, open, func(r io.Reader) error { return write(f.Path, f.Executable, r) }); err != nil {
			return err
		}
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, dir)
}

// Exported says what Export wrote.
type Exported struct {
	Task, Dataset string
	Files         int
	// OtherDatasets are the datasets of the task that were not exported.
	OtherDatasets []string
	Warnings      []string
}

// Export writes the task with that id, and its dataset with that
// description ("" for the active one), as a package at dest: a zip file
// if dest ends with ".zip", else a directory. dest must not exist (a
// directory may exist if empty).
func Export(ctx context.Context, db q.DBTX, store blob.Store, langs *language.Registry, taskID int32, description, dest string) (*Exported, error) {
	t, d, others, err := ReadTask(ctx, db, taskID, description)
	if err != nil {
		return nil, err
	}
	open := BlobOpener(store)
	enc, err := Encode(ctx, t, d, langs, open)
	if err != nil {
		return nil, err
	}
	res := &Exported{Task: enc.Task, Dataset: enc.Dataset, Files: len(enc.Files) + 1, OtherDatasets: others, Warnings: enc.Warnings}
	if !strings.EqualFold(filepath.Ext(dest), ".zip") {
		return res, WriteDir(ctx, enc, dest, open)
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	if err := WriteZip(ctx, enc, f, open); err != nil {
		f.Close()
		os.Remove(dest)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(dest)
		return nil, err
	}
	return res, nil
}

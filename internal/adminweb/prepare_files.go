// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Files one by one (R6.2; new): files dropped on the page are recognised
// by their names, as the layout of a package (docs: package format), and
// stored where they belong; the page lists what each file was taken for.
//
//	statement.pdf, statement.md, statement-<lang>.pdf, statement/<lang>.pdf  statement
//	attachments/<file>                                     attachment
//	<test>.in, input.<test>, input_<test>.txt (tests/...)  testcase input
//	<test>.out, <test>.ans, output.<test>, output_<test>.txt  testcase output
//	checker, manager, controller                           manager as it is
//	checker.cpp, manager.cpp, controller.cpp               compiled with g++ as at import
//	grader.<ext>, stub.<ext>, *.h, *.hpp                   manager
//	solutions/<name>.<verdict>.<ext>, <name>.<verdict>.<ext>  test submission

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/pkg"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// maxPreparedFile bounds one file of the files page.
const maxPreparedFile = 1 << 30

// Kinds of recognised files.
const (
	fileStatement  = "statement"
	fileAttachment = "attachment"
	fileInput      = "input"
	fileOutput     = "output"
	fileManager    = "manager"
	fileCompile    = "compile"
	fileSolution   = "solution"
	fileUnknown    = ""
)

// recognized is what a file was taken for.
type recognized struct {
	File string
	Kind string
	// Target: the language of a statement, the codename of a testcase,
	// the name of a manager or attachment.
	Target string
	// Description is shown in the list.
	Description string
	Error       string
}

var cppSources = []string{".cpp", ".cc", ".cxx", ".c++"}

var headerExts = []string{".h", ".hh", ".hpp", ".hxx"}

// recognize tells what a file of a task with that task type is, by its
// name.
func (s *Server) recognize(name, taskType, defaultLang string) recognized {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	dir, base := path.Split(name)
	dir = strings.TrimSuffix(dir, "/")
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		dir = dir[i+1:]
	}
	ext := strings.ToLower(path.Ext(base))
	stem := strings.TrimSuffix(base, path.Ext(base))
	r := recognized{File: name}
	lang := func(s string) string {
		if code, ok := importer.LanguageCodes[strings.ToLower(s)]; ok {
			return code
		}
		return s
	}
	codename := func(c string) (string, bool) { return c, importer.ValidCodename(c) }
	switch {
	case dir == "attachments":
		r.Kind, r.Target = fileAttachment, base
	case dir == "solutions" || expectedVerdict(base) != "":
		r.Kind, r.Target = fileSolution, base
	case (ext == ".pdf" || ext == ".md") && (dir == "statement" || dir == "statements"):
		r.Kind, r.Target = fileStatement, lang(stem)
	case (ext == ".pdf" || ext == ".md") && strings.HasPrefix(strings.ToLower(stem), "statement"):
		l := strings.TrimLeft(stem[len("statement"):], "-_.")
		if l == "" {
			l = defaultLang
		}
		r.Kind, r.Target = fileStatement, lang(l)
	case ext == ".in":
		r.Kind, r.Target = fileInput, stem
	case ext == ".out" || ext == ".ans":
		r.Kind, r.Target = fileOutput, stem
	case strings.HasPrefix(base, "input_") && ext == ".txt":
		r.Kind, r.Target = fileInput, strings.TrimSuffix(strings.TrimPrefix(base, "input_"), ".txt")
	case strings.HasPrefix(base, "output_") && ext == ".txt" && taskType != tasktype.NameOutputOnly:
		r.Kind, r.Target = fileOutput, strings.TrimSuffix(strings.TrimPrefix(base, "output_"), ".txt")
	case strings.HasPrefix(base, "input."):
		r.Kind, r.Target = fileInput, strings.TrimPrefix(base, "input.")
	case strings.HasPrefix(base, "output."):
		r.Kind, r.Target = fileOutput, strings.TrimPrefix(base, "output.")
	case base == "checker" || base == "manager" || base == "controller":
		r.Kind, r.Target = fileManager, base
	case (stem == "checker" || stem == "controller" || stem == "manager" && taskType == tasktype.NameCommunication) &&
		slices.Contains(cppSources, ext):
		r.Kind, r.Target = fileCompile, stem
	case stem == "grader" || stem == "stub" || stem == "manager" || slices.Contains(headerExts, ext):
		r.Kind, r.Target = fileManager, base
	}
	switch r.Kind {
	case fileInput, fileOutput:
		if _, ok := codename(r.Target); !ok {
			r.Error = fmt.Sprintf("invalid testcase name %q", r.Target)
		}
	case fileStatement:
		if !importer.ValidCodename(r.Target) {
			r.Error = fmt.Sprintf("invalid language %q", r.Target)
		}
	case fileManager, fileAttachment:
		if !importer.ValidFilename(r.Target) {
			r.Error = fmt.Sprintf("invalid file name %q", r.Target)
		}
	}
	return r
}

// describe says in words what a recognised file is.
func (c *Ctx) describe(r recognized) string {
	switch r.Kind {
	case fileStatement:
		return fmt.Sprintf(c.T("statement in %s"), r.Target)
	case fileAttachment:
		return fmt.Sprintf(c.T("attachment %s"), r.Target)
	case fileInput:
		return fmt.Sprintf(c.T("input of testcase %s"), r.Target)
	case fileOutput:
		return fmt.Sprintf(c.T("output of testcase %s"), r.Target)
	case fileManager:
		return fmt.Sprintf(c.T("manager %s"), r.Target)
	case fileCompile:
		return fmt.Sprintf(c.T("manager %s, compiled from the source"), r.Target)
	case fileSolution:
		if v := expectedVerdict(r.Target); v != "" {
			return fmt.Sprintf(c.T("test submission expected to be %s"), v)
		}
		return c.T("test submission")
	}
	return c.T("not recognized: ignored")
}

// filesData is the data of task_files.html.
type filesData struct {
	*Page
	Task     *q.Task
	Datasets []q.Dataset
	Dataset  *q.Dataset
	Results  []recognized
}

// taskFilesPage is GET /task/<id>/files.
func (s *Server) taskFilesPage(c *Ctx) {
	s.renderFiles(c, nil)
}

func (s *Server) renderFiles(c *Ctx, results []recognized) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	datasets, err := s.st.Q.AdminTaskDatasets(c.Context(), t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	d := filesData{Task: t, Datasets: datasets, Dataset: pickDataset(c, datasets, t), Results: results}
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("task_files.html", d)
}

// taskFilesPost is POST /task/<id>/files: the files are recognised,
// stored and listed; testcases, managers, statements and attachments are
// written in one transaction (a file with the name of an existing one
// replaces it), solutions become test submissions.
func (s *Server) taskFilesPost(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	ctx := c.Context()
	datasets, err := s.st.Q.AdminTaskDatasets(ctx, t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	ds := pickDataset(c, datasets, t)
	if ds == nil {
		c.NotFound()
		return
	}
	defaultLang := c.ArgDefault("language", "en")
	fhs := c.Files("files")
	if len(fhs) == 0 {
		c.Notify(N("Invalid data"), N("Please choose the files."), nil)
		c.Redirect(c.URL("task", t.ID, "files"))
		return
	}
	var results []recognized
	digests := map[int]string{}
	contents := map[string][]byte{} // sources to compile and headers
	var sols []solution
	for i, fh := range fhs {
		r := s.recognize(fh.Filename, ds.TaskType, defaultLang)
		if r.Kind != fileUnknown && r.Error == "" {
			switch r.Kind {
			case fileCompile, fileSolution:
				data, ok, err := ReadUpload(fh, maxSolutionSize)
				switch {
				case err != nil:
					r.Error = err.Error()
				case !ok:
					r.Error = "too large"
				case r.Kind == fileSolution:
					sol, err := s.solutionFiles(t, r.Target, data)
					if err != nil {
						r.Error = err.Error()
					} else {
						sols = append(sols, sol)
					}
				default:
					contents[path.Base(fh.Filename)] = data
				}
			default:
				if fh.Size > maxPreparedFile {
					r.Error = "too large"
					break
				}
				d, _, err := c.PutUpload(fh)
				if err != nil {
					r.Error = err.Error()
				}
				digests[i] = d
				if r.Kind == fileManager && slices.Contains(headerExts, strings.ToLower(path.Ext(r.Target))) {
					if data, ok, _ := ReadUpload(fh, 16<<20); ok {
						contents[r.Target] = data
					}
				}
			}
		}
		r.Description = c.describe(r)
		results = append(results, r)
	}
	// Compile the programs sent as C++ sources (with the headers sent).
	comparator := false
	var params []json.RawMessage
	if json.Unmarshal(ds.TaskTypeParameters, &params) == nil {
		for _, p := range params {
			if v, ok := jsonString(p); ok && v == tasktype.OutputEvalComparator {
				comparator = true
			}
		}
	}
	for i, r := range results {
		if r.Kind != fileCompile || r.Error != "" {
			continue
		}
		bin, err := pkg.CompileFiles(path.Base(fhs[i].Filename), contents, comparator && r.Target == "checker")
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		d, err := s.blobs.PutBytes(ctx, bin)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		digests[i] = d
	}
	existing, err := s.st.Q.AdminDatasetTestcases(ctx, ds.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	type io2 struct{ in, out string }
	tests := map[string]*io2{}
	var order []string
	for _, tc := range existing {
		tests[tc.Codename] = &io2{tc.Input, tc.Output}
	}
	changed := map[string]bool{}
	for i, r := range results {
		if r.Error != "" || (r.Kind != fileInput && r.Kind != fileOutput) {
			continue
		}
		tio := tests[r.Target]
		if tio == nil {
			tio = &io2{out: emptyDigest}
			tests[r.Target] = tio
		}
		if r.Kind == fileInput {
			tio.in = digests[i]
		} else {
			tio.out = digests[i]
		}
		if !changed[r.Target] {
			changed[r.Target] = true
			order = append(order, r.Target)
		}
	}
	for _, codename := range order {
		if tests[codename].in == "" {
			for i := range results {
				if results[i].Target == codename && results[i].Kind == fileOutput {
					results[i].Error = c.T("the testcase has no input: send its .in file too")
				}
			}
			delete(changed, codename)
		}
	}
	err = c.Write(func(tx *Tx) error {
		var done []string
		for i, r := range results {
			if r.Error != "" {
				continue
			}
			switch r.Kind {
			case fileStatement:
				if _, err := tx.Q.AdminUpsertStatement(tx.Ctx, q.AdminUpsertStatementParams{TaskID: t.ID, Language: r.Target, Digest: digests[i]}); err != nil {
					return err
				}
			case fileAttachment:
				if _, err := tx.Q.AdminUpsertAttachment(tx.Ctx, q.AdminUpsertAttachmentParams{TaskID: t.ID, Filename: r.Target, Digest: digests[i]}); err != nil {
					return err
				}
			case fileManager, fileCompile:
				if _, err := tx.Q.AdminUpsertManager(tx.Ctx, q.AdminUpsertManagerParams{DatasetID: ds.ID, Filename: r.Target, Digest: digests[i]}); err != nil {
					return err
				}
			default:
				continue
			}
			done = append(done, r.File+": "+r.Kind+" "+r.Target)
		}
		for _, codename := range order {
			if !changed[codename] {
				continue
			}
			tio := tests[codename]
			if _, err := tx.Q.AdminUpsertTestcase(tx.Ctx, q.AdminUpsertTestcaseParams{DatasetID: ds.ID, Codename: codename,
				Input: tio.in, Output: tio.out}); err != nil {
				return err
			}
			done = append(done, "testcase "+codename)
		}
		contestChanged(tx, t.ContestID)
		return tx.Audit(AuditEntry{Action: "task.files", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Details: map[string]any{"task": t.Name, "files": done}})
	})
	if !c.Commit(err) {
		s.renderFiles(c, results)
		return
	}
	if len(sols) > 0 {
		_, err := s.createTestSubmissions(c, t, sols)
		c.Commit(err)
	}
	s.notifyTask(ctx, t.ContestID, t.ID, ds.ID)
	s.renderFiles(c, results)
}

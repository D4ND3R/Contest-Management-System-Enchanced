// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The helpers are ported from cms/grading/tasktypes/util.py, Batch.py
// (_do_compile) and cms/service/Worker.py (execute_job_group) of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package tasktype

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// Execute runs a job with its task type (upstream Worker.execute_job_group
// and TaskType.execute_job): it compiles or evaluates according to the
// operation and fills res, which must come from j.NewResult. res is
// always left valid (res.Validate): when the job fails, the returned
// error is recorded with res.Fail (and res.Tombstone for ErrTombstone).
// The error is for logs and metrics; a nil error means a result to store.
func Execute(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	start := time.Now()
	err := execute(ctx, env, j, res)
	total := time.Since(start)
	res.Durations.Total = total
	res.Durations.Run = max(0, total-res.Durations.Fetch-res.Durations.Store)
	if err == nil {
		finish(res)
		if err = res.Validate(); err != nil {
			err = fmt.Errorf("tasktype: invalid result: %w", err)
		}
	}
	if err != nil {
		res.Fail(err)
		res.Text, res.AdminText, res.Stats = nil, "", nil
		res.Tombstone = errors.Is(err, ErrTombstone)
	}
	return err
}

func execute(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tt, err := Get(j.TaskType, j.TaskTypeParameters)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConfiguration, err)
	}
	switch k := j.Operation.Type; {
	case k.IsCompilation():
		return tt.Compile(ctx, env, j, res)
	case k.IsEvaluation():
		return tt.Evaluate(ctx, env, j, res)
	default:
		return fmt.Errorf("tasktype: unknown operation %q", k)
	}
}

// finish marks a result as successful and keeps its texts within the
// limits of the judge protocol (valid UTF-8, no NUL, bounded length).
func finish(res *job.Result) {
	res.Success = true
	res.Error = ""
	n := 0
	for i, t := range res.Text {
		t = cleanText(t)
		if n+len(t) > job.MaxTextLen {
			t = job.TruncateUTF8(t, job.MaxTextLen-n)
		}
		n += len(t)
		res.Text[i] = t
	}
	res.AdminText = job.TruncateUTF8(cleanText(res.AdminText), job.MaxTextLen)
}

// cleanText replaces invalid UTF-8 and removes NUL characters.
func cleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// Names of files and managers used by several task types (upstream
// class constants).
const (
	// checkerCodename is the manager that checks outputs.
	checkerCodename = "checker"
	// graderBasename is the basename of the grader (grader.%l), also the
	// entry point (Java class) of programs compiled with it.
	graderBasename = "grader"
	// userOutputFilename is the output of the contestant in the box of a
	// checker (upstream EVAL_USER_OUTPUT_FILENAME).
	userOutputFilename = "user_output.txt"
	// maxUserOutput bounds the output of a user test that is stored
	// (upstream trunc_len=100 * 1024).
	maxUserOutput = 100 * 1024
)

// Texts of the task types that are not messages of the steps (upstream
// N_ marks: msgids of the "cms" catalogs).
const (
	textNoCompilation    = "No compilation needed"
	textFileNotSubmitted = "File not submitted"
)

// run carries a job through a task type.
type run struct {
	ctx context.Context
	env *Env
	j   *job.Job
	res *job.Result

	// mu guards closed and what closeBox writes in res: Interactive
	// closes the boxes of its solutions in other goroutines.
	mu     sync.Mutex
	closed map[Box]bool
}

func newRun(ctx context.Context, env *Env, j *job.Job, res *job.Result) *run {
	return &run{ctx: ctx, env: env, j: j, res: res}
}

func configError(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrConfiguration}, args...)...)
}

// language returns the language of the job.
func (r *run) language() (*language.Language, error) {
	if r.j.Language == "" {
		return nil, configError("the job has no language")
	}
	l, err := r.env.Languages.Get(r.j.Language)
	if err != nil {
		return nil, configError("%v", err)
	}
	return l, nil
}

// checkFiles checks the number of files of the submission (upstream
// check_files_number).
func (r *run) checkFiles(n int, orMore bool) error {
	if orMore && len(r.j.Files) < n {
		return configError("submission contains %d files, at least %d are required; "+
			"ensure the submission format is correct.", len(r.j.Files), n)
	}
	if !orMore && len(r.j.Files) != n {
		return configError("submission contains %d files, exactly %d are required; "+
			"ensure the submission format is correct.", len(r.j.Files), n)
	}
	return nil
}

// executable checks that the job has exactly one executable (upstream
// check_executables_number) and returns it.
func (r *run) executable() (name, digest string, err error) {
	if len(r.j.Executables) != 1 {
		return "", "", configError("submission contains %d executables, exactly 1 are expected; "+
			"consider invalidating compilations.", len(r.j.Executables))
	}
	for name, digest = range r.j.Executables {
	}
	return name, digest, nil
}

// manager returns the digest of a manager of the dataset (upstream
// check_manager_present).
func (r *run) manager(name string) (string, error) {
	d, ok := r.j.Managers[name]
	if !ok {
		return "", configError("dataset is missing manager '%s'.", name)
	}
	return d, nil
}

// newBox creates a box and records it in the result.
func (r *run) newBox(name string) (Box, error) {
	b, err := r.env.Boxes.NewBox(r.ctx, name)
	if err != nil {
		return nil, fmt.Errorf("creating the sandbox %s: %w", name, err)
	}
	r.res.Sandboxes = append(r.res.Sandboxes, b.Dir())
	return b, nil
}

// open returns the content of a stored file; what names it in errors.
func (r *run) open(digest, what string) (io.ReadCloser, error) {
	if digest == job.TombstoneDigest {
		return nil, fmt.Errorf("%w: %s", ErrTombstone, what)
	}
	rc, err := r.env.Store.Open(r.ctx, digest)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", what, err)
	}
	return rc, nil
}

// put copies a stored file into a box (upstream create_file_from_storage).
func (r *run) put(box Box, name, digest string, executable bool) error {
	start := time.Now()
	defer func() { r.res.Durations.Fetch += time.Since(start) }()
	rc, err := r.open(digest, name)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := box.CreateFileFrom(name, rc, executable); err != nil {
		return fmt.Errorf("copying %s into the sandbox: %w", name, err)
	}
	return nil
}

// putAll copies stored files (name -> digest) into a box, in name order.
func (r *run) putAll(box Box, files map[string]string, executable bool) error {
	for _, name := range sortedKeys(files) {
		if err := r.put(box, name, files[name], executable); err != nil {
			return err
		}
	}
	return nil
}

// store puts a file of a box in the store and returns its digest
// (upstream get_file_to_storage); max > 0 keeps only its first max bytes.
func (r *run) store(box Box, name string, max int64) (string, error) {
	start := time.Now()
	defer func() { r.res.Durations.Store += time.Since(start) }()
	f, err := box.Open(name)
	if err != nil {
		return "", fmt.Errorf("reading %s from the sandbox: %w", name, err)
	}
	defer f.Close()
	var rd io.Reader = f
	if max > 0 {
		rd = io.LimitReader(f, max)
	}
	d, _, err := r.env.Store.Put(r.ctx, rd)
	if err != nil {
		return "", fmt.Errorf("storing %s: %w", name, err)
	}
	return d, nil
}

// storeUserOutput stores the output of a user test when the job asks for
// it and the file exists (upstream job.user_output).
func (r *run) storeUserOutput(box Box, name string) error {
	if !r.j.GetOutput || !isRegular(box, name) {
		return nil
	}
	d, err := r.store(box, name, maxUserOutput)
	if err != nil {
		return err
	}
	r.res.UserOutput = d
	return nil
}

func isRegular(box Box, name string) bool {
	st, err := box.Stat(name)
	return err == nil && st.Mode().IsRegular()
}

// setOutcome records the outcome of an evaluation.
func (r *run) setOutcome(outcome float64, text []string, adminText string) {
	r.res.Outcome = job.FormatOutcome(outcome)
	r.res.Text = text
	r.res.AdminText = adminText
}

// noCompilation is the result of a compilation with nothing to compile.
func (r *run) noCompilation() {
	r.res.CompilationOutcome = job.CompilationOK
	r.res.Text = []string{textNoCompilation}
	r.res.Stats = nil
}

// sourceFilename is the file name of a codename in a language
// ("sum.%l" -> "sum.cpp").
func sourceFilename(codename string, lang *language.Language) string {
	return strings.ReplaceAll(codename, ".%l", lang.SourceExtension())
}

// executableName is the deterministic name of the executable of a
// submission (upstream _executable_filename): the codenames without
// ".%l", sorted and joined with "_", plus the extension of the language.
// With onlySources, codenames that do not end with ".%l" are left out
// (Batch).
func executableName(codenames []string, lang *language.Language, onlySources bool) string {
	var parts []string
	for _, c := range codenames {
		if onlySources && !strings.HasSuffix(c, ".%l") {
			continue
		}
		parts = append(parts, strings.ReplaceAll(c, ".%l", ""))
	}
	sort.Strings(parts)
	return strings.Join(parts, "_") + lang.ExecutableExtension
}

// compileSubmission compiles the files of the job, after the grader when
// there is one (upstream Batch._do_compile, and the compile of
// Communication and Interactive). With onlySources, only the codenames
// ending with ".%l" are compiled and name the executable. Files are
// compiled in codename order (upstream: the order of the submission's
// files).
func (r *run) compileSubmission(grader, onlySources bool) error {
	lang, err := r.language()
	if err != nil {
		return err
	}
	var sources []string
	files := map[string]string{}
	if grader {
		name := graderBasename + lang.SourceExtension()
		d, err := r.manager(name)
		if err != nil {
			return err
		}
		sources = append(sources, name)
		files[name] = d
	}
	codenames := sortedKeys(r.j.Files)
	for _, c := range codenames {
		if onlySources && !strings.HasSuffix(c, ".%l") {
			continue
		}
		name := sourceFilename(c, lang)
		sources = append(sources, name)
		files[name] = r.j.Files[c]
	}
	r.addCompilationManagers(files, lang)
	exe := executableName(codenames, lang, onlySources)
	return r.compileFiles(lang, files, sources, exe)
}

// addCompilationManagers adds the managers that take part in compilations
// in the language (upstream is_manager_for_compilation): copied into the
// box, not passed to the compiler.
func (r *run) addCompilationManagers(files map[string]string, lang *language.Language) {
	for name, d := range r.j.Managers {
		if lang.IsCompilationFile(name) {
			if _, dup := files[name]; !dup {
				files[name] = d
			}
		}
	}
}

// compileFiles copies files into a new box, compiles sources into exe and
// records the outcome, the text, the statistics and the executable.
func (r *run) compileFiles(lang *language.Language, files map[string]string, sources []string, exe string) error {
	if strings.TrimSuffix(exe, lang.ExecutableExtension) == "" {
		return configError("no source file to compile; ensure the submission format is correct.")
	}
	cmds, err := lang.CompilationCommands(sources, exe, true)
	if err != nil {
		return configError("%v", err)
	}
	box, err := r.newBox("compile")
	if err != nil {
		return err
	}
	defer r.closeBox(box)
	if err := r.putAll(box, files, false); err != nil {
		return err
	}
	cr, err := r.env.Steps.Compile(r.ctx, box, lang, cmds)
	if err != nil {
		return err
	}
	r.res.Text = cr.Text
	st := cr.Stats
	r.res.Stats = &st
	if !cr.OK {
		r.res.CompilationOutcome = job.CompilationFail
		return nil
	}
	d, err := r.store(box, exe, 0)
	if err != nil {
		return err
	}
	r.res.CompilationOutcome = job.CompilationOK
	r.res.Executables = map[string]string{exe: d}
	return nil
}

// userOutput says where the output to check is: a file of a box, or a
// stored file (submitted outputs).
type userOutput struct {
	box    Box
	name   string
	digest string
	// filename is the file the contestant had to write ("" for the
	// standard output), for the message when it is missing.
	filename string
}

// checkOutput checks the contestant's output (upstream eval_output): a
// comparison in the worker, or the checker manager in a new box with the
// trusted limits; extraArgs are passed to a comparator.
func (r *run) checkOutput(chk checker.Checker, out userOutput, extraArgs []string) (*checker.Outcome, error) {
	if out.box != nil {
		// Links are potential attacks: treated as missing files.
		st, err := out.box.Stat(out.name)
		if errors.Is(err, fs.ErrNotExist) || err == nil && !st.Mode().IsRegular() {
			return &checker.Outcome{Text: []string{
				steps.EvaluationMessages.Get(steps.EvaluationNoOutput).Message, out.filename}}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("checking the output: %w", err)
		}
	}
	if !chk.Kind.IsProgram() {
		return r.compareOutput(chk, out)
	}

	checkerDigest, err := r.manager(checkerCodename)
	if err != nil {
		return nil, err
	}
	box, err := r.newBox("check")
	if err != nil {
		return nil, err
	}
	defer r.closeBox(box)
	if out.box != nil {
		f, err := out.box.Open(out.name)
		if err != nil {
			return nil, fmt.Errorf("reading the output: %w", err)
		}
		err = box.CreateFileFrom(userOutputFilename, f, false)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("copying the output into the sandbox: %w", err)
		}
	} else if err := r.put(box, userOutputFilename, out.digest, false); err != nil {
		return nil, err
	}
	var readers []io.ReadCloser
	defer func() {
		for _, rc := range readers {
			rc.Close()
		}
	}()
	get := func(digest, what string) (io.Reader, error) {
		rc, err := r.open(digest, what)
		if err != nil {
			return nil, err
		}
		readers = append(readers, rc)
		return rc, nil
	}
	program, err := get(checkerDigest, "the checker")
	if err != nil {
		return nil, err
	}
	input, err := get(r.j.Input, "the input")
	if err != nil {
		return nil, err
	}
	correct, err := get(r.j.Output, "the correct output")
	if err != nil {
		return nil, err
	}
	o, err := chk.Check(r.ctx, r.env.Steps, box, checker.Files{
		UserOutput:     userOutputFilename,
		OutputFilename: out.filename,
		CorrectOutput:  correct,
		Input:          input,
		Program:        program,
		ExtraArgs:      extraArgs,
	})
	if err != nil {
		// A sandbox failure, or a checker that broke its protocol.
		return nil, fmt.Errorf("checking the output: %w", err)
	}
	return o, nil
}

// compareOutput compares the output with the correct one in the worker
// (upstream white_diff_fobj_step).
func (r *run) compareOutput(chk checker.Checker, out userOutput) (*checker.Outcome, error) {
	var user io.ReadCloser
	if out.box != nil {
		f, err := out.box.Open(out.name)
		if err != nil {
			return nil, fmt.Errorf("reading the output: %w", err)
		}
		user = f
	} else {
		rc, err := r.open(out.digest, "the output")
		if err != nil {
			return nil, err
		}
		user = rc
	}
	defer user.Close()
	correct, err := r.open(r.j.Output, "the correct output")
	if err != nil {
		return nil, err
	}
	defer correct.Close()
	return chk.Compare(user, correct)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

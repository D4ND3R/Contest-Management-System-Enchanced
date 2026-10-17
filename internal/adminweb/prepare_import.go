// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Task packages in the browser (R6.2; new): a zip of the own format
// (internal/importer/pkg) or of CMS's formats (italy_yaml, Polygon, TPS,
// through the loader registry) is uploaded, validated and written with
// Writer.DryRun to show what would change, then written for real on
// confirmation, as a new task or over an existing one. The options of a
// task are also its problem.yaml: downloaded, edited in the browser or
// uploaded back, with the same preview. The upload is kept in the blob
// store between the preview and the confirmation (the confirmation form
// carries its digest).

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	_ "github.com/d4nd3r/contest-management-system-enchanced/internal/importer/italy"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/pkg"
	_ "github.com/d4nd3r/contest-management-system-enchanced/internal/importer/polygon"
	_ "github.com/d4nd3r/contest-management-system-enchanced/internal/importer/tps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Limits of an uploaded package extracted to disk (formats other than
// the own one, which is read in place).
const (
	maxPackageFiles = 100000
	maxPackageSize  = 8 << 30
)

// errUnsafeZip: a member of the zip would land outside the directory
// (zip slip), is a link, or the archive is too large.
var errUnsafeZip = errors.New("unsafe zip archive")

// safeMemberName reports whether a zip member name stays inside the
// directory it is extracted to.
func safeMemberName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") ||
		len(name) >= 2 && name[1] == ':' {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// extractZip extracts the zip at src into dir, refusing paths outside it,
// links, and archives over the limits (declared sizes are checked first
// and the readers are limited to them).
func extractZip(src, dir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > maxPackageFiles {
		return fmt.Errorf("%w: more than %d files", errUnsafeZip, maxPackageFiles)
	}
	var total uint64
	for _, f := range zr.File {
		if !safeMemberName(f.Name) {
			return fmt.Errorf("%w: %q", errUnsafeZip, f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeType&^os.ModeDir != 0 {
			return fmt.Errorf("%w: %q is not a regular file", errUnsafeZip, f.Name)
		}
		total += f.UncompressedSize64
		if total > maxPackageSize {
			return fmt.Errorf("%w: larger than %d bytes", errUnsafeZip, maxPackageSize)
		}
	}
	for _, f := range zr.File {
		dest := filepath.Join(dir, filepath.FromSlash(f.Name))
		if rel, err := filepath.Rel(dir, dest); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("%w: %q", errUnsafeZip, f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := extractMember(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func extractMember(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	mode := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, &limitedReader{r: rc, n: int64(f.UncompressedSize64)}); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// loadedPackage is a package read from an upload.
type loadedPackage struct {
	Task      *importer.Task
	Solutions []pkg.Solution
	Report    *importer.Report
	Loader    string
	dir       string
}

// Close removes the temporary files.
func (p *loadedPackage) Close() {
	if p.dir != "" {
		os.RemoveAll(p.dir)
	}
}

// issues returns the issues of the report without the temporary paths.
func (p *loadedPackage) issues() []importer.Issue {
	var out []importer.Issue
	for _, i := range p.Report.Issues() {
		i.Where = strings.TrimPrefix(strings.ReplaceAll(i.Where, p.dir+string(filepath.Separator), ""), p.dir)
		i.Message = strings.ReplaceAll(i.Message, p.dir+string(filepath.Separator), "")
		out = append(out, i)
	}
	return out
}

// loadPackage reads the zip with that digest with the loader called name
// ("" detects it).
func (s *Server) loadPackage(ctx context.Context, digest, name string) (*loadedPackage, error) {
	if !blob.ValidDigest(digest) {
		return nil, errors.New("invalid package digest")
	}
	tmp, err := os.MkdirTemp("", "cms-admin-package-")
	if err != nil {
		return nil, err
	}
	lp := &loadedPackage{Report: &importer.Report{}, dir: tmp}
	zipPath := filepath.Join(tmp, "package.zip")
	if err := s.blobToFile(ctx, digest, zipPath); err != nil {
		lp.Close()
		return nil, err
	}
	opts := &importer.Options{Languages: s.langs, Report: lp.Report}
	if name == "" || name == pkg.Name || name == "package" {
		if name != "" || pkg.Detect(zipPath) {
			lp.Loader = pkg.Name
			p, err := pkg.Load(zipPath, opts)
			if err != nil {
				lp.Close()
				return nil, err
			}
			lp.Task, lp.Solutions = p.Task, p.Solutions
			return lp, nil
		}
	}
	dir := filepath.Join(tmp, "x")
	if err := os.Mkdir(dir, 0o755); err != nil {
		lp.Close()
		return nil, err
	}
	if err := extractZip(zipPath, dir); err != nil {
		lp.Close()
		return nil, err
	}
	// A zip with a single directory holds the package in it.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 1 && entries[0].IsDir() {
		dir = filepath.Join(dir, entries[0].Name())
	}
	l, err := importer.Default.ChooseTask(name, dir)
	if err != nil {
		lp.Close()
		return nil, err
	}
	lp.Loader = l.Name()
	t, err := l.LoadTask(dir, opts)
	if err == nil && t == nil {
		err = lp.Report.Err()
	}
	if err != nil {
		lp.Close()
		return nil, err
	}
	lp.Task = t
	return lp, nil
}

// blobToFile writes a blob to a new file.
func (s *Server) blobToFile(ctx context.Context, digest, dest string) error {
	rc, err := s.blobs.Open(ctx, digest)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// importPageData is the data of import_task.html.
type importPageData struct {
	*Page
	// Task is the task updated (nil: a new task).
	Task     *q.Task
	Loaders  []string
	Contests []q.AdminCoreContestNamesRow
}

// importTaskPage is GET /tasks/import and GET /task/<id>/import.
func (s *Server) importTaskPage(c *Ctx) {
	d := importPageData{}
	if c.R.PathValue("task_id") != "" {
		t, ok := c.loadTask(true)
		if !ok {
			return
		}
		d.Task = t
	} else if c.Can(PermAll) {
		var err error
		if d.Contests, err = s.st.Q.AdminCoreContestNames(c.Context()); err != nil {
			c.Fail(err)
			return
		}
	}
	for _, l := range importer.Default.Loaders() {
		if importer.Supports(l, importer.KindTask) {
			d.Loaders = append(d.Loaders, l.Name())
		}
	}
	var err error
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("import_task.html", d)
}

// importPreviewData is the data of import_preview.html.
type importPreviewData struct {
	*Page
	Task       *q.Task
	Title      string
	Loader     string
	Error      string
	Errors     []importer.Issue
	Warnings   []importer.Issue
	Changes    []importer.Change
	CanConfirm bool
	ConfirmURL string
	BackURL    string
	Hidden     []hiddenField
	Solutions  []pkg.Solution
	// Done: the import was written (the confirmation page).
	Done bool
}

type hiddenField struct{ Name, Value string }

// importOptions are the choices of an import form.
type importOptions struct {
	digest, loader string
	update         bool
	contestID      int32
	solutions      bool
}

func (c *Ctx) importOptions() importOptions {
	o := importOptions{digest: c.ArgDefault("digest", ""), loader: c.ArgDefault("loader", "")}
	_, o.update = c.Arg("update")
	_, o.solutions = c.Arg("submit_solutions")
	if c.Can(PermAll) {
		o.contestID, _ = c.PathIDArg("contest_id")
	}
	return o
}

func (o importOptions) hidden() []hiddenField {
	h := []hiddenField{{"digest", o.digest}, {"loader", o.loader}}
	if o.update {
		h = append(h, hiddenField{"update", "1"})
	}
	if o.contestID != 0 {
		h = append(h, hiddenField{"contest_id", fmt.Sprint(o.contestID)})
	}
	return h
}

// importTaskPost is POST /tasks/import and POST /task/<id>/import: the
// upload and its preview (nothing is written).
func (s *Server) importTaskPost(c *Ctx) {
	var target *q.Task
	if c.R.PathValue("task_id") != "" {
		t, ok := c.loadTask(true)
		if !ok {
			return
		}
		target = t
	}
	back := c.URL("tasks", "import")
	if target != nil {
		back = c.URL("task", target.ID, "import")
	}
	fh := c.File("package")
	if fh == nil {
		c.Notify(N("Invalid data"), N("Please choose the zip file of the package."), nil)
		c.Redirect(back)
		return
	}
	digest, _, err := c.PutUpload(fh)
	if err != nil {
		c.Fail(err)
		return
	}
	o := c.importOptions()
	o.digest = digest
	s.runImport(c, target, o, true, back)
}

// importConfirmPost is POST /tasks/import/confirm and POST
// /task/<id>/import/confirm: the import written.
func (s *Server) importConfirmPost(c *Ctx) {
	var target *q.Task
	if c.R.PathValue("task_id") != "" {
		t, ok := c.loadTask(false)
		if !ok {
			return
		}
		target = t
	}
	back := c.URL("tasks", "import")
	if target != nil {
		back = c.URL("task", target.ID, "import")
	}
	s.runImport(c, target, c.importOptions(), false, back)
}

// runImport loads the package and writes it (dryRun: only the preview).
func (s *Server) runImport(c *Ctx, target *q.Task, o importOptions, dryRun bool, back string) {
	ctx := c.Context()
	d := importPreviewData{Task: target, Title: c.T("Import a task package"), BackURL: back, Hidden: o.hidden()}
	d.ConfirmURL = c.URL("tasks", "import", "confirm")
	if target != nil {
		d.ConfirmURL = c.URL("task", target.ID, "import", "confirm")
		d.Title = c.T("Update the task from a package")
	}
	lp, err := s.loadPackage(ctx, o.digest, o.loader)
	if err != nil {
		d.Error = err.Error()
		s.renderPreview(c, &d)
		return
	}
	defer lp.Close()
	d.Loader, d.Solutions = lp.Loader, lp.Solutions
	if lp.Task == nil {
		d.Errors, d.Warnings = splitIssues(lp.issues())
		s.renderPreview(c, &d)
		return
	}
	opts := importer.TaskOptions{Update: o.update, ContestID: o.contestID, Report: lp.Report}
	if target != nil {
		// The package updates this task whatever its name says.
		lp.Task.Name = target.Name
		opts.Update, opts.ContestID = true, 0
	}
	w := importer.NewWriter(s.pool, s.blobs, s.langs)
	w.DryRun = dryRun
	res, err := w.WriteTask(ctx, lp.Task, opts)
	d.Errors, d.Warnings = splitIssues(lp.issues())
	if res != nil {
		d.Changes = res.Changes
	}
	if err != nil {
		d.Error = err.Error()
		s.renderPreview(c, &d)
		return
	}
	if dryRun {
		d.CanConfirm = c.Can(PermTasks)
		s.renderPreview(c, &d)
		return
	}
	taskID := res.TaskIDs[lp.Task.Name]
	var changes []string
	for _, ch := range res.Changes {
		changes = append(changes, ch.String())
	}
	err = c.Write(func(tx *Tx) error {
		return tx.Audit(AuditEntry{Action: "task.import", TargetType: "task", TargetID: int64(taskID), ContestID: res.ContestID,
			Details: map[string]any{"loader": lp.Loader, "package": o.digest, "changes": changes}})
	})
	if !c.Commit(err) {
		c.Redirect(back)
		return
	}
	if err := judging.Notify(ctx, s.kv, res.Events...); err != nil {
		s.log.Warn("notifying the dispatcher of an import", "err", err)
	}
	if o.solutions && taskID != 0 {
		s.submitPackageSolutions(c, taskID, lp.Solutions)
	}
	c.Redirect(c.URL("task", taskID))
}

// submitPackageSolutions submits the solutions of a package as test
// submissions of the task.
func (s *Server) submitPackageSolutions(c *Ctx, taskID int32, sols []pkg.Solution) {
	t, err := s.st.Q.GetTask(c.Context(), taskID)
	if err != nil {
		c.Fail(err)
		return
	}
	var list []solution
	for _, ps := range sols {
		if ps.File == nil {
			continue
		}
		rc, err := ps.File.Open()
		if err != nil {
			c.NotifyError(N("Operation failed."), err.Error())
			return
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxSolutionSize+1))
		rc.Close()
		if err != nil || len(data) > maxSolutionSize {
			c.NotifyError(N("Operation failed."), fmt.Sprintf("%s: unreadable or too large", ps.Name))
			continue
		}
		sol, err := s.solutionFiles(&t, path.Base(ps.Name), data)
		if err != nil {
			c.NotifyError(N("Operation failed."), fmt.Sprintf("%s: %v", ps.Name, err))
			continue
		}
		list = append(list, sol)
	}
	if len(list) > 0 {
		_, err := s.createTestSubmissions(c, &t, list)
		c.Commit(err)
	}
}

func splitIssues(issues []importer.Issue) (errs, warns []importer.Issue) {
	for _, i := range issues {
		if i.Severity == importer.SeverityError {
			errs = append(errs, i)
		} else {
			warns = append(warns, i)
		}
	}
	return errs, warns
}

func (s *Server) renderPreview(c *Ctx, d *importPreviewData) {
	var err error
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("import_preview.html", d)
}

// exportTaskPage is GET /task/<id>/export: the task (the dataset of
// dataset_id, else the live one) as a package zip.
func (s *Server) exportTaskPage(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	enc, err := s.encodeTask(c, t)
	if err != nil {
		c.NotifyError(N("Operation failed."), err.Error())
		c.Redirect(c.URL("task", t.ID))
		return
	}
	h := c.W.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="`+t.Name+`.zip"`)
	if err := pkg.WriteZip(c.Context(), enc, c.W, pkg.BlobOpener(s.blobs)); err != nil {
		s.log.Error("exporting a task", "task", t.ID, "err", err)
	}
}

// encodeTask encodes a task and one of its datasets (query argument
// dataset_id, else the live one) as a package.
func (s *Server) encodeTask(c *Ctx, t *q.Task) (*pkg.Encoded, error) {
	description := ""
	if id, ok := c.PathIDArg("dataset_id"); ok {
		ds, err := s.st.Q.GetDataset(c.Context(), id)
		if err != nil || ds.TaskID != t.ID {
			return nil, errors.New("no such dataset")
		}
		description = ds.Description
	}
	it, d, _, err := pkg.ReadTask(c.Context(), s.pool, t.ID, description)
	if err != nil {
		return nil, err
	}
	return pkg.Encode(c.Context(), it, d, s.langs, pkg.BlobOpener(s.blobs))
}

// problemYAMLData is the data of problem_yaml.html.
type problemYAMLData struct {
	*Page
	Task     *q.Task
	Datasets []q.Dataset
	Dataset  *q.Dataset
	Problem  string
	Warnings []string
	Error    string
}

// problemYAMLPage is GET /task/<id>/problem.yaml: the options of the task
// as problem.yaml, to edit and preview.
func (s *Server) problemYAMLPage(c *Ctx) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	datasets, err := s.st.Q.AdminTaskDatasets(c.Context(), t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	d := problemYAMLData{Task: t, Datasets: datasets, Dataset: pickDataset(c, datasets, t)}
	if enc, err := s.encodeTask(c, t); err != nil {
		d.Error = err.Error()
	} else {
		d.Problem, d.Warnings = string(enc.Problem), enc.Warnings
	}
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("problem_yaml.html", d)
}

// problemYAMLRaw is GET /task/<id>/problem.yaml/raw: the file.
func (s *Server) problemYAMLRaw(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	enc, err := s.encodeTask(c, t)
	if err != nil {
		c.NotifyError(N("Operation failed."), err.Error())
		c.Redirect(c.URL("task", t.ID, "problem.yaml"))
		return
	}
	h := c.W.Header()
	h.Set("Content-Type", "application/yaml; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="problem.yaml"`)
	_, _ = c.W.Write(enc.Problem)
}

// problemYAMLPost is POST /task/<id>/problem.yaml (preview) and POST
// /task/<id>/problem.yaml/confirm (write): the task exported as a package
// with the problem.yaml given (written or uploaded), imported over the
// task.
func (s *Server) problemYAMLPost(c *Ctx) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	confirm := strings.HasSuffix(c.R.URL.Path, "/confirm")
	ctx := c.Context()
	back := c.URL("task", t.ID, "problem.yaml")
	text := c.R.FormValue("problem")
	if fh := c.File("problem_file"); fh != nil {
		data, ok, err := ReadUpload(fh, 1<<20)
		if err != nil || !ok {
			c.NotifyError(N("Invalid field(s)"), "problem.yaml is unreadable or larger than 1 MiB")
			c.Redirect(back)
			return
		}
		text = string(data)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	d := importPreviewData{Task: t, Title: "problem.yaml", BackURL: back, Loader: pkg.Name,
		ConfirmURL: c.URL("task", t.ID, "problem.yaml", "confirm"), Hidden: []hiddenField{{"problem", text}}}
	if id, ok := c.PathIDArg("dataset_id"); ok {
		d.Hidden = append(d.Hidden, hiddenField{"dataset_id", fmt.Sprint(id)})
	}
	enc, err := s.encodeTask(c, t)
	if err != nil {
		d.Error = err.Error()
		s.renderPreview(c, &d)
		return
	}
	tmp, err := os.MkdirTemp("", "cms-admin-problem-")
	if err != nil {
		c.Fail(err)
		return
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "p")
	enc.Problem = []byte(text)
	if err := pkg.WriteDir(ctx, enc, dir, pkg.BlobOpener(s.blobs)); err != nil {
		c.Fail(err)
		return
	}
	rep := &importer.Report{}
	p, err := pkg.Load(dir, &importer.Options{Languages: s.langs, Report: rep})
	lp := &loadedPackage{Report: rep, dir: dir}
	if err != nil {
		d.Error = strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), "")
		s.renderPreview(c, &d)
		return
	}
	if p.Task == nil {
		d.Errors, d.Warnings = splitIssues(lp.issues())
		s.renderPreview(c, &d)
		return
	}
	if p.Task.Name != t.Name {
		rep.Warnf(pkg.ProblemFile, "the name is %q: rename the task in its page; the package updates %q", p.Task.Name, t.Name)
		p.Task.Name = t.Name
	}
	w := importer.NewWriter(s.pool, s.blobs, s.langs)
	w.DryRun = !confirm
	res, err := w.WriteTask(ctx, p.Task, importer.TaskOptions{Update: true, Report: rep})
	d.Errors, d.Warnings = splitIssues(lp.issues())
	if res != nil {
		d.Changes = res.Changes
	}
	if err != nil {
		d.Error = err.Error()
		s.renderPreview(c, &d)
		return
	}
	if !confirm {
		d.CanConfirm = c.Can(PermTasks)
		s.renderPreview(c, &d)
		return
	}
	var changes []string
	for _, ch := range res.Changes {
		changes = append(changes, ch.String())
	}
	err = c.Write(func(tx *Tx) error {
		return tx.Audit(AuditEntry{Action: "task.problem_yaml", TargetType: "task", TargetID: int64(t.ID), ContestID: contestOf(t),
			Details: map[string]any{"problem_yaml": text, "changes": changes}})
	})
	if c.Commit(err) {
		if err := judging.Notify(ctx, s.kv, res.Events...); err != nil {
			s.log.Warn("notifying the dispatcher of a problem.yaml change", "err", err)
		}
	}
	c.Redirect(c.URL("task", t.ID))
}

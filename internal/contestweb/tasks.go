// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/task.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015-2016 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package contestweb

// Task pages (CMS handlers/task.py): description, statements and
// attachments.

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// statementLink is a statement of the description page.
type statementLink struct {
	Language string
	// Name is the language's name in the interface language (CMS
	// format_locale); Bold: it differs from the code (CMS shows it bold).
	Name string
	Bold bool
	URL  string
	// Primary: among the task's primary statements; Preferred: among
	// the user's preferred languages.
	Primary, Preferred bool
}

// attachmentLink is an attachment of the description page.
type attachmentLink struct {
	Filename string
	URL      string
	// MimeType is "" when the name gives none; Icon is the mimetypes
	// icon (without ".png"); TypeName the translated name of the type.
	MimeType string
	Icon     string
	TypeName string
	// Size is nil when the store cannot tell.
	Size *int64
}

// compilationCommand is a row of the compilation commands.
type compilationCommand struct {
	Language string
	Commands []string
}

// taskDescriptionData is the data of task_description.html.
type taskDescriptionData struct {
	*Page
	Statements  []statementLink
	TaskType    string
	TimeLimit   *float64
	MemoryLimit *int64
	// Compilation is nil when the task type compiles nothing (CMS
	// get_compilation_commands returns None).
	Compilation []compilationCommand
	HasCommands bool
	TaskRules   TokenRules
	Attachments []attachmentLink
}

// taskDescriptionPage is GET tasks/{task}/description (CMS
// TaskDescriptionHandler).
func (s *Server) taskDescriptionPage(c *Ctx) {
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	page, err := c.Page("task_description")
	if err != nil {
		c.Fail(err)
		return
	}
	page.Task = t
	files, err := s.st.Q.CWSTaskFiles(c.Context(), t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	d := taskDescriptionData{Page: page, TaskType: t.TaskTypeName(), TimeLimit: t.TimeLimit,
		MemoryLimit: t.MemoryLimit, TaskRules: mustTokenRules(t)}
	locale := scoreLocale{c.Lang}.locale()
	preferred := c.Participation.User.PreferredLanguages
	for _, f := range files {
		switch f.Kind {
		case "statement":
			name := locale.FormatLocale(f.Name)
			d.Statements = append(d.Statements, statementLink{
				Language: f.Name, Name: name, Bold: name != f.Name,
				URL:       c.URL.Join("tasks", t.Name, "statements", f.Name, t.Name+"."+f.Name+".pdf"),
				Primary:   slices.Contains(t.PrimaryStatements, f.Name),
				Preferred: slices.Contains(preferred, f.Name),
			})
		case "attachment":
			a := attachmentLink{Filename: f.Name, URL: c.URL.Join("tasks", t.Name, "attachments", f.Name),
				MimeType: web.MimeType(f.Name)}
			if a.MimeType != "" {
				a.Icon = web.MimeIcon(a.MimeType)
				a.TypeName = web.MimeComment(a.MimeType, c.Lang.ID)
			}
			if n, err := s.blobSize(c.Context(), f.Digest); err == nil {
				a.Size = &n
			} else {
				s.log.Warn("size of an attachment", "task", t.Name, "file", f.Name, "err", err)
			}
			d.Attachments = append(d.Attachments, a)
		}
	}
	// Jinja's sort and dictsort ignore the case.
	sort.SliceStable(d.Statements, func(i, j int) bool {
		return strings.ToLower(d.Statements[i].Language) < strings.ToLower(d.Statements[j].Language)
	})
	sort.SliceStable(d.Attachments, func(i, j int) bool {
		return strings.ToLower(d.Attachments[i].Filename) < strings.ToLower(d.Attachments[j].Filename)
	})
	if tt, err := s.taskType(t); err == nil && tt != nil {
		if cmds := tt.CompilationCommands(s.langs, t.SubmissionFormat); cmds != nil {
			d.HasCommands = true
			allowed := t.AllowedLanguages(c.Contest)
			for lang, cc := range cmds {
				if !slices.Contains(allowed, lang) {
					continue
				}
				cmd := compilationCommand{Language: lang}
				for _, args := range cc {
					cmd.Commands = append(cmd.Commands, strings.Join(args, " "))
				}
				d.Compilation = append(d.Compilation, cmd)
			}
			sort.Slice(d.Compilation, func(i, j int) bool {
				a, b := strings.ToLower(d.Compilation[i].Language), strings.ToLower(d.Compilation[j].Language)
				if a != b {
					return a < b
				}
				return d.Compilation[i].Language < d.Compilation[j].Language
			})
		}
	} else if err != nil {
		c.Fail(err)
		return
	}
	c.Render(http.StatusOK, "task_description.html", d)
}

// blobSizes caches the sizes of blobs (CMS file_cacher.get_size): a
// digest's content never changes.
var blobSizes sync.Map

// blobSize returns the size of a blob of the store.
func (s *Server) blobSize(ctx context.Context, digest string) (int64, error) {
	if v, ok := blobSizes.Load(digest); ok {
		return v.(int64), nil
	}
	n, err := s.blobs.Stat(ctx, digest)
	if err != nil {
		return 0, err
	}
	blobSizes.Store(digest, n)
	return n, nil
}

// Statement formats, told from the content: CMS only has PDF statements;
// a statement that is not a PDF is served as LaTeX source to download or
// rendered as Markdown.
const (
	statementSniff    = 4096
	maxMarkdownLength = 4 << 20
)

// taskStatementData is the data of task_statement.html (a Markdown
// statement).
type taskStatementData struct {
	*Page
	Language string
	HTML     template.HTML
}

// taskStatementPage is GET tasks/{task}/statements/{lang}[/…] (CMS
// TaskStatementViewHandler): the statement in a language, shown in the
// browser. Anything after the language is ignored (the page links add
// the file name, for the title browsers show).
func (s *Server) taskStatementPage(c *Ctx) {
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	lang := c.R.PathValue("lang")
	if !validPathName(lang) {
		c.Error(http.StatusNotFound)
		return
	}
	st, err := s.st.Q.GetStatementByLanguage(c.Context(), q.GetStatementByLanguageParams{TaskID: t.ID, Language: lang})
	if errors.Is(err, pgx.ErrNoRows) {
		c.Error(http.StatusNotFound)
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	base := t.Name + "." + lang
	if lang == "" {
		base = t.Name
	}
	switch kind, content := s.statementKind(c.Context(), st.Digest); kind {
	case "tex":
		s.serveFile(c, st.Digest, base+".tex", "text/x-tex; charset=utf-8", "attachment")
	case "md":
		page, err := c.Page("task_description")
		if err != nil {
			c.Fail(err)
			return
		}
		page.Task = t
		c.Render(http.StatusOK, "task_statement.html", taskStatementData{Page: page, Language: lang,
			HTML: web.Markdown(string(content))})
	default:
		s.serveFile(c, st.Digest, base+".pdf", "application/pdf", "inline")
	}
}

// statementKind tells the format of a statement from its first bytes:
// "pdf" (also when the content cannot be read: the download reports the
// error), "tex" (LaTeX source) or "md" (other UTF-8 text, with its whole
// content).
func (s *Server) statementKind(ctx context.Context, digest string) (string, []byte) {
	if !blob.ValidDigest(digest) {
		return "pdf", nil
	}
	rc, err := s.blobs.Open(ctx, digest)
	if err != nil {
		return "pdf", nil
	}
	defer rc.Close()
	head := make([]byte, statementSniff)
	n, _ := io.ReadFull(rc, head)
	head = head[:n]
	if bytes.HasPrefix(head, []byte("%PDF")) || !utf8.Valid(trimPartialRune(head)) {
		return "pdf", nil
	}
	if bytes.Contains(head, []byte(`\documentclass`)) || bytes.Contains(head, []byte(`\begin{document}`)) {
		return "tex", nil
	}
	rest, err := io.ReadAll(io.LimitReader(rc, maxMarkdownLength))
	if err != nil || len(head)+len(rest) >= maxMarkdownLength || !utf8.Valid(append(head, rest...)) {
		return "pdf", nil
	}
	return "md", append(head, rest...)
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of a
// prefix of a text.
func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			return b
		}
	}
	return b
}

// taskAttachmentPage is GET tasks/{task}/attachments/{filename} (CMS
// TaskAttachmentViewHandler).
func (s *Server) taskAttachmentPage(c *Ctx) {
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	name := c.R.PathValue("filename")
	if !validPathName(name) {
		c.Error(http.StatusNotFound)
		return
	}
	a, err := s.st.Q.GetAttachmentByFilename(c.Context(), q.GetAttachmentByFilenameParams{TaskID: t.ID, Filename: name})
	if errors.Is(err, pgx.ErrNoRows) {
		c.Error(http.StatusNotFound)
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	s.serveFile(c, a.Digest, name, "", "")
}

// validPathName reports whether a name of the URL can name a row: valid
// UTF-8 without NUL (PostgreSQL rejects those; nothing has such a name).
func validPathName(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

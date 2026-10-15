// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template/parse"
)

// ErrNoTemplate is returned when a page does not exist.
var ErrNoTemplate = errors.New("web: no such template")

// EngineOptions configures NewEngine.
type EngineOptions struct {
	// Templates holds the *.html templates of the site (an embed.FS or
	// os.DirFS). Names are paths relative to its root ("overview.html",
	// "macro/submission.html").
	Templates fs.FS
	// Funcs adds the functions of the site. It is called once for every
	// template instance (see Engine) with a function that returns the
	// Render of the request being executed, so the functions can use the
	// request state without receiving it as an argument. It must return
	// the same names every time. Site functions override the standard
	// ones of the same name.
	Funcs func(cur func() *Render) template.FuncMap
	// Static resolves {{static}}; nil leaves static URLs without hash.
	Static *Static
	// Tests adds named tests for all, any and dictselect (Jinja tests).
	Tests map[string]Test
	// LibraryDirs are the directories whose files only define templates
	// (macros) for the others; default {"macro"}, as in CMS.
	LibraryDirs []string
	// MaxIdle caps the idle instances kept per page (default 64).
	MaxIdle int
}

// Engine renders the html/template pages of a site with the layout
// inheritance and the functions of CMS's Jinja2 environment.
//
// Inheritance: a template opening with the comment (after other comments
// such as the license header, if any)
//
//	{{/* extends "contest.html" */}}
//
// is rendered as its parent (recursively up to a template without that
// line) with every template it defines ({{define}} or {{block}})
// replacing the parent's one of the same name, as Jinja's {% extends %}
// and {% block %}; an empty definition empties the block, as in Jinja.
// Templates referenced with {{template "name"}} are looked up among the
// files of the library directories (their {{define}}s, the CMS macros)
// and among the file names (a file included whole, {% include %}).
//
// Whitespace: the trees are trimmed as Jinja's trim_blocks and
// lstrip_blocks options of CMS do: a line holding only a control action
// ({{if}}, {{else}}, {{end}}, {{range}}, {{with}}, {{define}},
// {{block}}, {{template}}, {{break}}, {{continue}}, comments and variable
// assignments) leaves no blank line or indentation in the output; and,
// as Jinja, the final newline of every file is dropped. A page ported
// line by line from CMS renders to the same bytes, except inside
// <script>, where html/template escapes strings differently ("\/" for
// "/", "\u002b" for "+": the same JavaScript values).
//
// Templates are parsed once by NewEngine. Executing an html/template
// escapes it the first time and its functions cannot change afterwards,
// so the engine keeps a pool of instances per page, each with the
// functions bound to the Render of the request it is executing; an
// instance is escaped once and reused, and requests never share one.
type Engine struct {
	pages   map[string]*page
	names   []string
	static  *Static
	tests   map[string]Test
	siteFns func(cur func() *Render) template.FuncMap
	maxIdle int
}

type page struct {
	name   string
	master *template.Template // never executed; instances are clones

	mu    sync.Mutex
	clone sync.Mutex
	free  []*instance
}

type instance struct {
	t  *template.Template
	rc *Render
}

// tfile is one parsed template file.
type tfile struct {
	name    string
	extends string
	library bool
	trees   map[string]*parse.Tree
}

// leadingComment matches a comment action at the start of a template.
var (
	leadingComment = regexp.MustCompile(`^\s*\{\{-?\s*/\*([\s\S]*?)\*/\s*-?\}\}`)
	extendsRe      = regexp.MustCompile(`^\s*extends\s+"([^"]+)"\s*$`)
)

// extendsOf returns the parent named by an {{/* extends "x" */}} comment
// among the comments that open the template (a license header may come
// first), or "".
func extendsOf(src []byte) string {
	for {
		m := leadingComment.FindSubmatchIndex(src)
		if m == nil {
			return ""
		}
		if e := extendsRe.FindSubmatch(src[m[2]:m[3]]); e != nil {
			return string(e[1])
		}
		src = src[m[1]:]
	}
}

// NewEngine parses every *.html file of o.Templates, checks the
// inheritance chains and the references between templates, and escapes
// one instance of every page (executing it once with nil data, so the
// functions must tolerate it), so template errors are found at startup.
func NewEngine(o EngineOptions) (*Engine, error) {
	if o.Templates == nil {
		return nil, errors.New("web: no templates")
	}
	e := &Engine{
		pages:   map[string]*page{},
		static:  o.Static,
		tests:   map[string]Test{},
		siteFns: o.Funcs,
		maxIdle: o.MaxIdle,
	}
	if e.maxIdle <= 0 {
		e.maxIdle = 64
	}
	for k, v := range builtinTests {
		e.tests[k] = v
	}
	for k, v := range o.Tests {
		e.tests[k] = v
	}
	libDirs := o.LibraryDirs
	if libDirs == nil {
		libDirs = []string{"macro"}
	}
	// The functions known at parse time (bodies are bound per instance).
	placeholder := e.funcs(func() *Render { return nil })
	check := map[string]any{}
	for k, v := range placeholder {
		check[k] = v
	}
	for _, b := range textBuiltins {
		check[b] = true
	}

	files := map[string]*tfile{}
	err := fs.WalkDir(o.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".html" {
			return nil
		}
		src, err := fs.ReadFile(o.Templates, p)
		if err != nil {
			return err
		}
		// Jinja strips a single trailing newline from every template
		// (keep_trailing_newline is off in CMS).
		if bytes.HasSuffix(src, []byte("\r\n")) {
			src = src[:len(src)-2]
		} else if bytes.HasSuffix(src, []byte("\n")) {
			src = src[:len(src)-1]
		}
		f := &tfile{name: p, trees: map[string]*parse.Tree{}}
		f.extends = extendsOf(src)
		for _, dir := range libDirs {
			if strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/") {
				f.library = true
			}
		}
		t := parse.New(p)
		t.Mode = parse.ParseComments
		if _, err := t.Parse(string(src), "{{", "}}", f.trees, check); err != nil {
			return err
		}
		for name, tree := range f.trees {
			trimBlocks(tree.Root, name != p)
		}
		files[p] = f
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("web: templates: %w", err)
	}
	// Index: file names, and the definitions of the library files.
	index := map[string]string{}
	for name := range files {
		index[name] = name
	}
	libNames := make([]string, 0, len(files))
	for name, f := range files {
		if f.library {
			libNames = append(libNames, name)
		}
	}
	sort.Strings(libNames)
	for _, name := range libNames {
		for def := range files[name].trees {
			if prev, ok := index[def]; ok && prev != name && files[prev].library && def != prev {
				return nil, fmt.Errorf("web: templates: %q is defined in %s and %s", def, prev, name)
			}
			if _, isFile := files[def]; !isFile {
				index[def] = name
			}
		}
	}
	for name, f := range files {
		if f.library {
			continue
		}
		p, err := e.build(name, files, index, placeholder)
		if err != nil {
			return nil, fmt.Errorf("web: templates: %s: %w", name, err)
		}
		e.pages[name] = p
		e.names = append(e.names, name)
	}
	sort.Strings(e.names)
	// Escape one instance of every page now: html/template reports
	// contexts it cannot handle (an action inside an unquoted attribute
	// name, a branch ending in another context...) at the first
	// execution only.
	for _, name := range e.names {
		p := e.pages[name]
		inst, err := e.newInstance(p)
		if err != nil {
			return nil, fmt.Errorf("web: templates: %s: %w", name, err)
		}
		inst.rc = zeroRender()
		err = inst.t.Execute(io.Discard, nil)
		inst.rc = nil
		var terr *template.Error
		if errors.As(err, &terr) {
			return nil, fmt.Errorf("web: templates: %w", err)
		}
		p.put(inst, e.maxIdle)
	}
	return e, nil
}

// textBuiltins are the functions of text/template, needed to check the
// trees parsed with text/template/parse directly.
var textBuiltins = []string{"and", "call", "html", "index", "slice", "js", "len", "not", "or",
	"print", "printf", "println", "urlquery", "eq", "ge", "gt", "le", "lt", "ne"}

// build assembles the master template of a page: its inheritance chain,
// plus the library files and included files its templates reference.
func (e *Engine) build(name string, files map[string]*tfile, index map[string]string, fns template.FuncMap) (*page, error) {
	var chain []*tfile // root first
	seen := map[string]bool{}
	for cur := name; ; {
		f := files[cur]
		if f == nil {
			return nil, fmt.Errorf("extends missing template %q", cur)
		}
		if seen[cur] {
			return nil, fmt.Errorf("inheritance loop through %q", cur)
		}
		seen[cur] = true
		chain = append([]*tfile{f}, chain...)
		if f.extends == "" {
			break
		}
		cur = f.extends
	}
	final := map[string]*parse.Tree{}
	for _, f := range chain {
		for n, t := range f.trees {
			final[n] = t
		}
	}
	// Pull referenced templates in until nothing is missing. Library and
	// included files come before the chain: the chain's definitions win.
	var extra []*tfile
	included := map[string]bool{}
	for _, f := range chain {
		included[f.name] = true
	}
	for {
		missing := ""
		for _, t := range final {
			for _, ref := range templateRefs(t.Root) {
				if _, ok := final[ref]; !ok {
					missing = ref
					break
				}
			}
			if missing != "" {
				break
			}
		}
		if missing == "" {
			break
		}
		fname, ok := index[missing]
		if !ok || included[fname] {
			return nil, fmt.Errorf("no template %q", missing)
		}
		included[fname] = true
		extra = append(extra, files[fname])
		final = map[string]*parse.Tree{}
		for _, f := range append(append([]*tfile{}, extra...), chain...) {
			for n, t := range f.trees {
				final[n] = t
			}
		}
	}
	entry := chain[0].name
	master := template.New(entry).Funcs(fns)
	names := make([]string, 0, len(final))
	for n := range final {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := master.AddParseTree(n, final[n]); err != nil {
			return nil, err
		}
	}
	return &page{name: name, master: master}, nil
}

// templateRefs returns the names of the templates invoked under n.
func templateRefs(n parse.Node) []string {
	var out []string
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.IfNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.TemplateNode:
			out = append(out, x.Name)
		}
	}
	walk(n)
	return out
}

func (e *Engine) newInstance(p *page) (*instance, error) {
	p.clone.Lock()
	t, err := p.master.Clone()
	p.clone.Unlock()
	if err != nil {
		return nil, err
	}
	inst := &instance{t: t}
	t.Funcs(e.funcs(func() *Render { return inst.rc }))
	return inst, nil
}

func (p *page) get() *instance {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := len(p.free); n > 0 {
		inst := p.free[n-1]
		p.free = p.free[:n-1]
		return inst
	}
	return nil
}

func (p *page) put(inst *instance, maxIdle int) {
	p.mu.Lock()
	if len(p.free) < maxIdle {
		p.free = append(p.free, inst)
	}
	p.mu.Unlock()
}

// Pages returns the names of the templates that can be rendered (every
// file outside the library directories), sorted.
func (e *Engine) Pages() []string { return e.names }

// Has reports whether name can be rendered.
func (e *Engine) Has(name string) bool { return e.pages[name] != nil }

// Execute renders the page name with rc as the request state of the
// functions and data as the dot.
func (e *Engine) Execute(w io.Writer, name string, rc *Render, data any) error {
	p := e.pages[name]
	if p == nil {
		return fmt.Errorf("%w: %q", ErrNoTemplate, name)
	}
	if rc == nil {
		rc = zeroRender()
	}
	inst := p.get()
	if inst == nil {
		var err error
		if inst, err = e.newInstance(p); err != nil {
			return err
		}
	}
	inst.rc = rc
	ok := false
	defer func() {
		inst.rc = nil
		if ok {
			p.put(inst, e.maxIdle)
		}
	}()
	err := inst.t.Execute(w, data)
	ok = true // a failed execution leaves the instance usable; a panic does not
	return err
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Render executes the page into a buffer and sends it with status: a
// template error sends nothing (the caller renders an error page), so a
// page never stops halfway. Like CMS (Tornado), the response carries
// Cache-Control: no-cache, must-revalidate unless the handler set
// another one, and a 200 answer to GET or HEAD carries an ETag (SHA-1 of
// the body) and becomes 304 Not Modified when it matches If-None-Match.
func (e *Engine) Render(w http.ResponseWriter, r *http.Request, status int, name string, rc *Render, data any) error {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= 1<<20 {
			bufPool.Put(buf)
		}
	}()
	if err := e.Execute(buf, name, rc, data); err != nil {
		return err
	}
	WriteBody(w, r, status, "text/html; charset=UTF-8", buf.Bytes())
	return nil
}

// WriteBody sends a complete response body as Engine.Render does
// (Cache-Control default, ETag and 304 for 200 answers to GET/HEAD,
// no body for HEAD).
func WriteBody(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-cache, must-revalidate")
	}
	if status == http.StatusOK && r != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) && h.Get("Etag") == "" {
		sum := sha1.Sum(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		h.Set("Etag", etag)
		if etagMatch(r.Header.Get("If-None-Match"), etag) {
			h.Del("Content-Type")
			h.Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r == nil || r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// etagMatch reports whether an If-None-Match header matches etag (weak
// comparison, "*" matches anything).
func etagMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// trimBlocks applies Jinja's trim_blocks and lstrip_blocks to a tree:
// the newline right after a control action is removed, and so are the
// spaces and tabs between the start of a line and a control action.
// inner is true for {{define}}/{{block}} bodies, which start and end
// next to a control action.
func trimBlocks(root *parse.ListNode, inner bool) {
	trimList(root, inner, inner, !inner)
}

// isControl reports whether n is a Jinja "block" tag rather than an
// output tag: these lines vanish from the output.
func isControl(n parse.Node) bool {
	switch x := n.(type) {
	case *parse.IfNode, *parse.RangeNode, *parse.WithNode, *parse.TemplateNode,
		*parse.CommentNode, *parse.BreakNode, *parse.ContinueNode:
		return true
	case *parse.ActionNode:
		return x.Pipe != nil && len(x.Pipe.Decl) > 0
	}
	return false
}

// trimList trims the text nodes of l. afterTag: l starts right after a
// control action (its first newline goes); beforeTag: l ends right before
// one; atLineStart: the start of l is the start of a line.
func trimList(l *parse.ListNode, afterTag, beforeTag, atLineStart bool) {
	if l == nil {
		return
	}
	nodes := l.Nodes
	// Decide on the original texts first: whether a text ends at the
	// start of a line depends on the newline that trim_blocks removes.
	type cut struct{ head, tail bool }
	cuts := make([]cut, len(nodes))
	for i, n := range nodes {
		t, ok := n.(*parse.TextNode)
		if !ok {
			continue
		}
		prevCtl := (i == 0 && afterTag) || (i > 0 && isControl(nodes[i-1]))
		nextCtl := (i == len(nodes)-1 && beforeTag) || (i < len(nodes)-1 && isControl(nodes[i+1]))
		cuts[i].head = prevCtl && (bytes.HasPrefix(t.Text, []byte("\n")) || bytes.HasPrefix(t.Text, []byte("\r\n")))
		if nextCtl {
			nl := bytes.LastIndexByte(t.Text, '\n')
			rest := t.Text[nl+1:]
			lineStart := nl >= 0 || (i == 0 && atLineStart)
			cuts[i].tail = lineStart && len(bytes.Trim(rest, " \t")) == 0 && len(rest) > 0
		}
	}
	for i, n := range nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			txt := x.Text
			if cuts[i].tail {
				nl := bytes.LastIndexByte(txt, '\n')
				txt = txt[:nl+1]
			}
			if cuts[i].head {
				if bytes.HasPrefix(txt, []byte("\r\n")) {
					txt = txt[2:]
				} else if bytes.HasPrefix(txt, []byte("\n")) {
					txt = txt[1:]
				}
			}
			x.Text = txt
		case *parse.IfNode:
			trimList(x.List, true, true, false)
			trimList(x.ElseList, true, true, false)
		case *parse.RangeNode:
			trimList(x.List, true, true, false)
			trimList(x.ElseList, true, true, false)
		case *parse.WithNode:
			trimList(x.List, true, true, false)
			trimList(x.ElseList, true, true, false)
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package extract collects the translatable strings of Go code and
// templates into a .pot catalog (run by `make generate`).
//
// Recognised forms:
//
//	Go:        T("msg"), Tn("one", "many", n), Tc("ctx", "msg"), and the
//	           same as methods (cat.T(...)); N("msg") and Nn("one", "many")
//	           mark strings translated later.
//	Templates: {{T "msg"}}, {{Tn "one" "many" .N}}, {{Tc "ctx" "msg"}},
//	           anywhere in a pipeline.
package extract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
)

// Message is one translatable string and where it appears.
type Message struct {
	Context, ID, Plural string
	Refs                []string
}

// kinds maps function names to the positions of (context, id, plural).
var kinds = map[string][3]int{
	"T":  {-1, 0, -1},
	"N":  {-1, 0, -1},
	"Tn": {-1, 0, 1},
	"Nn": {-1, 0, 1},
	"Tc": {0, 1, -1},
}

// Collector accumulates messages, merging duplicates.
type Collector struct {
	msgs  map[string]*Message
	order []string
}

// NewCollector returns an empty collector.
func NewCollector() *Collector { return &Collector{msgs: map[string]*Message{}} }

func (c *Collector) add(m Message, ref string) error {
	if m.ID == "" {
		return fmt.Errorf("%s: empty message", ref)
	}
	k := m.Context + "\x04" + m.ID
	if prev, ok := c.msgs[k]; ok {
		if prev.Plural != m.Plural {
			return fmt.Errorf("%s: %q used with different plural forms", ref, m.ID)
		}
		prev.Refs = append(prev.Refs, ref)
		return nil
	}
	m.Refs = []string{ref}
	c.msgs[k] = &m
	c.order = append(c.order, k)
	return nil
}

// Messages returns the collected messages sorted by msgid (stable output
// keeps the diffs of the .pot reviewable).
func (c *Collector) Messages() []Message {
	out := make([]Message, 0, len(c.msgs))
	for _, k := range c.order {
		out = append(out, *c.msgs[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Context < out[j].Context
	})
	return out
}

// Without returns msgs minus the messages (same context and msgid) that
// already are in the catalogs skip, typically cms.pot: the strings of a
// site that CMS already translates stay in the "cms" domain and only new
// ones land in admin.pot.
func Without(msgs []Message, skip ...*i18n.File) []Message {
	have := map[string]bool{}
	for _, f := range skip {
		for _, e := range f.Entries {
			have[e.Context+"\x04"+e.ID] = true
		}
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if !have[m.Context+"\x04"+m.ID] {
			out = append(out, m)
		}
	}
	return out
}

func build(name string, args []string) (Message, bool) {
	k, ok := kinds[name]
	if !ok {
		return Message{}, false
	}
	need := k[1]
	if k[0] > need {
		need = k[0]
	}
	if k[2] > need {
		need = k[2]
	}
	if len(args) <= need {
		return Message{}, false
	}
	m := Message{ID: args[k[1]]}
	if k[0] >= 0 {
		m.Context = args[k[0]]
	}
	if k[2] >= 0 {
		m.Plural = args[k[2]]
	}
	return m, true
}

// Go collects messages from Go source.
func (c *Collector) Go(filename string, src []byte) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return err
	}
	var ferr error
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || ferr != nil {
			return ferr == nil
		}
		var name string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		default:
			return true
		}
		if _, ok := kinds[name]; !ok {
			return true
		}
		var args []string
		for _, a := range call.Args {
			lit, ok := a.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				break
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				break
			}
			args = append(args, s)
		}
		if m, ok := build(name, args); ok {
			pos := fset.Position(call.Pos())
			ferr = c.add(m, fmt.Sprintf("%s:%d", filepath.ToSlash(filename), pos.Line))
		}
		return true
	})
	return ferr
}

// Template collects messages from an html/template or text/template file.
func (c *Collector) Template(filename string, src []byte) error {
	trees := map[string]*parse.Tree{}
	t := parse.New(filename)
	t.Mode = parse.ParseComments | parse.SkipFuncCheck
	if _, err := t.Parse(string(src), "{{", "}}", trees); err != nil {
		return err
	}
	var ferr error
	for _, tree := range trees {
		if tree.Root == nil {
			continue
		}
		walk(tree.Root, func(cmd *parse.CommandNode) {
			if ferr != nil || len(cmd.Args) == 0 {
				return
			}
			id, ok := cmd.Args[0].(*parse.IdentifierNode)
			if !ok {
				return
			}
			var args []string
			for _, a := range cmd.Args[1:] {
				s, ok := a.(*parse.StringNode)
				if !ok {
					break
				}
				args = append(args, s.Text)
			}
			if m, ok := build(id.Ident, args); ok {
				line := 1 + strings.Count(string(src[:int(cmd.Pos)]), "\n")
				ferr = c.add(m, fmt.Sprintf("%s:%d", filepath.ToSlash(filename), line))
			}
		})
	}
	return ferr
}

func walk(n parse.Node, fn func(*parse.CommandNode)) {
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return
		}
		for _, c := range x.Nodes {
			walk(c, fn)
		}
	case *parse.ActionNode:
		walk(x.Pipe, fn)
	case *parse.PipeNode:
		if x == nil {
			return
		}
		for _, c := range x.Cmds {
			walk(c, fn)
		}
	case *parse.CommandNode:
		fn(x)
		for _, a := range x.Args {
			walk(a, fn)
		}
	case *parse.IfNode:
		walk(x.Pipe, fn)
		walk(x.List, fn)
		walk(x.ElseList, fn)
	case *parse.RangeNode:
		walk(x.Pipe, fn)
		walk(x.List, fn)
		walk(x.ElseList, fn)
	case *parse.WithNode:
		walk(x.Pipe, fn)
		walk(x.List, fn)
		walk(x.ElseList, fn)
	case *parse.TemplateNode:
		walk(x.Pipe, fn)
	}
}

// TemplateExts are the template file extensions scanned by Dir.
var TemplateExts = []string{".html", ".tmpl", ".txt"}

// Dir scans a directory tree: Go files (tests excluded) and templates.
func (c *Collector) Dir(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		isTmpl := false
		for _, e := range TemplateExts {
			isTmpl = isTmpl || ext == e
		}
		if ext != ".go" && !isTmpl {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if ext == ".go" {
			return c.Go(p, src)
		}
		return c.Template(p, src)
	})
}

// WritePOT writes msgs as a gettext template.
func WritePOT(w io.Writer, domain string, msgs []Message) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Translation template for the %q domain.\n", domain)
	b.WriteString("# Generated by `make generate` (cmd/i18n-extract); do not edit by hand.\n")
	b.WriteString("msgid \"\"\nmsgstr \"\"\n")
	b.WriteString("\"Content-Type: text/plain; charset=UTF-8\\n\"\n")
	b.WriteString("\"Content-Transfer-Encoding: 8bit\\n\"\n")
	for _, m := range msgs {
		b.WriteString("\n")
		for _, r := range m.Refs {
			fmt.Fprintf(&b, "#: %s\n", r)
		}
		if m.Context != "" {
			fmt.Fprintf(&b, "msgctxt %s\n", i18n.Quote(m.Context))
		}
		fmt.Fprintf(&b, "msgid %s\n", i18n.Quote(m.ID))
		if m.Plural != "" {
			fmt.Fprintf(&b, "msgid_plural %s\n", i18n.Quote(m.Plural))
			b.WriteString("msgstr[0] \"\"\nmsgstr[1] \"\"\n")
		} else {
			b.WriteString("msgstr \"\"\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

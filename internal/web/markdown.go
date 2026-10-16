// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"html/template"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdown renders like markdown-it's "js-default" preset, which CMS's
// markdown filter uses: CommonMark plus tables and strikethrough (two
// tildes), raw HTML disabled (it is shown as text, escaped), no linkify,
// no typographer, no hard breaks. Links and images with javascript:,
// vbscript:, file: or data: (except data:image/png|gif|jpeg|webp) URLs
// stay as text, as in markdown-it. markdown_it.go has the rules where
// goldmark differs from markdown-it-py.
var markdown = sync.OnceValue(func() goldmark.Markdown {
	p := parser.NewParser(
		parser.WithBlockParsers(without(parser.DefaultBlockParsers(), parser.NewHTMLBlockParser())...),
		parser.WithInlineParsers(without(parser.DefaultInlineParsers(),
			parser.NewRawHTMLParser(), parser.NewEmphasisParser(), parser.NewLinkParser())...),
		// markdown-it's rules (markdown_it.go).
		parser.WithInlineParsers(
			util.Prioritized(linkParser{parser.NewLinkParser()}, 200),
			util.Prioritized(emphasisParser{}, 500),
			util.Prioritized(strikeParser{}, 500),
		),
		parser.WithParagraphTransformers(without(parser.DefaultParagraphTransformers(),
			parser.LinkReferenceParagraphTransformer)...),
		parser.WithParagraphTransformers(util.Prioritized(refDefinitions{}, 100)),
		parser.WithASTTransformers(util.Prioritized(mditTransformer{}, 1000)),
	)
	md := goldmark.New(
		goldmark.WithParser(p),
		goldmark.WithExtensions(
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignStyle)),
		),
	)
	// markdown-it writes <s>, goldmark <del>; empty blockquotes and
	// punycode host names in autolinks (markdown_it.go).
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(strikeRenderer{}, 100),
		util.Prioritized(blockquoteRenderer{}, 100),
		util.Prioritized(autoLinkRenderer{}, 100),
	))
	return md
})

var (
	badProto = regexp.MustCompile(`^(vbscript|javascript|file|data):`)
	goodData = regexp.MustCompile(`^data:image/(gif|png|jpeg|webp);`)
)

// decodeDest decodes a destination as markdown-it does (backslash
// escapes and character references).
func decodeDest(dest []byte) []byte {
	return util.ResolveNumericReferences(util.ResolveEntityNames(util.UnescapePunctuations(dest)))
}

// validLink is markdown-it's validateLink, on the destination decoded as
// markdown-it decodes it.
func validLink(dest []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(decodeDest(dest))))
	if badProto.MatchString(s) {
		return goodData.MatchString(s)
	}
	return true
}

// strikeRenderer renders ~~text~~ as <s>text</s>, as markdown-it.
type strikeRenderer struct{}

func (strikeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(east.KindStrikethrough, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString("<s>")
		} else {
			_, _ = w.WriteString("</s>")
		}
		return ast.WalkContinue, nil
	})
}

// mditTransformer applies markdown-it's rules after parsing: lone
// tildes of strikethrough runs, dangerous links, host names in punycode,
// Unicode whitespace around blocks.
type mditTransformer struct{}

func (mditTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	fixStrikes(pc)
	unlinkDangerous(doc, reader.Source(), pc)
	trimMarkdownBlocks(doc, reader.Source())
}

// unlinkDangerous turns links, images and autolinks whose URL fails
// markdown-it's validateLink back into their source text, as markdown-it
// does (goldmark would render them with an empty URL): the label keeps
// its inline formatting, "](…)" is the text as written (markdown-it
// would also parse emphasis in it). The other links get their host name
// in punycode.
func unlinkDangerous(doc *ast.Document, src []byte, pc parser.Context) {
	var bad []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest *[]byte
		switch x := n.(type) {
		case *ast.Link:
			dest = &x.Destination
		case *ast.Image:
			dest = &x.Destination
		case *ast.AutoLink:
			if !validLink(x.URL(src)) {
				bad = append(bad, n)
			}
			return ast.WalkContinue, nil
		default:
			return ast.WalkContinue, nil
		}
		if !validLink(*dest) {
			bad = append(bad, n)
		} else if bytes.Equal(decodeDest(*dest), *dest) {
			// (A destination with escapes or references keeps its host.)
			*dest = []byte(punycodeHost(string(*dest)))
		}
		return ast.WalkContinue, nil
	})
	tails, _ := pc.Get(linkTails).(map[ast.Node][2]int)
	for _, n := range bad {
		parent := n.Parent()
		str := func(s string) { parent.InsertBefore(parent, n, ast.NewString([]byte(s))) }
		var dest, title []byte
		switch x := n.(type) {
		case *ast.AutoLink:
			str("<" + string(x.URL(src)) + ">")
			parent.RemoveChild(parent, n)
			continue
		case *ast.Link:
			str("[")
			dest, title = x.Destination, x.Title
		case *ast.Image:
			str("![")
			dest, title = x.Destination, x.Title
		}
		for c := n.FirstChild(); c != nil; {
			next := c.NextSibling()
			parent.InsertBefore(parent, n, c)
			c = next
		}
		if span, ok := tails[n]; ok {
			str(tailSource(n, src, span))
		} else {
			tail := "](" + string(util.ResolveNumericReferences(util.ResolveEntityNames(dest)))
			if title != nil {
				tail += ` "` + string(title) + `"`
			}
			str(tail + ")")
		}
		parent.RemoveChild(parent, n)
	}
}

// without drops the parsers of the same types as like (goldmark does
// not export the types of its parsers).
func without(vs []util.PrioritizedValue, like ...any) []util.PrioritizedValue {
	out := make([]util.PrioritizedValue, 0, len(vs))
	for _, v := range vs {
		if !slices.ContainsFunc(like, func(l any) bool { return reflect.TypeOf(v.Value) == reflect.TypeOf(l) }) {
			out = append(out, v)
		}
	}
	return out
}

// Markdown renders text as HTML (CMS markdown_filter), safe to insert in
// a page: raw HTML in text is escaped and dangerous link schemes are
// neutralized.
func Markdown(text string) template.HTML {
	var buf bytes.Buffer
	if err := markdown().Convert([]byte(text), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(text))
	}
	return template.HTML(buf.String())
}

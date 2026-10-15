// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"html/template"
	"reflect"
	"regexp"
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
// stay as text, as in markdown-it.
var markdown = sync.OnceValue(func() goldmark.Markdown {
	p := parser.NewParser(
		parser.WithBlockParsers(without(parser.DefaultBlockParsers(), parser.NewHTMLBlockParser())...),
		parser.WithInlineParsers(without(parser.DefaultInlineParsers(), parser.NewRawHTMLParser())...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
		parser.WithASTTransformers(util.Prioritized(unlinkDangerous{}, 1000)),
		// markdown-it only strikes with two tildes (goldmark accepts one).
		parser.WithInlineParsers(util.Prioritized(strikeParser{}, 500)),
	)
	md := goldmark.New(
		goldmark.WithParser(p),
		goldmark.WithExtensions(
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignStyle)),
		),
	)
	// markdown-it writes <s>, goldmark <del>.
	md.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(strikeRenderer{}, 100)))
	return md
})

// strikeParser is goldmark's strikethrough parser restricted to runs of
// exactly two tildes, as markdown-it's strikethrough rule.
type strikeParser struct{}

func (strikeParser) Trigger() []byte { return []byte{'~'} }

func (strikeParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 2, strikeDelimiter{})
	if node == nil || node.OriginalLength != 2 || before == '~' {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

func (strikeParser) CloseBlock(ast.Node, parser.Context) {}

type strikeDelimiter struct{}

func (strikeDelimiter) IsDelimiter(b byte) bool { return b == '~' }

func (strikeDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}

func (strikeDelimiter) OnMatch(int) ast.Node { return east.NewStrikethrough() }

var (
	badProto = regexp.MustCompile(`^(vbscript|javascript|file|data):`)
	goodData = regexp.MustCompile(`^data:image/(gif|png|jpeg|webp);`)
)

// validLink is markdown-it's validateLink, on the destination decoded as
// markdown-it decodes it (backslash escapes and character references).
func validLink(dest []byte) bool {
	u := util.ResolveNumericReferences(util.ResolveEntityNames(util.UnescapePunctuations(dest)))
	s := strings.ToLower(strings.TrimSpace(string(u)))
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

// unlinkDangerous turns links, images and autolinks whose URL fails
// markdown-it's validateLink back into their text, as markdown-it does
// (goldmark would render them with an empty URL). The text is rebuilt
// from the node: a destination written as <...> loses its brackets.
type unlinkDangerous struct{}

func (unlinkDangerous) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	var bad []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch x := n.(type) {
		case *ast.Link:
			if !validLink(x.Destination) {
				bad = append(bad, n)
			}
		case *ast.Image:
			if !validLink(x.Destination) {
				bad = append(bad, n)
			}
		case *ast.AutoLink:
			if !validLink(x.URL(reader.Source())) {
				bad = append(bad, n)
			}
		}
		return ast.WalkContinue, nil
	})
	src := reader.Source()
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
		tail := "](" + string(util.ResolveNumericReferences(util.ResolveEntityNames(dest)))
		if title != nil {
			tail += ` "` + string(title) + `"`
		}
		str(tail + ")")
		parent.RemoveChild(parent, n)
	}
}

// without drops the parsers of the same type as like (goldmark does not
// export the types of its HTML parsers).
func without(vs []util.PrioritizedValue, like any) []util.PrioritizedValue {
	out := make([]util.PrioritizedValue, 0, len(vs))
	for _, v := range vs {
		if reflect.TypeOf(v.Value) != reflect.TypeOf(like) {
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

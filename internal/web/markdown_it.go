// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

// The rules of markdown-it-py (CMS's markdown filter) where goldmark
// follows another version of CommonMark or does something else:
// emphasis flanking (only Unicode P punctuation counts, not symbols),
// strikethrough runs of any length, Unicode whitespace trimmed around
// paragraphs, headings and table cells, empty blockquotes, host names
// in punycode, and links rejected by validateLink shown as their source.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mditPunct is markdown-it's punctuation for the delimiter rules: ASCII
// punctuation and Unicode P (goldmark also counts symbols, as
// CommonMark 0.31: "*£*bravo." differs).
func mditPunct(r rune) bool {
	if r < utf8.RuneSelf {
		return util.IsPunct(byte(r))
	}
	return unicode.IsPunct(r)
}

// mditSpace is markdown-it's isWhiteSpace.
func mditSpace(r rune) bool {
	switch r {
	case 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0xa0, 0x1680, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// pySpace is Python's str.isspace, which markdown-it-py's strip() uses.
func pySpace(r rune) bool {
	return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
}

// scanDelimiter is parser.ScanDelimiter with markdown-it's character
// classes.
func scanDelimiter(line []byte, before rune, minimum int, processor parser.DelimiterProcessor) *parser.Delimiter {
	c := line[0]
	if !processor.IsDelimiter(c) {
		return nil
	}
	j := 0
	for j < len(line) && line[j] == c {
		j++
	}
	if j < minimum {
		return nil
	}
	after := rune(' ')
	if j < len(line) {
		after = util.ToRune(line, j)
	}
	beforePunct, beforeSpace := mditPunct(before), mditSpace(before)
	afterPunct, afterSpace := mditPunct(after), mditSpace(after)
	isLeft := !afterSpace && (!afterPunct || beforeSpace || beforePunct)
	isRight := !beforeSpace && (!beforePunct || afterSpace || afterPunct)
	canOpen, canClose := isLeft, isRight
	if c == '_' {
		canOpen = isLeft && (!isRight || beforePunct)
		canClose = isRight && (!isLeft || afterPunct)
	}
	return parser.NewDelimiter(canOpen, canClose, j, c, processor)
}

// emphasisParser is goldmark's emphasis parser with scanDelimiter.
type emphasisParser struct{}

func (emphasisParser) Trigger() []byte { return []byte{'*', '_'} }

func (emphasisParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := scanDelimiter(line, before, 1, emphasisDelimiter{})
	if node == nil {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

type emphasisDelimiter struct{}

func (emphasisDelimiter) IsDelimiter(b byte) bool { return b == '*' || b == '_' }

func (emphasisDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}

func (emphasisDelimiter) OnMatch(consumes int) ast.Node { return ast.NewEmphasis(consumes) }

// loneTildes is the context key of the odd tildes of strikethrough runs.
var loneTildes = parser.NewContextKey()

// strikeParser is markdown-it's strikethrough rule: a run of two tildes
// or more is a pair of tildes after another (each a delimiter of its
// own), after a lone tilde when the run is odd; the lone tilde moves
// after the closing tags when the run closes (fixStrikes).
type strikeParser struct{}

func (strikeParser) Trigger() []byte { return []byte{'~'} }

func (strikeParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	run := strikeDelimiter{run: segment.Start}
	d := scanDelimiter(line, before, 2, run)
	if d == nil {
		return nil
	}
	n := d.OriginalLength
	start := segment.Start
	if n%2 == 1 {
		lone := ast.NewTextSegment(text.NewSegment(start, start+1))
		parent.AppendChild(parent, lone)
		list, _ := pc.Get(loneTildes).([]*ast.Text)
		pc.Set(loneTildes, append(list, lone))
		start++
		n--
	}
	var last *parser.Delimiter
	for i := 0; i < n; i += 2 {
		if last != nil {
			parent.AppendChild(parent, last)
		}
		last = parser.NewDelimiter(d.CanOpen, d.CanClose, 2, '~', run)
		last.Segment = text.NewSegment(start+i, start+i+2)
		pc.PushDelimiter(last)
	}
	block.Advance(d.OriginalLength)
	return last
}

func (strikeParser) CloseBlock(ast.Node, parser.Context) {}

// strikeDelimiter is the processor of the pairs of a run: pairs of the
// same run do not match each other (markdown-it's balance_pairs).
type strikeDelimiter struct{ run int }

func (strikeDelimiter) IsDelimiter(b byte) bool { return b == '~' }

func (d strikeDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	o, ok1 := opener.Processor.(strikeDelimiter)
	c, ok2 := closer.Processor.(strikeDelimiter)
	return ok1 && ok2 && o.run != c.run
}

func (strikeDelimiter) OnMatch(int) ast.Node { return east.NewStrikethrough() }

// fixStrikes moves the lone tilde of a run that closed strikethroughs
// after them (markdown-it's postProcess): delimiter processing left it
// as the last child of the innermost one.
func fixStrikes(pc parser.Context) {
	list, _ := pc.Get(loneTildes).([]*ast.Text)
	for _, t := range list {
		p := t.Parent()
		if p == nil || p.Kind() != east.KindStrikethrough || p.LastChild() != t {
			continue
		}
		for p.Parent() != nil && p.Parent().Kind() == east.KindStrikethrough && p.Parent().LastChild() == p {
			p = p.Parent()
		}
		t.Parent().RemoveChild(t.Parent(), t)
		p.Parent().InsertAfter(p.Parent(), p, t)
	}
}

// trimMarkdownBlocks strips the Unicode whitespace around the text of
// paragraphs, headings and table cells, as markdown-it-py's strip()
// (goldmark only strips spaces and tabs).
func trimMarkdownBlocks(doc *ast.Document, src []byte) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindParagraph, ast.KindTextBlock, ast.KindHeading, east.KindTableCell:
			trimInline(n, src)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}

func trimInline(n ast.Node, src []byte) {
	// Whitespace texts at the start go, with their line breaks.
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			break
		}
		v := t.Segment.Value(src)
		rest := strings.TrimLeftFunc(string(v), pySpace)
		t.Segment = t.Segment.WithStart(t.Segment.Start + len(v) - len(rest))
		if rest != "" {
			break
		}
		t.SetSoftLineBreak(false)
		t.SetHardLineBreak(false)
	}
	// And at the end (a line break before them is trailing whitespace).
	for c := n.LastChild(); c != nil; c = c.PreviousSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			break
		}
		t.SetSoftLineBreak(false)
		t.SetHardLineBreak(false)
		v := t.Segment.Value(src)
		rest := strings.TrimRightFunc(string(v), pySpace)
		t.Segment = t.Segment.WithStop(t.Segment.Start + len(rest))
		if rest != "" {
			break
		}
	}
}

// blockquoteRenderer writes an empty blockquote on one line, as
// markdown-it (one with only link reference definitions is empty).
type blockquoteRenderer struct{}

func emptyBlock(n ast.Node) bool {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Kind() != ast.KindLinkReferenceDefinition {
			return false
		}
	}
	return true
}

func (blockquoteRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindBlockquote, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch {
		case !entering:
			_, _ = w.WriteString("</blockquote>\n")
		case !emptyBlock(n):
			_, _ = w.WriteString("<blockquote>\n")
		default:
			_, _ = w.WriteString("<blockquote>")
		}
		return ast.WalkContinue, nil
	})
}

// autoLinkRenderer is goldmark's autolink renderer with the host name in
// punycode (markdown-it's normalizeLink). Dangerous URLs never get here
// (unlinkDangerous).
type autoLinkRenderer struct{}

func (autoLinkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindAutoLink, func(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		n := node.(*ast.AutoLink)
		u := n.URL(src)
		if n.AutoLinkType == ast.AutoLinkURL {
			u = []byte(punycodeHost(string(u)))
		}
		u = util.URLEscape(u, false)
		_, _ = w.WriteString(`<a href="`)
		if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(string(u)), "mailto:") {
			_, _ = w.WriteString("mailto:")
		}
		_, _ = w.Write(util.EscapeHTML(u))
		_, _ = w.WriteString(`">`)
		_, _ = w.Write(util.EscapeHTML(n.Label(src)))
		_, _ = w.WriteString(`</a>`)
		return ast.WalkContinue, nil
	})
}

// linkTails is the context key of the source of the links' tails.
var linkTails = parser.NewContextKey()

// linkParser is goldmark's link parser; it remembers where the "](…)"
// (or "]", "][ref]") of each link and image is, so that unlinkDangerous
// can show it as written.
type linkParser struct{ parser.InlineParser }

func (p linkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	_, start := block.Position()
	n := p.InlineParser.Parse(parent, block, pc)
	switch n.(type) {
	case *ast.Link, *ast.Image:
		_, end := block.Position()
		m, _ := pc.Get(linkTails).(map[ast.Node][2]int)
		if m == nil {
			m = map[ast.Node][2]int{}
			pc.Set(linkTails, m)
		}
		m[n] = [2]int{start.Start, end.Start}
	}
	return n
}

// CloseBlock forwards to goldmark's parser (it clears the brackets left
// open in the block).
func (p linkParser) CloseBlock(parent ast.Node, block text.Reader, pc parser.Context) {
	if cb, ok := p.InlineParser.(parser.CloseBlocker); ok {
		cb.CloseBlock(parent, block, pc)
	}
}

// tailSource returns the source between two offsets of a block's lines
// (without the prefixes of the lines, such as "> ").
func tailSource(n ast.Node, src []byte, span [2]int) string {
	for n != nil && n.Type() != ast.TypeBlock {
		n = n.Parent()
	}
	if n == nil {
		return ""
	}
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		s := lines.At(i)
		lo, hi := max(s.Start, span[0]), min(s.Stop, span[1])
		if lo < hi {
			b.Write(src[lo:hi])
		}
	}
	return b.String()
}

// punycodeHost converts the host name of an http:, https: or mailto:
// URL, or of a protocol-relative one ("//host"), to punycode when it is
// not ASCII (markdown-it's normalizeLink with mdurl.parse). A host it
// cannot isolate safely is left alone.
func punycodeHost(u string) string {
	if isASCII(u) {
		return u
	}
	var rest string
	switch {
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		rest = u[strings.Index(u, "//")+2:]
	case strings.HasPrefix(u, "//"):
		rest = u[2:]
	case strings.HasPrefix(u, "mailto:") && !strings.HasPrefix(u, "mailto://"):
		rest = u[len("mailto:"):]
	default:
		return u
	}
	prefix := u[:len(u)-len(rest)]
	// The authority ends at / ? # (mdurl's hostEndingChars); the user
	// info at the last @ before that.
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	auth := ""
	if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
		auth, rest = rest[:at], rest[at+1:]
		end -= at + 1
		if auth != "" {
			auth += "@"
		}
	}
	// The host stops at the characters mdurl does not allow in it.
	if i := strings.IndexAny(rest[:end], "%;\\ \r\n\t{}|^`<>\"'"); i >= 0 {
		end = i
	}
	host, tail := rest[:end], rest[end:]
	if p := strings.LastIndexByte(host, ':'); p >= 0 && isDigits(host[p+1:]) {
		host, tail = host[:p], host[p:]+tail
	}
	if strings.HasPrefix(host, "[") {
		return u
	}
	// mdurl keeps the valid labels (split at ASCII dots): letters,
	// digits, + _ - and non-ASCII characters, at most 63. In the first
	// invalid one it keeps the ASCII ones of that class at its start; the
	// rest goes to the path.
	labels := strings.Split(host, ".")
	for i, l := range labels {
		if hostLabel(l) {
			continue
		}
		k := 0
		for k < len(l) && k < 63 && asciiHostByte(l[k]) {
			k++
		}
		tail = strings.Join(append([]string{l[k:]}, labels[i+1:]...), ".") + tail
		host = strings.Join(append(labels[:i:i], l[:k]), ".")
		break
	}
	if host == "" || len(host) > 255 {
		return u
	}
	return prefix + auth + punycodeDomain(host) + tail
}

// hostLabel is mdurl's hostnamePartPattern, a non-ASCII character
// counting as a letter.
func hostLabel(l string) bool {
	n := 0
	for _, r := range l {
		if r < utf8.RuneSelf && !asciiHostByte(byte(r)) {
			return false
		}
		n++
	}
	return n <= 63
}

func asciiHostByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '_' || c == '-'
}

// punycodeDomain is markdown-it's punycode.toASCII: the labels (split at
// . 。 ． ｡) with non-ASCII characters become xn--<punycode>.
func punycodeDomain(host string) string {
	labels := strings.FieldsFunc(host, func(r rune) bool {
		return r == '.' || r == '。' || r == '．' || r == '｡'
	})
	for i, l := range labels {
		if !isASCII(l) {
			labels[i] = "xn--" + punycode(l)
		}
	}
	return strings.Join(labels, ".")
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// punycode is the Punycode encoding of RFC 3492 (as Python's "punycode"
// codec: the case of the basic code points is kept).
func punycode(s string) string {
	const (
		base        = 36
		tMin        = 1
		tMax        = 26
		skew        = 38
		damp        = 700
		initialBias = 72
		initialN    = 128
	)
	runes := []rune(s)
	var out []byte
	for _, r := range runes {
		if r < initialN {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	adapt := func(delta, points int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / points
		k := 0
		for delta > ((base-tMin)*tMax)/2 {
			delta /= base - tMin
			k += base
		}
		return k + (base-tMin+1)*delta/(delta+skew)
	}
	n, delta, bias := initialN, 0, initialBias
	for h < len(runes) {
		m := int(^uint(0) >> 1)
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tMin {
						t = tMin
					} else if t > tMax {
						t = tMax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out)
}

// refDefinitions is goldmark's LinkReferenceParagraphTransformer, except
// that a definition whose destination fails validateLink is not one (as
// in markdown-it): it and the rest of the paragraph stay a paragraph.
type refDefinitions struct{}

// refContext drops the references from the first dangerous one on.
type refContext struct {
	parser.Context
	bad bool
}

func (c *refContext) AddReference(r parser.Reference) {
	if c.bad || !validLink(r.Destination()) {
		c.bad = true
		return
	}
	c.Context.AddReference(r)
}

func (refDefinitions) Transform(node *ast.Paragraph, reader text.Reader, pc parser.Context) {
	parent, prev, next := node.Parent(), node.PreviousSibling(), node.NextSibling()
	orig := append([]text.Segment(nil), node.Lines().Sliced(0, node.Lines().Len())...)
	rc := &refContext{Context: pc}
	parser.LinkReferenceParagraphTransformer.Transform(node, reader, rc)
	if !rc.bad {
		return
	}
	// The definitions made from this paragraph lie between prev and the
	// paragraph (or next, if it was emptied and removed).
	first := parent.FirstChild()
	if prev != nil {
		first = prev.NextSibling()
	}
	from := -1
	for c := first; c != nil && c != node && c != next; {
		d, ok := c.(*ast.LinkReferenceDefinition)
		nc := c.NextSibling()
		if ok && (from >= 0 || !validLink(d.Destination)) {
			if from < 0 {
				start := d.Lines().At(0).Start
				for i, s := range orig {
					if s.Start <= start && start <= s.Stop {
						from = i
						break
					}
				}
			}
			parent.RemoveChild(parent, c)
		}
		c = nc
	}
	if from < 0 {
		return
	}
	var lines text.Segments
	for _, s := range orig[from:] {
		lines.Append(s)
	}
	last := lines.At(lines.Len() - 1)
	lines.Set(lines.Len()-1, last.TrimRightSpace(reader.Source()))
	if node.Parent() == nil {
		blank := node.HasBlankPreviousLines()
		node = ast.NewParagraph()
		node.SetBlankPreviousLines(blank)
		if next != nil {
			parent.InsertBefore(parent, next, node)
		} else {
			parent.AppendChild(parent, node)
		}
	}
	node.SetLines(&lines)
}

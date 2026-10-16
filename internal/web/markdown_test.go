// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestMarkdownLikeMarkdownIt compares with the output of markdown-it-py
// 3.0 ("js-default", the preset of CMS's markdown filter) for the same
// inputs (TestMarkdownCorpus has many more).
func TestMarkdownLikeMarkdownIt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello *world*",
			"<p>hello <em>world</em></p>\n"},
		{"<b>bold</b> & <script>alert(1)</script>",
			"<p>&lt;b&gt;bold&lt;/b&gt; &amp; &lt;script&gt;alert(1)&lt;/script&gt;</p>\n"},
		{"<div>block</div>",
			"<p>&lt;div&gt;block&lt;/div&gt;</p>\n"},
		{"[x](javascript:alert(1))",
			"<p>[x](javascript:alert(1))</p>\n"},
		{"[x](JaVaScRiPt:alert(1))",
			"<p>[x](JaVaScRiPt:alert(1))</p>\n"},
		{"![i](data:image/png;base64,AAAA)",
			"<p><img src=\"data:image/png;base64,AAAA\" alt=\"i\"></p>\n"},
		{"[ok](https://example.org/a?b=c&d=e \"t\")",
			"<p><a href=\"https://example.org/a?b=c&amp;d=e\" title=\"t\">ok</a></p>\n"},
		{"~~strike~~",
			"<p><s>strike</s></p>\n"},
		{"| a | b |\n|:-|-:|\n| 1 | 2 |",
			"<table>\n<thead>\n<tr>\n<th style=\"text-align:left\">a</th>\n<th style=\"text-align:right\">b</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td style=\"text-align:left\">1</td>\n<td style=\"text-align:right\">2</td>\n</tr>\n</tbody>\n</table>\n"},
		{"line1\nline2",
			"<p>line1\nline2</p>\n"},
		{"```c\nint x;\n```",
			"<pre><code class=\"language-c\">int x;\n</code></pre>\n"},
		{"<http://example.org>",
			"<p><a href=\"http://example.org\">http://example.org</a></p>\n"},
		{"a\n\n---\n\nb",
			"<p>a</p>\n<hr>\n<p>b</p>\n"},
		{"# Title\n\ntext",
			"<h1>Title</h1>\n<p>text</p>\n"},
		{"\"quotes\" & 'apos'",
			"<p>&quot;quotes&quot; &amp; 'apos'</p>\n"},
		{"<!-- comment -->",
			"<p>&lt;!-- comment --&gt;</p>\n"},
		{"x <span onclick=\"alert(1)\">y</span>",
			"<p>x &lt;span onclick=&quot;alert(1)&quot;&gt;y&lt;/span&gt;</p>\n"},
		{"1. one\n2. two",
			"<ol>\n<li>one</li>\n<li>two</li>\n</ol>\n"},
		{"> quote",
			"<blockquote>\n<p>quote</p>\n</blockquote>\n"},
		{"`<code>`",
			"<p><code>&lt;code&gt;</code></p>\n"},
		{"![img](x.png)",
			"<p><img src=\"x.png\" alt=\"img\"></p>\n"},
		{"www.example.org",
			"<p>www.example.org</p>\n"},
		{"- a\n  - b\n- c",
			"<ul>\n<li>a\n<ul>\n<li>b</li>\n</ul>\n</li>\n<li>c</li>\n</ul>\n"},
		{"_em_ and __strong__ and **bold**",
			"<p><em>em</em> and <strong>strong</strong> and <strong>bold</strong></p>\n"},
		{"[link](https://example.org/a_(b))",
			"<p><a href=\"https://example.org/a_(b)\">link</a></p>\n"},
		{"`` a ` b ``",
			"<p><code>a ` b</code></p>\n"},
		{"line  \nbreak",
			"<p>line<br>\nbreak</p>\n"},
		{"&copy; &amp; &#35; &nbsp;",
			"<p>© &amp; #  </p>\n"},
		{"<a@b.co>",
			"<p><a href=\"mailto:a@b.co\">a@b.co</a></p>\n"},
		{"[ref][1]\n\n[1]: https://example.org \"T\"",
			"<p><a href=\"https://example.org\" title=\"T\">ref</a></p>\n"},
		{"a | b\n--|--\n1 | 2",
			"<table>\n<thead>\n<tr>\n<th>a</th>\n<th>b</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td>1</td>\n<td>2</td>\n</tr>\n</tbody>\n</table>\n"},
		{"\\*not em\\*",
			"<p>*not em*</p>\n"},
		{"[a](javascript&colon;alert(1))",
			"<p>[a](javascript:alert(1))</p>\n"},
		{"[a](file:///etc/passwd)",
			"<p>[a](file:///etc/passwd)</p>\n"},
		{"![x](data:image/svg+xml;base64,PHN2Zz4=)",
			"<p>![x](data:image/svg+xml;base64,PHN2Zz4=)</p>\n"},
		{"![x](data:image/gif;base64,R0lG)",
			"<p><img src=\"data:image/gif;base64,R0lG\" alt=\"x\"></p>\n"},
		{"    indented code",
			"<pre><code>indented code\n</code></pre>\n"},
		{"Setext\n===",
			"<h1>Setext</h1>\n"},
		{"* * *",
			"<hr>\n"},
		{"1) a\n2) b",
			"<ol>\n<li>a</li>\n<li>b</li>\n</ol>\n"},
		{"a\\\nb",
			"<p>a<br>\nb</p>\n"},
		{"<span>ok</span>",
			"<p>&lt;span&gt;ok&lt;/span&gt;</p>\n"},
		{"[x](https://e.org/?a=1&b=2)",
			"<p><a href=\"https://e.org/?a=1&amp;b=2\">x</a></p>\n"},
		{"~~a~~b~c~",
			"<p><s>a</s>b~c~</p>\n"},
		{"| a |\n|---|\n| `|` |",
			"<table>\n<thead>\n<tr>\n<th>a</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td>`</td>\n</tr>\n</tbody>\n</table>\n"},
		{"Problem **A**: read `n` (1 ≤ n ≤ 10^9)",
			"<p>Problem <strong>A</strong>: read <code>n</code> (1 ≤ n ≤ 10^9)</p>\n"},
		{"",
			""},
		{"[](https://e.org)",
			"<p><a href=\"https://e.org\"></a></p>\n"},
		{"[a](  https://e.org  )",
			"<p><a href=\"https://e.org\">a</a></p>\n"},
		{"[a](https://e.org/\"onmouseover=\"x)",
			"<p><a href=\"https://e.org/%22onmouseover=%22x\">a</a></p>\n"},
		{"![a\"b](x.png \"t'\")",
			"<p><img src=\"x.png\" alt=\"a&quot;b\" title=\"t'\"></p>\n"},
	}
	for _, c := range cases {
		if got := string(Markdown(c.in)); got != c.want {
			t.Errorf("Markdown(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestMarkdownSafety checks that no script can come out of the filter.
func TestMarkdownSafety(t *testing.T) {
	for _, in := range []string{
		"<script>alert(1)</script>",
		"<img src=x onerror=alert(1)>",
		"[a](javascript:alert(1))",
		"[a](  javascript:alert(1)  )",
		"[a](java\nscript:alert(1))",
		"[a](&#106;avascript:alert(1))",
		"[a](vbscript:x)",
		"[a](data:text/html;base64,PHNjcmlwdD4=)",
		"![a](data:image/svg+xml;base64,PHN2Zz4=)",
		"<javascript:alert(1)>",
		"[a][r]\n\n[r]: javascript:alert(1)",
		"<a href=\"javascript:alert(1)\">x</a>",
		"<iframe src=//evil></iframe>",
		"[x](\"onmouseover=alert(1))",
		"`<script>`",
		"[a](javascript&colon;alert(1))",
		"[a](<javascript:alert(1)>)",
		"[a](java\\script:alert(1))",
		"<vbscript:x>",
		// Rejected links shown as their source, escaped.
		"[x]( javascript:a \"<script>alert(1)</script>\")",
		"[x](<javascript:a><script>)",
		"[a]: javascript:x \"<script>\"\n\n[a]",
		"> [a]: javascript:x\n> <script>\n\n[a]",
		// Host names in punycode.
		"[x](http://ä\"><script>.de/)",
		"<http://ä.de/\"><script>>",
	} {
		got := strings.ToLower(string(Markdown(in)))
		for _, bad := range []string{"<script", "<img src=x", "<iframe", "<a href=\"javascript", "href=\"vbscript",
			"href=\"data:", "src=\"data:image/svg", "href=\"&#106;"} {
			if strings.Contains(got, bad) {
				t.Errorf("Markdown(%q) = %q contains %q", in, got, bad)
			}
		}
	}
}

// knownMarkdownDifferences are the inputs of the corpus where the filter
// differs from markdown-it-py: a link rejected by validateLink is shown
// as its source text, where markdown-it also parses emphasis and other
// inline syntax in the "](…)" part (or across it).
var knownMarkdownDifferences = map[string]bool{
	"[x](javascript:*a*)":   true,
	"*a [x](javascript:b*)": true,
}

// TestMarkdownCorpus renders the corpus of testdata/markdown (the
// examples of the CommonMark specification and cases of this fork) and
// compares with markdown-it-py's output (README there).
func TestMarkdownCorpus(t *testing.T) {
	for _, file := range []string{"commonmark_spec.json", "cases.json"} {
		b, err := os.ReadFile("testdata/markdown/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var cases []struct{ In, Out string }
		if err := json.Unmarshal(b, &cases); err != nil {
			t.Fatal(err)
		}
		bad := 0
		for _, c := range cases {
			if got := string(Markdown(c.In)); got != c.Out && !knownMarkdownDifferences[c.In] {
				bad++
				t.Errorf("%s: Markdown(%q)\n got %q\nwant %q", file, c.In, got, c.Out)
			}
		}
		t.Logf("%s: %d cases, %d differ", file, len(cases), bad)
	}
}

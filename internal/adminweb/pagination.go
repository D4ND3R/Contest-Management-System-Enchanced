// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Pagination of the admin lists (D24): CMS shows 50 rows per page and the
// selector of templates/macro/pages.html ("Pages: 1 2 3 ... First Last").
// A list handler counts its rows (bounded by an index) and reads only the
// rows of the page:
//
//	pg := c.Pager(count, 50)             // the page of ?page= (0-based)
//	rows := ...LIMIT pg.Size OFFSET pg.Offset()
//	{{template "pages.selector" .Pager}} // in the template
//
// Lists that use keyset pagination (no count) put their own "newer" /
// "older" links (the audit page does).

import (
	"net/url"
	"strconv"
)

// DefaultPageSize is CMS's page_size.
const DefaultPageSize = 50

// Pager is one page of a list.
type Pager struct {
	// Page is the 0-based number of the page shown, Pages the number of
	// pages, Count the number of rows, Size the rows per page.
	Page, Pages int
	Count       int64
	Size        int

	base  string
	query url.Values
}

// Pager returns the page of the request's "page" argument (0 when absent
// or invalid, clamped to the last page) for count rows of size per page.
func (c *Ctx) Pager(count int64, size int) *Pager {
	if size <= 0 {
		size = DefaultPageSize
	}
	p := &Pager{Count: count, Size: size, base: lastSegment(c.R.URL.EscapedPath()), query: c.R.URL.Query()}
	p.Pages = int((count + int64(size) - 1) / int64(size))
	if n, err := strconv.Atoi(c.ArgDefault("page", "0")); err == nil && n > 0 {
		p.Page = n
	}
	if p.Pages > 0 && p.Page >= p.Pages {
		p.Page = p.Pages - 1
	}
	return p
}

// lastSegment returns the last element of a path (the page's own URL
// relative to its directory).
func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// Offset is the number of rows before the page.
func (p *Pager) Offset() int { return p.Page * p.Size }

// URL returns the URL of page i (CMS page_url(page=i)), keeping the other
// arguments of the request.
func (p *Pager) URL(i int) string {
	q := url.Values{}
	for k, v := range p.query {
		q[k] = v
	}
	q.Set("page", strconv.Itoa(i))
	return p.base + "?" + q.Encode()
}

// PagerLink is an entry of the selector: a page number (1-based Label)
// that is a link unless it is the current page.
type PagerLink struct {
	Label   int
	URL     string
	Current bool
}

// Range returns the entries of CMS's range_selector(page_url, page,
// pages, start, end) for the selector: every page up to 20 pages,
// otherwise from page-2 to the end.
func (p *Pager) Range() []PagerLink {
	start, end := 0, p.Pages
	if p.Pages > 20 {
		start, end = p.Page-2, p.Pages+3
	}
	start, end = max(0, start), min(p.Pages, end)
	var out []PagerLink
	for i := start; i < end; i++ {
		out = append(out, PagerLink{Label: i + 1, URL: p.URL(i), Current: i == p.Page})
	}
	return out
}

// Last is the number of the last page.
func (p *Pager) Last() int { return p.Pages - 1 }

// Many reports whether there are more than 20 pages (the selector then
// has First and Last).
func (p *Pager) Many() bool { return p.Pages > 20 }

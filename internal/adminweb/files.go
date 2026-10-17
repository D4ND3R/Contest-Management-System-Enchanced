// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from FileFromDigestHandler in cms/server/admin/handlers/base.py
// and MarkdownRenderHandler in cms/server/admin/handlers/main.py of CMS:
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

import (
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// filePage is GET /file/<digest>/<name> (CMS FileFromDigestHandler): any
// file of the store by its digest, as text/plain with the given name.
// Admins may read every file (test data, submissions...).
func (s *Server) filePage(c *Ctx) {
	s.ServeFile(c, c.R.PathValue("digest"), c.R.PathValue("filename"), "text/plain")
}

// ServeFile streams a file of the blob store as a download (CMS
// FileHandlerMixin.fetch): contentType "" guesses it from the name.
func (s *Server) ServeFile(c *Ctx, digest, filename, contentType string) {
	err := web.ServeBlob(c.W, c.R, s.blobs, web.Download{Digest: digest, Filename: filename, ContentType: contentType})
	if err != nil && c.Context().Err() == nil {
		s.log.Error("serving a file", "digest", digest, "err", err)
	}
}

// renderMarkdownPost is POST /render_markdown (CMS
// MarkdownRenderHandler): the HTML of the "input" argument, for the
// previews of the message forms (macro/markdown_input.html).
func (s *Server) renderMarkdownPost(c *Ctx) {
	in, ok := c.Arg("input")
	if !ok {
		c.BadRequest()
		return
	}
	c.W.Header().Set("Content-Type", "text/html; charset=UTF-8")
	_, _ = c.W.Write([]byte(web.Markdown(in)))
}

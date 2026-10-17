// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Submissions and user tests (work C): the pages of
// cms/server/admin/handlers/submission.py, usertest.py,
// contestsubmission.py and DatasetSubmissionsHandler of dataset.py, and
// the reevaluation RPC (invalidate_submission). Until they are written
// their handlers answer 501 (routes.go and rpc.go list them).

import "encoding/json"

func (s *Server) contestSubmissionsPage(c *Ctx) { c.NotImplemented() }

func (s *Server) contestUserTestsPage(c *Ctx) { c.NotImplemented() }

func (s *Server) datasetSubmissionsPage(c *Ctx) { c.NotImplemented() }

func (s *Server) submissionPage(c *Ctx) { c.NotImplemented() }

func (s *Server) submissionCommentPost(c *Ctx) { c.NotImplemented() }

func (s *Server) submissionOfficialPost(c *Ctx) { c.NotImplemented() }

func (s *Server) submissionFilePage(c *Ctx) { c.NotImplemented() }

func (s *Server) submissionDiffPage(c *Ctx) { c.NotImplemented() }

func (s *Server) userTestPage(c *Ctx) { c.NotImplemented() }

func (s *Server) userTestFilePage(c *Ctx) { c.NotImplemented() }

func (s *Server) rpcInvalidateSubmission(c *Ctx, args json.RawMessage) (any, error) {
	return nil, errRPCNotImplemented
}

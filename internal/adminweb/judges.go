// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// The judges page (work C, R6.7; new): workers, queues, jobs in progress,
// requeueing; and the RPC methods enable_worker and disable_worker of the
// overview. Until they are written they answer 501.

import "encoding/json"

func (s *Server) judgesPage(c *Ctx) { c.NotImplemented() }

func (s *Server) rpcEnableWorker(c *Ctx, args json.RawMessage) (any, error) {
	return nil, errRPCNotImplemented
}

func (s *Server) rpcDisableWorker(c *Ctx, args json.RawMessage) (any, error) {
	return nil, errRPCNotImplemented
}

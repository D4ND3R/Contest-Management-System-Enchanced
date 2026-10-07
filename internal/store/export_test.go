// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store

import "testing"

// SetOpaqueIDDraw replaces the opaque id generator until the test ends.
// Tests that use it must not run in parallel.
func SetOpaqueIDDraw(t testing.TB, draw func() int64) {
	old := drawOpaqueID
	drawOpaqueID = draw
	t.Cleanup(func() { drawOpaqueID = old })
}

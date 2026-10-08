// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build race

package live

// raceEnabled: the race detector limits goroutines (8128) and slows
// everything down; the fan-out test uses fewer clients under it.
const raceEnabled = true

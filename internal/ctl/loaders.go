// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

// The loaders register themselves in importer.Default from init.
import (
	_ "github.com/d4nd3r/contest-management-system-enchanced/internal/importer/italy"
	_ "github.com/d4nd3r/contest-management-system-enchanced/internal/importer/polygon"
	_ "github.com/d4nd3r/contest-management-system-enchanced/internal/importer/tps"
)

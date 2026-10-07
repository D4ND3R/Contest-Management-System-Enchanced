// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"os"
	"testing"
)

// helperEnv names the variable that turns the test binary into a helper
// program (see helper_test.go): the seccomp tests exec the test binary,
// through the launcher, to make the system calls under test.
const helperEnv = "CMS_SANDBOX_TEST_HELPER"

func TestMain(m *testing.M) {
	// The test binary is the launcher of the boxes it runs.
	MaybeRunLauncher()
	if h := os.Getenv(helperEnv); h != "" {
		os.Exit(runHelper(h))
	}
	os.Exit(m.Run())
}

func runHelper(name string) int {
	f, ok := helpers[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown helper %q\n", name)
		return 99
	}
	return f()
}

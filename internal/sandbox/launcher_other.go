// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build !linux

package sandbox

import "errors"

var errNotLinux = errors.New("seccomp is only supported on Linux")

func launcherImage() ([]byte, error) { return nil, errNotLinux }

func checkSeccompSupport() error { return errNotLinux }

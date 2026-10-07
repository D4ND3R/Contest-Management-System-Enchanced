// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package configfiles embeds the configuration files shipped with CMS: the
// sample cms.yaml (every setting with its default value) and the built-in
// language definitions of config/languages, which internal/grading/language
// loads.
package configfiles

import (
	"embed"
	"io/fs"
)

// SampleConfig is config/cms.sample.yaml.
//
//go:embed cms.sample.yaml
var SampleConfig []byte

//go:embed languages/*.yaml
var languages embed.FS

// Languages returns the built-in language definitions: one YAML file per
// language at the root of the file system.
func Languages() fs.FS {
	sub, err := fs.Sub(languages, "languages")
	if err != nil {
		panic(err) // the directory is embedded: cannot happen
	}
	return sub
}

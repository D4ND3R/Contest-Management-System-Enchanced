// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package configfiles

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSampleConfigEmbedded(t *testing.T) {
	disk, err := os.ReadFile("cms.sample.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(SampleConfig) == 0 || !bytes.Equal(SampleConfig, disk) {
		t.Fatal("SampleConfig differs from cms.sample.yaml")
	}
}

func TestLanguagesEmbedded(t *testing.T) {
	onDisk, err := filepath.Glob("languages/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := fs.Glob(Languages(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(embedded) != len(onDisk) || len(embedded) < 15 {
		t.Fatalf("embedded %d language files, %d on disk (want the 13 of CMS + Go and Kotlin)", len(embedded), len(onDisk))
	}
	for _, name := range embedded {
		a, err := fs.ReadFile(Languages(), name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join("languages", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s: embedded copy differs", name)
		}
	}
}

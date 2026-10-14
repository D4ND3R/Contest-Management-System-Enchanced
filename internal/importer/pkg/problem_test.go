// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestPyFloat(t *testing.T) {
	for f, want := range map[float64]string{
		0: "0.0", 1: "1.0", -2: "-2.0", 0.1: "0.1", 100.0 / 3: "33.333333333333336", 1e-4: "0.0001",
		1e-5: "1e-05", 1.5e-7: "1.5e-07", 1e15: "1000000000000000.0", 1e16: "1e+16", 123456.5: "123456.5",
		math.Copysign(0, -1): "-0.0",
	} {
		if got := pyFloat(f); got != want {
			t.Errorf("pyFloat(%v) = %q, want %q", f, got, want)
		}
	}
}

// TestNumber: a number keeps its type through YAML and JSON.
func TestNumber(t *testing.T) {
	for in, want := range map[string]string{
		"40": "40", "40.0": "40.0", "-3": "-3", "2.5": "2.5", "1e2": "100.0", "0.00001": "1e-05", "0x10": "16",
	} {
		var n number
		if err := yaml.Unmarshal([]byte(in), &n); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		j, err := json.Marshal(n)
		if err != nil || string(j) != want {
			t.Errorf("%s: JSON %s %v, want %s", in, j, err, want)
		}
		y, err := yaml.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		var back number
		if err := yaml.Unmarshal(y, &back); err != nil || back != n {
			t.Errorf("%s: YAML %q reads back as %+v (%v)", in, y, back, err)
		}
	}
	for _, in := range []string{"ten", ".nan", ".inf", "[1]", "true", "'5'"} {
		var n number
		if err := yaml.Unmarshal([]byte(in), &n); err == nil || !strings.Contains(err.Error(), "expected a number") {
			t.Errorf("%s: %+v %v", in, n, err)
		}
	}
}

func TestSelectionYAML(t *testing.T) {
	for in, want := range map[string]string{
		"all": "all", "1-3, 5": "1-3, 5", "[1, '02', 3-4]": "1, 02, 3-4", "''": "", "~": "", "a,,b": "a, b", "[all]": "all",
	} {
		var s selection
		if err := yaml.Unmarshal([]byte(in), &s); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if s.String() != want {
			t.Errorf("%s: %q, want %q", in, s.String(), want)
		}
	}
	var s selection
	if err := yaml.Unmarshal([]byte("[[1]]"), &s); err == nil {
		t.Error("a nested list is a selection")
	}
}

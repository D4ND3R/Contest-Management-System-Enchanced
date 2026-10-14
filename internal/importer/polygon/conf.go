// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// taskConf is what files/cms_conf.py says: the columns of the task
// (general) and of each dataset, by testset name (datasets).
type taskConf struct {
	path     string
	general  *pyValue
	datasets *pyValue
}

// readConf reads the cms_conf.py at path; a missing file is no conf.
func readConf(path string) (*taskConf, error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	vars, err := pyModuleVars(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s: %w (the file is read as Python literals, not run)", path, err)
	}
	c := &taskConf{path: path}
	if g, ok := vars["general"]; ok {
		if g.kind != pyDict {
			return nil, fmt.Errorf("%s: general must be a dict, not %s", path, g.kind)
		}
		c.general = &g
	}
	if d, ok := vars["datasets"]; ok {
		if d.kind != pyDict {
			return nil, fmt.Errorf("%s: datasets must be a dict, not %s", path, d.kind)
		}
		c.datasets = &d
	}
	return c, nil
}

// dataset returns the options of the testset called name.
func (c *taskConf) dataset(name string) (*pyValue, error) {
	if c == nil || c.datasets == nil {
		return nil, nil
	}
	v, ok := c.datasets.get(pyStrValue(name))
	if !ok {
		return nil, nil
	}
	if v.kind != pyDict {
		return nil, fmt.Errorf("datasets[%q] must be a dict, not %s", name, v.kind)
	}
	return &v, nil
}

// unusedDatasets lists the keys of datasets that name no testset
// (upstream ignores them).
func (c *taskConf) unusedDatasets(testsets []string) []string {
	if c == nil || c.datasets == nil {
		return nil
	}
	var out []string
	for _, k := range c.datasets.items {
		found := false
		for _, t := range testsets {
			if pyEqual(k, pyStrValue(t)) {
				found = true
			}
		}
		if !found {
			out = append(out, pyRepr(k))
		}
	}
	sort.Strings(out)
	return out
}

// fields iterates over a dict whose keys must be strings.
func fields(d *pyValue, fn func(key string, v pyValue) error) error {
	for i, k := range d.items {
		if k.kind != pyStr {
			return fmt.Errorf("key %s is not a string", pyRepr(k))
		}
		if err := fn(k.s, d.vals[i]); err != nil {
			return fmt.Errorf("%s: %w", k.s, err)
		}
	}
	return nil
}

// applyGeneral sets the columns of the task listed in general, as
// upstream's Task(**args) after args.update(general).
func applyGeneral(t *importer.Task, g *pyValue) error {
	return fields(g, func(key string, v pyValue) error {
		var err error
		switch key {
		case "name":
			t.Name, err = toStr(v)
		case "title":
			t.Title, err = toStr(v)
		case "submission_format":
			t.SubmissionFormat, err = toStrList(v, false)
		case "primary_statements":
			t.PrimaryStatements, err = toStrList(v, false)
		case "allowed_languages":
			t.AllowedLanguages, err = toStrList(v, true)
		case "token_mode":
			t.Tokens.Mode, err = toStr(v)
		case "token_max_number":
			t.Tokens.MaxNumber, err = toOptInt32(v)
		case "token_min_interval":
			t.Tokens.MinInterval, err = toDuration(v)
		case "token_gen_initial":
			t.Tokens.GenInitial, err = toInt32(v)
		case "token_gen_number":
			t.Tokens.GenNumber, err = toInt32(v)
		case "token_gen_interval":
			t.Tokens.GenInterval, err = toDuration(v)
		case "token_gen_max":
			t.Tokens.GenMax, err = toOptInt32(v)
		case "max_submission_number":
			t.MaxSubmissionNumber, err = toOptInt32(v)
		case "max_user_test_number":
			t.MaxUserTestNumber, err = toOptInt32(v)
		case "min_submission_interval":
			t.MinSubmissionInterval, err = toOptDuration(v)
		case "min_user_test_interval":
			t.MinUserTestInterval, err = toOptDuration(v)
		case "feedback_level":
			t.FeedbackLevel, err = toStr(v)
		case "score_precision":
			t.ScorePrecision, err = toInt32(v)
		case "score_mode":
			t.ScoreMode, err = toStr(v)
		default:
			err = errors.New("not a column of the task that cms_conf.py can set")
		}
		return err
	})
}

// applyDataset sets the columns of a dataset listed in its options.
func applyDataset(d *importer.Dataset, o *pyValue) error {
	return fields(o, func(key string, v pyValue) error {
		var err error
		switch key {
		case "description":
			d.Description, err = toStr(v)
		case "autojudge":
			if v.kind != pyBool {
				return fmt.Errorf("expected a bool, got %s", v.kind)
			}
			d.Autojudge = v.i != 0
		case "time_limit":
			if v.kind == pyNone {
				d.TimeLimit = nil
				break
			}
			if !v.isNumber() || v.kind == pyBool {
				return fmt.Errorf("expected a number of seconds, got %s", v.kind)
			}
			d.TimeLimit = importer.Ptr(v.float())
		case "memory_limit":
			if v.kind == pyNone {
				d.MemoryLimit = nil
				break
			}
			var n int64
			if n, err = toInt64(v); err == nil {
				d.MemoryLimit = &n
			}
		case "task_type":
			d.TaskType, err = toStr(v)
		case "score_type":
			d.ScoreType, err = toStr(v)
		case "task_type_parameters":
			d.TaskTypeParameters, err = pyJSON(v)
		case "score_type_parameters":
			d.ScoreTypeParameters, err = pyJSON(v)
		default:
			err = errors.New("not a column of the dataset that cms_conf.py can set")
		}
		return err
	})
}

func toStr(v pyValue) (string, error) {
	if v.kind != pyStr {
		return "", fmt.Errorf("expected a string, got %s", v.kind)
	}
	return v.s, nil
}

func toStrList(v pyValue, nullable bool) ([]string, error) {
	if nullable && v.kind == pyNone {
		return nil, nil
	}
	if v.kind != pyList && v.kind != pyTuple {
		return nil, fmt.Errorf("expected a list of strings, got %s", v.kind)
	}
	out := make([]string, len(v.items))
	for i, it := range v.items {
		s, err := toStr(it)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

func toInt64(v pyValue) (int64, error) {
	switch v.kind {
	case pyInt:
		return v.i, nil
	case pyFloat:
		// Upstream wants an int; a float with no fraction is taken.
		if v.f == math.Trunc(v.f) && math.Abs(v.f) < 1<<62 {
			return int64(v.f), nil
		}
	}
	return 0, fmt.Errorf("expected an integer, got %s", pyRepr(v))
}

func toInt32(v pyValue) (int32, error) {
	n, err := toInt64(v)
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%d is out of range", n)
	}
	return int32(n), nil
}

func toOptInt32(v pyValue) (*int32, error) {
	if v.kind == pyNone {
		return nil, nil
	}
	n, err := toInt32(v)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// toDuration takes a timedelta (as upstream) or a number of seconds.
func toDuration(v pyValue) (time.Duration, error) {
	switch {
	case v.kind == pyTimedelta:
		return time.Duration(v.i) * time.Microsecond, nil
	case v.isNumber() && v.kind != pyBool:
		s := v.float()
		if math.Abs(s) > math.MaxInt64/float64(time.Second) {
			return 0, errors.New("interval out of range")
		}
		return time.Duration(math.Round(s*1e6)) * time.Microsecond, nil
	}
	return 0, fmt.Errorf("expected a timedelta, got %s", v.kind)
}

func toOptDuration(v pyValue) (*time.Duration, error) {
	if v.kind == pyNone {
		return nil, nil
	}
	d, err := toDuration(v)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

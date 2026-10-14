// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"fmt"
	"io"
	"sync"
)

// Severity tells errors (the package cannot be imported) from warnings
// (it can, but something looks wrong).
type Severity string

// The severities.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Issue is a problem found in a package.
type Issue struct {
	Severity Severity `json:"severity"`
	// Where locates it: "task sum", "task sum: dataset \"Default\":
	// testcase 003", or a file of the package.
	Where   string `json:"where"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Where == "" {
		return string(i.Severity) + ": " + i.Message
	}
	return string(i.Severity) + ": " + i.Where + ": " + i.Message
}

// Report collects the issues found while loading, validating and writing a
// package: `cms ctl validate-task` prints it, the import commands print
// its warnings, and the zip import of the administration shows it as a
// preview. It is safe for concurrent use.
type Report struct {
	mu     sync.Mutex
	issues []Issue
}

// Errorf adds an error.
func (r *Report) Errorf(where, format string, args ...any) {
	r.add(SeverityError, where, fmt.Sprintf(format, args...))
}

// Warnf adds a warning.
func (r *Report) Warnf(where, format string, args ...any) {
	r.add(SeverityWarning, where, fmt.Sprintf(format, args...))
}

func (r *Report) add(s Severity, where, msg string) {
	r.mu.Lock()
	r.issues = append(r.issues, Issue{Severity: s, Where: where, Message: msg})
	r.mu.Unlock()
}

// Merge adds the issues of other.
func (r *Report) Merge(other *Report) {
	if other == nil || other == r {
		return
	}
	issues := other.Issues()
	r.mu.Lock()
	r.issues = append(r.issues, issues...)
	r.mu.Unlock()
}

// Issues returns the issues in the order they were found.
func (r *Report) Issues() []Issue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Issue(nil), r.issues...)
}

// Errors and Warnings return the issues of one severity.
func (r *Report) Errors() []Issue   { return r.filter(SeverityError) }
func (r *Report) Warnings() []Issue { return r.filter(SeverityWarning) }

func (r *Report) filter(s Severity) []Issue {
	var out []Issue
	for _, i := range r.Issues() {
		if i.Severity == s {
			out = append(out, i)
		}
	}
	return out
}

// HasErrors reports whether some issue is an error.
func (r *Report) HasErrors() bool { return len(r.Errors()) > 0 }

// Err returns a *ValidationError if the report has errors, else nil.
func (r *Report) Err() error {
	if n := len(r.Errors()); n > 0 {
		return &ValidationError{Report: r, Count: n}
	}
	return nil
}

// WriteTo prints one issue per line, errors first.
func (r *Report) WriteTo(w io.Writer) (int64, error) {
	var n int64
	for _, s := range []Severity{SeverityError, SeverityWarning} {
		for _, i := range r.filter(s) {
			m, err := fmt.Fprintln(w, i.String())
			n += int64(m)
			if err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// ValidationError is returned when a package has errors: nothing was
// written. Report has the details.
type ValidationError struct {
	Report *Report
	Count  int
}

func (e *ValidationError) Error() string {
	errs := e.Report.Errors()
	if len(errs) == 1 {
		return "invalid package: " + errs[0].String()
	}
	return fmt.Sprintf("invalid package: %d errors, the first: %s", e.Count, errs[0].String())
}

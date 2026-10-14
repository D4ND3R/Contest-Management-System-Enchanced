// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// TestRechecks: ids not visible are looked at again after 100ms, 200ms...
// 7 times, then given up; an id found is forgotten.
func TestRechecks(t *testing.T) {
	var r rechecks
	now := time.Unix(1000, 0)
	if again, gone := r.note(now, []int32{1, 2, 3}, []int32{2, 3}, false); again != 2 || gone != 0 {
		t.Fatal(again, gone)
	}
	if got := r.due(now.Add(99 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("due early: %+v", got)
	}
	if next := r.next(); !next.Equal(now.Add(recheckFirst)) {
		t.Fatalf("next %v", next)
	}
	now = now.Add(recheckFirst)
	got := r.due(now)
	if len(got) != 1 || got[0].ID != "" || got[0].Event.Kind != judging.EventSubmission ||
		!slices.Equal(got[0].Event.SubmissionIDs, []int32{2, 3}) {
		t.Fatalf("due: %+v", got)
	}
	// 2 is visible now; 3 never is.
	r.note(now, []int32{2, 3}, []int32{3}, false)
	if _, ok := r.tries[recheckKey{false, 2}]; ok {
		t.Error("2 still remembered")
	}
	looked, wait := 1, 2*recheckFirst
	for {
		if next := r.next(); !next.Equal(now.Add(wait)) {
			t.Fatalf("recheck %d: next in %v, want %v", looked+1, next.Sub(now), wait)
		}
		now = now.Add(wait)
		if got := r.due(now); len(got) != 1 {
			t.Fatalf("recheck %d: %+v", looked+1, got)
		}
		looked++
		again, gone := r.note(now, []int32{3}, []int32{3}, false)
		if gone == 1 {
			break
		}
		if again != 1 {
			t.Fatal(again, gone)
		}
		wait *= 2
	}
	if looked != recheckTries || len(r.tries) != 0 || !r.next().IsZero() {
		t.Errorf("given up after %d rechecks (want %d), %d remembered", looked, recheckTries, len(r.tries))
	}
	// User tests are separate.
	r.note(now, []int32{3}, []int32{3}, true)
	if got := r.due(now.Add(recheckFirst)); len(got) != 1 || got[0].Event.Kind != judging.EventUserTest {
		t.Errorf("user tests: %+v", got)
	}
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"slices"
	"testing"
	"time"
)

// TestPendingMissing: Pending reports the ids it was given and could not
// see (here: not committed yet, and never existed), so that the dispatcher
// looks at them again instead of waiting for a sweep.
func TestPendingMissing(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	seen := f.submit(p, task, time.Minute)
	seenUT := f.userTest(p, task, time.Minute, nil)

	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var unseen, unseenUT int32
	if err := tx.QueryRow(f.ctx, `INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		VALUES (999, $1, $2, $3, 'C++17 / g++') RETURNING id`, p, task.id, t0).Scan(&unseen); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(f.ctx, `INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		VALUES ($1, $2, $3, 'C++17 / g++', $4) RETURNING id`, p, task.id, t0, f.put("input")).Scan(&unseenUT); err != nil {
		t.Fatal(err)
	}

	w, err := f.j.Pending(f.ctx, Targets{
		SubmissionIDs: []int32{seen, unseen, unseen, 123456},
		UserTestIDs:   []int32{seenUT, unseenUT},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.MissingSubmissions, []int32{unseen, 123456}) || !slices.Equal(w.MissingUserTests, []int32{unseenUT}) {
		t.Errorf("missing: submissions %v, user tests %v", w.MissingSubmissions, w.MissingUserTests)
	}
	if len(w.Ops) != 2 {
		t.Errorf("operations %v, want the compilations of the visible ones", opKeys(w.Ops))
	}
}

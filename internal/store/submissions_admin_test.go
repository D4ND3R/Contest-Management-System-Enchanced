// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"slices"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// TestListSubmissionsAdminFilters covers the filters of the admin
// submission lists (R6.3): official, language and numbered pages
// (Offset), alone and with the others.
func TestListSubmissionsAdminFilters(t *testing.T) {
	f, _ := newFixture(t)
	ctx, s := f.ctx, f.s
	x := newSubmissionsFixture(f)
	_, err := s.Pool.Exec(ctx, `UPDATE submissions SET official = false WHERE id = $1`, x.s3.ID)
	check(t, err)
	_, err = s.Pool.Exec(ctx, `UPDATE submissions SET language = 'Python 3 / CPython' WHERE id = ANY($1)`, []int32{x.s2.ID, x.s6.ID})
	check(t, err)
	no, yes := false, true

	for _, tc := range []struct {
		name   string
		filter store.SubmissionFilter
		want   []q.Submission
	}{
		{"unofficial", store.SubmissionFilter{ContestID: x.c.ID, Official: &no}, []q.Submission{x.s3}},
		{"official", store.SubmissionFilter{ContestID: x.c.ID, Official: &yes}, []q.Submission{x.s6, x.s5, x.s4, x.s2, x.s1}},
		{"language", store.SubmissionFilter{ContestID: x.c.ID, Language: "Python 3 / CPython"}, []q.Submission{x.s6, x.s2}},
		{"language and task", store.SubmissionFilter{ContestID: x.c.ID, TaskID: x.b.ID, Language: "Python 3 / CPython"}, nil},
		{"language and status", store.SubmissionFilter{ContestID: x.c.ID, Language: "Python 3 / CPython", Status: store.StatusScored},
			[]q.Submission{x.s6}},
		{"offset", store.SubmissionFilter{ContestID: x.c.ID, Limit: 2, Offset: 2}, []q.Submission{x.s4, x.s3}},
		{"offset, last page", store.SubmissionFilter{ContestID: x.c.ID, Limit: 4, Offset: 4}, []q.Submission{x.s2, x.s1}},
		{"offset past the end", store.SubmissionFilter{ContestID: x.c.ID, Offset: 10}, nil},
		{"offset and participation", store.SubmissionFilter{ParticipationID: x.alice.ID, Limit: 1, Offset: 1}, []q.Submission{x.s1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := must[[]store.SubmissionRow](t)(s.ListSubmissions(ctx, tc.filter))
			if got, want := ids(rows), subIDs(tc.want...); !slices.Equal(got, want) {
				t.Fatalf("ids %v, want %v", got, want)
			}
			if tc.filter.Offset == 0 {
				n := must[int64](t)(s.CountSubmissions(ctx, tc.filter))
				if n != int64(len(tc.want)) {
					t.Fatalf("CountSubmissions = %d, want %d", n, len(tc.want))
				}
			}
		})
	}
	for _, off := range []int{-1, store.MaxOffset + 1} {
		if _, err := s.ListSubmissions(ctx, store.SubmissionFilter{ContestID: x.c.ID, Offset: off}); err == nil {
			t.Fatalf("ListSubmissions accepted offset %d", off)
		}
	}
}

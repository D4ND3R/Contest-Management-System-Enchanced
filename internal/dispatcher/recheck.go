// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// rechecks are the new submissions and user tests that a notification
// named and Pending could not see: most likely notified before the
// transaction that inserts them committed (judging.Notify asks for the
// opposite). Acknowledged with the rest, they would wait for the next
// periodic sweep; instead they are looked at again after 100ms, 200ms...
// 6.4s (7 times), then left to the sweep. The state is in memory: a new
// leader starts with a sweep anyway.
type rechecks struct {
	tries   map[recheckKey]int
	batches []recheckBatch
}

type recheckKey struct {
	userTest bool
	id       int32
}

// recheckBatch is a group of ids to look at again at a time.
type recheckBatch struct {
	at        time.Time
	subs, uts []int32
}

const (
	recheckFirst = 100 * time.Millisecond
	recheckTries = 7
	// maxRechecks bounds the ids remembered (notifications of ids that
	// never appear, sent by a broken client).
	maxRechecks = 1 << 16
)

// note records what Pending found for the asked ids of a kind: the
// missing ones are looked at again later, the others forgotten. It
// returns how many missing ids were given up (left to the sweep).
func (r *rechecks) note(now time.Time, asked, missing []int32, userTest bool) (rechecked, givenUp int) {
	if len(r.tries) > 0 && len(asked) > len(missing) {
		miss := make(map[int32]bool, len(missing))
		for _, id := range missing {
			miss[id] = true
		}
		for _, id := range asked {
			if !miss[id] {
				delete(r.tries, recheckKey{userTest, id})
			}
		}
	}
	if len(missing) == 0 {
		return 0, 0
	}
	if r.tries == nil {
		r.tries = map[recheckKey]int{}
	}
	byTries := map[int][]int32{}
	for _, id := range missing {
		k := recheckKey{userTest, id}
		n, seen := r.tries[k]
		if n >= recheckTries || !seen && len(r.tries) >= maxRechecks {
			delete(r.tries, k)
			givenUp++
			continue
		}
		r.tries[k] = n + 1
		byTries[n] = append(byTries[n], id)
		rechecked++
	}
	for n, ids := range byTries {
		b := recheckBatch{at: now.Add(recheckFirst << n)}
		if userTest {
			b.uts = ids
		} else {
			b.subs = ids
		}
		r.batches = append(r.batches, b)
	}
	return rechecked, givenUp
}

// due returns, as notifications without a stream id, the ids to look at
// again now.
func (r *rechecks) due(now time.Time) []judging.EventMessage {
	var out []judging.EventMessage
	keep := r.batches[:0]
	for _, b := range r.batches {
		if b.at.After(now) {
			keep = append(keep, b)
			continue
		}
		if len(b.subs) > 0 {
			out = append(out, judging.EventMessage{Event: judging.Event{Kind: judging.EventSubmission, SubmissionIDs: b.subs}})
		}
		if len(b.uts) > 0 {
			out = append(out, judging.EventMessage{Event: judging.Event{Kind: judging.EventUserTest, UserTestIDs: b.uts}})
		}
	}
	r.batches = keep
	return out
}

// next returns when the next batch is due (zero: none).
func (r *rechecks) next() time.Time {
	var t time.Time
	for _, b := range r.batches {
		if t.IsZero() || b.at.Before(t) {
			t = b.at
		}
	}
	return t
}

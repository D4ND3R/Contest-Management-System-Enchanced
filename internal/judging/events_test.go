// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func TestEvents(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()

	// Events sent before any dispatcher created the group are read too.
	big := make([]int32, 1500)
	for i := range big {
		big[i] = int32(i + 1)
	}
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := Notify(ctx, c,
		Event{Kind: EventSubmission, SubmissionIDs: []int32{1, 2}, At: at},
		Event{Kind: EventInvalidate, SubmissionIDs: big, UserTestIDs: []int32{9}, Level: LevelEvaluation, ArchiveSandbox: true},
	); err != nil {
		t.Fatal(err)
	}
	if err := Notify(ctx, c, Event{}); err == nil {
		t.Error("an event without kind was sent")
	}
	if err := Notify(ctx, c); err != nil {
		t.Error(err)
	}
	// An entry that is not an event.
	if err := c.Redis().XAdd(ctx, &redis.XAddArgs{Stream: c.Key(EventsStream), Values: []any{"x", "y"}}).Err(); err != nil {
		t.Fatal(err)
	}

	msgs, err := ReadEvents(ctx, c, "d1", 10, 100*time.Millisecond, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("%d events: %+v", len(msgs), msgs)
	}
	e := msgs[0].Event
	if msgs[0].Err != nil || e.Kind != EventSubmission || !slices.Equal(e.SubmissionIDs, []int32{1, 2}) || !e.At.Equal(at) {
		t.Errorf("first event %+v", msgs[0])
	}
	// The big invalidation is split in two.
	e1, e2 := msgs[1].Event, msgs[2].Event
	if len(e1.SubmissionIDs) != 1000 || len(e1.UserTestIDs) != 0 || len(e2.SubmissionIDs) != 500 ||
		!slices.Equal(e2.UserTestIDs, []int32{9}) || e2.Level != LevelEvaluation || !e2.ArchiveSandbox || e2.At.IsZero() {
		t.Errorf("split events %+v %+v", e1, e2)
	}
	if msgs[3].Err == nil {
		t.Errorf("an entry without event decoded: %+v", msgs[3])
	}

	// Not acknowledged: after a restart the consumer reads them again;
	// another consumer of the group does not get them.
	if more, err := ReadEvents(ctx, c, "d2", 10, 10*time.Millisecond, false); err != nil || len(more) != 0 {
		t.Errorf("second consumer: %+v %v", more, err)
	}
	pending, err := ReadEvents(ctx, c, "d1", 10, time.Second, true)
	if err != nil || len(pending) != 4 || pending[1].ID != msgs[1].ID {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if err := AckEvents(ctx, c, ids...); err != nil {
		t.Fatal(err)
	}
	if err := AckEvents(ctx, c); err != nil {
		t.Fatal(err)
	}
	if pending, err := ReadEvents(ctx, c, "d1", 10, 0, true); err != nil || len(pending) != 0 {
		t.Errorf("after ack: %+v %v", pending, err)
	}
	// Nothing new: a read without blocking returns at once.
	start := time.Now()
	if more, err := ReadEvents(ctx, c, "d1", 10, 0, false); err != nil || len(more) != 0 || time.Since(start) > time.Second {
		t.Errorf("empty read: %+v %v", more, err)
	}
	// A new event wakes a blocked reader.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = Notify(ctx, c, Event{Kind: EventSweep, ContestID: 3})
	}()
	got, err := ReadEvents(ctx, c, "d1", 10, 5*time.Second, false)
	if err != nil || len(got) != 1 || got[0].Event.Kind != EventSweep || got[0].Event.ContestID != 3 {
		t.Errorf("blocked read: %+v %v", got, err)
	}

	// A dispatcher that died: another one claims its events once idle.
	if claimed, err := ClaimEvents(ctx, c, "d2", 0, 10); err != nil || len(claimed) != 1 ||
		claimed[0].Event.Kind != EventSweep {
		t.Errorf("claimed: %+v %v", claimed, err)
	}
	if pending, err := ReadEvents(ctx, c, "d1", 10, 0, true); err != nil || len(pending) != 0 {
		t.Errorf("d1 still has %+v %v", pending, err)
	}
	if claimed, err := ClaimEvents(ctx, c, "d1", time.Hour, 10); err != nil || len(claimed) != 0 {
		t.Errorf("claimed events that are not idle: %+v %v", claimed, err)
	}
	if claimed, err := ClaimEvents(ctx, testutil.Redis(t), "d1", 0, 10); err != nil || len(claimed) != 0 {
		t.Errorf("claimed without a group: %+v %v", claimed, err)
	}

	if _, err := DecodeEvent(map[string]any{"event": "{"}); err == nil {
		t.Error("invalid JSON decoded")
	}
	if _, err := DecodeEvent(map[string]any{"event": "{}"}); err == nil {
		t.Error("an event without kind decoded")
	}
}

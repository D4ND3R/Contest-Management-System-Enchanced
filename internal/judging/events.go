// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// The notifications to the dispatcher (upstream: the RPCs new_submission,
// new_user_test, submission_tokened, invalidate_submission,
// search_operations_not_done...) travel through a durable Valkey stream,
// so they survive a restart of the dispatcher; it reads them with a
// consumer group and acknowledges them once handled. Losing one is
// harmless: the periodic Sweep finds the work anyway, the notification
// only makes it immediate.
const (
	// EventsStream is the stream key, under the key prefix of the
	// installation (kv.Client.Key).
	EventsStream = "judging:events"
	// EventsGroup is the consumer group of the dispatchers.
	EventsGroup = "dispatcher"
	// EventsMaxLen bounds the stream (approximately): older events are
	// trimmed if no dispatcher reads them for a long time.
	EventsMaxLen = 100000
	// eventField is the field of a stream entry that holds the event.
	eventField = "event"
	// maxEventIDs bounds the ids of one event; Notify splits larger ones.
	maxEventIDs = 1000
)

// EventKind says what happened.
type EventKind string

// The kinds of events.
const (
	// EventSubmission: new submissions (SubmissionIDs). The dispatcher
	// refreshes their task scores (RefreshTaskScoresOf) and enqueues
	// their work (Pending).
	EventSubmission EventKind = "submission"
	// EventUserTest: new user tests (UserTestIDs).
	EventUserTest EventKind = "user_test"
	// EventToken: tokens played on SubmissionIDs (the site refreshed the
	// task scores in its transaction); the dispatcher sends them to the
	// scoreboard.
	EventToken EventKind = "token"
	// EventInvalidate: results were invalidated (Invalidation.Events):
	// SubmissionIDs and UserTestIDs need work again, with ArchiveSandbox.
	EventInvalidate EventKind = "invalidate"
	// EventDataset: the active dataset of TaskID changed, or the
	// autojudge flag of DatasetID, or the task or its active dataset was
	// edited: the submissions of the task need a sweep (ContestID; upstream
	// search_operations_not_done) and the scoreboard the scores of the
	// task on its active dataset again (upstream ProxyService
	// dataset_updated).
	EventDataset EventKind = "dataset"
	// EventSweep asks for a sweep of ContestID (0: every contest) now.
	EventSweep EventKind = "sweep"
)

// Event is a notification for the dispatcher. It is encoded as JSON in
// the field "event" of a stream entry.
type Event struct {
	Kind           EventKind `json:"kind"`
	ContestID      int32     `json:"contest_id,omitempty"`
	TaskID         int32     `json:"task_id,omitempty"`
	DatasetID      int32     `json:"dataset_id,omitempty"`
	SubmissionIDs  []int32   `json:"submission_ids,omitempty"`
	UserTestIDs    []int32   `json:"user_test_ids,omitempty"`
	Level          Level     `json:"level,omitempty"`
	ArchiveSandbox bool      `json:"archive_sandbox,omitempty"`
	// At is when the event was sent (Notify sets it if zero).
	At time.Time `json:"at"`
}

// Events returns the notifications of an invalidation, with at most 1000
// ids each.
func (inv *Invalidation) Events(archiveSandbox bool) []Event {
	var out []Event
	subs, uts := inv.Submissions, inv.UserTests
	for len(subs) > 0 || len(uts) > 0 {
		e := Event{Kind: EventInvalidate, Level: inv.Level, ArchiveSandbox: archiveSandbox}
		n := min(len(subs), maxEventIDs)
		e.SubmissionIDs, subs = subs[:n], subs[n:]
		m := min(len(uts), maxEventIDs-n)
		e.UserTestIDs, uts = uts[:m], uts[m:]
		out = append(out, e)
	}
	return out
}

// Notify sends events to the dispatcher in one round trip. Events with
// more than 1000 ids are split.
func Notify(ctx context.Context, c *kv.Client, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	pipe := c.Redis().Pipeline()
	now := time.Now().UTC()
	key := c.Key(EventsStream)
	for _, e := range events {
		if e.Kind == "" {
			return errors.New("judging: event without kind")
		}
		if e.At.IsZero() {
			e.At = now
		}
		for _, part := range split(e) {
			b, err := json.Marshal(part)
			if err != nil {
				return err
			}
			pipe.XAdd(ctx, &redis.XAddArgs{
				Stream: key, MaxLen: EventsMaxLen, Approx: true,
				Values: []any{eventField, b},
			})
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

// split splits an event with too many ids.
func split(e Event) []Event {
	if len(e.SubmissionIDs)+len(e.UserTestIDs) <= maxEventIDs {
		return []Event{e}
	}
	var out []Event
	subs, uts := e.SubmissionIDs, e.UserTestIDs
	for len(subs) > 0 || len(uts) > 0 {
		p := e
		n := min(len(subs), maxEventIDs)
		p.SubmissionIDs, subs = subs[:n], subs[n:]
		m := min(len(uts), maxEventIDs-n)
		p.UserTestIDs, uts = uts[:m], uts[m:]
		out = append(out, p)
	}
	return out
}

// EventMessage is an event read from the stream.
type EventMessage struct {
	// ID is the stream entry, for AckEvents.
	ID    string
	Event Event
	// Err is set when the entry cannot be decoded (acknowledge it anyway).
	Err error
}

// DecodeEvent decodes the values of a stream entry.
func DecodeEvent(values map[string]any) (Event, error) {
	var e Event
	raw, ok := values[eventField].(string)
	if !ok {
		return e, fmt.Errorf("judging: stream entry without %q", eventField)
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return e, fmt.Errorf("judging: invalid event: %w", err)
	}
	if e.Kind == "" {
		return e, errors.New("judging: event without kind")
	}
	return e, nil
}

// ReadEvents reads up to count events for consumer (one name per
// dispatcher process), waiting up to block for new ones (block <= 0: not
// at all; keep it below the read timeout of the client). It creates the
// consumer group on first use (from the start of the stream). With
// pending set it returns instead the events this consumer read and did
// not acknowledge (after a restart: read them first).
func ReadEvents(ctx context.Context, c *kv.Client, consumer string, count int64, block time.Duration, pending bool) ([]EventMessage, error) {
	key := c.Key(EventsStream)
	start := ">"
	if pending {
		start = "0"
	}
	args := &redis.XReadGroupArgs{Group: EventsGroup, Consumer: consumer, Streams: []string{key, start},
		Count: count, Block: block}
	if pending || block <= 0 {
		args.Block = -1 // do not wait (BLOCK 0 would wait forever)
	}
	res, err := c.Redis().XReadGroup(ctx, args).Result()
	if err != nil && strings.HasPrefix(err.Error(), "NOGROUP") {
		if err := c.Redis().XGroupCreateMkStream(ctx, key, EventsGroup, "0").Err(); err != nil &&
			!strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return nil, err
		}
		res, err = c.Redis().XReadGroup(ctx, args).Result()
	}
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []EventMessage
	for _, s := range res {
		for _, m := range s.Messages {
			e, err := DecodeEvent(m.Values)
			out = append(out, EventMessage{ID: m.ID, Event: e, Err: err})
		}
	}
	return out, nil
}

// ClaimEvents takes over, for consumer, up to count events that another
// consumer read more than minIdle ago and never acknowledged: a
// dispatcher that died and does not come back under the same name (one
// that does reads them again with ReadEvents and pending set). Losing
// them would be harmless (the sweep), but they would stay pending in the
// group forever.
func ClaimEvents(ctx context.Context, c *kv.Client, consumer string, minIdle time.Duration, count int64) ([]EventMessage, error) {
	msgs, _, err := c.Redis().XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: c.Key(EventsStream), Group: EventsGroup, Consumer: consumer,
		MinIdle: minIdle, Start: "0-0", Count: count,
	}).Result()
	if errors.Is(err, redis.Nil) || err != nil && strings.HasPrefix(err.Error(), "NOGROUP") {
		return nil, nil // nothing was ever read
	}
	if err != nil {
		return nil, err
	}
	out := make([]EventMessage, 0, len(msgs))
	for _, m := range msgs {
		e, err := DecodeEvent(m.Values)
		out = append(out, EventMessage{ID: m.ID, Event: e, Err: err})
	}
	return out, nil
}

// AckEvents acknowledges handled events.
func AckEvents(ctx context.Context, c *kv.Client, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return c.Redis().XAck(ctx, c.Key(EventsStream), EventsGroup, ids...).Err()
}

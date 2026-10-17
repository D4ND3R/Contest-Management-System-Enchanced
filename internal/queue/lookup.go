// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// ErrNoMessage: Get found no message with that id (it was acknowledged,
// dead-lettered or deleted meanwhile).
var ErrNoMessage = errors.New("queue: no such message")

// Get returns the message id of priority prio as it is now in the queue,
// ready or in flight, without taking it: the judges page of the
// administration shows it and acts on it. For a message in flight
// Consumer is the consumer holding it and Deliveries its deliveries (the
// message can then be given back with Nack); for a ready one both are
// zero. Ack on the returned message removes it from the queue for good
// (and frees its dedup key), whether ready or in flight.
func (q *Queue) Get(ctx context.Context, prio int, id string) (*Message, error) {
	if prio < 0 || prio >= len(q.streams) {
		return nil, fmt.Errorf("queue %s: no priority %d", q.name, prio)
	}
	stream := q.streams[prio]
	pipe := q.rdb.Pipeline()
	entry := pipe.XRangeN(ctx, stream, id, id, 1)
	pending := pipe.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: Group, Start: id, End: id, Count: 1})
	_, _ = pipe.Exec(ctx)
	if err := entry.Err(); err != nil {
		return nil, fmt.Errorf("queue %s: get: %w", q.name, err)
	}
	if len(entry.Val()) == 0 {
		return nil, ErrNoMessage
	}
	consumer, deliveries := "", 0
	if err := pending.Err(); err == nil && len(pending.Val()) > 0 {
		p := pending.Val()[0]
		consumer, deliveries = p.Consumer, int(p.RetryCount)
	} else if err != nil && !missingGroup(err) {
		return nil, fmt.Errorf("queue %s: get: %w", q.name, err)
	}
	m := entry.Val()[0]
	fields := make([]any, 0, 2*len(m.Values))
	for k, v := range m.Values {
		fields = append(fields, k, v)
	}
	// A malformed message is returned too: it can still be dropped.
	msg, _ := q.parse(prio, m.ID, fields, deliveries, consumer)
	return msg, nil
}

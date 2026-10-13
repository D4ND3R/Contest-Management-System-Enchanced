// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package queue is a durable priority queue on Valkey Streams (D1). The
// dispatcher sends compilation and evaluation jobs to the workers through
// one, and the workers send results back through another (with a single
// priority).
//
// A queue has P priorities (0 is the highest) and keeps, under the
// installation prefix (kv.Client.Key):
//
//	queue:<name>:p<i>    one stream per priority, with one consumer group
//	queue:<name>:delayed sorted set of messages waiting for a retry
//	queue:<name>:dedup   hash dedup key -> message currently holding it
//	queue:<name>:signal  list of wake-up tokens for blocked consumers
//	queue:<name>:dead    stream of dead letters
//
// Every operation is one Lua script (scripts.go), hence atomic.
//
// Guarantees, all covered by the tests:
//
//   - At-least-once delivery: a message leaves the queue only when it is
//     acknowledged (Ack, AckAndEnqueue), dead-lettered or explicitly
//     deleted. If its consumer dies before acknowledging it, another
//     consumer gets it once it has been idle for the visibility timeout;
//     long jobs keep it with Extend (KeepAlive).
//   - No duplicates in normal operation: the consumer group hands each
//     message to one consumer, and Extend/Retry/Nack refuse to act on a
//     message the consumer no longer holds (ErrNotOwner). Duplicates are
//     only possible when a consumer stalls for longer than the visibility
//     timeout, which is why results must be idempotent (D21).
//   - Strict priority: Consume always returns a message of the highest
//     priority that has one ready; within a priority, the oldest first
//     (abandoned messages, which are older, before new ones).
//   - Retries with exponential backoff (Retry); after MaxAttempts the
//     message goes to the dead letters. A message whose consumers die
//     MaxDeliveries times (a poison message) is dead-lettered as well.
//   - Optional deduplication: while a message with a dedup key is queued,
//     in flight or waiting for a retry, Enqueue with the same key adds
//     nothing.
//
// Abandoned messages are reclaimed inside the consume script with XPENDING
// IDLE and XCLAIM rather than XAUTOCLAIM, so the delivery count is checked
// before a poison message is handed out again.
//
// Valkey must persist its data (appendonly) for the queue to survive a
// restart of Valkey itself; if it loses everything, the consumer groups
// are recreated on the fly and the dispatcher's sweeper re-enqueues what
// the database says is unfinished. The scripts touch every key of a queue
// (and AckAndEnqueue those of two queues) in one call, so the queues need
// a single Valkey primary (Sentinel for failover), not Valkey Cluster.
package queue

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// Group is the consumer group of every stream.
const Group = "cms"

// MaxPriorities bounds Options.Priorities.
const MaxPriorities = 16

// maxBatch is the number of messages EnqueueBatch adds per script call, so
// a large batch does not block Valkey for long.
const maxBatch = 256

// maxErrorLen bounds the error text stored with a retried message.
const maxErrorLen = 4096

// Errors.
var (
	// ErrNotOwner: the consumer no longer holds the message (it was
	// acknowledged, dead-lettered, or reclaimed by another consumer after
	// the visibility timeout). The consumer should stop working on it.
	ErrNotOwner = errors.New("queue: the message is no longer held by this consumer")
	// ErrDifferentServer: AckAndEnqueue needs both queues on one client.
	ErrDifferentServer = errors.New("queue: both queues must use the same Valkey client")
)

// Options configures a queue. The zero value of each field means its
// default.
type Options struct {
	// Priorities is the number of priorities (default 1); 0 is the
	// highest.
	Priorities int
	// Visibility is how long a delivered message may stay idle (neither
	// acknowledged nor extended) before another consumer reclaims it
	// (default 30s). Consumers of long jobs call Extend (KeepAlive) more
	// often than this.
	Visibility time.Duration
	// MaxAttempts is the number of attempts before Retry dead-letters a
	// message (default 3, like CMS MAX_*_TRIES).
	MaxAttempts int
	// MaxDeliveries is the number of deliveries without acknowledgement
	// (consumers that died) before a message is dead-lettered (default 5).
	MaxDeliveries int
	// BackoffBase is the delay before the second attempt; it doubles with
	// each attempt up to BackoffMax (defaults 1s and 1m).
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// BackoffJitter shortens each delay by a random fraction in
	// [0, BackoffJitter) so that jobs that failed together do not all come
	// back together (default 0: deterministic; must be < 1).
	BackoffJitter float64
	// PollInterval is how often an idle consumer looks for abandoned and
	// delayed messages, which do not wake it (default 1s).
	PollInterval time.Duration
	// DeadMaxLen bounds the dead-letter stream (default 10000, trimmed
	// approximately).
	DeadMaxLen int64
}

func (o Options) withDefaults() (Options, error) {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	if o.Priorities == 0 {
		o.Priorities = 1
	}
	def(&o.Visibility, 30*time.Second)
	def(&o.BackoffBase, time.Second)
	def(&o.BackoffMax, time.Minute)
	def(&o.PollInterval, time.Second)
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.MaxDeliveries == 0 {
		o.MaxDeliveries = 5
	}
	if o.DeadMaxLen == 0 {
		o.DeadMaxLen = 10000
	}
	switch {
	case o.Priorities < 1 || o.Priorities > MaxPriorities:
		return o, fmt.Errorf("queue: priorities must be between 1 and %d", MaxPriorities)
	case o.Visibility < 10*time.Millisecond:
		return o, errors.New("queue: the visibility timeout must be at least 10ms")
	case o.MaxAttempts < 1 || o.MaxDeliveries < 1:
		return o, errors.New("queue: max attempts and max deliveries must be at least 1")
	case o.BackoffBase < 0 || o.BackoffMax < o.BackoffBase:
		return o, errors.New("queue: backoff must satisfy 0 <= base <= max")
	case o.BackoffJitter < 0 || o.BackoffJitter >= 1:
		return o, errors.New("queue: backoff jitter must be in [0, 1)")
	case o.PollInterval < time.Millisecond:
		return o, errors.New("queue: the poll interval must be at least 1ms")
	case o.DeadMaxLen < 1:
		return o, errors.New("queue: the dead-letter bound must be positive")
	}
	return o, nil
}

// Queue is a named durable priority queue. It is safe for concurrent use.
type Queue struct {
	rdb  redis.UniversalClient
	name string
	opts Options

	streams []string
	delayed string
	dedup   string
	signal  string
	dead    string
	// block is streams, delayed, dedup, signal, dead: the KEYS of most
	// scripts.
	block []string

	n counters
}

// counters are the operations done by this process (Collector).
type counters struct {
	enqueued, duplicates, delivered, redelivered, acked, retried, dead atomic.Int64
}

// ValidName reports whether s can name a queue: 1 to 64 characters among
// a-z, 0-9, "_" and "-", starting with a letter or digit.
func ValidName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '_' || c == '-')
		if !ok {
			return false
		}
	}
	return true
}

// Open returns the queue name on c, creating its streams and consumer
// group if needed and loading its scripts.
func Open(ctx context.Context, c *kv.Client, name string, opts Options) (*Queue, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("queue: invalid name %q", name)
	}
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	q := &Queue{
		rdb:     c.Redis(),
		name:    name,
		opts:    o,
		delayed: c.Key("queue", name, "delayed"),
		dedup:   c.Key("queue", name, "dedup"),
		signal:  c.Key("queue", name, "signal"),
		dead:    c.Key("queue", name, "dead"),
	}
	for i := range o.Priorities {
		q.streams = append(q.streams, c.Key("queue", name, "p"+strconv.Itoa(i)))
	}
	q.block = append(append([]string{}, q.streams...), q.delayed, q.dedup, q.signal, q.dead)
	if err := q.ensureGroups(ctx); err != nil {
		return nil, err
	}
	// One call per script, not a pipeline: Script.Load records the SHA the
	// server returns, which a pipelined command does not have yet (it would
	// record "" and turn every later EVALSHA into a failed EVALSHA plus an
	// EVAL).
	for _, s := range allScripts {
		if err := s.Load(ctx, q.rdb).Err(); err != nil {
			return nil, fmt.Errorf("queue %s: loading scripts: %w", name, err)
		}
	}
	return q, nil
}

// Name returns the name of the queue.
func (q *Queue) Name() string { return q.name }

// Options returns the effective options (defaults filled in).
func (q *Queue) Options() Options { return q.opts }

// ensureGroups creates the streams and their consumer group. The group
// starts at 0 so messages added before it existed are delivered too.
func (q *Queue) ensureGroups(ctx context.Context) error {
	pipe := q.rdb.Pipeline()
	cmds := make([]*redis.StatusCmd, len(q.streams))
	for i, s := range q.streams {
		cmds[i] = pipe.XGroupCreateMkStream(ctx, s, Group, "0")
	}
	_, _ = pipe.Exec(ctx)
	for i, cmd := range cmds {
		if err := cmd.Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("queue %s: creating the group of %s: %w", q.name, q.streams[i], err)
		}
	}
	return nil
}

// missingGroup reports whether err means a stream or its group vanished
// (Valkey lost its data or someone deleted the keys).
func missingGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOGROUP")
}

// run runs a script; if a stream or its group vanished it recreates them
// and runs the script once more.
func (q *Queue) run(ctx context.Context, s *redis.Script, keys []string, args ...any) (any, error) {
	v, err := s.Run(ctx, q.rdb, keys, args...).Result()
	if missingGroup(err) {
		if gerr := q.ensureGroups(ctx); gerr != nil {
			return nil, gerr
		}
		v, err = s.Run(ctx, q.rdb, keys, args...).Result()
	}
	return v, err
}

// EnqueueOptions are the optional settings of a message.
type EnqueueOptions struct {
	// DedupKey, if set, makes Enqueue a no-op while another message with
	// the same key is queued, in flight or waiting for a retry. It must
	// identify the logical job including anything that makes a new
	// version of it (for jobs: the operation and its generation).
	DedupKey string
	// Attempt is the attempt number of the message (default 1), for
	// callers that retry by enqueueing again.
	Attempt int
	// Delay postpones delivery.
	Delay time.Duration
}

// Item is one message of EnqueueBatch.
type Item struct {
	Priority int
	Payload  []byte
	Options  EnqueueOptions
}

// Enqueued says what Enqueue did with a message.
type Enqueued struct {
	// ID identifies the message holding the dedup key: the new message,
	// or the one already queued when Added is false.
	ID string
	// Added is false when the dedup key was already taken.
	Added bool
}

// Enqueue adds a message with the given priority.
func (q *Queue) Enqueue(ctx context.Context, prio int, payload []byte, opts EnqueueOptions) (Enqueued, error) {
	res, err := q.EnqueueBatch(ctx, []Item{{Priority: prio, Payload: payload, Options: opts}})
	if err != nil {
		return Enqueued{}, err
	}
	return res[0], nil
}

// EnqueueBatch adds several messages in one round trip per 256 messages;
// each group of 256 is atomic.
func (q *Queue) EnqueueBatch(ctx context.Context, items []Item) ([]Enqueued, error) {
	out := make([]Enqueued, 0, len(items))
	for start := 0; start < len(items); start += maxBatch {
		chunk := items[start:min(start+maxBatch, len(items))]
		args := make([]any, 0, 2+6*len(chunk))
		args = append(args, len(q.streams), len(chunk))
		for _, it := range chunk {
			if it.Priority < 0 || it.Priority >= len(q.streams) {
				return out, fmt.Errorf("queue %s: priority %d out of range [0, %d)", q.name, it.Priority, len(q.streams))
			}
			o := it.Options
			if o.Attempt == 0 {
				o.Attempt = 1
			}
			if o.Attempt < 0 || o.Delay < 0 {
				return out, fmt.Errorf("queue %s: negative attempt or delay", q.name)
			}
			var nonce string
			delay := delayMs(o.Delay)
			if delay > 0 {
				nonce = strconv.FormatUint(rand.Uint64(), 36) + strconv.FormatUint(rand.Uint64(), 36)
			}
			args = append(args, it.Priority, o.Attempt, delay, nonce, o.DedupKey, it.Payload)
		}
		v, err := q.run(ctx, enqueueScript, q.block, args...)
		if err != nil {
			return out, fmt.Errorf("queue %s: enqueue: %w", q.name, err)
		}
		vals, _ := v.([]any)
		if len(vals) != 2*len(chunk) {
			return out, fmt.Errorf("queue %s: enqueue: unexpected reply %v", q.name, v)
		}
		for i := range chunk {
			e := Enqueued{ID: str(vals[2*i]), Added: num(vals[2*i+1]) == 1}
			if e.Added {
				q.n.enqueued.Add(1)
			} else {
				q.n.duplicates.Add(1)
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// Holding reports, for each dedup key, whether a message holding it is
// queued, in flight or waiting for a retry (Enqueue with that key would
// add nothing), in one round trip per 1000 keys. The answer can change
// right after: a producer that is the only one to enqueue those keys
// uses it to skip building messages that are already there.
func (q *Queue) Holding(ctx context.Context, keys []string) ([]bool, error) {
	out := make([]bool, 0, len(keys))
	for start := 0; start < len(keys); start += 1000 {
		chunk := keys[start:min(start+1000, len(keys))]
		vals, err := q.rdb.HMGet(ctx, q.dedup, chunk...).Result()
		if err != nil {
			return nil, fmt.Errorf("queue %s: reading dedup keys: %w", q.name, err)
		}
		for _, v := range vals {
			out = append(out, v != nil)
		}
	}
	return out, nil
}

// delayMs rounds a positive delay up to whole milliseconds.
func delayMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}

// Message is a delivered message.
type Message struct {
	// ID is the stream entry id ("<ms>-<seq>").
	ID       string
	Priority int
	// Attempt is 1 for the first attempt and grows with each Retry.
	Attempt int
	// Deliveries counts the deliveries of this attempt: more than 1 means
	// earlier consumers died (or stalled) without acknowledging it.
	Deliveries int
	Payload    []byte
	DedupKey   string
	// LastError is the cause given to Retry by the previous attempt.
	LastError string
	// EnqueuedAt is when this attempt entered its stream (server clock).
	EnqueuedAt time.Time
	// Consumer is the consumer holding the message.
	Consumer string
}

// Consume returns the next message for consumer, waiting up to block for
// one (block <= 0: do not wait). It returns nil, nil when none arrived.
//
// Each consumer (each concurrent job slot of a worker) must use its own
// name, stable across restarts of its process: at startup, Release gives
// back what the previous incarnation held.
func (q *Queue) Consume(ctx context.Context, consumer string, block time.Duration) (*Message, error) {
	if consumer == "" {
		return nil, errors.New("queue: empty consumer name")
	}
	deadline := time.Now().Add(block)
	for {
		msg, nextDue, err := q.pick(ctx, consumer)
		if err != nil || msg != nil {
			return msg, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, nil
		}
		wait := min(left, q.opts.PollInterval)
		if nextDue > 0 && nextDue < wait {
			wait = nextDue
		}
		q.wait(ctx, wait)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// pick runs pickScript. It returns the message, or the time until the
// next delayed message is due (0: none).
func (q *Queue) pick(ctx context.Context, consumer string) (*Message, time.Duration, error) {
	for {
		v, err := q.run(ctx, pickScript, q.block, Group, consumer, q.opts.Visibility.Milliseconds(),
			q.opts.MaxDeliveries, q.opts.DeadMaxLen, len(q.streams))
		if err != nil {
			return nil, 0, fmt.Errorf("queue %s: consume: %w", q.name, err)
		}
		vals, _ := v.([]any)
		if len(vals) == 2 {
			if due := num(vals[1]); due > 0 {
				return nil, time.Duration(due) * time.Millisecond, nil
			}
			return nil, 0, nil
		}
		if len(vals) != 4 {
			return nil, 0, fmt.Errorf("queue %s: consume: unexpected reply %v", q.name, v)
		}
		msg, perr := q.parse(int(num(vals[0])), str(vals[1]), vals[2], int(num(vals[3])), consumer)
		if perr != nil {
			// Not written by Enqueue: keep it out of the way and go on.
			if _, err := q.retry(ctx, msg, perr.Error(), 0); err != nil {
				return nil, 0, err
			}
			continue
		}
		if msg.Deliveries > 1 {
			q.n.redelivered.Add(1)
		} else {
			q.n.delivered.Add(1)
		}
		return msg, 0, nil
	}
}

// parse builds a message from a stream entry. On error it still returns a
// message usable to dead-letter the entry.
func (q *Queue) parse(prio int, id string, fields any, deliveries int, consumer string) (*Message, error) {
	msg := &Message{ID: id, Priority: prio, Deliveries: deliveries, Consumer: consumer, Attempt: 1}
	msg.EnqueuedAt, _ = idTime(id)
	f, _ := fields.([]any)
	var hasPayload, hasAttempt bool
	for i := 0; i+1 < len(f); i += 2 {
		v := str(f[i+1])
		switch str(f[i]) {
		case "p":
			msg.Payload, hasPayload = []byte(v), true
		case "a":
			n, err := strconv.Atoi(v)
			if err == nil && n >= 1 {
				msg.Attempt, hasAttempt = n, true
			}
		case "d":
			msg.DedupKey = v
		case "e":
			msg.LastError = v
		}
	}
	if !hasPayload || !hasAttempt {
		return msg, errors.New("malformed message: no payload or attempt")
	}
	return msg, nil
}

// wait blocks until a wake-up token arrives or d elapses. Errors are
// ignored: the next pick reports them.
func (q *Queue) wait(ctx context.Context, d time.Duration) {
	if d >= time.Second {
		// The typed command extends the read deadline of the connection.
		_ = q.rdb.BLPop(ctx, d.Truncate(time.Second), q.signal).Err()
		return
	}
	// BLPop only takes whole seconds; shorter waits stay under the default
	// read timeout of the client.
	d = max(d, time.Millisecond)
	_ = q.rdb.Do(ctx, "blpop", q.signal, strconv.FormatFloat(d.Seconds(), 'f', 3, 64)).Err()
}

// Ack acknowledges a message: it leaves the queue for good and its dedup
// key is freed. Acknowledging a message twice, or one another consumer
// reclaimed, is harmless.
func (q *Queue) Ack(ctx context.Context, msg *Message) error {
	stream, err := q.streamOf(msg)
	if err != nil {
		return err
	}
	if _, err := ackScript.Run(ctx, q.rdb, []string{stream, q.dedup}, Group, msg.ID, msg.DedupKey).Result(); err != nil {
		return fmt.Errorf("queue %s: ack: %w", q.name, err)
	}
	q.n.acked.Add(1)
	return nil
}

// AckAndEnqueue atomically acknowledges msg and enqueues payload on dst
// (a worker acknowledges a job and publishes its result). If msg was no
// longer pending (another consumer finished it after reclaiming it),
// nothing is enqueued and acked is false. Both queues must use the same
// client.
func (q *Queue) AckAndEnqueue(ctx context.Context, msg *Message, dst *Queue, prio int, payload []byte, opts EnqueueOptions) (acked bool, err error) {
	if q.rdb != dst.rdb {
		return false, ErrDifferentServer
	}
	stream, err := q.streamOf(msg)
	if err != nil {
		return false, err
	}
	if prio < 0 || prio >= len(dst.streams) {
		return false, fmt.Errorf("queue %s: priority %d out of range [0, %d)", dst.name, prio, len(dst.streams))
	}
	if opts.Attempt == 0 {
		opts.Attempt = 1
	}
	var nonce string
	delay := delayMs(opts.Delay)
	if delay > 0 {
		nonce = strconv.FormatUint(rand.Uint64(), 36) + strconv.FormatUint(rand.Uint64(), 36)
	}
	keys := append([]string{stream, q.dedup}, dst.block...)
	v, err := dst.run(ctx, ackEnqueueScript, keys, Group, msg.ID, msg.DedupKey, len(dst.streams),
		prio, opts.Attempt, delay, nonce, opts.DedupKey, payload)
	if err != nil {
		return false, fmt.Errorf("queue %s: ack and enqueue on %s: %w", q.name, dst.name, err)
	}
	vals, _ := v.([]any)
	if len(vals) != 3 {
		return false, fmt.Errorf("queue %s: ack and enqueue: unexpected reply %v", q.name, v)
	}
	if num(vals[0]) != 1 {
		return false, nil
	}
	q.n.acked.Add(1)
	if num(vals[2]) == 1 {
		dst.n.enqueued.Add(1)
	} else {
		dst.n.duplicates.Add(1)
	}
	return true, nil
}

// Backoff returns the delay before the attempt after attempt:
// BackoffBase·2^(attempt-1), at most BackoffMax, minus the jitter.
func (q *Queue) Backoff(attempt int) time.Duration {
	d := q.opts.BackoffBase
	for i := 1; i < attempt && d < q.opts.BackoffMax; i++ {
		d *= 2
	}
	d = min(d, q.opts.BackoffMax)
	if q.opts.BackoffJitter > 0 {
		d -= time.Duration(rand.Float64() * q.opts.BackoffJitter * float64(d))
	}
	return d
}

// Retry gives up the current attempt of a held message because of cause:
// the message comes back after Backoff(msg.Attempt) with Attempt+1, or
// goes to the dead letters (dead is true) if it already had MaxAttempts.
// It returns ErrNotOwner if the consumer no longer holds the message.
func (q *Queue) Retry(ctx context.Context, msg *Message, cause error) (dead bool, err error) {
	text := "retry requested"
	if cause != nil {
		text = cause.Error()
	}
	return q.retry(ctx, msg, text, q.opts.MaxAttempts)
}

func (q *Queue) retry(ctx context.Context, msg *Message, cause string, maxAttempts int) (bool, error) {
	if _, err := q.streamOf(msg); err != nil {
		return false, err
	}
	if len(cause) > maxErrorLen {
		cause = cause[:maxErrorLen]
	}
	v, err := q.run(ctx, retryScript, q.block, Group, msg.Consumer, msg.ID, msg.Priority, msg.Attempt+1,
		maxAttempts, q.Backoff(msg.Attempt).Milliseconds(), msg.Payload, msg.DedupKey, cause,
		q.opts.DeadMaxLen, len(q.streams))
	if err != nil {
		return false, fmt.Errorf("queue %s: retry: %w", q.name, err)
	}
	switch str(v) {
	case "retry":
		q.n.retried.Add(1)
		return false, nil
	case "dead":
		q.n.dead.Add(1)
		return true, nil
	case "notowner":
		return false, ErrNotOwner
	}
	return false, fmt.Errorf("queue %s: retry: unexpected reply %v", q.name, v)
}

// Extend resets the idle time of a held message, so it is not reclaimed
// for another visibility timeout. It returns ErrNotOwner if the consumer
// no longer holds it.
func (q *Queue) Extend(ctx context.Context, msg *Message) error {
	stream, err := q.streamOf(msg)
	if err != nil {
		return err
	}
	v, err := extendScript.Run(ctx, q.rdb, []string{stream}, Group, msg.Consumer, msg.ID).Int64()
	if missingGroup(err) {
		return ErrNotOwner
	}
	if err != nil {
		return fmt.Errorf("queue %s: extend: %w", q.name, err)
	}
	if v == 0 {
		return ErrNotOwner
	}
	return nil
}

// KeepAlive extends msg every third of the visibility timeout until the
// returned cancel function is called. The returned context is cancelled,
// with cause ErrNotOwner, if the consumer loses the message; the job
// should then stop. Transient errors are retried at the next tick. Use
// the parent context, not the returned one, to acknowledge the message.
func (q *Queue) KeepAlive(ctx context.Context, msg *Message) (context.Context, context.CancelFunc) {
	jctx, cancel := context.WithCancelCause(ctx)
	go func() {
		t := time.NewTicker(max(q.opts.Visibility/3, time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-jctx.Done():
				return
			case <-t.C:
				if err := q.Extend(jctx, msg); errors.Is(err, ErrNotOwner) {
					cancel(ErrNotOwner)
					return
				}
			}
		}
	}()
	return jctx, func() { cancel(context.Canceled) }
}

// Nack gives a held message back without counting a delivery or an
// attempt (a worker shutting down in the middle of a job): another
// consumer gets it right away.
func (q *Queue) Nack(ctx context.Context, msg *Message) error {
	stream, err := q.streamOf(msg)
	if err != nil {
		return err
	}
	v, err := nackScript.Run(ctx, q.rdb, []string{stream, q.signal}, Group, msg.Consumer, msg.ID,
		q.opts.Visibility.Milliseconds()).Int64()
	if missingGroup(err) {
		return ErrNotOwner
	}
	if err != nil {
		return fmt.Errorf("queue %s: nack: %w", q.name, err)
	}
	if v == 0 {
		return ErrNotOwner
	}
	return nil
}

// Release makes every message held by consumer available to the others
// right away and returns how many there were. Workers call it at startup
// for each of their consumer names, so the jobs of a crashed or killed
// previous incarnation do not wait for the visibility timeout. The
// deliveries count as failed ones.
func (q *Queue) Release(ctx context.Context, consumer string) (int, error) {
	v, err := q.run(ctx, releaseScript, q.block, Group, consumer, q.opts.Visibility.Milliseconds(), len(q.streams))
	if err != nil {
		return 0, fmt.Errorf("queue %s: release: %w", q.name, err)
	}
	return int(num(v)), nil
}

// streamOf returns the stream of a message of this queue.
func (q *Queue) streamOf(msg *Message) (string, error) {
	if msg == nil || msg.Priority < 0 || msg.Priority >= len(q.streams) {
		return "", fmt.Errorf("queue %s: not a message of this queue", q.name)
	}
	return q.streams[msg.Priority], nil
}

// str and num read script replies.
func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case int64:
		return strconv.FormatInt(s, 10)
	}
	return ""
}

func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

// idTime returns the time encoded in a stream id ("<ms>-<seq>").
func idTime(id string) (time.Time, error) {
	ms, _, ok := strings.Cut(id, "-")
	if !ok {
		return time.Time{}, fmt.Errorf("queue: invalid stream id %q", id)
	}
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("queue: invalid stream id %q", id)
	}
	return time.UnixMilli(n), nil
}

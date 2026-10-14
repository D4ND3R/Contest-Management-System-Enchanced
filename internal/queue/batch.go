// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// MaxConsumeBatch bounds the messages of one ConsumeBatch.
const MaxConsumeBatch = 1000

// pickBatchScript is pickScript for up to count messages: due delayed
// messages are promoted, then for each priority, highest first, the
// abandoned messages (the oldest first; those abandoned max_deliveries
// times go to the dead letters) and then the new ones, until count. It
// removes one wake-up token per message taken: they were pushed for
// messages that are not there any more for anyone else, and would only
// make a consumer that waits next wake up for nothing.
// KEYS: key block. ARGV: group, consumer, min_idle_ms, max_deliveries,
// dead_maxlen, P, count. Returns {{prio, id, fields, deliveries}...}, or
// {-1, ms until the next delayed message is due (-1: none)}.
var pickBatchScript = redis.NewScript(prelude + `
local group, consumer = ARGV[1], ARGV[2]
local min_idle, max_del, dead_max, P = tonumber(ARGV[3]), tonumber(ARGV[4]), ARGV[5], tonumber(ARGV[6])
local count = tonumber(ARGV[7])
local delayed, dedupk, sig, deadk = KEYS[P + 1], KEYS[P + 2], KEYS[P + 3], KEYS[P + 4]
local now = now_ms()

local due = redis.call('ZRANGEBYSCORE', delayed, '-inf', now, 'LIMIT', 0, 100)
for _, m in ipairs(due) do
  local nonce, prio, attempt, dedup, err, payload = decode_delayed(m)
  if prio >= P then prio = P - 1 end
  if prio < 0 then prio = 0 end
  local id = xadd(KEYS[prio + 1], attempt, payload, dedup, err)
  redis.call('ZREM', delayed, m)
  if dedup ~= '' and redis.call('HGET', dedupk, dedup) == 'z' .. nonce then
    redis.call('HSET', dedupk, dedup, id)
  end
end
signal(sig, #due)

local out = {}
for i = 1, P do
  if #out >= count then break end
  local s = KEYS[i]
  -- Bounded like pickScript: entries that cannot be claimed (deleted)
  -- are skipped, not looked at forever.
  local looked = 0
  while #out < count and looked < count + 32 do
    local r = redis.call('XPENDING', s, group, 'IDLE', min_idle, '-', '+', count - #out)
    if #r == 0 then break end
    for _, p in ipairs(r) do
      looked = looked + 1
      local id, owner, deliveries = p[1], p[2], tonumber(p[4])
      local e = redis.call('XCLAIM', s, group, consumer, min_idle, id)[1]
      if e then
        if deliveries >= max_del then
          local f = e[2]
          local dedup = field(f, 'd')
          dead_letter(deadk, dead_max, i - 1, field(f, 'a'), field(f, 'p'), dedup,
            'abandoned: delivered ' .. deliveries .. ' times without acknowledgement (last consumer ' .. owner .. ')',
            id, deliveries)
          redis.call('XACK', s, group, id)
          redis.call('XDEL', s, id)
          dedup_release(dedupk, dedup, id)
        else
          out[#out + 1] = {i - 1, id, e[2], deliveries + 1}
        end
      end
    end
  end
  if #out < count then
    local r = redis.call('XREADGROUP', 'GROUP', group, consumer, 'COUNT', count - #out, 'STREAMS', s, '>')
    if r then
      for _, e in ipairs(r[1][2]) do
        out[#out + 1] = {i - 1, e[1], e[2], 1}
      end
    end
  end
end

if #out > 0 then
  redis.call('LTRIM', sig, #out, -1)
  return out
end
local nxt = redis.call('ZRANGE', delayed, 0, 0, 'WITHSCORES')
if nxt[2] then return {-1, math.max(tonumber(nxt[2]) - now, 1)} end
return {-1, -1}
`)

// ackBatchScript acknowledges and deletes messages of any priority.
// KEYS: the P streams, then dedup. ARGV: group, P, n, then per message
// prio, id, dedup. Returns how many were pending.
var ackBatchScript = redis.NewScript(prelude + `
local group, P, n = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3])
local dedupk = KEYS[P + 1]
local acked = 0
for i = 0, n - 1 do
  local b = 4 + i * 3
  local s, id = KEYS[tonumber(ARGV[b]) + 1], ARGV[b + 1]
  acked = acked + redis.call('XACK', s, group, id)
  redis.call('XDEL', s, id)
  dedup_release(dedupk, ARGV[b + 2], id)
end
return acked
`)

// ConsumeBatch is Consume for up to n messages (at most
// MaxConsumeBatch), taken in one round trip in the order Consume would
// return them: the highest priority first and, within each, the abandoned
// messages before the new ones. It waits up to block for the first one and
// returns none (nil, nil) if nothing arrived. A consumer that takes
// several messages at once holds them all: it must handle (or Nack) each
// within the visibility timeout.
func (q *Queue) ConsumeBatch(ctx context.Context, consumer string, n int, block time.Duration) ([]*Message, error) {
	if consumer == "" {
		return nil, errors.New("queue: empty consumer name")
	}
	n = min(MaxConsumeBatch, max(n, 1))
	deadline := time.Now().Add(block)
	for {
		msgs, nextDue, err := q.pickBatch(ctx, consumer, n)
		if err != nil || len(msgs) > 0 {
			return msgs, err
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

// pickBatch runs pickBatchScript. It returns the messages, or the time
// until the next delayed message is due (0: none).
func (q *Queue) pickBatch(ctx context.Context, consumer string, count int) ([]*Message, time.Duration, error) {
	for {
		v, err := q.run(ctx, pickBatchScript, q.block, Group, consumer, q.opts.Visibility.Milliseconds(),
			q.opts.MaxDeliveries, q.opts.DeadMaxLen, len(q.streams), count)
		if err != nil {
			return nil, 0, fmt.Errorf("queue %s: consume: %w", q.name, err)
		}
		vals, _ := v.([]any)
		if len(vals) == 2 {
			if _, nothing := vals[0].(int64); nothing {
				if due := num(vals[1]); due > 0 {
					return nil, time.Duration(due) * time.Millisecond, nil
				}
				return nil, 0, nil
			}
		}
		out := make([]*Message, 0, len(vals))
		malformed := false
		for _, e := range vals {
			f, _ := e.([]any)
			if len(f) != 4 {
				return nil, 0, fmt.Errorf("queue %s: consume: unexpected reply %v", q.name, v)
			}
			msg, perr := q.parse(int(num(f[0])), str(f[1]), f[2], int(num(f[3])), consumer)
			if perr != nil {
				// Not written by Enqueue: keep it out of the way.
				if _, err := q.retry(ctx, msg, perr.Error(), 0); err != nil {
					return nil, 0, err
				}
				malformed = true
				continue
			}
			if msg.Deliveries > 1 {
				q.n.redelivered.Add(1)
			} else {
				q.n.delivered.Add(1)
			}
			out = append(out, msg)
		}
		if len(out) > 0 || !malformed {
			return out, 0, nil
		}
	}
}

// AckBatch acknowledges messages of this queue (as Ack, each) in one round
// trip per 256 messages.
func (q *Queue) AckBatch(ctx context.Context, msgs []*Message) error {
	keys := append(append([]string{}, q.streams...), q.dedup)
	for start := 0; start < len(msgs); start += maxBatch {
		chunk := msgs[start:min(start+maxBatch, len(msgs))]
		args := make([]any, 0, 3+3*len(chunk))
		args = append(args, Group, len(q.streams), len(chunk))
		for _, m := range chunk {
			if _, err := q.streamOf(m); err != nil {
				return err
			}
			args = append(args, m.Priority, m.ID, m.DedupKey)
		}
		if err := ackBatchScript.Run(ctx, q.rdb, keys, args...).Err(); err != nil {
			return fmt.Errorf("queue %s: ack: %w", q.name, err)
		}
		q.n.acked.Add(int64(len(chunk)))
	}
	return nil
}

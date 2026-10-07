// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue

import "github.com/redis/go-redis/v9"

// Every state change of a queue is one Lua script, so it is atomic: two
// consumers can never take the same message, a message is never both in a
// stream and in the delayed set, and the dedup index always agrees with
// where the message is.
//
// Scripts receive the "key block" of a queue: its P priority streams, then
// the delayed set, the dedup hash, the signal list and the dead-letter
// stream (see Queue.block). Every key a script touches is declared in
// KEYS. Times come from the server clock (TIME), so workers with skewed
// clocks agree on visibility timeouts and backoff deadlines.
//
// Stream entries have the fields a (attempt), p (payload) and, when set,
// d (dedup key) and e (error of the previous attempt). Dead letters add
// q (priority), src (id of the abandoned entry) and n (deliveries).
//
// The dedup hash maps a dedup key to where its message is: the id of its
// stream entry, or "z"..nonce while it waits in the delayed set. Every
// script that moves a message rewrites (or deletes) the mapping only if it
// still points at that message, so a late Ack of an old copy never frees
// the key of a newer one.

// prelude holds the helpers shared by the scripts.
const prelude = `
local MAX_SIGNALS = 64

local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function field(f, name)
  for i = 1, #f, 2 do
    if f[i] == name then return f[i + 1] end
  end
  return ''
end

local function xadd(stream, attempt, payload, dedup, err)
  local args = {'XADD', stream, '*', 'a', attempt, 'p', payload}
  if dedup ~= '' then args[#args + 1] = 'd'; args[#args + 1] = dedup end
  if err ~= '' then args[#args + 1] = 'e'; args[#args + 1] = err end
  return redis.call(unpack(args))
end

-- One token per message that became ready wakes one blocked consumer.
local function signal(key, n)
  if n <= 0 then return end
  if n > MAX_SIGNALS then n = MAX_SIGNALS end
  for _ = 1, n do redis.call('LPUSH', key, '1') end
  redis.call('LTRIM', key, 0, MAX_SIGNALS - 1)
end

-- A delayed message is "<nonce>\n<prio>\n<attempt>\n<#dedup>\n<#err>\n"
-- followed by dedup, err and payload (lengths make it binary safe).
local function encode_delayed(nonce, prio, attempt, dedup, err, payload)
  return nonce .. '\n' .. prio .. '\n' .. attempt .. '\n' .. #dedup .. '\n' ..
    #err .. '\n' .. dedup .. err .. payload
end

local function decode_delayed(m)
  local h, pos = {}, 1
  for i = 1, 5 do
    local e = string.find(m, '\n', pos, true)
    h[i] = string.sub(m, pos, e - 1)
    pos = e + 1
  end
  local dl, el = tonumber(h[4]), tonumber(h[5])
  return h[1], tonumber(h[2]), h[3], string.sub(m, pos, pos + dl - 1),
    string.sub(m, pos + dl, pos + dl + el - 1), string.sub(m, pos + dl + el)
end

local function dedup_release(dedupk, dedup, tag)
  if dedup ~= '' and redis.call('HGET', dedupk, dedup) == tag then
    redis.call('HDEL', dedupk, dedup)
  end
end

local function dead_letter(deadk, maxlen, prio, attempt, payload, dedup, reason, src, deliveries)
  redis.call('XADD', deadk, 'MAXLEN', '~', maxlen, '*', 'q', prio, 'a', attempt,
    'p', payload, 'd', dedup, 'e', reason, 'src', src, 'n', deliveries)
end

-- The key block starting after KEYS[base]: streams base+1..base+P, then
-- delayed, dedup, signal and dead.
local function enqueue_one(base, P, now, prio, attempt, delay, nonce, dedup, err, payload)
  local dedupk = KEYS[base + P + 2]
  if dedup ~= '' then
    local cur = redis.call('HGET', dedupk, dedup)
    if cur then return cur, 0, 0 end
  end
  local id, ready = nil, 0
  if delay > 0 then
    redis.call('ZADD', KEYS[base + P + 1], now + delay,
      encode_delayed(nonce, prio, attempt, dedup, err, payload))
    id = 'z' .. nonce
  else
    id = xadd(KEYS[base + prio + 1], attempt, payload, dedup, err)
    ready = 1
  end
  if dedup ~= '' then redis.call('HSET', dedupk, dedup, id) end
  return id, 1, ready
end
`

// enqueueScript adds n messages.
// KEYS: key block. ARGV: P, n, then per message prio, attempt, delay_ms,
// nonce, dedup, payload. Returns id1, added1, id2, added2...
var enqueueScript = redis.NewScript(prelude + `
local P, n = tonumber(ARGV[1]), tonumber(ARGV[2])
local now = now_ms()
local out, ready = {}, 0
for i = 0, n - 1 do
  local b = 3 + i * 6
  local id, added, r = enqueue_one(0, P, now, tonumber(ARGV[b]), ARGV[b + 1],
    tonumber(ARGV[b + 2]), ARGV[b + 3], ARGV[b + 4], '', ARGV[b + 5])
  out[#out + 1] = id
  out[#out + 1] = added
  ready = ready + r
end
signal(KEYS[P + 3], ready)
return out
`)

// pickScript promotes due delayed messages and hands one message to the
// consumer: for each priority, highest first, the oldest abandoned
// message (idle for at least the visibility timeout) and then the oldest
// new one. A message abandoned max_deliveries times goes to the dead
// letters instead.
// KEYS: key block. ARGV: group, consumer, min_idle_ms, max_deliveries,
// dead_maxlen, P. Returns {prio, id, fields, deliveries}, or {-1, ms until
// the next delayed message is due (-1: none)}.
var pickScript = redis.NewScript(prelude + `
local group, consumer = ARGV[1], ARGV[2]
local min_idle, max_del, dead_max, P = tonumber(ARGV[3]), tonumber(ARGV[4]), ARGV[5], tonumber(ARGV[6])
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
-- This consumer takes one of them itself.
signal(sig, #due - 1)

for i = 1, P do
  local s = KEYS[i]
  for _ = 1, 32 do
    local r = redis.call('XPENDING', s, group, 'IDLE', min_idle, '-', '+', 1)
    if #r == 0 then break end
    local id, owner, deliveries = r[1][1], r[1][2], tonumber(r[1][4])
    local c = redis.call('XCLAIM', s, group, consumer, min_idle, id)
    local e = c[1]
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
        return {i - 1, id, e[2], deliveries + 1}
      end
    end
  end
  local r = redis.call('XREADGROUP', 'GROUP', group, consumer, 'COUNT', 1, 'STREAMS', s, '>')
  if r then
    local e = r[1][2][1]
    return {i - 1, e[1], e[2], 1}
  end
end

local nxt = redis.call('ZRANGE', delayed, 0, 0, 'WITHSCORES')
if nxt[2] then return {-1, math.max(tonumber(nxt[2]) - now, 1)} end
return {-1, -1}
`)

// ackScript acknowledges and deletes a message.
// KEYS: stream, dedup. ARGV: group, id, dedup. Returns 1 if it was pending.
var ackScript = redis.NewScript(prelude + `
local n = redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
dedup_release(KEYS[2], ARGV[3], ARGV[2])
return n
`)

// ackEnqueueScript acknowledges a message and, only if it was still
// pending, enqueues another one (usually on another queue).
// KEYS: stream, dedup of the acknowledged message, then the key block of
// the destination. ARGV: group, id, dedup, P, prio, attempt, delay_ms,
// nonce, dedup, payload. Returns {acked, id, added}.
var ackEnqueueScript = redis.NewScript(prelude + `
local n = redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
dedup_release(KEYS[2], ARGV[3], ARGV[2])
if n == 0 then return {0, '', 0} end
local P = tonumber(ARGV[4])
local id, added, ready = enqueue_one(2, P, now_ms(), tonumber(ARGV[5]), ARGV[6],
  tonumber(ARGV[7]), ARGV[8], ARGV[9], '', ARGV[10])
signal(KEYS[2 + P + 3], ready)
return {1, id, added}
`)

// retryScript schedules the next attempt of a message held by the
// consumer, or dead-letters it when attempts are exhausted.
// KEYS: key block. ARGV: group, consumer, id, prio, next_attempt,
// max_attempts, backoff_ms, payload, dedup, error, dead_maxlen, P.
// Returns 'retry', 'dead' or 'notowner'.
var retryScript = redis.NewScript(prelude + `
local group, consumer, id = ARGV[1], ARGV[2], ARGV[3]
local prio, attempt, max_att = tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6])
local backoff, payload, dedup, err, dead_max = tonumber(ARGV[7]), ARGV[8], ARGV[9], ARGV[10], ARGV[11]
local P = tonumber(ARGV[12])
local s, delayed, dedupk, deadk = KEYS[prio + 1], KEYS[P + 1], KEYS[P + 2], KEYS[P + 4]
local r = redis.call('XPENDING', s, group, id, id, 1, consumer)
if #r == 0 then return 'notowner' end
redis.call('XACK', s, group, id)
redis.call('XDEL', s, id)
if attempt > max_att then
  dead_letter(deadk, dead_max, prio, attempt - 1, payload, dedup, err, id, r[1][4])
  dedup_release(dedupk, dedup, id)
  return 'dead'
end
redis.call('ZADD', delayed, now_ms() + backoff, encode_delayed(id, prio, attempt, dedup, err, payload))
if dedup ~= '' and redis.call('HGET', dedupk, dedup) == id then
  redis.call('HSET', dedupk, dedup, 'z' .. id)
end
return 'retry'
`)

// extendScript resets the idle time of a message held by the consumer.
// KEYS: stream. ARGV: group, consumer, id. Returns 0 if not held.
var extendScript = redis.NewScript(`
local r = redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[3], ARGV[3], 1, ARGV[2])
if #r == 0 then return 0 end
redis.call('XCLAIM', KEYS[1], ARGV[1], ARGV[2], 0, ARGV[3], 'JUSTID')
return 1
`)

// nackScript gives a held message back without counting a delivery: it
// becomes idle for the visibility timeout, so the next pick reclaims it.
// KEYS: stream, signal. ARGV: group, consumer, id, idle_ms.
var nackScript = redis.NewScript(prelude + `
local r = redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[3], ARGV[3], 1, ARGV[2])
if #r == 0 then return 0 end
local d = tonumber(r[1][4]) - 1
if d < 0 then d = 0 end
redis.call('XCLAIM', KEYS[1], ARGV[1], ARGV[2], 0, ARGV[3], 'IDLE', ARGV[4], 'RETRYCOUNT', d, 'JUSTID')
signal(KEYS[2], 1)
return 1
`)

// releaseScript makes every message held by a consumer reclaimable now
// (its process restarted or is shutting down). The deliveries count, as
// for a crash.
// KEYS: key block. ARGV: group, consumer, idle_ms, P. Returns how many.
var releaseScript = redis.NewScript(prelude + `
local group, consumer, idle, P = ARGV[1], ARGV[2], ARGV[3], tonumber(ARGV[4])
local total = 0
for i = 1, P do
  local r = redis.call('XPENDING', KEYS[i], group, '-', '+', 10000, consumer)
  for _, e in ipairs(r) do
    redis.call('XCLAIM', KEYS[i], group, consumer, 0, e[1], 'IDLE', idle, 'JUSTID')
    total = total + 1
  end
end
signal(KEYS[P + 3], total)
return total
`)

// requeueDeadScript moves a dead letter back to its stream with attempt 1.
// KEYS: key block. ARGV: dead id, P. Returns {id, added}, or {"", -1} if
// there is no such dead letter.
var requeueDeadScript = redis.NewScript(prelude + `
local P = tonumber(ARGV[2])
local deadk = KEYS[P + 4]
local e = redis.call('XRANGE', deadk, ARGV[1], ARGV[1])[1]
if not e then return {'', -1} end
local f = e[2]
local prio = tonumber(field(f, 'q')) or 0
if prio >= P then prio = P - 1 end
if prio < 0 then prio = 0 end
local id, added, ready = enqueue_one(0, P, now_ms(), prio, '1', 0, '', field(f, 'd'), '', field(f, 'p'))
redis.call('XDEL', deadk, ARGV[1])
signal(KEYS[P + 3], ready)
return {id, added}
`)

// allScripts are loaded when a queue opens, so calls send EVALSHA with the
// hash only (Script.Run falls back to EVAL if the server lost them).
var allScripts = []*redis.Script{
	enqueueScript, pickScript, ackScript, ackEnqueueScript, retryScript,
	extendScript, nackScript, releaseScript, requeueDeadScript,
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The loop follows the main loops of cms/service/EvaluationService.py and
// cms/service/ScoringService.py of CMS (new_submission, new_user_test,
// write_results, invalidate_submission, _missing_operations):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// leader is the state of the main loop while this dispatcher holds the
// lease. Everything that reads the database to decide what to enqueue,
// and everything that stores results, runs in this one goroutine: that is
// what lets intake skip the operations whose result is on its way (see
// schedule).
type leader struct {
	d *Dispatcher

	// background are the results whose work is re-evaluation work (see
	// JobPriority); fresh the new submissions whose time to score is
	// measured.
	background map[judging.ResultKey]struct{}
	fresh      map[int32]struct{}
	// retry are operations whose enqueueing failed (Valkey trouble).
	retry, retryBackground []judging.Operation

	// Notifications: recovering while those left unacknowledged by the
	// previous leader are handled; then a reader goroutine delivers new
	// ones on events. pending were read and not handled yet.
	recovering bool
	events     chan []judging.EventMessage
	pending    []judging.EventMessage
	readerCtx  context.Context
	readerWG   sync.WaitGroup
	// rechecks are notified ids that were not visible yet.
	rechecks rechecks

	sweep        sweepState
	nextDead     time.Time
	nextProgress time.Time
	lastProgress time.Time
	dirty        bool
	progress     progressState
}

func newLeader(d *Dispatcher) *leader {
	return &leader{
		d:          d,
		background: map[judging.ResultKey]struct{}{},
		fresh:      map[int32]struct{}{},
		recovering: true,
		events:     make(chan []judging.EventMessage, 4),
		dirty:      true,
	}
}

// fence fails once the lease is not valid any more: nothing is written
// after that.
func (l *leader) fence() error {
	if !l.d.lease.Valid() {
		return ErrLeaseLost
	}
	return nil
}

// fenceIf is fence when fenced is set.
func (l *leader) fenceIf(fenced bool) error {
	if !fenced {
		return nil
	}
	return l.fence()
}

// run is the main loop. It returns when ctx ends (shutdown, lease lost),
// or with errGiveUp when judging keeps failing.
func (l *leader) run(ctx context.Context) error {
	d := l.d
	var stopReader context.CancelFunc
	l.readerCtx, stopReader = context.WithCancel(ctx)
	defer func() {
		stopReader()
		l.readerWG.Wait()
	}()
	// The results the previous leader held come back at once.
	if n, err := d.results.Release(ctx, ResultsConsumer); err != nil {
		return err
	} else if n > 0 {
		d.log.Info("taking over the results of the previous dispatcher", "results", n)
	}
	l.sweep.request(0)
	delay := 100 * time.Millisecond
	for ctx.Err() == nil {
		step, err := l.step(ctx)
		if err == nil {
			delay = 100 * time.Millisecond
			d.noteOK()
			continue
		}
		if ctx.Err() != nil || errors.Is(err, ErrLeaseLost) || errors.Is(err, errCrash) {
			break
		}
		d.m.loopErrors.WithLabelValues(step).Inc()
		if errors.Is(err, errGiveUp) {
			return err
		}
		d.noteError(err)
		if gerr := d.giveUp(err); gerr != nil {
			return gerr
		}
		d.log.Warn("judging step failed, retrying", "step", step, "err", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		delay = min(2*delay, 5*time.Second)
	}
	return context.Cause(ctx)
}

// step is one round of the loop: retried operations, results,
// notifications, a sweep step, dead letters and progress. It returns the
// name of the part that failed.
func (l *leader) step(ctx context.Context) (string, error) {
	d := l.d
	if len(l.retry)+len(l.retryBackground) > 0 {
		ops, bg := l.retry, l.retryBackground
		l.retry, l.retryBackground = nil, nil
		err := errors.Join(l.enqueueOrKeep(ctx, ops, false), l.enqueueOrKeep(ctx, bg, true))
		if err != nil {
			return "enqueue", err
		}
	}

	// Notifications left by the previous leader first.
	if l.recovering {
		if err := l.recoverEvents(ctx); err != nil {
			return "events", err
		}
	}
	l.collectEvents()
	l.pending = append(l.pending, l.rechecks.due(time.Now())...)

	// Results: wait for one only when there is nothing else to do.
	now := time.Now()
	block := time.Duration(0)
	if len(l.pending) == 0 && !l.recovering && !l.sweep.due(now) {
		block = d.opts.IdleWait
		if wake := l.nextWake(); !wake.IsZero() {
			block = min(block, max(time.Until(wake), 0))
		}
	}
	if _, _, _, err := l.consumeResults(ctx, block, time.Time{}); err != nil {
		return "results", err
	}

	l.collectEvents()
	if len(l.pending) > 0 {
		if err := l.handleEvents(ctx, l.pending); err != nil {
			return "events", err
		}
		l.pending = nil
	}

	if !l.recovering && l.sweep.due(time.Now()) {
		if err := l.sweepStep(ctx); err != nil {
			return "sweep", err
		}
	}

	now = time.Now()
	if !now.Before(l.nextDead) {
		if _, err := l.deadLetters(ctx, d.jobs, d.results); err != nil {
			return "dead letters", err
		}
		if err := l.staleEvents(ctx); err != nil {
			return "events", err
		}
		l.nextDead = now.Add(d.opts.DeadLetterInterval)
	}
	if !now.Before(l.nextProgress) && (l.dirty || now.Sub(l.lastProgress) >= 15*time.Second) {
		// Counters are informative: a failure is logged, not retried.
		if err := l.publishProgress(ctx); err != nil && ctx.Err() == nil {
			d.log.Warn("cannot publish the judging counters", "err", err)
		}
		l.dirty = false
		l.lastProgress = now
		l.nextProgress = now.Add(d.opts.ProgressInterval)
	}
	return "", nil
}

// nextWake is the earliest time something periodic is due (zero: none).
func (l *leader) nextWake() time.Time {
	var t time.Time
	consider := func(c time.Time) {
		if !c.IsZero() && (t.IsZero() || c.Before(t)) {
			t = c
		}
	}
	consider(l.nextDead)
	consider(l.rechecks.next())
	if !l.sweep.active && len(l.sweep.queued) == 0 {
		consider(l.sweep.next)
	}
	if l.dirty {
		consider(l.nextProgress)
	}
	return t
}

// --- results ---

// consumeResults takes up to ResultBatch results, in one round trip per
// queue.MaxConsumeBatch (waiting up to block for the first one), stores
// them and enqueues what follows. It returns how many it took and the
// dedup keys of their operations. With until set, past reports that the
// drain of schedule is over: a result entered the queue after until, or
// the queue had no more results (every result that entered it before
// until was there to take).
func (l *leader) consumeResults(ctx context.Context, block time.Duration, until time.Time) (n int, keys []string, past bool, err error) {
	d := l.d
	// A former leader taking results would hide them from the next one
	// for a visibility timeout.
	if err := l.fence(); err != nil {
		return 0, nil, false, err
	}
	var msgs []*queue.Message
	more := true
	for more && len(msgs) < d.opts.ResultBatch {
		b := time.Duration(0)
		if len(msgs) == 0 {
			b = block
		}
		want := min(d.opts.ResultBatch-len(msgs), queue.MaxConsumeBatch)
		got, cerr := d.results.ConsumeBatch(ctx, ResultsConsumer, want, b)
		if cerr != nil {
			err = cerr
			break
		}
		msgs = append(msgs, got...)
		more = len(got) == want
	}
	if !until.IsZero() {
		past = !more || slices.ContainsFunc(msgs, func(m *queue.Message) bool { return m.EnqueuedAt.After(until) })
	}
	if len(msgs) == 0 {
		return 0, nil, past, err
	}
	keys, perr := l.process(ctx, msgs)
	return len(msgs), keys, past, errors.Join(err, perr)
}

// process stores the results of msgs in one transaction, enqueues the
// next operations, publishes the changes and acknowledges the messages.
// A crash before the acknowledgement makes the results come back, which
// is harmless (idempotent writes, D21).
func (l *leader) process(ctx context.Context, msgs []*queue.Message) ([]string, error) {
	d := l.d
	results := make([]*job.Result, 0, len(msgs))
	keys := make([]string, 0, len(msgs))
	for _, m := range msgs {
		r, err := job.DecodeResult(m.Payload)
		if err != nil {
			d.m.results.WithLabelValues("invalid").Inc()
			d.log.Error("undecodable result dropped", "message", m.ID, "err", err)
			continue
		}
		results = append(results, r)
		keys = append(keys, resultKey(r))
	}
	if len(results) > 0 {
		start := time.Now()
		if d.hooks.beforeCommit != nil && d.hooks.beforeCommit() {
			return keys, errCrash
		}
		ch, err := l.apply(ctx, results)
		if errors.Is(err, errPoison) {
			// A batch of one that cannot be stored: dropped (the sweep
			// schedules its operation again).
			d.m.results.WithLabelValues("error").Inc()
			d.log.Error("result dropped: it cannot be stored", "operation", results[0].Operation.Key(), "err", err)
			ch, err = &judging.Changes{}, nil
		}
		if err != nil {
			return keys, err
		}
		if d.hooks.afterCommit != nil && d.hooks.afterCommit() {
			return keys, errCrash
		}
		d.m.batchSize.Observe(float64(len(results)))
		l.countResults(results, ch)
		if err := l.enqueueOrKeep(ctx, ch.Next, false); err != nil {
			d.log.Warn("cannot enqueue the next operations, will retry", "err", err)
		}
		l.published(ctx, ch, ScoreUpdate{})
		d.m.applyTime.Observe(time.Since(start).Seconds())
	}
	l.ack(ctx, msgs)
	return keys, nil
}

// resultKey is the dedup key of the operation of a result (as
// judging.Operation.DedupKey).
func resultKey(r *job.Result) string {
	return judging.Operation{Operation: r.Operation, Generation: r.Generation}.DedupKey()
}

// errPoison: a result that breaks its transaction while the database
// works.
var errPoison = errors.New("dispatcher: the result cannot be stored")

// apply stores results, retrying while the database fails (judging is
// failing then: Health, and errGiveUp after a while, so that a standby
// that reaches the database takes over). When the database works but the
// batch keeps failing, the results are stored one by one and a result
// that fails alone is dropped (the sweep schedules its operation again).
func (l *leader) apply(ctx context.Context, results []*job.Result) (*judging.Changes, error) {
	d := l.d
	delay := 100 * time.Millisecond
	for attempt := 1; ; attempt++ {
		if err := l.fence(); err != nil {
			return nil, err
		}
		ch, err := d.judge.Apply(ctx, results)
		if err == nil {
			return ch, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		d.m.applyErrors.Inc()
		d.noteError(err)
		d.log.Warn("cannot store results, retrying", "results", len(results), "attempt", attempt, "err", err)
		if attempt >= 3 && l.databaseUp(ctx) {
			if len(results) == 1 {
				return nil, fmt.Errorf("%w: %w", errPoison, err)
			}
			return l.applyOneByOne(ctx, results)
		}
		if gerr := d.giveUp(err); gerr != nil {
			return nil, gerr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, 5*time.Second)
	}
}

func (l *leader) applyOneByOne(ctx context.Context, results []*job.Result) (*judging.Changes, error) {
	all := &judging.Changes{}
	for _, r := range results {
		ch, err := l.apply(ctx, []*job.Result{r})
		if errors.Is(err, errPoison) {
			l.d.m.results.WithLabelValues("error").Inc()
			l.d.log.Error("result dropped: it cannot be stored", "operation", r.Operation.Key(), "err", err)
			continue
		}
		if err != nil {
			return nil, err
		}
		mergeChanges(all, ch)
	}
	return all, nil
}

func mergeChanges(dst, src *judging.Changes) {
	dst.Next = append(dst.Next, src.Next...)
	dst.Submissions = append(dst.Submissions, src.Submissions...)
	dst.UserTests = append(dst.UserTests, src.UserTests...)
	dst.TaskScores = append(dst.TaskScores, src.TaskScores...)
	dst.Dropped = append(dst.Dropped, src.Dropped...)
	dst.GaveUp = append(dst.GaveUp, src.GaveUp...)
	dst.ScoreErrors = append(dst.ScoreErrors, src.ScoreErrors...)
}

func (l *leader) databaseUp(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return l.d.pool.Ping(pctx) == nil
}

// countResults updates the result counters.
func (l *leader) countResults(results []*job.Result, ch *judging.Changes) {
	m := l.d.m
	dropped := map[string]int{}
	for _, dr := range ch.Dropped {
		m.results.WithLabelValues(string(dr.Reason)).Inc()
		dropped[judging.Operation{Operation: dr.Operation, Generation: dr.Generation}.DedupKey()+"#"+fmt.Sprint(dr.Attempt)]++
	}
	for _, r := range results {
		if k := resultKey(r) + "#" + fmt.Sprint(r.Attempt); dropped[k] > 0 {
			dropped[k]--
			continue
		}
		if r.Success {
			m.results.WithLabelValues("applied").Inc()
		} else {
			m.results.WithLabelValues("failed").Inc()
		}
	}
}

// ack acknowledges messages in one round trip, even during a shutdown
// (the results are stored). A failure only means the messages come back
// later, as duplicates.
func (l *leader) ack(ctx context.Context, msgs []*queue.Message) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := l.d.results.AckBatch(ctx, msgs); err != nil && ctx.Err() == nil {
		l.d.log.Warn("cannot acknowledge results", "results", len(msgs), "err", err)
	}
}

// enqueueOrKeep enqueues the operations that follow a committed
// transaction (Changes.Next) and keeps them for the next step if Valkey
// fails.
//
// It is not fenced and does not stop with ctx: once the transaction
// committed nothing else computes them (its results are acknowledged
// next, and a result received again is a duplicate, which has no next
// operations), so a leader that has just lost its lease or is stopping
// still enqueues them. That is harmless (D36): while a job is held its
// operation@generation dedup key makes a second copy a no-op, and a job
// of a stale generation, or one the next leader already ran, only
// produces a result that is dropped.
func (l *leader) enqueueOrKeep(ctx context.Context, ops []judging.Operation, background bool) error {
	if len(ops) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := l.enqueueJobs(ctx, ops, background, false)
	if err != nil {
		if background {
			l.retryBackground = append(l.retryBackground, ops...)
		} else {
			l.retry = append(l.retry, ops...)
		}
	}
	return err
}

// --- intake ---

// schedule enqueues the operations of work read from the database and
// scores the results that only need it.
//
// Operations whose job is queued or running are dropped first (the job
// queue holds their dedup key). The others may still have a result on
// its way: the worker acknowledged the job (freeing its key) and the
// result waits in the results queue. Such a result entered the queue
// before Holding answered, so schedule first stores every result that
// arrived until then (the drain) and drops the operations they were for.
// The job may also have been dead-lettered (abandoned MaxDeliveries
// times by workers that died running it), which frees its key at once:
// those dead letters are stored as failed attempts first, and their
// operations dropped (the failure enqueues the retry, whose attempt counts
// the dead one; enqueued again from what was read before, the same
// attempt would kill MaxDeliveries more workers). Only this goroutine
// enqueues jobs and stores results, so what is left needs a job: no
// operation is ever run twice because of a sweep or a repeated
// notification.
func (l *leader) schedule(ctx context.Context, w *judging.Work, background bool) (enqueued int, err error) {
	d := l.d
	ops, err := l.notHeld(ctx, w.Ops)
	if err != nil {
		return 0, err
	}
	if len(ops) > 0 {
		dead, err := l.deadLetters(ctx, d.jobs)
		if err != nil {
			return 0, err
		}
		until, err := d.kv.Redis().Time(ctx).Result()
		if err != nil {
			return 0, err
		}
		consumed := map[string]bool{}
		for _, k := range dead {
			consumed[k] = true
		}
		for {
			n, keys, past, err := l.consumeResults(ctx, 0, until)
			if err != nil {
				return 0, err
			}
			for _, k := range keys {
				consumed[k] = true
			}
			if n == 0 || past {
				break
			}
		}
		ops = slices.DeleteFunc(ops, func(op judging.Operation) bool { return consumed[op.DedupKey()] })
		// A large re-evaluation is built in chunks, storing the results
		// that arrive in between so verdicts do not wait for it. Those
		// results are of jobs that were queued (held) when Holding
		// answered, so they concern none of the operations left.
		for len(ops) > 0 {
			n := min(len(ops), scheduleChunk)
			added, err := l.enqueue(ctx, ops[:n], background)
			enqueued += added
			if err != nil {
				return enqueued, err
			}
			ops = ops[n:]
			if len(ops) > 0 {
				if _, _, _, err := l.consumeResults(ctx, 0, time.Time{}); err != nil {
					return enqueued, err
				}
			}
		}
	}
	if len(w.ToScore) > 0 {
		if err := l.fence(); err != nil {
			return enqueued, err
		}
		ch, err := d.judge.Score(ctx, w.ToScore)
		if err != nil {
			return enqueued, err
		}
		if err := l.enqueueOrKeep(ctx, ch.Next, background); err != nil {
			d.log.Warn("cannot enqueue the next operations, will retry", "err", err)
		}
		l.published(ctx, ch, ScoreUpdate{})
	}
	return enqueued, nil
}

// Chunks of large intakes (a whole contest reevaluated): the ids read by
// one Pending, the operations built by one BuildJobs (variables for the
// tests).
var (
	pendingChunk  = 5000
	scheduleChunk = 1000
)

// --- notifications ---

// recoverEvents handles, one batch per step, the notifications the
// previous leader read and did not acknowledge (they are pending for the
// shared consumer name; those of other names are claimed first), then
// those sent while no dispatcher was active. Then it starts the reader of
// new ones. The first sweep waits for it: what it would find is mostly
// notified, and notified work gets the contestant priorities.
func (l *leader) recoverEvents(ctx context.Context) error {
	d := l.d
	if _, err := judging.ClaimEvents(ctx, d.kv, EventsConsumer, time.Minute, int64(d.opts.EventBatch)); err != nil {
		return err
	}
	for _, pending := range []bool{true, false} {
		msgs, err := judging.ReadEvents(ctx, d.kv, EventsConsumer, int64(d.opts.EventBatch), 0, pending)
		if err != nil {
			return err
		}
		if len(msgs) > 0 {
			return l.handleEvents(ctx, msgs)
		}
	}
	l.recovering = false
	l.readerWG.Add(1)
	go l.readEvents(l.readerCtx)
	return nil
}

// staleEvents handles the notifications read long ago and never
// acknowledged (read by a reader that was stopping when a new leader had
// already recovered): they would stay pending forever.
func (l *leader) staleEvents(ctx context.Context) error {
	if l.recovering {
		return nil
	}
	msgs, err := judging.ClaimEvents(ctx, l.d.kv, EventsConsumer, time.Minute, int64(l.d.opts.EventBatch))
	if err != nil || len(msgs) == 0 {
		return err
	}
	return l.handleEvents(ctx, msgs)
}

// readEvents reads new notifications and hands them to the loop.
func (l *leader) readEvents(ctx context.Context) {
	defer l.readerWG.Done()
	d := l.d
	for ctx.Err() == nil {
		msgs, err := judging.ReadEvents(ctx, d.kv, EventsConsumer, int64(d.opts.EventBatch), time.Second, false)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			d.log.Warn("cannot read notifications", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		select {
		case l.events <- msgs:
		case <-ctx.Done():
			return // unacknowledged: the next leader handles them
		}
	}
}

// collectEvents moves what the reader delivered to pending.
func (l *leader) collectEvents() {
	for len(l.pending) < l.d.opts.EventBatch {
		select {
		case msgs := <-l.events:
			l.pending = append(l.pending, msgs...)
		default:
			return
		}
	}
}

// handleEvents handles notifications together and acknowledges them. It
// is idempotent, so a failure retries the whole batch. Messages without an
// ID are rechecks (not acknowledged).
func (l *leader) handleEvents(ctx context.Context, msgs []judging.EventMessage) error {
	d := l.d
	var (
		fresh, invalidated, archived, rescored []int32
		userTests, utInvalidated, utArchived   []int32
		tokens, tasks                          []int32
		ids                                    []string
	)
	for _, m := range msgs {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
		if m.Err != nil {
			d.log.Warn("invalid notification dropped", "id", m.ID, "err", m.Err)
			continue
		}
		e := m.Event
		if m.ID != "" {
			d.m.events.WithLabelValues(string(e.Kind)).Inc()
		}
		switch e.Kind {
		case judging.EventSubmission:
			fresh = append(fresh, e.SubmissionIDs...)
		case judging.EventUserTest:
			userTests = append(userTests, e.UserTestIDs...)
		case judging.EventToken:
			tokens = append(tokens, e.SubmissionIDs...)
		case judging.EventInvalidate:
			switch {
			case e.Level == judging.LevelScore:
				// Rescored on every dataset, not only those to judge
				// (upstream ScoringService.invalidate_submission).
				rescored = append(rescored, e.SubmissionIDs...)
				utInvalidated = append(utInvalidated, e.UserTestIDs...)
			case e.ArchiveSandbox:
				archived = append(archived, e.SubmissionIDs...)
				utArchived = append(utArchived, e.UserTestIDs...)
			default:
				invalidated = append(invalidated, e.SubmissionIDs...)
				utInvalidated = append(utInvalidated, e.UserTestIDs...)
			}
		case judging.EventDataset:
			if e.TaskID != 0 {
				tasks = append(tasks, e.TaskID)
			}
			l.sweep.request(e.ContestID)
		case judging.EventSweep:
			l.sweep.request(e.ContestID)
		default:
			d.log.Warn("unknown notification dropped", "id", m.ID, "kind", string(e.Kind))
		}
	}
	if len(fresh)+len(tokens) > 0 {
		// The task scores of new submissions (partial) and of tokens
		// (tokened score, and the score in max_tokened_last), in case the
		// site did not refresh them in its transaction.
		if err := l.fence(); err != nil {
			return err
		}
		changes, err := d.judge.RefreshTaskScoresOf(ctx, slices.Concat(fresh, tokens))
		if err != nil {
			return err
		}
		l.published(ctx, &judging.Changes{TaskScores: changes}, ScoreUpdate{})
		for _, id := range fresh {
			if len(l.fresh) < maxBackground {
				l.fresh[id] = struct{}{}
			}
		}
	}
	for _, t := range []struct {
		subs, uts   []int32
		archive     bool
		background  bool
		allDatasets bool
		// recheck: new ids, which may not be visible yet.
		recheck bool
	}{
		{fresh, nil, false, false, false, true},
		{nil, userTests, false, false, false, true},
		{invalidated, utInvalidated, false, true, false, false},
		{archived, utArchived, true, true, false, false},
		{rescored, nil, false, true, true, false},
	} {
		for len(t.subs)+len(t.uts) > 0 {
			subs, uts := t.subs[:min(len(t.subs), pendingChunk)], t.uts[:min(len(t.uts), pendingChunk)]
			t.subs, t.uts = t.subs[len(subs):], t.uts[len(uts):]
			w, err := d.judge.Pending(ctx, judging.Targets{SubmissionIDs: subs, UserTestIDs: uts,
				ArchiveSandbox: t.archive, ScoreAllDatasets: t.allDatasets})
			if err != nil {
				return err
			}
			if _, err := l.schedule(ctx, w, t.background); err != nil {
				return err
			}
			if t.recheck {
				l.recheck(subs, w.MissingSubmissions, false)
				l.recheck(uts, w.MissingUserTests, true)
			}
		}
	}
	if len(tokens)+len(tasks) > 0 {
		slices.Sort(tasks)
		d.m.scoreUpdates.Inc()
		d.scores.Push(ctx, ScoreUpdate{Tokens: tokens, Tasks: slices.Compact(tasks)})
	}
	l.dirty = true
	return judging.AckEvents(ctx, d.kv, ids...)
}

// recheck looks again later at the new submissions or user tests that
// Pending could not see (see rechecks).
func (l *leader) recheck(asked, missing []int32, userTest bool) {
	if len(asked) == 0 {
		return
	}
	kind := "submissions"
	if userTest {
		kind = "user_tests"
	}
	again, givenUp := l.rechecks.note(time.Now(), asked, missing, userTest)
	if again > 0 {
		l.d.m.rechecks.WithLabelValues(kind).Add(float64(again))
		l.d.log.Debug("notified before they were visible: looking again shortly", "kind", kind, "ids", again)
	}
	if givenUp > 0 {
		l.d.log.Warn("notified and never visible (deleted, or the notification was sent before a commit that "+
			"did not happen): left to the sweep", "kind", kind, "ids", givenUp)
	}
}

// --- sweeps ---

// sweepState is the sweep in progress (one step per round of the loop)
// and the sweeps asked for.
type sweepState struct {
	active  bool
	contest int32
	cursor  judging.SweepCursor
	started time.Time
	found   int
	// repaired counts the cached task scores created or fixed.
	repaired int
	// queued are the contests to sweep next (0: every contest).
	queued []int32
	// next is when the next periodic sweep of every contest starts.
	next time.Time
}

// request asks for a sweep of a contest (0: every contest).
func (s *sweepState) request(contest int32) {
	if slices.Contains(s.queued, 0) || slices.Contains(s.queued, contest) {
		return
	}
	if contest == 0 {
		s.queued = s.queued[:0]
	}
	s.queued = append(s.queued, contest)
}

// due reports whether a sweep step should run now.
func (s *sweepState) due(now time.Time) bool {
	return s.active || len(s.queued) > 0 || !s.next.IsZero() && !now.Before(s.next)
}

// sweepStep runs one step of the current sweep, starting one if due.
func (l *leader) sweepStep(ctx context.Context) error {
	d := l.d
	s := &l.sweep
	if !s.active {
		if len(s.queued) == 0 {
			s.queued = append(s.queued, 0) // periodic
		}
		s.active, s.contest, s.cursor, s.started, s.found, s.repaired = true, s.queued[0], judging.SweepCursor{}, time.Now(), 0, 0
		s.queued = s.queued[1:]
		if s.contest == 0 {
			s.next = time.Time{}
		}
	}
	res, err := d.judge.Sweep(ctx, judging.SweepOptions{
		ContestID: s.contest, Cursor: s.cursor, Window: d.opts.SweepWindow, Limit: d.opts.SweepLimit,
	})
	if err != nil {
		return err
	}
	n, err := l.schedule(ctx, &res.Work, true)
	if err != nil {
		return err
	}
	if len(res.MissingTaskScores) > 0 {
		// Participants with submissions and no cached task score (data
		// from before the cache, an import): the pages would show 0.
		if err := l.fence(); err != nil {
			return err
		}
		changes, err := d.judge.RefreshTaskScoresFor(ctx, res.MissingTaskScores)
		if err != nil {
			return err
		}
		d.m.sweepFound.WithLabelValues("task_scores").Add(float64(len(res.MissingTaskScores)))
		s.repaired += len(changes)
		l.published(ctx, &judging.Changes{TaskScores: changes}, ScoreUpdate{})
	}
	if n > 0 {
		d.m.sweepFound.WithLabelValues("operations").Add(float64(n))
	}
	if k := len(res.ToScore); k > 0 {
		d.m.sweepFound.WithLabelValues("to_score").Add(float64(k))
	}
	s.found += n + len(res.ToScore)
	s.cursor = res.Next
	if !res.Done {
		return nil
	}
	// The cached task scores computed on a dataset that is not active any
	// more (changed without ActivateDataset).
	if err := l.fence(); err != nil {
		return err
	}
	changes, err := d.judge.RepairTaskScores(ctx, s.contest)
	if err != nil {
		return err
	}
	l.published(ctx, &judging.Changes{TaskScores: changes}, ScoreUpdate{})
	s.repaired += len(changes)
	took := time.Since(s.started)
	d.m.sweeps.Inc()
	d.m.sweepTime.Observe(took.Seconds())
	level := d.log.Debug
	if s.found > 0 || s.repaired > 0 {
		level = d.log.Info
	}
	level("sweep done", "contest", s.contest, "found", s.found, "task_scores_repaired", s.repaired,
		"took_ms", took.Milliseconds())
	if s.contest == 0 {
		s.next = time.Now().Add(d.opts.SweepInterval)
	}
	s.active = false
	return nil
}

// --- dead letters ---

// deadLetters handles the dead letters of queues (the job and result
// queues), every one present, ResultBatch at a time. A job abandoned by
// its workers MaxDeliveries times (a job that kills its worker, or
// workers that keep dying) becomes a failed attempt, so it is retried and
// eventually given up like any failed job instead of looping through the
// sweep. A dead result (the dispatcher died that many times holding it) is
// stored as any other result. It returns the dedup keys of the operations
// whose dead letters it handled.
func (l *leader) deadLetters(ctx context.Context, queues ...*queue.Queue) ([]string, error) {
	d := l.d
	var keys []string
	for _, q := range queues {
		label := "results"
		if q == d.jobs {
			label = "jobs"
		}
		// Bounded: dead letters keep coming while workers die.
		for range maxDeadRounds {
			dead, err := q.ListDead(ctx, int64(d.opts.ResultBatch))
			if err != nil {
				return keys, err
			}
			if len(dead) == 0 {
				break
			}
			var results []*job.Result
			ids := make([]string, 0, len(dead))
			for _, dl := range dead {
				ids = append(ids, dl.ID)
				if dl.DedupKey != "" {
					keys = append(keys, dl.DedupKey)
				}
				if q == d.jobs {
					jb, err := job.DecodeJob(dl.Payload)
					if err != nil {
						d.log.Error("undecodable dead job dropped", "id", dl.ID, "err", err)
						continue
					}
					r := jb.NewResult("dispatcher")
					r.Fail(fmt.Errorf("the job was abandoned by its workers: %s", dl.Reason))
					results = append(results, r)
					d.log.Warn("job abandoned by its workers counted as a failed attempt", "job", jb.ID, "reason", dl.Reason)
				} else {
					r, err := job.DecodeResult(dl.Payload)
					if err != nil {
						d.log.Error("undecodable dead result dropped", "id", dl.ID, "err", err)
						continue
					}
					results = append(results, r)
					keys = append(keys, resultKey(r))
				}
			}
			if len(results) > 0 {
				ch, err := l.apply(ctx, results)
				if errors.Is(err, errPoison) {
					d.log.Error("dead letter dropped: it cannot be stored", "err", err)
					ch, err = &judging.Changes{}, nil
				}
				if err != nil {
					return keys, err
				}
				l.countResults(results, ch)
				if err := l.enqueueOrKeep(ctx, ch.Next, false); err != nil {
					d.log.Warn("cannot enqueue the next operations, will retry", "err", err)
				}
				l.published(ctx, ch, ScoreUpdate{})
			}
			d.m.deadLetters.WithLabelValues(label).Add(float64(len(ids)))
			if err := q.DeleteDead(ctx, ids...); err != nil {
				return keys, err
			}
			if len(dead) < d.opts.ResultBatch {
				break
			}
		}
	}
	return keys, nil
}

// maxDeadRounds bounds the batches of dead letters of one queue handled
// at once.
const maxDeadRounds = 64

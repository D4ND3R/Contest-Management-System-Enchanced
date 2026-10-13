// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"slices"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// progressState is what the last progress publication said, to publish
// only the changes of the worker list and to reset the counters of a
// contest that has nothing left.
type progressState struct {
	workers  map[string]string // name -> live.Worker* state
	contests map[int32]bool    // contests with unfinished results
}

// workerState is the live state of a registered worker.
func workerState(w registry.Info) string {
	switch {
	case w.Draining:
		return live.WorkerDraining
	case w.Error != "":
		return live.WorkerBroken
	}
	return live.WorkerUp
}

// publishProgress publishes the judging counters of the admin pages (the
// whole installation, and each contest with unfinished results) and the
// workers that appeared, disappeared or changed state. It reads the queue
// state and the worker list from Valkey and one query over the unfinished
// results; the dispatcher calls it at most once per ProgressInterval.
func (l *leader) publishProgress(ctx context.Context) error {
	d := l.d
	st, err := d.jobs.Stats(ctx)
	if err != nil {
		return err
	}
	workers, err := registry.List(ctx, d.kv)
	if err != nil {
		return err
	}
	prog, err := d.judge.Progress(ctx)
	if err != nil {
		return err
	}
	all := map[string]int64{
		live.CounterQueued:   st.Ready,
		live.CounterInFlight: st.Pending,
		live.CounterRetrying: st.Delayed,
		live.CounterDead:     st.Dead,
		live.CounterWorkers:  int64(len(workers)),
		// Summed below; present even when 0.
		live.CounterCompiling: 0, live.CounterEvaluating: 0, live.CounterScoring: 0,
	}
	var evs []live.Event
	contests := map[int32]bool{}
	for _, p := range prog {
		all[live.CounterCompiling] += p.Compiling
		all[live.CounterEvaluating] += p.Evaluating
		all[live.CounterScoring] += p.Scoring
		if p.ContestID == 0 {
			continue
		}
		contests[p.ContestID] = true
		evs = append(evs, live.Judging{Contest: int64(p.ContestID), Counters: map[string]int64{
			live.CounterCompiling: p.Compiling, live.CounterEvaluating: p.Evaluating, live.CounterScoring: p.Scoring,
		}}.Event())
	}
	for c := range l.progress.contests {
		if !contests[c] {
			evs = append(evs, live.Judging{Contest: int64(c), Counters: map[string]int64{
				live.CounterCompiling: 0, live.CounterEvaluating: 0, live.CounterScoring: 0,
			}}.Event())
		}
	}
	evs = append(evs, live.Judging{Counters: all}.Event())
	l.progress.contests = contests

	// Worker changes; the first list after taking the lease is the
	// baseline.
	states := make(map[string]string, len(workers))
	for _, w := range workers {
		states[w.Name] = workerState(w)
	}
	if l.progress.workers != nil {
		names := make([]string, 0, len(states)+len(l.progress.workers))
		for n := range states {
			names = append(names, n)
		}
		for n := range l.progress.workers {
			if _, ok := states[n]; !ok {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		for _, n := range names {
			now, ok := states[n]
			if !ok {
				now = live.WorkerDown
			}
			before, had := l.progress.workers[n]
			if !had {
				before = live.WorkerDown
			}
			if now != before {
				evs = append(evs, live.Worker{Name: n, State: now}.Event())
			}
		}
	}
	l.progress.workers = states
	d.live.Publish(evs...)
	return nil
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2011-2018 Luca Wehrstedt and the CMS contributors
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmsranking/Scoring.py of CMS (http://cms-dev.github.io/).

package rankingweb

import (
	"cmp"
	"slices"
	"strconv"
)

// subState is a submission as a score sees it: the entity plus what its
// subchanges set so far (CMS sets score, token and extra on the entity).
type subState struct {
	key        string
	user, task string
	time       int64
	score      float64
	token      bool
	extra      []string
}

type change struct {
	key string
	sc  *Subchange
}

type histPoint struct {
	time  int64
	score float64
}

// score is the score of a user on a task and its history (CMS Score).
// Submissions all have different times (CMS enforces a minimum delay);
// subchanges with the same time are applied in the order of their keys.
type score struct {
	mode     string
	subs     map[string]*subState
	changes  []change // sorted by time, then key
	released []float64
	last     *subState
	history  []histPoint
}

func newScore(mode string) *score {
	return &score{mode: mode, subs: map[string]*subState{}}
}

func (s *score) get() float64 {
	if len(s.history) == 0 {
		return 0
	}
	return s.history[len(s.history)-1].score
}

func (s *score) removeReleased(v float64) {
	if i := slices.Index(s.released, v); i >= 0 {
		s.released = slices.Delete(s.released, i, i+1)
	}
}

func (s *score) appendChange(c change) {
	sub := s.subs[c.sc.Submission]
	if sub == nil {
		return
	}
	if sub.token {
		s.removeReleased(sub.score)
	}
	if c.sc.Score != nil {
		sub.score = float64(*c.sc.Score)
	}
	if c.sc.Token != nil {
		sub.token = *c.sc.Token
	}
	if c.sc.Extra != nil {
		sub.extra = *c.sc.Extra
	}
	if sub.token {
		s.released = append(s.released, sub.score)
	}
	if c.sc.Score != nil && (s.last == nil || sub.time > s.last.time) {
		s.last = sub
	}

	var v float64
	switch s.mode {
	case ScoreModeMax:
		first := true
		for _, x := range s.subs {
			if first || x.score > v {
				v, first = x.score, false
			}
		}
	case ScoreModeMaxSubtask:
		var best []float64
		for _, x := range s.subs {
			vals := []float64{x.score}
			if len(x.extra) > 0 {
				vals = make([]float64, len(x.extra))
				for i, e := range x.extra {
					vals[i], _ = strconv.ParseFloat(e, 64)
				}
			}
			for i, f := range vals {
				if i >= len(best) {
					// zip_longest fills the missing columns with 0.
					best = append(best, max(0, f))
				} else {
					best[i] = max(best[i], f)
				}
			}
		}
		for _, f := range best {
			v += f
		}
	default: // ScoreModeMaxTokenedLast
		rel := 0.0
		for _, x := range s.released {
			rel = max(rel, x)
		}
		lastScore := 0.0
		if s.last != nil {
			lastScore = s.last.score
		}
		v = max(rel, lastScore)
	}
	if v != s.get() {
		s.history = append(s.history, histPoint{c.sc.Time, v})
	}
}

func (s *score) resetHistory() {
	s.last = nil
	s.released = s.released[:0]
	s.history = s.history[:0]
	for _, sub := range s.subs {
		sub.score, sub.token, sub.extra = 0, false, nil
	}
	for _, c := range s.changes {
		s.appendChange(c)
	}
}

func changeLess(a, b change) bool {
	return a.sc.Time < b.sc.Time || (a.sc.Time == b.sc.Time && a.key < b.key)
}

func (s *score) createSubchange(key string, sc *Subchange) {
	c := change{key, sc}
	if n := len(s.changes); n == 0 || changeLess(s.changes[n-1], c) {
		s.changes = append(s.changes, c)
		s.appendChange(c)
		return
	}
	i, _ := slices.BinarySearchFunc(s.changes, c, func(a, b change) int {
		if changeLess(a, b) {
			return -1
		}
		if changeLess(b, a) {
			return 1
		}
		return 0
	})
	s.changes = slices.Insert(s.changes, i, c)
	s.resetHistory()
}

// removeChange deletes the subchange key, if present, and resets the
// history.
func (s *score) removeChange(key string) bool {
	i := slices.IndexFunc(s.changes, func(c change) bool { return c.key == key })
	if i < 0 {
		return false
	}
	s.changes = slices.Delete(s.changes, i, i+1)
	s.resetHistory()
	return true
}

// scoring holds the scores of every user and task (CMS ScoringStore). The
// scoreboard keeps two: every subchange (administrators) and the public
// one, without the subchanges hidden by a freeze (include).
type scoring struct {
	st      *state
	scores  map[string]map[string]*score
	include func(*Subchange) bool // nil: every subchange
	notify  func(user, task string, v float64)
}

func newScoring(st *state, include func(*Subchange) bool) *scoring {
	return &scoring{st: st, scores: map[string]map[string]*score{}, include: include}
}

// init loads the stored submissions and subchanges (CMS init_store).
func (sc *scoring) init() {
	notify := sc.notify
	sc.notify = nil
	for key, e := range sc.st.items[kSubmission] {
		sc.createSubmission(key, e.(*Submission))
	}
	keys := sortedKeys(sc.st.items[kSubchange])
	for _, key := range keys {
		sc.createSubchange(key, sc.st.items[kSubchange][key].(*Subchange))
	}
	sc.notify = notify
}

func (sc *scoring) changed(user, task string, old float64, obj *score) {
	if v := obj.get(); v != old && sc.notify != nil {
		sc.notify(user, task, v)
	}
}

func (sc *scoring) taskMode(task string) string {
	if t, ok := sc.st.items[kTask][task].(*Task); ok {
		return t.ScoreMode
	}
	return ScoreModeMaxTokenedLast
}

func (sc *scoring) obj(user, task string) *score {
	return sc.scores[user][task]
}

func (sc *scoring) createSubmission(key string, sub *Submission) {
	byTask := sc.scores[sub.User]
	if byTask == nil {
		byTask = map[string]*score{}
		sc.scores[sub.User] = byTask
	}
	obj := byTask[sub.Task]
	if obj == nil {
		obj = newScore(sc.taskMode(sub.Task))
		byTask[sub.Task] = obj
	}
	old := obj.get()
	obj.subs[key] = &subState{key: key, user: sub.User, task: sub.Task, time: sub.Time}
	sc.changed(sub.User, sub.Task, old, obj)
}

func (sc *scoring) updateSubmission(key string, old, sub *Submission) {
	if old.User != sub.User || old.Task != sub.Task {
		sc.deleteSubmission(key, old)
		sc.createSubmission(key, sub)
		// Its subchanges stay with the old score (as CMS): send them again.
		return
	}
	obj := sc.obj(sub.User, sub.Task)
	if obj == nil {
		sc.createSubmission(key, sub)
		return
	}
	before := obj.get()
	obj.subs[key] = &subState{key: key, user: sub.User, task: sub.Task, time: sub.Time}
	obj.mode = sc.taskMode(sub.Task)
	obj.resetHistory()
	sc.changed(sub.User, sub.Task, before, obj)
}

func (sc *scoring) deleteSubmission(key string, sub *Submission) {
	obj := sc.obj(sub.User, sub.Task)
	if obj == nil {
		return
	}
	before := obj.get()
	if _, ok := obj.subs[key]; ok {
		delete(obj.subs, key)
		obj.changes = slices.DeleteFunc(obj.changes, func(c change) bool { return c.sc.Submission == key })
		obj.resetHistory()
	}
	sc.changed(sub.User, sub.Task, before, obj)
	if len(obj.subs) == 0 {
		delete(sc.scores[sub.User], sub.Task)
	}
	if len(sc.scores[sub.User]) == 0 {
		delete(sc.scores, sub.User)
	}
}

func (sc *scoring) submissionOf(ch *Subchange) *Submission {
	sub, _ := sc.st.items[kSubmission][ch.Submission].(*Submission)
	return sub
}

func (sc *scoring) createSubchange(key string, ch *Subchange) {
	if sc.include != nil && !sc.include(ch) {
		return
	}
	sub := sc.submissionOf(ch)
	if sub == nil {
		return
	}
	obj := sc.obj(sub.User, sub.Task)
	if obj == nil {
		return
	}
	before := obj.get()
	obj.createSubchange(key, ch)
	sc.changed(sub.User, sub.Task, before, obj)
}

func (sc *scoring) updateSubchange(key string, old, ch *Subchange) {
	if old.Submission != ch.Submission {
		sc.deleteSubchange(key, old)
		sc.createSubchange(key, ch)
		return
	}
	sub := sc.submissionOf(ch)
	if sub == nil {
		return
	}
	obj := sc.obj(sub.User, sub.Task)
	if obj == nil {
		return
	}
	before := obj.get()
	obj.removeChange(key)
	if sc.include == nil || sc.include(ch) {
		obj.createSubchange(key, ch)
	}
	sc.changed(sub.User, sub.Task, before, obj)
}

func (sc *scoring) deleteSubchange(key string, ch *Subchange) {
	// A subchange deleted with its submission: deleteSubmission cleans up.
	sub := sc.submissionOf(ch)
	if sub == nil {
		return
	}
	obj := sc.obj(sub.User, sub.Task)
	if obj == nil {
		return
	}
	before := obj.get()
	obj.removeChange(key)
	sc.changed(sub.User, sub.Task, before, obj)
}

// updateMode applies a new score mode of a task (CMS only does it when a
// submission is updated).
func (sc *scoring) updateMode(task, mode string) {
	for user, byTask := range sc.scores {
		obj := byTask[task]
		if obj == nil || obj.mode == mode {
			continue
		}
		before := obj.get()
		obj.mode = mode
		obj.resetHistory()
		sc.changed(user, task, before, obj)
	}
}

func (sc *scoring) get(user, task string) float64 {
	if obj := sc.obj(user, task); obj != nil {
		return obj.get()
	}
	return 0
}

// historyEntry is [user, task, time, score] (CMS get_global_history).
type historyEntry [4]any

// globalHistory merges every history, by time, then score, user and task
// (the order of the heap of CMS).
func (sc *scoring) globalHistory() []historyEntry {
	type ent struct {
		user, task string
		p          histPoint
	}
	var all []ent
	for user, byTask := range sc.scores {
		for task, obj := range byTask {
			for _, p := range obj.history {
				all = append(all, ent{user, task, p})
			}
		}
	}
	slices.SortFunc(all, func(a, b ent) int {
		return cmp.Or(cmp.Compare(a.p.time, b.p.time), cmp.Compare(a.p.score, b.p.score),
			cmp.Compare(a.user, b.user), cmp.Compare(a.task, b.task))
	})
	out := make([]historyEntry, len(all))
	for i, e := range all {
		out[i] = historyEntry{e.user, e.task, e.p.time, Float(e.p.score)}
	}
	return out
}

// scoreMap returns the positive scores by user and task (/scores).
func (sc *scoring) scoreMap() map[string]map[string]Float {
	out := map[string]map[string]Float{}
	for user, byTask := range sc.scores {
		for task, obj := range byTask {
			if v := obj.get(); v > 0 {
				if out[user] == nil {
					out[user] = map[string]Float{}
				}
				out[user][task] = Float(v)
			}
		}
	}
	return out
}

// sublistEntry is a submission of /sublist/<user> (the __dict__ of a CMS
// Submission).
type sublistEntry struct {
	Key   string   `json:"key"`
	User  string   `json:"user"`
	Task  string   `json:"task"`
	Time  int64    `json:"time"`
	Score Float    `json:"score"`
	Token bool     `json:"token"`
	Extra []string `json:"extra"`
}

// sublist returns the submissions of user sorted by task and time;
// visible filters them (nil: all).
func (sc *scoring) sublist(user string, visible func(*subState) bool) []sublistEntry {
	out := []sublistEntry{}
	for task := range sc.st.items[kTask] {
		obj := sc.obj(user, task)
		if obj == nil {
			continue
		}
		for _, s := range obj.subs {
			if visible != nil && !visible(s) {
				continue
			}
			extra := s.extra
			if extra == nil {
				extra = []string{}
			}
			out = append(out, sublistEntry{s.key, s.user, s.task, s.time, Float(s.score), s.token, extra})
		}
	}
	slices.SortFunc(out, func(a, b sublistEntry) int {
		return cmp.Or(cmp.Compare(a.Task, b.Task), cmp.Compare(a.Time, b.Time))
	})
	return out
}

// eachScore calls f for every user and task with a score object.
func (sc *scoring) eachScore(f func(user, task string, v float64)) {
	for user, byTask := range sc.scores {
		for task, obj := range byTask {
			f(user, task, obj.get())
		}
	}
}

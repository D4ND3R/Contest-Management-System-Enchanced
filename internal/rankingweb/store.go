// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2011-2015 Luca Wehrstedt and the CMS contributors
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmsranking/Store.py and the DataWatcher of
// cmsranking/RankingWebServer.py of CMS (http://cms-dev.github.io/).

package rankingweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

type kind int

const (
	kContest kind = iota
	kTask
	kTeam
	kUser
	kSubmission
	kSubchange
	numKinds
)

// kinds describes each entity type: its event name, its path in the REST
// API and in CMS lib_dir, its parser and the kinds whose entities may
// become inconsistent when one of it is deleted (CMS Store depends).
var kinds = [numKinds]struct {
	name, path string
	parse      func(json.RawMessage) (entity, error)
	depends    []kind
}{
	kContest:    {"contest", "contests", parseContest, []kind{kTask}},
	kTask:       {"task", "tasks", parseTask, []kind{kSubmission}},
	kTeam:       {"team", "teams", parseTeam, []kind{kUser}},
	kUser:       {"user", "users", parseUser, []kind{kSubmission}},
	kSubmission: {"submission", "submissions", parseSubmission, []kind{kSubchange}},
	kSubchange:  {"subchange", "subchanges", parseSubchange, nil},
}

// state is every stored entity, by kind and key.
type state struct {
	items [numKinds]map[string]entity
}

func newState() *state {
	st := &state{}
	for k := range st.items {
		st.items[k] = map[string]entity{}
	}
	return st
}

func sortedKeys(m map[string]entity) []string {
	return slices.Sorted(maps.Keys(m))
}

// freezeOfTask returns the freeze time of the contest of a task.
func (st *state) freezeOfTask(task string) (int64, bool) {
	t, ok := st.items[kTask][task].(*Task)
	if !ok {
		return 0, false
	}
	c, ok := st.items[kContest][t.Contest].(*Contest)
	if !ok || c.Freeze == nil {
		return 0, false
	}
	return *c.Freeze, true
}

// Topics of the local hub: entity events go to everybody, score events
// of the public scores to the public and those of all the scores to
// administrators (they differ during a freeze).
const (
	topicAll    live.Topic = "all"
	topicPublic live.Topic = "public"
	topicFull   live.Topic = "full"
)

// Store keeps the scoreboard: the entities, the two scorings and the
// files of data_dir (persist.go). Writes are serialized (writeMu) and
// made durable before they are applied; reads take mu.
type Store struct {
	log *slog.Logger
	hub *live.Hub
	dir string

	writeMu sync.Mutex
	j       *journal

	mu      sync.RWMutex
	st      *state
	full    *scoring
	public  *scoring
	version atomic.Uint64

	persistErr atomic.Pointer[error]
}

// errNotFound is the error of an absent key.
var errNotFound = errors.New("not found")

func (s *Store) newScorings() {
	s.full = newScoring(s.st, nil)
	s.full.notify = s.scoreEvent(topicFull)
	s.full.init()
	s.public = s.newPublic()
}

func (s *Store) newPublic() *scoring {
	st := s.st
	p := newScoring(st, func(ch *Subchange) bool {
		sub, ok := st.items[kSubmission][ch.Submission].(*Submission)
		if !ok {
			return true
		}
		f, frozen := st.freezeOfTask(sub.Task)
		return !frozen || ch.Time < f
	})
	p.init()
	p.notify = s.scoreEvent(topicPublic)
	return p
}

func (s *Store) scoreEvent(topic live.Topic) func(user, task string, v float64) {
	return func(user, task string, v float64) {
		if s.hub != nil {
			s.hub.PublishRaw(topic, "score", []byte(user+" "+task+" "+PyFloat(v)))
		}
	}
}

func (s *Store) entityEvent(k kind, op, key string) {
	if s.hub != nil {
		s.hub.PublishRaw(topicAll, kinds[k].name, []byte(op+" "+key))
	}
}

// rebuildPublic recomputes the public scores after a freeze changed and
// sends the scores that changed.
func (s *Store) rebuildPublic() {
	old := s.public
	p := s.newPublic()
	seen := map[[2]string]bool{}
	p.eachScore(func(user, task string, v float64) {
		seen[[2]string{user, task}] = true
		if old.get(user, task) != v {
			p.notify(user, task, v)
		}
	})
	old.eachScore(func(user, task string, v float64) {
		if !seen[[2]string{user, task}] && v != 0 {
			p.notify(user, task, 0)
		}
	})
	s.public = p
}

// op is a write, as stored in the journal.
type op struct {
	Kind  string                     `json:"k"`
	Op    string                     `json:"op"` // put, del, delall
	Key   string                     `json:"key,omitempty"`
	Items map[string]json.RawMessage `json:"items,omitempty"`
}

func kindByName(name string) (kind, bool) {
	for k := range numKinds {
		if kinds[k].name == name {
			return k, true
		}
	}
	return 0, false
}

func encode(e entity) json.RawMessage {
	b, _ := json.Marshal(e)
	return b
}

// Put creates or updates the entity key (CMS create and update).
func (s *Store) Put(k kind, key string, raw json.RawMessage) error {
	if !ValidKey(key) {
		return invalid("Invalid key")
	}
	return s.Merge(k, map[string]json.RawMessage{key: raw})
}

// Merge creates or updates several entities (CMS merge_list): all of them
// or none.
func (s *Store) Merge(k kind, data map[string]json.RawMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	items := map[string]entity{}
	for key, raw := range data {
		if !ValidKey(key) {
			return invalid("[entity %s] Invalid key", key)
		}
		e, err := kinds[k].parse(raw)
		if err != nil {
			return fmt.Errorf("[entity %s] %w", key, err)
		}
		if !e.consistent(s.st) {
			return invalid("[entity %s] Inconsistent data", key)
		}
		items[key] = e
	}
	// Entities sent again unchanged are dropped: no journal, no events
	// (the dispatcher resends everything periodically).
	o := op{Kind: kinds[k].name, Op: "put", Items: map[string]json.RawMessage{}}
	for key, e := range items {
		enc := encode(e)
		if old, ok := s.st.items[k][key]; ok && bytes.Equal(encode(old), enc) {
			delete(items, key)
			continue
		}
		o.Items[key] = enc
	}
	if len(items) == 0 {
		return nil
	}
	if err := s.persist(o); err != nil {
		return err
	}
	s.mu.Lock()
	s.applyPut(k, items)
	s.mu.Unlock()
	s.maybeCompact()
	return nil
}

// Delete deletes an entity and those that depend on it (CMS delete).
func (s *Store) Delete(k kind, key string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, ok := s.st.items[k][key]; !ok {
		return errNotFound
	}
	if err := s.persist(op{Kind: kinds[k].name, Op: "del", Key: key}); err != nil {
		return err
	}
	s.mu.Lock()
	s.applyDelete(k, key)
	s.mu.Unlock()
	s.maybeCompact()
	return nil
}

// DeleteAll deletes every entity of a kind (CMS delete_list).
func (s *Store) DeleteAll(k kind) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if len(s.st.items[k]) == 0 {
		return nil
	}
	if err := s.persist(op{Kind: kinds[k].name, Op: "delall"}); err != nil {
		return err
	}
	s.mu.Lock()
	s.applyDeleteAll(k)
	s.mu.Unlock()
	s.maybeCompact()
	return nil
}

// apply replays an operation of the journal.
func (s *Store) apply(o op) error {
	k, ok := kindByName(o.Kind)
	if !ok {
		return fmt.Errorf("unknown kind %q", o.Kind)
	}
	switch o.Op {
	case "put":
		items := map[string]entity{}
		for key, raw := range o.Items {
			e, err := kinds[k].parse(raw)
			if err != nil {
				return fmt.Errorf("%s %s: %w", o.Kind, key, err)
			}
			items[key] = e
		}
		s.applyPut(k, items)
	case "del":
		if _, ok := s.st.items[k][o.Key]; ok {
			s.applyDelete(k, o.Key)
		}
	case "delall":
		s.applyDeleteAll(k)
	default:
		return fmt.Errorf("unknown operation %q", o.Op)
	}
	return nil
}

// applyPut stores items and runs the callbacks. The caller holds mu.
func (s *Store) applyPut(k kind, items map[string]entity) {
	defer s.version.Add(1)
	rebuild := false
	for _, key := range sortedKeys(items) {
		e := items[key]
		old, exists := s.st.items[k][key]
		s.st.items[k][key] = e
		if !exists {
			s.created(k, key, e)
			continue
		}
		rebuild = s.updated(k, key, old, e) || rebuild
	}
	if rebuild {
		s.rebuildPublic()
	}
}

func (s *Store) created(k kind, key string, e entity) {
	switch k {
	case kSubmission:
		s.full.createSubmission(key, e.(*Submission))
		s.public.createSubmission(key, e.(*Submission))
	case kSubchange:
		s.full.createSubchange(key, e.(*Subchange))
		s.public.createSubchange(key, e.(*Subchange))
	default:
		s.entityEvent(k, "create", key)
	}
}

// updated runs the callbacks of an update; it reports whether the public
// scores must be recomputed (a freeze changed).
func (s *Store) updated(k kind, key string, old, e entity) bool {
	switch k {
	case kSubmission:
		s.full.updateSubmission(key, old.(*Submission), e.(*Submission))
		s.public.updateSubmission(key, old.(*Submission), e.(*Submission))
		return false
	case kSubchange:
		s.full.updateSubchange(key, old.(*Subchange), e.(*Subchange))
		s.public.updateSubchange(key, old.(*Subchange), e.(*Subchange))
		return false
	}
	s.entityEvent(k, "update", key)
	switch k {
	case kContest:
		o, n := old.(*Contest), e.(*Contest)
		return (o.Freeze == nil) != (n.Freeze == nil) || (o.Freeze != nil && *o.Freeze != *n.Freeze)
	case kTask:
		o, n := old.(*Task), e.(*Task)
		if o.ScoreMode != n.ScoreMode {
			s.full.updateMode(key, n.ScoreMode)
			s.public.updateMode(key, n.ScoreMode)
		}
		return o.Contest != n.Contest
	}
	return false
}

// applyDelete deletes key and, recursively, what became inconsistent.
// The caller holds mu.
func (s *Store) applyDelete(k kind, key string) {
	defer s.version.Add(1)
	old, ok := s.st.items[k][key]
	if !ok {
		return
	}
	delete(s.st.items[k], key)
	for _, d := range kinds[k].depends {
		for _, dk := range sortedKeys(s.st.items[d]) {
			if e, ok := s.st.items[d][dk]; ok && !e.consistent(s.st) {
				s.applyDelete(d, dk)
			}
		}
	}
	switch k {
	case kSubmission:
		s.full.deleteSubmission(key, old.(*Submission))
		s.public.deleteSubmission(key, old.(*Submission))
	case kSubchange:
		s.full.deleteSubchange(key, old.(*Subchange))
		s.public.deleteSubchange(key, old.(*Subchange))
	default:
		s.entityEvent(k, "delete", key)
		if k == kContest && old.(*Contest).Freeze != nil {
			s.rebuildPublic()
		}
	}
}

func (s *Store) applyDeleteAll(k kind) {
	for _, key := range sortedKeys(s.st.items[k]) {
		s.applyDelete(k, key)
	}
}

// Reads. The caller holds mu (RLock): View and the handlers do.

// get returns the JSON of an entity.
func (s *Store) get(k kind, key string) (json.RawMessage, bool) {
	e, ok := s.st.items[k][key]
	if !ok {
		return nil, false
	}
	return encode(e), true
}

// list returns the JSON of every entity of a kind.
func (s *Store) list(k kind) json.RawMessage {
	out := make(map[string]json.RawMessage, len(s.st.items[k]))
	for key, e := range s.st.items[k] {
		out[key] = encode(e)
	}
	b, _ := json.Marshal(out)
	return b
}

// contestNames returns the CMS names of the stored contests (for the
// secret links).
func (s *Store) contestNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.st.items[kContest]))
	for key := range s.st.items[kContest] {
		names = append(names, DecodeID(key))
	}
	return names
}

// scoring returns the scores a viewer sees.
func (s *Store) scoring(admin bool) *scoring {
	if admin {
		return s.full
	}
	return s.public
}

// visibleSubmission hides from the public the submissions made after the
// freeze of their contest.
func (s *Store) visibleSubmission(admin bool) func(*subState) bool {
	if admin {
		return nil
	}
	return func(sub *subState) bool {
		f, frozen := s.st.freezeOfTask(sub.task)
		return !frozen || sub.time < f
	}
}

// Check is a health check: it fails when the last write could not be
// made durable.
func (s *Store) Check() error {
	if p := s.persistErr.Load(); p != nil && *p != nil {
		return *p
	}
	return nil
}

func kindByPath(path string) (kind, bool) {
	for k := range numKinds {
		if kinds[k].path == strings.Trim(path, "/") {
			return k, true
		}
	}
	return 0, false
}

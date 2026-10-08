// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestEventContract pins the names, topics and payloads documented in the
// package doc: pages depend on them.
func TestEventContract(t *testing.T) {
	counters := map[string]int64{CounterQueued: 3, CounterWorkers: 2}
	cases := []struct {
		ev     Event
		topics []Topic
		json   string
	}{
		{Submission{Contest: 1, Participation: 2, Task: 3, Dataset: 4, ID: 5, Opaque: 9007199254740993,
			Status: SubmissionEvaluating, New: true, ActiveDataset: true}.Event(),
			[]Topic{"a:1", "p:2"},
			`{"v":1,"contest":1,"participation":2,"task":3,"dataset":4,"submission":5,"opaque":"9007199254740993","status":"evaluating","new":true}`},
		{Submission{Contest: 1, Participation: 2, Task: 3, Dataset: 8, ID: 5, Status: SubmissionScored}.Event(),
			[]Topic{"a:1"},
			`{"v":1,"contest":1,"participation":2,"task":3,"dataset":8,"submission":5,"status":"scored"}`},
		{UserTest{Contest: 1, Participation: 2, Task: 3, ID: 6, Status: UserTestEvaluated}.Event(),
			[]Topic{"p:2", "a:1"},
			`{"v":1,"contest":1,"participation":2,"task":3,"user_test":6,"status":"evaluated"}`},
		{TaskScore{Contest: 1, Participation: 2, Task: 3}.Event(),
			[]Topic{"p:2"}, `{"v":1,"contest":1,"participation":2,"task":3}`},
		{Announcement{Contest: 1, ID: 7}.Event(),
			[]Topic{"c:1", "a:1"}, `{"v":1,"contest":1,"announcement":7}`},
		{PrivateMessage{Contest: 1, Participation: 2, ID: 8}.Event(),
			[]Topic{"p:2", "a:1"}, `{"v":1,"contest":1,"participation":2,"message":8}`},
		{Answer{Contest: 1, Participation: 2, Question: 9}.Event(),
			[]Topic{"p:2"}, `{"v":1,"contest":1,"participation":2,"question":9}`},
		{Question{Contest: 1, Participation: 2, ID: 9, Change: QuestionNew}.Event(),
			[]Topic{"a:1"}, `{"v":1,"contest":1,"participation":2,"question":9,"change":"new"}`},
		{ContestChange{Contest: 1}.Event(),
			[]Topic{"c:1", "a:1"}, `{"v":1,"contest":1}`},
		{ParticipationChange{Contest: 1, Participation: 2}.Event(),
			[]Topic{"p:2", "a:1"}, `{"v":1,"contest":1,"participation":2}`},
		{ScoreboardChange{Contest: 1, Participations: []int64{5, 2, 5}}.Event(),
			[]Topic{"s:1"}, `{"v":1,"contest":1,"participations":[2,5]}`},
		{Judging{Counters: counters}.Event(),
			[]Topic{"a"}, `{"v":1,"counters":{"queued":3,"workers":2}}`},
		{Judging{Contest: 1, Counters: counters}.Event(),
			[]Topic{"a:1"}, `{"v":1,"contest":1,"counters":{"queued":3,"workers":2}}`},
		{Worker{Name: "w1", State: WorkerUp}.Event(),
			[]Topic{"a"}, `{"v":1,"worker":"w1","state":"up"}`},
	}
	for _, c := range cases {
		if !validName(c.ev.Name) || reserved(c.ev.Name) {
			t.Errorf("invalid name %q", c.ev.Name)
		}
		if !slices.Equal(c.ev.Topics, c.topics) {
			t.Errorf("%s: topics %v, want %v", c.ev.Name, c.ev.Topics, c.topics)
		}
		b, err := json.Marshal(c.ev.Data)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.json {
			t.Errorf("%s:\n got %s\nwant %s", c.ev.Name, b, c.json)
		}
		want := c.ev.Name == EventScoreboard || c.ev.Name == EventJudging
		if c.ev.Throttle != want {
			t.Errorf("%s: throttle %v", c.ev.Name, c.ev.Throttle)
		}
	}
}

func TestScoreboardMerge(t *testing.T) {
	a := ScoreboardChange{Contest: 1, Participations: []int64{3, 1}}.Event().Data.(ScoreboardChange)
	b := ScoreboardChange{Contest: 1, Participations: []int64{2, 3}}.Event().Data.(ScoreboardChange)
	m := a.merge(b).(ScoreboardChange)
	if !slices.Equal(m.Participations, []int64{1, 2, 3}) || m.All {
		t.Errorf("merged %+v", m)
	}
	if !slices.Equal(a.Participations, []int64{1, 3}) {
		t.Errorf("merge changed its receiver: %+v", a)
	}
	all := ScoreboardChange{Contest: 1, All: true}.Event().Data.(ScoreboardChange)
	if m := a.merge(all).(ScoreboardChange); !m.All || m.Participations != nil {
		t.Errorf("merge with all: %+v", m)
	}
	var many []int64
	for i := range MaxScoreboardIDs {
		many = append(many, int64(i+10))
	}
	big := ScoreboardChange{Contest: 1, Participations: many}.Event().Data.(ScoreboardChange)
	if m := a.merge(big).(ScoreboardChange); !m.All || m.Participations != nil {
		t.Errorf("too many ids must become all: %d ids", len(m.Participations))
	}
}

func TestIDsAndFrames(t *testing.T) {
	for _, seq := range []uint64{1, 0x1f, 1767225600000000, 1<<64 - 1} {
		got, ok := ParseID(FormatID(seq))
		if !ok || got != seq {
			t.Errorf("round trip of %d: %d %v", seq, got, ok)
		}
	}
	for _, bad := range []string{"", "xyz", "-1", "10000000000000000", " 1"} {
		if _, ok := ParseID(bad); ok {
			t.Errorf("ParseID(%q) accepted", bad)
		}
	}
	got := string(appendFrame(nil, 0x2a, "score", []byte("a\r\nb\rc\nd")))
	want := "id: 2a\nevent: score\ndata: a\ndata: b\ndata: c\ndata: d\n\n"
	if got != want {
		t.Errorf("frame %q, want %q", got, want)
	}
	if got := string(appendFrame(nil, 0, "x", nil)); got != "event: x\ndata: \n\n" {
		t.Errorf("empty frame %q", got)
	}
	m := newMessage(7, "p:1", "submission", []byte(`{"v":1}`))
	if string(m.Data) != `{"v":1}` || string(m.frame) != "id: 7\nevent: submission\ndata: {\"v\":1}\n\n" {
		t.Errorf("message %q %q", m.Data, m.frame)
	}
	if m := newMessage(7, "r", "user", []byte("a\nb")); string(m.Data) != "a\nb" {
		t.Errorf("multi-line data %q", m.Data)
	}
}

func TestNamesAndTopics(t *testing.T) {
	for _, n := range []string{"score", "user_test", "a1"} {
		if !validName(n) {
			t.Errorf("%q rejected", n)
		}
	}
	for _, n := range []string{"", "Score", "a:b", "a b", "a\nb", strings.Repeat("a", 65)} {
		if validName(n) {
			t.Errorf("%q accepted", n)
		}
	}
	for _, tp := range []Topic{ParticipationTopic(1), AdminTopic, "r"} {
		if !validTopic(tp) {
			t.Errorf("%q rejected", tp)
		}
	}
	for _, tp := range []Topic{"", "p:*", "a b", "x?", "[a]"} {
		if validTopic(tp) {
			t.Errorf("%q accepted", tp)
		}
	}
	if got := globEscape(`cms:*?[]\:`); got != `cms:\*\?\[\]\\:` {
		t.Errorf("globEscape %q", got)
	}
}

// TestNoDatabaseAccess: the package must stay usable by the worker and
// the ranking, which never touch PostgreSQL (D12, D14).
func TestNoDatabaseAccess(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/jackc/pgx") || strings.HasSuffix(dep, "/internal/db") ||
			strings.HasSuffix(dep, "/internal/store") {
			t.Errorf("live depends on %s", dep)
		}
	}
}

func TestResyncSpread(t *testing.T) {
	for _, c := range []struct {
		reason  string
		streams int
		want    time.Duration
	}{
		{ReasonOverflow, 1500, 2 * time.Second},
		{ReasonReconnect, 1, 2 * time.Second},
		{ReasonReconnect, 1500, 7500 * time.Millisecond},
		{ReasonGap, 1500, 7500 * time.Millisecond},
		{ReasonReconnect, 100000, 30 * time.Second},
	} {
		if got := ResyncSpread(c.reason, c.streams); got != c.want {
			t.Errorf("ResyncSpread(%s, %d) = %v, want %v", c.reason, c.streams, got, c.want)
		}
	}
	var p ResyncPayload
	if err := json.Unmarshal(resyncData(ReasonGap, 7500*time.Millisecond), &p); err != nil ||
		p != (ResyncPayload{V: 1, Reason: ReasonGap, Spread: 7500}) {
		t.Errorf("resync data: %+v %v", p, err)
	}
}

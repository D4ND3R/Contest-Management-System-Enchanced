// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package rankingweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

func newTestHub() *live.Hub {
	return live.NewLocalHub(nil, prometheus.NewRegistry(), live.HubOptions{})
}

func openStore(t *testing.T, dir string) (*Store, *live.Hub) {
	t.Helper()
	hub := newTestHub()
	s, err := Open(dir, hub, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, hub
}

// events collects the events published to the subscription so far, as
// "<topic> <event> <data>".
func events(t *testing.T, sub *live.Subscription) []string {
	t.Helper()
	var out []string
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		m, err := sub.Next(ctx)
		cancel()
		if err != nil {
			return out
		}
		out = append(out, fmt.Sprintf("%s %s %s", m.Topic, m.Event, m.Data))
	}
}

func subscribe(hub *live.Hub) *live.Subscription {
	return hub.Subscribe([]live.Topic{topicAll, topicPublic, topicFull}, "")
}

func mustPut(t *testing.T, s *Store, k kind, key, raw string) {
	t.Helper()
	if err := s.Put(k, key, json.RawMessage(raw)); err != nil {
		t.Fatalf("put %s %s: %v", kinds[k].name, key, err)
	}
}

func sub(user, task string, time int64) string {
	return fmt.Sprintf(`{"user":%q,"task":%q,"time":%d}`, user, task, time)
}

func scoreChange(submission string, time int64, score float64, extra ...string) string {
	e, _ := json.Marshal(extra)
	if extra == nil {
		e = []byte("[]")
	}
	return fmt.Sprintf(`{"submission":%q,"time":%d,"score":%s,"extra":%s}`, submission, time, PyFloat(score), e)
}

func tokenChange(submission string, time int64) string {
	return fmt.Sprintf(`{"submission":%q,"time":%d,"token":true}`, submission, time)
}

// seed stores a contest c (freeze: none when 0), team it, users u1 (in it)
// and u2 and task a with the score mode.
func seed(t *testing.T, s *Store, mode string, freeze int64) {
	t.Helper()
	c := jsContest
	if freeze > 0 {
		c = fmt.Sprintf(`{"name":"Contest","begin":0,"end":1000,"score_precision":2,"freeze":%d}`, freeze)
	}
	mustPut(t, s, kContest, "c", c)
	mustPut(t, s, kTeam, "it", jsTeam)
	mustPut(t, s, kUser, "u1", jsUser)
	mustPut(t, s, kUser, "u2", `{"f_name":"B","l_name":"C","team":null}`)
	mustPut(t, s, kTask, "a", jsTask("c", mode))
}

func (s *Store) score(admin bool, user, task string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scoring(admin).get(user, task)
}

func (s *Store) history(admin bool) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(mustJSON(s.scoring(admin).globalHistory()))
}

func TestStoreCreateUpdateDeleteEvents(t *testing.T) {
	s, hub := openStore(t, t.TempDir())
	ev := subscribe(hub)
	defer ev.Close()

	seed(t, s, ScoreModeMax, 0)
	want := []string{"all contest create c", "all team create it", "all user create u1", "all user create u2", "all task create a"}
	if got := events(t, ev); !slices.Equal(got, want) {
		t.Fatalf("create events:\n got %q\nwant %q", got, want)
	}

	// Sending the same data again changes nothing (no events).
	seed(t, s, ScoreModeMax, 0)
	if got := events(t, ev); len(got) != 0 {
		t.Fatalf("unchanged entities sent events: %q", got)
	}

	mustPut(t, s, kTeam, "it", `{"name":"Italia"}`)
	if got := events(t, ev); !slices.Equal(got, []string{"all team update it"}) {
		t.Fatalf("update events: %q", got)
	}

	// Inconsistent data is rejected, all or nothing.
	err := s.Merge(kUser, map[string]json.RawMessage{
		"u3": json.RawMessage(`{"f_name":"x","l_name":"y","team":null}`),
		"u4": json.RawMessage(`{"f_name":"x","l_name":"y","team":"nope"}`),
	})
	if !errors.Is(err, ErrInvalidData) || !strings.Contains(err.Error(), "[entity u4] ") {
		t.Fatalf("inconsistent merge: %v", err)
	}
	if _, ok := s.st.items[kUser]["u3"]; ok {
		t.Fatal("a rejected merge stored part of its entities")
	}
	if err := s.Put(kTask, "b", json.RawMessage(jsTask("nope", "max"))); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("task of a missing contest: %v", err)
	}
	if err := s.Put(kSubmission, "1", json.RawMessage(sub("u1", "nope", 1))); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("submission of a missing task: %v", err)
	}
	if err := s.Put(kSubchange, "x", json.RawMessage(tokenChange("nope", 1))); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("subchange of a missing submission: %v", err)
	}
	if err := s.Put(kTeam, "a-b", json.RawMessage(jsTeam)); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("invalid key: %v", err)
	}

	mustPut(t, s, kSubmission, "1", sub("u1", "a", 10))
	mustPut(t, s, kSubchange, "10_1s", scoreChange("1", 10, 40))
	mustPut(t, s, kSubmission, "2", sub("u2", "a", 20))
	mustPut(t, s, kSubchange, "20_2s", scoreChange("2", 20, 70))
	// Submissions and subchanges send score events only.
	want = []string{"full score u1 a 40.0", "public score u1 a 40.0", "full score u2 a 70.0", "public score u2 a 70.0"}
	if got := events(t, ev); !slices.Equal(got, want) {
		t.Fatalf("score events:\n got %q\nwant %q", got, want)
	}

	// Deleting a team deletes its users, their submissions and
	// subchanges, and their scores go to 0.
	if err := s.Delete(kTeam, "it"); err != nil {
		t.Fatal(err)
	}
	want = []string{"full score u1 a 0.0", "public score u1 a 0.0", "all user delete u1", "all team delete it"}
	if got := events(t, ev); !slices.Equal(got, want) {
		t.Fatalf("cascade events:\n got %q\nwant %q", got, want)
	}
	for k, key := range map[kind]string{kUser: "u1", kSubmission: "1", kSubchange: "10_1s"} {
		if _, ok := s.st.items[k][key]; ok {
			t.Errorf("%s %s survived the deletion of its team", kinds[k].name, key)
		}
	}
	if _, ok := s.full.scores["u1"]; ok {
		t.Error("the score of a deleted user is kept")
	}
	if err := s.Delete(kTeam, "it"); !errors.Is(err, errNotFound) {
		t.Errorf("deleting a missing key: %v", err)
	}

	// Deleting the contest deletes everything that depends on it.
	if err := s.Delete(kContest, "c"); err != nil {
		t.Fatal(err)
	}
	want = []string{"full score u2 a 0.0", "public score u2 a 0.0", "all task delete a", "all contest delete c"}
	if got := events(t, ev); !slices.Equal(got, want) {
		t.Fatalf("contest cascade events:\n got %q\nwant %q", got, want)
	}
	if n := len(s.st.items[kTask]) + len(s.st.items[kSubmission]) + len(s.st.items[kSubchange]); n != 0 {
		t.Errorf("%d entities survived the deletion of their contest", n)
	}
	if len(s.st.items[kUser]) != 1 {
		t.Error("users do not depend on contests")
	}

	if err := s.DeleteAll(kUser); err != nil {
		t.Fatal(err)
	}
	if got := events(t, ev); !slices.Equal(got, []string{"all user delete u2"}) {
		t.Fatalf("delete all events: %q", got)
	}
}

func TestScoreModes(t *testing.T) {
	type step struct {
		k        kind
		key, raw string
	}
	cases := []struct {
		mode    string
		steps   []step
		score   float64
		history string
	}{
		{ScoreModeMax, []step{
			{kSubmission, "1", sub("u1", "a", 10)}, {kSubchange, "10_1s", scoreChange("1", 10, 30)},
			{kSubmission, "2", sub("u1", "a", 20)}, {kSubchange, "20_2s", scoreChange("2", 20, 50)},
			{kSubmission, "3", sub("u1", "a", 30)}, {kSubchange, "30_3s", scoreChange("3", 30, 40)},
		}, 50, `[["u1","a",10,30.0],["u1","a",20,50.0]]`},
		// The last submission counts, and the best tokened one.
		{ScoreModeMaxTokenedLast, []step{
			{kSubmission, "1", sub("u1", "a", 10)}, {kSubchange, "10_1s", scoreChange("1", 10, 50)},
			{kSubmission, "2", sub("u1", "a", 20)}, {kSubchange, "20_2s", scoreChange("2", 20, 20)},
			{kSubchange, "25_1t", tokenChange("1", 25)},
			{kSubmission, "3", sub("u1", "a", 30)}, {kSubchange, "30_3s", scoreChange("3", 30, 10)},
		}, 50, `[["u1","a",10,50.0],["u1","a",20,20.0],["u1","a",25,50.0]]`},
		// The best of each subtask, over all submissions (zip_longest).
		{ScoreModeMaxSubtask, []step{
			{kSubmission, "1", sub("u1", "a", 10)}, {kSubchange, "10_1s", scoreChange("1", 10, 15, "10.0", "0.0", "5.0")},
			{kSubmission, "2", sub("u1", "a", 20)}, {kSubchange, "20_2s", scoreChange("2", 20, 20, "0.0", "20.0")},
		}, 35, `[["u1","a",10,15.0],["u1","a",20,35.0]]`},
		// Without details, the score of a submission is one subtask.
		{ScoreModeMaxSubtask, []step{
			{kSubmission, "1", sub("u1", "a", 10)}, {kSubchange, "10_1s", scoreChange("1", 10, 15)},
			{kSubmission, "2", sub("u1", "a", 20)}, {kSubchange, "20_2s", scoreChange("2", 20, 12)},
		}, 15, `[["u1","a",10,15.0]]`},
		// A missing subtask counts as 0, even against negative scores.
		{ScoreModeMaxSubtask, []step{
			{kSubmission, "1", sub("u1", "a", 10)}, {kSubchange, "10_1s", scoreChange("1", 10, -3, "-1.0", "-2.0")},
			{kSubmission, "2", sub("u1", "a", 20)}, {kSubchange, "20_2s", scoreChange("2", 20, -4, "-4.0")},
		}, -1, `[["u1","a",10,-3.0],["u1","a",20,-1.0]]`},
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%d_%s", i, c.mode), func(t *testing.T) {
			s, _ := openStore(t, t.TempDir())
			seed(t, s, c.mode, 0)
			for _, st := range c.steps {
				mustPut(t, s, st.k, st.key, st.raw)
			}
			if got := s.score(true, "u1", "a"); got != c.score {
				t.Errorf("score %v, want %v", got, c.score)
			}
			if got := s.history(true); got != c.history {
				t.Errorf("history\n got %s\nwant %s", got, c.history)
			}
		})
	}
}

func TestScoreHistoryOrderAndChanges(t *testing.T) {
	s, hub := openStore(t, t.TempDir())
	seed(t, s, ScoreModeMaxTokenedLast, 0)
	mustPut(t, s, kSubmission, "1", sub("u1", "a", 10))
	mustPut(t, s, kSubmission, "2", sub("u1", "a", 20))
	mustPut(t, s, kSubmission, "3", sub("u2", "a", 15))
	// Subchanges arrive out of order: the history is recomputed.
	mustPut(t, s, kSubchange, "20_2s", scoreChange("2", 20, 30))
	mustPut(t, s, kSubchange, "10_1s", scoreChange("1", 10, 60))
	mustPut(t, s, kSubchange, "15_3s", scoreChange("3", 15, 60))
	want := `[["u1","a",10,60.0],["u2","a",15,60.0],["u1","a",20,30.0]]`
	if got := s.history(true); got != want {
		t.Fatalf("history\n got %s\nwant %s", got, want)
	}

	ev := subscribe(hub)
	defer ev.Close()
	// Updating a subchange recomputes; same time: the order of the keys.
	mustPut(t, s, kSubchange, "20_2s", scoreChange("2", 20, 80))
	mustPut(t, s, kSubchange, "20_2t", tokenChange("1", 20))
	if got := s.score(true, "u1", "a"); got != 80 {
		t.Fatalf("score %v, want 80", got)
	}
	// Deleting a subchange recomputes.
	if err := s.Delete(kSubchange, "20_2s"); err != nil {
		t.Fatal(err)
	}
	if got := s.score(true, "u1", "a"); got != 60 {
		t.Fatalf("score after deleting the last subchange %v, want 60 (the tokened one)", got)
	}
	got := events(t, ev)
	want2 := []string{"full score u1 a 80.0", "public score u1 a 80.0", "full score u1 a 60.0", "public score u1 a 60.0"}
	if !slices.Equal(got, want2) {
		t.Fatalf("events\n got %q\nwant %q", got, want2)
	}

	// A new score mode applies to the existing scores.
	mustPut(t, s, kTask, "a", jsTask("c", ScoreModeMax))
	if got := s.score(true, "u1", "a"); got != 60 {
		t.Fatalf("max score %v", got)
	}

	// /scores lists positive scores only; /sublist the submissions.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if got := string(mustJSON(s.full.scoreMap())); got != `{"u1":{"a":60.0},"u2":{"a":60.0}}` {
		t.Errorf("scores %s", got)
	}
	wantSubs := `[{"key":"1","user":"u1","task":"a","time":10,"score":60.0,"token":true,"extra":[]},` +
		`{"key":"2","user":"u1","task":"a","time":20,"score":0.0,"token":false,"extra":[]}]`
	if got := string(mustJSON(s.full.sublist("u1", nil))); got != wantSubs {
		t.Errorf("sublist\n got %s\nwant %s", got, wantSubs)
	}
	if got := string(mustJSON(s.full.sublist("nobody", nil))); got != `[]` {
		t.Errorf("sublist of an unknown user %s", got)
	}
}

func TestFreeze(t *testing.T) {
	s, hub := openStore(t, t.TempDir())
	seed(t, s, ScoreModeMax, 500)
	ev := subscribe(hub)
	defer ev.Close()
	mustPut(t, s, kSubmission, "1", sub("u1", "a", 100))
	mustPut(t, s, kSubchange, "100_1s", scoreChange("1", 100, 40))
	mustPut(t, s, kSubmission, "2", sub("u1", "a", 600))
	mustPut(t, s, kSubchange, "600_2s", scoreChange("2", 600, 90))
	if a, p := s.score(true, "u1", "a"), s.score(false, "u1", "a"); a != 90 || p != 40 {
		t.Fatalf("admin %v public %v, want 90 and 40", a, p)
	}
	want := []string{"full score u1 a 40.0", "public score u1 a 40.0", "full score u1 a 90.0"}
	if got := events(t, ev); !slices.Equal(got, want) {
		t.Fatalf("events during the freeze\n got %q\nwant %q", got, want)
	}
	if got := s.history(false); got != `[["u1","a",100,40.0]]` {
		t.Errorf("public history %s", got)
	}
	s.mu.RLock()
	pub := s.public.sublist("u1", s.visibleSubmission(false))
	adm := s.full.sublist("u1", s.visibleSubmission(true))
	s.mu.RUnlock()
	if len(pub) != 1 || pub[0].Key != "1" || len(adm) != 2 {
		t.Errorf("sublists: public %v, admin %v", pub, adm)
	}

	// Moving the freeze later reveals the score; removing it as well.
	mustPut(t, s, kContest, "c", `{"name":"Contest","begin":0,"end":1000,"score_precision":2,"freeze":700}`)
	if p := s.score(false, "u1", "a"); p != 90 {
		t.Fatalf("public score after moving the freeze %v", p)
	}
	want = []string{"all contest update c", "public score u1 a 90.0"}
	if got := events(t, ev); !slices.Equal(got, want) {
		t.Fatalf("events of the unfreeze\n got %q\nwant %q", got, want)
	}
	mustPut(t, s, kContest, "c", `{"name":"Contest","begin":0,"end":1000,"score_precision":2,"freeze":50}`)
	if p := s.score(false, "u1", "a"); p != 0 {
		t.Fatalf("public score with an earlier freeze %v", p)
	}
	mustPut(t, s, kContest, "c", jsContest)
	if p := s.score(false, "u1", "a"); p != 90 {
		t.Fatalf("public score without freeze %v", p)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	seed(t, s, ScoreModeMax, 500)
	mustPut(t, s, kSubmission, "1", sub("u1", "a", 100))
	mustPut(t, s, kSubchange, "100_1s", scoreChange("1", 100, 40))
	mustPut(t, s, kSubmission, "2", sub("u2", "a", 600))
	mustPut(t, s, kSubchange, "600_2s", scoreChange("2", 600, 90))
	if err := s.Delete(kUser, "u2"); err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, kSubmission, "3", sub("u1", "a", 700))
	mustPut(t, s, kSubchange, "700_3s", scoreChange("3", 700, 95))
	snapshot := func(s *Store) string {
		s.mu.RLock()
		defer s.mu.RUnlock()
		b, err := s.snapshotBytes()
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + s.history(true) + s.history(false)
	}
	want := snapshot(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(kTeam, "x", json.RawMessage(jsTeam)); err == nil {
		t.Error("a closed store accepted a write")
	}

	// Restart: the journal is replayed.
	s2, _ := openStore(t, dir)
	if got := snapshot(s2); got != want {
		t.Fatalf("state after a restart\n got %s\nwant %s", got, want)
	}
	if s2.score(false, "u1", "a") != 40 || s2.score(true, "u1", "a") != 95 {
		t.Fatal("scores not restored")
	}
	// After a restart the journal is folded into the snapshot.
	if fi, err := os.Stat(filepath.Join(dir, journalFile)); err != nil || fi.Size() != 0 {
		t.Fatalf("journal after a restart: %v %v", fi, err)
	}
	mustPut(t, s2, kTeam, "fr", `{"name":"France"}`)
	want = snapshot(s2)
	_ = s2.Close()

	// A torn last line (a crash in the middle of an unacknowledged write)
	// is dropped; the writes before it are kept.
	f, err := os.OpenFile(filepath.Join(dir, journalFile), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"k":"team","op":"put","items":{"de":{"na`)
	_ = f.Close()
	s3, _ := openStore(t, dir)
	if got := snapshot(s3); got != want {
		t.Fatalf("state after a torn line\n got %s\nwant %s", got, want)
	}
	mustPut(t, s3, kTeam, "de", `{"name":"Germany"}`)
	want = snapshot(s3)
	_ = s3.Close()
	s4, _ := openStore(t, dir)
	if got := snapshot(s4); got != want {
		t.Fatalf("state after a write following a torn line\n got %s\nwant %s", got, want)
	}
}

// A failed write of the journal must not leave a partial line that hides
// the acknowledged writes after it at the next start.
func TestPersistFailureLeavesNoPartialLine(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	mustPut(t, s, kTeam, "it", jsTeam)
	// Simulate a write that stopped half way: the journal has a partial
	// line and the write reported an error.
	good := s.j.size
	if _, err := s.j.f.WriteString(`{"k":"team","op":"put","it`); err != nil {
		t.Fatal(err)
	}
	s.rollbackJournal()
	if fi, _ := s.j.f.Stat(); fi.Size() != good {
		t.Fatalf("journal size %d after a rollback, want %d", fi.Size(), good)
	}
	mustPut(t, s, kTeam, "fr", `{"name":"France"}`)
	_ = s.Close()
	s2, _ := openStore(t, dir)
	if _, ok := s2.st.items[kTeam]["fr"]; !ok {
		t.Fatal("an acknowledged write after a failed one was lost")
	}
}

func TestImportCMSLibDir(t *testing.T) {
	dir := t.TempDir()
	write := func(path, data string) {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("contests/c.json", jsContest)
	write("tasks/a.json", jsTask("c", ScoreModeMax))
	write("teams/it.json", jsTeam)
	write("users/u1.json", jsUser)
	write("submissions/1.json", sub("u1", "a", 10))
	write("subchanges/10_1s.json", scoreChange("1", 10, 55))
	write("subchanges/bad.json", `{"submission":"1"}`)
	write("subchanges/notes.txt", `x`)
	s, _ := openStore(t, dir)
	if got := s.score(false, "u1", "a"); got != 55 {
		t.Fatalf("imported score %v", got)
	}
	if _, ok := s.st.items[kSubchange]["bad"]; ok {
		t.Error("an invalid file was imported")
	}
	if _, err := os.Stat(filepath.Join(dir, snapshotFile)); err != nil {
		t.Errorf("no snapshot after the import: %v", err)
	}
}

func TestReplayIgnoresBadEntries(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"k":"team","op":"put","items":{"it":{"name":"Italy"}}}`,
		`{"k":"nope","op":"put"}`,
		`{"k":"team","op":"explode"}`,
		`{"k":"team","op":"put","items":{"bad":{"x":1}}}`,
		`{"k":"team","op":"put","items":{"fr":{"name":"France"}}}`,
		`{"k":"team","op":"del","key":"missing"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, journalFile), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := openStore(t, dir)
	got := sortedKeys(s.st.items[kTeam])
	if !reflect.DeepEqual(got, []string{"fr", "it"}) {
		t.Fatalf("teams after replay: %v", got)
	}
}

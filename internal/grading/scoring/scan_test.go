// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// referenceSubtaskScores is parseSubtaskScores written with encoding/json
// and Python's rules: a list of objects, each with the keys "idx" (a
// hashable value) and "score" (a number).
func referenceSubtaskScores(details []byte) (m subtaskScores, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(details))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return m, false
	}
	list, isList := v.([]any)
	if !isList {
		return m, false
	}
	for _, el := range list {
		obj, isObj := el.(map[string]any)
		if !isObj {
			return m, false
		}
		idx, hasIdx := obj["idx"]
		score, hasScore := obj["score"]
		if !hasIdx || !hasScore {
			return m, false
		}
		var k subtaskKey
		switch x := idx.(type) {
		case json.Number:
			f, err := strconv.ParseFloat(string(x), 64)
			if err != nil {
				return m, false
			}
			k = subtaskKey{kind: kindNumber, num: f}
		case string:
			k = subtaskKey{kind: kindString, str: x}
		case bool:
			k = subtaskKey{kind: kindNumber}
			if x {
				k.num = 1
			}
		case nil:
			k = subtaskKey{kind: kindNull}
		default:
			return m, false
		}
		n, isNum := score.(json.Number)
		if !isNum {
			return m, false
		}
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return m, false
		}
		m.set(k, f)
	}
	return m, true
}

func sameScores(a, b subtaskScores, aok, bok bool) bool {
	if aok != bok {
		return false
	}
	if !aok {
		return true
	}
	return reflect.DeepEqual(a.keys, b.keys) && reflect.DeepEqual(a.scores, b.scores) ||
		len(a.keys) == 0 && len(b.keys) == 0
}

// Tricky strings: quotes, backslashes and brackets inside strings.
var trickyStrings = []string{
	`""`, `"a"`, `"\""`, `"\\"`, `"]}"`, `"[{\"idx\": 1}]"`, `"\\\""`, `"\u0069dx"`, `"ñ"`, `"\n"`,
	`"\\\\"`, `"\\\\\"]"`, `"x\\"`,
}

func randSpace(r *rand.Rand) string {
	return []string{"", "", " ", "\n  ", "\t"}[r.IntN(5)]
}

func randScalar(r *rand.Rand) string {
	switch r.IntN(8) {
	case 0:
		return strconv.Itoa(r.IntN(5) - 1)
	case 1:
		return strconv.FormatFloat(r.Float64()*100, 'g', -1, 64)
	case 2:
		return []string{"1.0", "-0", "1e2", "2E-1", "0.5"}[r.IntN(5)]
	case 3:
		return trickyStrings[r.IntN(len(trickyStrings))]
	case 4:
		return []string{"true", "false", "null"}[r.IntN(3)]
	default:
		return strconv.Itoa(r.IntN(4))
	}
}

func randValue(r *rand.Rand, depth int) string {
	if depth > 2 || r.IntN(3) > 0 {
		return randScalar(r)
	}
	n := r.IntN(4)
	parts := make([]string, n)
	if r.IntN(2) == 0 {
		for i := range parts {
			parts[i] = randSpace(r) + randValue(r, depth+1) + randSpace(r)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	for i := range parts {
		parts[i] = randSpace(r) + trickyStrings[r.IntN(len(trickyStrings))] + randSpace(r) + ":" + randSpace(r) + randValue(r, depth+1)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

var keyChoices = []string{`"idx"`, `"score"`, `"testcases"`, `"max_score"`, `"IDX"`, `"Score"`, `"\u0069dx"`, `"sc\u006fre"`, `"x"`}

func randDetails(r *rand.Rand) string {
	if r.IntN(15) == 0 {
		return randValue(r, 0)
	}
	n := r.IntN(5)
	elems := make([]string, n)
	for i := range elems {
		if r.IntN(12) == 0 {
			elems[i] = randValue(r, 1)
			continue
		}
		var members []string
		if r.IntN(6) > 0 {
			members = append(members, `"idx":`+randSpace(r)+randScalar(r))
		}
		if r.IntN(6) > 0 {
			score := strconv.FormatFloat(float64(r.IntN(1000))/8, 'g', -1, 64)
			if r.IntN(8) == 0 {
				score = randScalar(r)
			}
			members = append(members, `"score":`+score)
		}
		for range r.IntN(4) {
			members = append(members, keyChoices[r.IntN(len(keyChoices))]+randSpace(r)+":"+randSpace(r)+randValue(r, 1))
		}
		r.Shuffle(len(members), func(a, b int) { members[a], members[b] = members[b], members[a] })
		for j := range members {
			members[j] = randSpace(r) + members[j] + randSpace(r)
		}
		elems[i] = "{" + strings.Join(members, ",") + "}"
	}
	return randSpace(r) + "[" + strings.Join(elems, ","+randSpace(r)) + "]" + randSpace(r)
}

func TestParseSubtaskScoresMatchesReference(t *testing.T) {
	r := rand.New(rand.NewPCG(49, 1))
	okCount := 0
	for i := range 30000 {
		doc := randDetails(r)
		if !json.Valid([]byte(doc)) {
			t.Fatalf("generated invalid JSON %s", doc)
		}
		got, gotOK := parseSubtaskScores([]byte(doc))
		want, wantOK := referenceSubtaskScores([]byte(doc))
		if !sameScores(got, want, gotOK, wantOK) {
			t.Fatalf("case %d %s:\n got %+v %v\nwant %+v %v", i, doc, got, gotOK, want, wantOK)
		}
		if gotOK && len(got.keys) > 0 {
			okCount++
		}
	}
	if okCount < 3000 {
		t.Errorf("only %d documents with subtasks", okCount)
	}
}

func FuzzParseSubtaskScores(f *testing.F) {
	for _, seed := range []string{
		`[]`, `[{"idx": 0, "score": 1.5}]`, `[{"score": 2, "idx": "a", "testcases": [{"idx": "]"}]}]`,
		`[{"idx": 1, "score": 3}, {"idx": 1.0, "score": 4}]`, `{"idx": 0}`, `[1]`, `[{"idx": [0], "score": 1}]`,
		`[{"\u0069dx": 2, "score": 1}]`, `[{"idx": 0, "score": "1"}]`, ` [ { "idx" : null , "score" : -0 } ] `,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		if !json.Valid(doc) {
			return
		}
		got, gotOK := parseSubtaskScores(doc)
		want, wantOK := referenceSubtaskScores(doc)
		if !sameScores(got, want, gotOK, wantOK) {
			t.Fatalf("%s:\n got %+v %v\nwant %+v %v", doc, got, gotOK, want, wantOK)
		}
	})
}

func ExampleTaskScore() {
	subs := []Submission{
		{Scored: true, Score: 40, ScoreDetails: json.RawMessage(`[{"idx": 0, "score": 40}, {"idx": 1, "score": 0}]`)},
		{Scored: true, Score: 60, ScoreDetails: json.RawMessage(`[{"idx": 0, "score": 0}, {"idx": 1, "score": 60}]`)},
	}
	for _, mode := range []string{ModeMax, ModeMaxSubtask, ModeMaxTokenedLast} {
		score, partial, _ := TaskScore(subs, mode, 0, Full)
		fmt.Println(mode, score, partial)
	}
	// Output:
	// max 60 false
	// max_subtask 100 false
	// max_tokened_last 60 false
}

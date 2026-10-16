// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// captureDB is a q.DBTX that remembers the last query instead of running
// it, to EXPLAIN it.
type captureDB struct {
	q.DBTX
	sql string
}

func (c *captureDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	c.sql = sql
	return nil, context.Canceled
}

// scans returns the tables a plan reads with a sequential scan.
func scans(n map[string]any) []string {
	var out []string
	if n["Node Type"] == "Seq Scan" {
		out = append(out, n["Relation Name"].(string))
	}
	if ps, ok := n["Plans"].([]any); ok {
		for _, p := range ps {
			out = append(out, scans(p.(map[string]any))...)
		}
	}
	return out
}

// TestParticipationTokensPlan: the tokens of a participant are found
// from their submissions (tokens_submission_id_key), not by reading
// every token of the installation (it runs on every submissions page
// while tokens are enabled and on every token played). The generic plan
// is checked, the one pgx's cached statements end up using.
func TestParticipationTokensPlan(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	// 500 participants with 70 submissions each, half of them with a
	// token.
	ts.Exec(`INSERT INTO users (first_name, last_name, username, password)
		SELECT 'U', 'U' || i, 'user' || i, 'plaintext:x' FROM generate_series(1, 500) i`)
	ts.Exec(`INSERT INTO participations (contest_id, user_id, group_id)
		SELECT c.id, u.id, c.main_group_id FROM contests c, users u WHERE c.id = $1 AND u.username LIKE 'user%'`, ts.Contest)
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		SELECT p.id * 1000 + i, p.id, (SELECT min(id) FROM tasks WHERE contest_id = $1),
		       '2026-01-01'::timestamptz + i * interval '10 seconds', 'C++17 / g++'
		FROM participations p, generate_series(1, 70) i WHERE p.contest_id = $1`, ts.Contest)
	ts.Exec(`INSERT INTO tokens (submission_id, timestamp) SELECT s.id, s.timestamp FROM submissions s
		WHERE s.id % 2 = 0 AND NOT EXISTS (SELECT 1 FROM tokens t WHERE t.submission_id = s.id)`)
	ts.Exec(`ANALYZE tokens`)
	ts.Exec(`ANALYZE submissions`)

	c := &captureDB{}
	_, _ = q.New(c).CWSParticipationTokens(context.Background(), 1)
	// The simple protocol: $1 stays a parameter of the generic plan.
	conn, err := ts.Pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res, err := conn.Conn().PgConn().Exec(context.Background(), "EXPLAIN (GENERIC_PLAN, FORMAT JSON) "+c.sql).ReadAll()
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	out := res[0].Rows[0][0]
	var plans []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal(out, &plans); err != nil {
		t.Fatal(err)
	}
	if s := scans(plans[0].Plan); len(s) > 0 {
		t.Errorf("the plan reads whole tables %v:\n%s", s, out)
	}

	// Same rows as before.
	rows, err := ts.S.st.Q.CWSParticipationTokens(context.Background(), ts.Participation("alice"))
	if err != nil {
		t.Fatal(err)
	}
	var want int
	if err := ts.Pool.QueryRow(context.Background(), `SELECT count(*) FROM tokens tk
		JOIN submissions s ON s.id = tk.submission_id WHERE s.participation_id = $1`, ts.Participation("alice")).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if len(rows) != want || want == 0 {
		t.Errorf("%d tokens, want %d", len(rows), want)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Timestamp.Before(rows[i-1].Timestamp) {
			t.Error("tokens not in order")
		}
	}
}

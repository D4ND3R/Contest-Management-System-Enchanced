// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Changes and the audit log (R6.6; new, CMS has none). Every handler that
// changes data does it inside Ctx.Write:
//
//	err := c.Write(func(tx *Tx) error {
//		old, err := tx.Q.GetUser(tx.Ctx, id)
//		if err != nil {
//			return err
//		}
//		upd, err := tx.Q.UpdateUser(tx.Ctx, params)
//		if err != nil {
//			return err
//		}
//		tx.Publish(live.ContestChange{Contest: ...}.Event())
//		return tx.Audit(AuditEntry{Action: "user.update", TargetType: "user",
//			TargetID: int64(id), Before: old, After: upd})
//	})
//	if c.Commit(err) { c.Redirect(c.URL("user", id)) } else { ... }
//
// Write runs fn in one transaction (retried on serialization failures:
// fn must be idempotent and only touch the database through tx); the
// transaction fails unless fn recorded at least one audit entry, so a
// change can never be written without its entry, and the entries commit
// or roll back with the change. Live events given to Publish go out only
// after the commit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// errNoAudit: a Write recorded no audit entry (a bug of the handler).
var errNoAudit = errors.New("adminweb: change without an audit log entry")

// Tx is the transaction of a Write.
type Tx struct {
	// Ctx is the context of the request.
	Ctx context.Context
	// Q runs the queries of internal/store/q in the transaction.
	Q *q.Queries
	// Tx is the pgx transaction, for hand-written statements.
	Tx pgx.Tx

	c       *Ctx
	audited int
	events  []live.Event
}

// AuditEntry describes one change (a row of audit_log).
type AuditEntry struct {
	// Action names what happened: "<target>.<verb>" ("contest.update",
	// "task.create", "submission.reevaluate", "question.reply").
	Action string
	// TargetType is the kind of object ("contest", "task", "dataset",
	// "user", "admin", "server_settings"...) and TargetID its id (0:
	// none).
	TargetType string
	TargetID   int64
	// ContestID is the contest the change belongs to (0: none), for the
	// audit page of the contest.
	ContestID int32
	// Before and After are the object before and after the change (rows
	// of q, structs or maps); the entry keeps the fields that differ.
	// Before nil: a creation (After is kept whole); After nil: a
	// deletion (Before is kept whole). Fields named like passwords are
	// replaced by "[redacted]".
	Before, After any
	// Details are added as they are (the arguments of an action:
	// {"level": "evaluation"}, {"count": 12}).
	Details map[string]any
}

// Write runs fn in a transaction that must record an audit entry (see
// the top of this file). It returns fn's error, errNoAudit, or the
// database's.
func (c *Ctx) Write(fn func(tx *Tx) error) error {
	var events []live.Event
	err := c.S.st.RunTx(c.Context(), pgx.TxOptions{}, func(ptx pgx.Tx) error {
		t := &Tx{Ctx: c.Context(), Q: c.S.st.Q.WithTx(ptx), Tx: ptx, c: c}
		if err := fn(t); err != nil {
			return err
		}
		if t.audited == 0 {
			return errNoAudit
		}
		events = t.events
		return nil
	})
	if err != nil {
		return err
	}
	if len(events) > 0 {
		c.S.pub.Publish(events...)
	}
	return nil
}

// Publish queues live events, sent after the commit.
func (t *Tx) Publish(evs ...live.Event) { t.events = append(t.events, evs...) }

// Audit records an entry in the transaction.
func (t *Tx) Audit(e AuditEntry) error {
	if e.Action == "" || e.TargetType == "" {
		return fmt.Errorf("adminweb: audit entry without action or target type")
	}
	details, err := auditDetails(e)
	if err != nil {
		return err
	}
	if t.c.Admin != nil {
		// admin_id becomes NULL when the admin is deleted.
		details["admin"] = t.c.Admin.Username
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	p := q.CreateAuditEntryParams{At: t.c.Now, Action: e.Action, TargetType: e.TargetType, Details: raw}
	if t.c.Admin != nil {
		p.AdminID = &t.c.Admin.ID
	}
	if e.TargetID != 0 {
		p.TargetID = &e.TargetID
	}
	if e.ContestID != 0 {
		p.ContestID = &e.ContestID
	}
	if a := t.c.ClientAddr(); a.IsValid() {
		p.Ip = &a
	}
	if _, err := t.Q.CreateAuditEntry(t.Ctx, p); err != nil {
		return err
	}
	t.audited++
	return nil
}

// redacted replaces the values of secret fields in the log.
const redacted = "[redacted]"

// secretField reports the fields whose values never go to the log.
func secretField(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "password") || strings.Contains(n, "authentication") ||
		strings.Contains(n, "secret") || strings.Contains(n, "token")
}

// auditDetails builds the details of an entry: {"changes": {field: [old,
// new]}} for an update, {"created": {...}} or {"deleted": {...}}, plus
// e.Details.
func auditDetails(e AuditEntry) (map[string]any, error) {
	out := map[string]any{}
	before, err := asObject(e.Before)
	if err != nil {
		return nil, err
	}
	after, err := asObject(e.After)
	if err != nil {
		return nil, err
	}
	switch {
	case before != nil && after != nil:
		changes := map[string]any{}
		for _, k := range unionKeys(before, after) {
			o, n := before[k], after[k]
			if reflect.DeepEqual(o, n) {
				continue
			}
			if secretField(k) {
				changes[k] = []any{redacted, redacted}
			} else {
				changes[k] = []any{o, n}
			}
		}
		out["changes"] = changes
	case after != nil:
		out["created"] = redact(after)
	case before != nil:
		out["deleted"] = redact(before)
	}
	for k, v := range e.Details {
		if secretField(k) {
			v = redacted
		}
		out[k] = v
	}
	return out, nil
}

// asObject turns a row into a map through its JSON form; nil stays nil.
// Rows of q marshal with their Go field names, which become the names of
// the columns ("PermissionAll" → "permission_all").
func asObject(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("adminweb: audit: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		// Not an object: keep it under "value".
		var x any
		if err := json.Unmarshal(b, &x); err != nil {
			return nil, err
		}
		return map[string]any{"value": x}, nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[snakeCase(k)] = v
	}
	return out, nil
}

// snakeCase turns a Go field name into a column name: "TimeLimit" →
// "time_limit", "ID" → "id", "ContestID" → "contest_id"; names that are
// already lower case stay.
func snakeCase(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		upper := r >= 'A' && r <= 'Z'
		if upper && i > 0 {
			prevLower := rs[i-1] >= 'a' && rs[i-1] <= 'z' || rs[i-1] >= '0' && rs[i-1] <= '9'
			nextLower := i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z'
			if prevLower || (nextLower && rs[i-1] >= 'A' && rs[i-1] <= 'Z') {
				b.WriteByte('_')
			}
		}
		if upper {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func unionKeys(a, b map[string]any) []string {
	var keys []string
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func redact(m map[string]any) map[string]any {
	for k := range m {
		if secretField(k) {
			m[k] = redacted
		}
	}
	return m
}

// auditIP is the address an entry records (tests).
func auditIP(a *netip.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}

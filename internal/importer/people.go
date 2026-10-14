// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The rules are those of cmscontrib/ImportUser.py, ImportTeam.py and the
// participations of ImportContest.py of CMS:
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// UserOptions are the options of a user import (cmsImportUser --all).
type UserOptions struct {
	// ContestID adds every user of the list without a participation in
	// this contest to its main group (-c).
	ContestID int32
	// Update updates the users that exist; without it they are left
	// alone (upstream refuses them one by one).
	Update bool
	// Report, if set, receives the issues.
	Report *Report
}

// WriteUsers stores users: the new ones are created (with a random
// plaintext password if they have none), the existing ones are left alone
// or updated with opts.Update, matched by username.
func (w *Writer) WriteUsers(ctx context.Context, users []User, opts UserOptions) (*Result, error) {
	res := newResult(opts.Report)
	newValidator(w.langs(), res.Report).users(users)
	if err := res.Report.Err(); err != nil {
		return res, err
	}
	var out *Result
	err := w.inTx(ctx, func(tx pgx.Tx, qq *q.Queries) error {
		x := newTxWriter(ctx, tx, qq, false)
		ids, err := x.writeUsers(users, opts.Update, true)
		if err != nil {
			return err
		}
		if opts.ContestID != 0 {
			c, err := qq.ImportLockContestByID(ctx, opts.ContestID)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("contest %d does not exist", opts.ContestID)
			}
			if err != nil {
				return err
			}
			x.res.ContestID = c.ID
			if err := x.addToContest(c, users, ids); err != nil {
				return err
			}
			x.res.Events = append(x.res.Events, judging.Event{Kind: judging.EventSweep, ContestID: c.ID})
		}
		out = x.res
		return nil
	})
	if err != nil {
		return res, err
	}
	w.done(res, out)
	return res, nil
}

// TeamOptions are the options of a team import (cmsImportTeam --all).
type TeamOptions struct {
	// Update renames the teams that exist; without it they are left
	// alone.
	Update bool
	// Report, if set, receives the issues.
	Report *Report
}

// WriteTeams stores teams, matched by code.
func (w *Writer) WriteTeams(ctx context.Context, teams []Team, opts TeamOptions) (*Result, error) {
	res := newResult(opts.Report)
	newValidator(w.langs(), res.Report).teams(teams)
	if err := res.Report.Err(); err != nil {
		return res, err
	}
	var out *Result
	err := w.inTx(ctx, func(tx pgx.Tx, qq *q.Queries) error {
		x := newTxWriter(ctx, tx, qq, false)
		if _, err := x.writeTeams(teams, opts.Update, true); err != nil {
			return err
		}
		out = x.res
		return nil
	})
	if err != nil {
		return res, err
	}
	w.done(res, out)
	return res, nil
}

// userRow is a user as the JSON of ImportCreateUsers and
// ImportUpdateUsers.
type userRow struct {
	ID                 int32    `json:"id,omitempty"`
	FirstName          string   `json:"first_name"`
	LastName           string   `json:"last_name"`
	Username           string   `json:"username"`
	Password           string   `json:"password"`
	Email              *string  `json:"email"`
	Timezone           *string  `json:"timezone"`
	PreferredLanguages []string `json:"preferred_languages"`
}

// writeUsers creates the users that do not exist and, with update,
// updates those that changed; it returns the ids of all of them by
// username. report says whether existing users are listed in the result
// (user imports) or only linked (contest imports).
func (x *txWriter) writeUsers(users []User, update, report bool) (map[string]int32, error) {
	ids := make(map[string]int32, len(users))
	if len(users) == 0 {
		return ids, nil
	}
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Username
	}
	existing, err := x.qq.ImportUsersByUsername(x.ctx, names)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]q.User, len(existing))
	for _, u := range existing {
		byName[u.Username] = u
		ids[u.Username] = u.ID
	}
	var create, change []userRow
	for _, u := range users {
		ex, ok := byName[u.Username]
		if !ok {
			pw := u.Password
			if pw == "" {
				pw = RandomPassword()
			}
			create = append(create, userRow{
				FirstName: u.FirstName, LastName: u.LastName, Username: u.Username, Password: pw,
				Email: u.Email, Timezone: u.Timezone, PreferredLanguages: nonNil(u.PreferredLanguages),
			})
			continue
		}
		if !update {
			if report {
				x.res.add(Skipped, "user", u.Username, ex.ID, "already exists (use --update to update it)")
			}
			continue
		}
		want := userRow{
			ID: ex.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username, Password: u.Password,
			Email: u.Email, Timezone: u.Timezone, PreferredLanguages: nonNil(u.PreferredLanguages),
		}
		if want.Password == "" {
			want.Password = ex.Password
		}
		cur := userRow{
			ID: ex.ID, FirstName: ex.FirstName, LastName: ex.LastName, Username: ex.Username, Password: ex.Password,
			Email: ex.Email, Timezone: ex.Timezone, PreferredLanguages: nonNil(ex.PreferredLanguages),
		}
		if reflect.DeepEqual(cur, want) {
			if report {
				x.res.add(Unchanged, "user", u.Username, ex.ID, "")
			}
			continue
		}
		change = append(change, want)
		x.res.add(Updated, "user", u.Username, ex.ID, "")
	}
	if len(create) > 0 {
		b, err := json.Marshal(create)
		if err != nil {
			return nil, err
		}
		rows, err := x.qq.ImportCreateUsers(x.ctx, b)
		if err != nil {
			return nil, fmt.Errorf("creating users: %w", err)
		}
		for _, r := range rows {
			ids[r.Username] = r.ID
			x.res.add(Created, "user", r.Username, r.ID, "")
		}
	}
	if len(change) > 0 {
		b, err := json.Marshal(change)
		if err != nil {
			return nil, err
		}
		if _, err := x.qq.ImportUpdateUsers(x.ctx, b); err != nil {
			return nil, fmt.Errorf("updating users: %w", err)
		}
	}
	return ids, nil
}

// writeTeams is writeUsers for teams.
func (x *txWriter) writeTeams(teams []Team, update, report bool) (map[string]int32, error) {
	ids := make(map[string]int32, len(teams))
	if len(teams) == 0 {
		return ids, nil
	}
	codes := make([]string, len(teams))
	for i, t := range teams {
		codes[i] = t.Code
	}
	existing, err := x.qq.ImportTeamsByCode(x.ctx, codes)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]q.Team, len(existing))
	for _, t := range existing {
		byCode[t.Code] = t
		ids[t.Code] = t.ID
	}
	var create, rename q.ImportCreateTeamsParams
	var renameIDs []int32
	for _, t := range teams {
		ex, ok := byCode[t.Code]
		switch {
		case !ok:
			create.Codes = append(create.Codes, t.Code)
			create.Names = append(create.Names, t.Name)
		case !update:
			if report {
				x.res.add(Skipped, "team", t.Code, ex.ID, "already exists (use --update to update it)")
			}
		case ex.Name == t.Name:
			if report {
				x.res.add(Unchanged, "team", t.Code, ex.ID, "")
			}
		default:
			renameIDs = append(renameIDs, ex.ID)
			rename.Names = append(rename.Names, t.Name)
			x.res.add(Updated, "team", t.Code, ex.ID, fmt.Sprintf("renamed %q", t.Name))
		}
	}
	if len(create.Codes) > 0 {
		rows, err := x.qq.ImportCreateTeams(x.ctx, create)
		if err != nil {
			return nil, fmt.Errorf("creating teams: %w", err)
		}
		for _, r := range rows {
			ids[r.Code] = r.ID
			x.res.add(Created, "team", r.Code, r.ID, "")
		}
	}
	if len(renameIDs) > 0 {
		if _, err := x.qq.ImportUpdateTeams(x.ctx, q.ImportUpdateTeamsParams{Ids: renameIDs, Names: rename.Names}); err != nil {
			return nil, fmt.Errorf("updating teams: %w", err)
		}
	}
	return ids, nil
}

// participationRow is a participation as the JSON of
// ImportCreateParticipations and ImportUpdateParticipations.
type participationRow struct {
	ID           int32    `json:"id,omitempty"`
	UserID       int32    `json:"user_id"`
	GroupID      int32    `json:"group_id"`
	TeamID       *int32   `json:"team_id"`
	Password     *string  `json:"password"`
	IP           []string `json:"ip"`
	Hidden       bool     `json:"hidden"`
	Unrestricted bool     `json:"unrestricted"`
	DelayUS      int64    `json:"delay_us"`
	ExtraUS      int64    `json:"extra_us"`
}

func rowOf(p q.Participation) participationRow {
	r := participationRow{
		ID: p.ID, UserID: p.UserID, GroupID: p.GroupID, TeamID: p.TeamID, Password: p.Password,
		Hidden: p.Hidden, Unrestricted: p.Unrestricted,
		DelayUS: p.DelayTime.Microseconds(), ExtraUS: p.ExtraTime.Microseconds(),
	}
	if p.Ip != nil {
		r.IP = make([]string, len(p.Ip))
		for i, n := range p.Ip {
			r.IP[i] = n.String()
		}
	}
	return r
}

// participationTargets are what participations refer to.
type participationTargets struct {
	contestID int32
	groups    map[string]int32 // by name
	mainGroup int32
	// users and teams are the ids known already (written by the same
	// import), by username and code.
	users, teams map[string]int32
}

// writeParticipations creates and updates the participations of a
// contest (upstream ImportContest._participation_to_db): the user (and
// team) must exist; the group is always set, the other fields only when
// given. With deleteStale, the participations of users not in the list are
// deleted (with their submissions).
func (x *txWriter) writeParticipations(tg participationTargets, parts []Participation, deleteStale bool) error {
	ctx, qq := x.ctx, x.qq
	usernames := make([]string, len(parts))
	var codes []string
	seenCodes := make(map[string]bool)
	for i, p := range parts {
		usernames[i] = p.Username
		if p.Team != nil && *p.Team != "" && !seenCodes[*p.Team] {
			seenCodes[*p.Team] = true
			codes = append(codes, *p.Team)
		}
	}
	// The users and teams written by this import are known; the others
	// are looked up (and locked) in one query each.
	userIDs := make(map[string]int32, len(parts))
	var lookup []string
	for _, n := range usernames {
		if id, ok := tg.users[n]; ok {
			userIDs[n] = id
		} else {
			lookup = append(lookup, n)
		}
	}
	if len(lookup) > 0 {
		users, err := qq.ImportUsersByUsername(ctx, lookup)
		if err != nil {
			return err
		}
		for _, u := range users {
			userIDs[u.Username] = u.ID
		}
	}
	var missing []string
	for _, n := range usernames {
		if _, ok := userIDs[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("users not found in the database: %s (import them with `cms ctl import-users`, or with --users)", listSome(missing))
	}
	teamIDs := make(map[string]int32, len(codes))
	lookup = nil
	for _, c := range codes {
		if id, ok := tg.teams[c]; ok {
			teamIDs[c] = id
		} else {
			lookup = append(lookup, c)
		}
	}
	if len(codes) > 0 {
		if len(lookup) > 0 {
			teams, err := qq.ImportTeamsByCode(ctx, lookup)
			if err != nil {
				return err
			}
			for _, t := range teams {
				teamIDs[t.Code] = t.ID
			}
		}
		missing = nil
		for _, c := range codes {
			if _, ok := teamIDs[c]; !ok {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("teams not found in the database: %s (import them with `cms ctl import-team`, or with --users)", listSome(missing))
		}
	}
	existing, err := qq.ListParticipationsByContest(ctx, tg.contestID)
	if err != nil {
		return err
	}
	byUser := make(map[int32]q.Participation, len(existing))
	for _, e := range existing {
		byUser[e.Participation.UserID] = e.Participation
	}
	var create, change []participationRow
	keep := make(map[int32]bool, len(parts))
	for _, p := range parts {
		uid := userIDs[p.Username]
		keep[uid] = true
		ex, exists := byUser[uid]
		cur := participationRow{UserID: uid}
		if exists {
			cur = rowOf(ex)
		}
		want := cur
		want.GroupID = tg.mainGroup
		if p.Group != "" {
			gid, ok := tg.groups[p.Group]
			if !ok {
				return fmt.Errorf("participation of %s: group %q does not exist in the contest", p.Username, p.Group)
			}
			want.GroupID = gid
		}
		if p.Team != nil {
			want.TeamID = nil
			if *p.Team != "" {
				id := teamIDs[*p.Team]
				want.TeamID = &id
			}
		}
		if p.Password != nil {
			want.Password = nil
			if *p.Password != "" {
				want.Password = p.Password
			}
		}
		if p.IP != nil {
			want.IP = make([]string, len(p.IP))
			for i, n := range p.IP {
				want.IP[i] = n.String()
			}
		}
		if p.Hidden != nil {
			want.Hidden = *p.Hidden
		}
		if p.Unrestricted != nil {
			want.Unrestricted = *p.Unrestricted
		}
		if p.DelayTime != nil {
			want.DelayUS = p.DelayTime.Microseconds()
		}
		if p.ExtraTime != nil {
			want.ExtraUS = p.ExtraTime.Microseconds()
		}
		switch {
		case !exists:
			create = append(create, want)
			x.res.add(Created, "participation", p.Username, 0, "")
		case reflect.DeepEqual(cur, want):
		default:
			change = append(change, want)
			x.res.add(Updated, "participation", p.Username, ex.ID, "")
		}
	}
	if len(create) > 0 {
		b, err := json.Marshal(create)
		if err != nil {
			return err
		}
		if _, err := qq.ImportCreateParticipations(ctx, q.ImportCreateParticipationsParams{ContestID: tg.contestID, Participations: b}); err != nil {
			return fmt.Errorf("creating participations: %w", err)
		}
	}
	if len(change) > 0 {
		b, err := json.Marshal(change)
		if err != nil {
			return err
		}
		if _, err := qq.ImportUpdateParticipations(ctx, b); err != nil {
			return fmt.Errorf("updating participations: %w", err)
		}
	}
	var stale []int32
	var staleNames []string
	for _, e := range existing {
		if !keep[e.Participation.UserID] {
			stale = append(stale, e.Participation.ID)
			staleNames = append(staleNames, e.User.Username)
		}
	}
	if len(stale) > 0 {
		if !deleteStale {
			x.res.Report.Warnf("contest", "%s of the contest not in the package kept: %s (--delete-stale-participations deletes them with their submissions)",
				plural(len(stale), "participation"), listSome(staleNames))
		} else {
			if _, err := qq.ImportDeleteParticipations(ctx, stale); err != nil {
				return err
			}
			for i, id := range stale {
				x.res.add(Deleted, "participation", staleNames[i], id, "with its submissions")
			}
		}
	}
	return nil
}

// addToContest gives the users of the list without a participation in the
// contest one in its main group (upstream ImportUser -c).
func (x *txWriter) addToContest(c q.Contest, users []User, ids map[string]int32) error {
	if c.MainGroupID == nil {
		return fmt.Errorf("contest %s has no main group", c.Name)
	}
	existing, err := x.qq.ListParticipationsByContest(x.ctx, c.ID)
	if err != nil {
		return err
	}
	has := make(map[int32]bool, len(existing))
	for _, e := range existing {
		has[e.Participation.UserID] = true
	}
	var create []participationRow
	for _, u := range users {
		id := ids[u.Username]
		if has[id] {
			continue
		}
		has[id] = true
		create = append(create, participationRow{UserID: id, GroupID: *c.MainGroupID})
		x.res.add(Created, "participation", u.Username, 0, "in contest "+c.Name)
	}
	if len(create) == 0 {
		return nil
	}
	b, err := json.Marshal(create)
	if err != nil {
		return err
	}
	_, err = x.qq.ImportCreateParticipations(x.ctx, q.ImportCreateParticipationsParams{ContestID: c.ID, Participations: b})
	return err
}

// listSome lists at most 10 names.
func listSome(names []string) string {
	if len(names) <= 10 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:10], ", ") + fmt.Sprintf(" and %d more", len(names)-10)
}

// microseconds of an optional duration, for the JSON of groups.
func microseconds(d *time.Duration) *int64 {
	if d == nil {
		return nil
	}
	v := d.Microseconds()
	return &v
}

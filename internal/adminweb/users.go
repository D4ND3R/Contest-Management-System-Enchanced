// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/user.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2017 Valentin Rosca <rosca.valentin2012@gmail.com>
// Copyright © 2021 Manuel Gundlach <manuel.gundlach@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Chuyang Wang <mail@chuyang-wang.de>

package adminweb

// Users (work A): the list, creation, removal, the page of a user with
// its participations. Teams are in users_teams.go, the users of a
// contest and the participation page in users_participations.go.

import (
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// peoplePageSize is the rows per page of the lists of users, teams and
// the users of a contest (D24; CMS shows them all).
const peoplePageSize = 100

// usersData is the data of users.html.
type usersData struct {
	*Page
	Users []q.AdminUsersPageRow
	Pager *Pager
	// Contests are offered by the import form (admins with PermAll).
	Contests []q.AdminContestsListRow
}

// usersPage is GET /users (CMS UserListHandler).
func (s *Server) usersPage(c *Ctx) {
	n, err := s.st.Q.AdminUsersCount(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	pg := c.Pager(n, peoplePageSize)
	rows, err := s.st.Q.AdminUsersPage(c.Context(), q.AdminUsersPageParams{Lim: int32(pg.Size), Off: int32(pg.Offset())})
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	d := usersData{Page: p, Users: rows, Pager: pg}
	if c.Admin.CanAll() {
		if d.Contests, err = s.st.Q.AdminContestsList(c.Context()); err != nil {
			c.Fail(err)
			return
		}
	}
	c.Render("users.html", d)
}

// usersPost is POST /users: the Remove button opens the confirmation.
func (s *Server) usersPost(c *Ctx) {
	id, _ := c.Arg("user_id")
	op, _ := c.Arg("operation")
	if c.isOp(op, "Remove") {
		if _, err := strconv.ParseInt(id, 10, 32); err != nil {
			c.BadRequest()
			return
		}
		c.Redirect(c.URL("users", id, "remove"))
		return
	}
	c.NotifyError(N("Invalid operation"), op)
	c.Redirect(c.URL("contests"))
}

// removeUserData is the data of user_remove.html.
type removeUserData struct {
	*Page
	User           q.User
	Participations int64
	Submissions    int64
}

// removeUserPage is GET /users/<id>/remove (CMS RemoveUserHandler).
func (s *Server) removeUserPage(c *Ctx) {
	id, _ := c.PathID("user_id")
	u, err := s.st.Q.GetUser(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	n, err := s.st.Q.AdminUserRemoveCounts(c.Context(), id)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("user_remove.html", removeUserData{Page: p, User: u, Participations: n.Participations, Submissions: n.Submissions})
}

// removeUserDelete is DELETE /users/<id>/remove: the user, its
// participations and everything they contain.
func (s *Server) removeUserDelete(c *Ctx) {
	id, _ := c.PathID("user_id")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetUser(tx.Ctx, id)
		if err != nil {
			return err
		}
		parts, err := tx.Q.AdminUserParticipations(tx.Ctx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Q.DeleteUser(tx.Ctx, id); err != nil {
			return err
		}
		for _, p := range parts {
			tx.Publish(live.ScoreboardChange{Contest: int64(p.ContestID), All: true}.Event())
		}
		return tx.Audit(AuditEntry{Action: "user.delete", TargetType: "user", TargetID: int64(id), Before: old,
			Details: map[string]any{"participations": len(parts)}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.WriteText("../../users")
}

// addUserPage is GET /users/add.
func (s *Server) addUserPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("add_user.html", p)
}

// userAttrs reads the form of a user over u (CMS UserHandler.post and
// AddUserHandler.post).
func (c *Ctx) userAttrs(u *q.User) error {
	f := c.Form()
	f.String(&u.FirstName, "first_name")
	f.String(&u.LastName, "last_name")
	if _, ok := f.Get("username"); ok || u.Username == "" {
		u.Username = f.Required("username", "username")
	}
	pw := &u.Password
	f.Password(&pw, u.Password, false)
	if pw != nil {
		u.Password = *pw
	}
	f.OptString(&u.Email, "email")
	f.StringList(&u.PreferredLanguages, "preferred_languages")
	f.OptString(&u.Timezone, "timezone")
	return f.Err()
}

// addUserPost is POST /users/add (CMS AddUserHandler).
func (s *Server) addUserPost(c *Ctx) {
	fallback := c.URL("users", "add")
	u := q.User{PreferredLanguages: []string{}}
	if err := c.userAttrs(&u); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	var created q.User
	err := c.Write(func(tx *Tx) error {
		var err error
		created, err = tx.Q.CreateUser(tx.Ctx, q.CreateUserParams{FirstName: u.FirstName, LastName: u.LastName,
			Username: u.Username, Password: u.Password, Email: u.Email, Timezone: u.Timezone,
			PreferredLanguages: u.PreferredLanguages})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "user.create", TargetType: "user", TargetID: int64(created.ID), After: created})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("user", created.ID))
	} else {
		c.Redirect(fallback)
	}
}

// userData is the data of user.html.
type userData struct {
	*Page
	User           q.User
	Participations []q.AdminUserParticipationsRow
	Contests       []q.AdminContestsWithoutUserRow
}

// userPage is GET /user/<id> (CMS UserHandler.get).
func (s *Server) userPage(c *Ctx) {
	id, _ := c.PathID("user_id")
	u, err := s.st.Q.GetUser(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	parts, err := s.st.Q.AdminUserParticipations(c.Context(), id)
	if err != nil {
		c.Fail(err)
		return
	}
	free, err := s.st.Q.AdminContestsWithoutUser(c.Context(), id)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("user.html", userData{Page: p, User: u, Participations: parts, Contests: free})
}

// userPost is POST /user/<id> (CMS UserHandler.post).
func (s *Server) userPost(c *Ctx) {
	id, _ := c.PathID("user_id")
	page := c.URL("user", id)
	var parts []q.AdminUserParticipationsRow
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetUser(tx.Ctx, id)
		if err != nil {
			return err
		}
		u := old
		if err := c.userAttrs(&u); err != nil {
			return &FieldError{Msg: err.Error()}
		}
		upd, err := tx.Q.UpdateUser(tx.Ctx, q.UpdateUserParams{ID: id, FirstName: u.FirstName, LastName: u.LastName,
			Username: u.Username, Password: u.Password, Email: u.Email, Timezone: u.Timezone,
			PreferredLanguages: u.PreferredLanguages})
		if err != nil {
			return err
		}
		if parts, err = tx.Q.AdminUserParticipations(tx.Ctx, id); err != nil {
			return err
		}
		for _, p := range parts {
			tx.Publish(live.ParticipationChange{Contest: int64(p.ContestID), Participation: int64(p.ID)}.Event(),
				live.ScoreboardChange{Contest: int64(p.ContestID), Participations: []int64{int64(p.ID)}}.Event())
		}
		return tx.Audit(AuditEntry{Action: "user.update", TargetType: "user", TargetID: int64(id), Before: old, After: upd})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		c.NotifyError(N("Invalid field(s)"), fe.Msg)
		c.Redirect(page)
		return
	}
	c.Commit(err)
	c.Redirect(page)
}

// addParticipationPost is POST /user/<id>/add_participation (CMS
// AddParticipationHandler): a participation in the main group of the
// selected contest.
func (s *Server) addParticipationPost(c *Ctx) {
	uid, _ := c.PathID("user_id")
	fallback := c.URL("user", uid)
	cid, err := strconv.ParseInt(c.ArgDefault("contest_id", ""), 10, 32)
	if err != nil {
		c.NotifyError(N("Invalid field(s)"), "Please select a valid contest")
		c.Redirect(fallback)
		return
	}
	var hidden, unrestricted bool
	f := c.Form()
	f.Bool(&hidden, "hidden")
	f.Bool(&unrestricted, "unrestricted")
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.GetUser(tx.Ctx, uid); err != nil {
			return err
		}
		g, err := tx.Q.GetMainGroup(tx.Ctx, int32(cid))
		if errors.Is(err, pgx.ErrNoRows) {
			if _, err := tx.Q.GetContest(tx.Ctx, int32(cid)); err != nil {
				return err
			}
			return Invalid("The contest has no main group.")
		}
		if err != nil {
			return err
		}
		return createParticipation(tx, q.CreateParticipationParams{ContestID: int32(cid), UserID: uid, GroupID: g.ID,
			Hidden: hidden, Unrestricted: unrestricted})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(fallback)
}

// createParticipation creates a participation with its audit entry and
// events.
func createParticipation(tx *Tx, p q.CreateParticipationParams) error {
	created, err := tx.Q.CreateParticipation(tx.Ctx, p)
	if err != nil {
		return err
	}
	tx.Publish(live.ScoreboardChange{Contest: int64(p.ContestID), All: true}.Event())
	return tx.Audit(AuditEntry{Action: "participation.create", TargetType: "participation",
		TargetID: int64(created.ID), ContestID: p.ContestID, After: created})
}

// deleteParticipation removes the participation of user uid in contest
// cid with its audit entry and events.
func deleteParticipation(tx *Tx, cid, uid int32) error {
	old, err := tx.Q.GetParticipationByContestUser(tx.Ctx, q.GetParticipationByContestUserParams{ContestID: cid, UserID: uid})
	if err != nil {
		return err
	}
	if _, err := tx.Q.DeleteParticipation(tx.Ctx, old.ID); err != nil {
		return err
	}
	tx.Publish(live.ParticipationChange{Contest: int64(cid), Participation: int64(old.ID)}.Event(),
		live.ScoreboardChange{Contest: int64(cid), All: true}.Event())
	return tx.Audit(AuditEntry{Action: "participation.delete", TargetType: "participation",
		TargetID: int64(old.ID), ContestID: cid, Before: old})
}

// editParticipationPost is POST /user/<id>/edit_participation (CMS
// EditParticipationHandler): Remove the selected participation.
func (s *Server) editParticipationPost(c *Ctx) {
	uid, _ := c.PathID("user_id")
	fallback := c.URL("user", uid)
	cid, err := strconv.ParseInt(c.ArgDefault("contest_id", ""), 10, 32)
	op, _ := c.Arg("operation")
	if err != nil || !c.isOp(op, "Remove") {
		msg := "Please select a valid operation"
		if err != nil {
			msg = "Please select a valid contest"
		}
		c.NotifyError(N("Invalid field(s)"), msg)
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error { return deleteParticipation(tx, int32(cid), uid) })
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(fallback)
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/admin.py of CMS:
// Copyright © 2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>

package adminweb

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// adminsData is the data of admins.html.
type adminsData struct {
	*Page
	Admins []q.Admin
}

// adminsPage is GET /admins (CMS AdminsHandler).
func (s *Server) adminsPage(c *Ctx) {
	rows, err := s.st.Q.AdminCoreAdmins(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("admins.html", adminsData{Page: p, Admins: rows})
}

// adminData is the data of admin.html and add_admin.html.
type adminData struct {
	*Page
	// Edited is the admin being edited (CMS admin_being_edited), nil
	// on the creation form.
	Edited *q.Admin
}

// addAdminPage is GET /admins/add.
func (s *Server) addAdminPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("add_admin.html", adminData{Page: p})
}

// adminPage is GET /admin/<id> (CMS AdminHandler.get).
func (s *Server) adminPage(c *Ctx) {
	id, _ := c.PathID("admin_id")
	a, err := s.st.Q.GetAdmin(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("admin.html", adminData{Page: p, Edited: &a})
}

// adminAttrs reads the form of an admin (CMS _admin_attrs) over a: the
// username and name are required; a non-empty password replaces the
// authentication (bcrypt); the permission and enabled checkboxes are
// read only when full is set (the form shows them only to admins with
// permission_all).
func (c *Ctx) adminAttrs(a *q.Admin, full bool) error {
	f := c.Form()
	username := f.Required("username", "username")
	name := f.Required("name", "admin name")
	if err := f.Err(); err != nil {
		return err
	}
	a.Username, a.Name = username, name
	if pw, _ := f.Get("password"); pw != "" {
		auth, err := web.HashPassword(pw, "bcrypt")
		if err != nil {
			return err
		}
		a.Authentication = auth
	}
	if full {
		f.Bool(&a.PermissionAll, "permission_all")
		f.Bool(&a.PermissionMessaging, "permission_messaging")
		f.Bool(&a.PermissionTasks, "permission_tasks")
		f.Bool(&a.Enabled, "enabled")
	}
	return nil
}

// errLastAdmin refuses to leave the installation without an enabled
// admin with every permission.
var errLastAdmin = Invalid("At least one enabled admin must have all permissions.")

// checkSuperAdmins fails when no enabled admin with permission_all is
// left (in the transaction of the change).
func checkSuperAdmins(tx *Tx) error {
	n, err := tx.Q.CountSuperAdmins(tx.Ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return errLastAdmin
	}
	return nil
}

// addAdminPost is POST /admins/add (CMS AddAdminHandler.post).
func (s *Server) addAdminPost(c *Ctx) {
	fallback := c.URL("admins", "add")
	a := q.Admin{Enabled: false}
	if err := c.adminAttrs(&a, true); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	if a.Authentication == "" {
		c.NotifyError(N("Invalid field(s)"), "Empty password not permitted.")
		c.Redirect(fallback)
		return
	}
	err := c.Write(func(tx *Tx) error {
		created, err := tx.Q.CreateAdmin(tx.Ctx, q.CreateAdminParams{
			Name: a.Name, Username: a.Username, Authentication: a.Authentication, Enabled: a.Enabled,
			PermissionAll: a.PermissionAll, PermissionMessaging: a.PermissionMessaging,
			PermissionTasks: a.PermissionTasks,
		})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "admin.create", TargetType: "admin",
			TargetID: int64(created.ID), After: created})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("admins"))
	} else {
		c.Redirect(fallback)
	}
}

// adminPost is POST /admin/<id> (CMS AdminHandler.post): admins with
// permission_all change anything; the others only their own name,
// username and password.
func (s *Server) adminPost(c *Ctx) {
	id, _ := c.PathID("admin_id")
	full := c.Admin.CanAll()
	var updated q.Admin
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetAdmin(tx.Ctx, id)
		if err != nil {
			return err
		}
		a := old
		if err := c.adminAttrs(&a, full); err != nil {
			return err
		}
		updated, err = tx.Q.UpdateAdmin(tx.Ctx, q.UpdateAdminParams{
			ID: id, Name: a.Name, Username: a.Username, Authentication: a.Authentication,
			Enabled: a.Enabled, PermissionAll: a.PermissionAll,
			PermissionMessaging: a.PermissionMessaging, PermissionTasks: a.PermissionTasks,
		})
		if err != nil {
			return err
		}
		if err := checkSuperAdmins(tx); err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "admin.update", TargetType: "admin", TargetID: int64(id),
			Before: old, After: updated})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if !c.Commit(err) {
		c.Redirect(c.URL("admin", id))
		return
	}
	s.log.Info("admin updated", "admin", id, "by", c.Admin.Username)
	if id == c.Admin.ID {
		if updated.Enabled {
			// The fingerprint in the cookie follows the new password.
			s.setLoginCookie(c.W, c.R, &Admin{Admin: updated}, c.Now)
		}
	}
	c.Redirect(c.URL("admins"))
}

// adminDelete is DELETE /admin/<id> (CMS AdminHandler.delete): it
// answers the page to open next.
func (s *Server) adminDelete(c *Ctx) {
	id, _ := c.PathID("admin_id")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetAdmin(tx.Ctx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Q.DeleteAdmin(tx.Ctx, id); err != nil {
			return err
		}
		if err := checkSuperAdmins(tx); err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "admin.delete", TargetType: "admin", TargetID: int64(id), Before: old})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.WriteText("../admins")
}

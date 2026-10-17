// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from require_permission in cms/server/admin/handlers/base.py and
// cms/server/admin/rpc_authorization.py of CMS:
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

import (
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Perm is what a route requires (R6.5). Every route of routes.go declares
// one: the zero value is not a permission and the server refuses to start
// with a route that lacks it (checkRoutes; TestRoutesDeclarePermissions).
//
//	PermPublic     no login (the login page)
//	PermRead       any enabled admin (CMS "authenticated"): every page
//	               can be seen read-only
//	PermMessaging  permission_all or permission_messaging: questions,
//	               announcements, private messages
//	PermTasks      permission_all or permission_tasks (the problem
//	               preparer): tasks, statements, attachments, datasets,
//	               managers and testcases of any contest, and their test
//	               submissions; not users, contests or their settings
//	PermAll        permission_all: everything else that changes data
type Perm int

const (
	permUndeclared Perm = iota
	// PermPublic routes need no login.
	PermPublic
	// PermRead routes need an enabled admin.
	PermRead
	// PermMessaging routes need permission_all or permission_messaging.
	PermMessaging
	// PermTasks routes need permission_all or permission_tasks.
	PermTasks
	// PermAll routes need permission_all.
	PermAll
)

// String names the permission (logs, tests).
func (p Perm) String() string {
	switch p {
	case PermPublic:
		return "public"
	case PermRead:
		return "read"
	case PermMessaging:
		return "messaging"
	case PermTasks:
		return "tasks"
	case PermAll:
		return "all"
	}
	return "undeclared"
}

// Admin is the logged-in administrator (CMS current_user).
type Admin struct {
	q.Admin
}

// Can reports whether the admin has the permission p: the single
// authorization rule of the site (routes, RPC methods and the buttons
// pages enable all use it).
func (a *Admin) Can(p Perm) bool {
	if a == nil || !a.Enabled {
		return p == PermPublic
	}
	switch p {
	case PermPublic, PermRead:
		return true
	case PermMessaging:
		return a.PermissionAll || a.PermissionMessaging
	case PermTasks:
		return a.PermissionAll || a.PermissionTasks
	case PermAll:
		return a.PermissionAll
	}
	return false
}

// CanMessaging, CanTasks and CanAll are Can for the templates
// ({{if .Admin.CanAll}}): CMS templates test admin.permission_all.
func (a *Admin) CanMessaging() bool { return a.Can(PermMessaging) }

// CanTasks reports PermTasks.
func (a *Admin) CanTasks() bool { return a.Can(PermTasks) }

// CanAll reports PermAll.
func (a *Admin) CanAll() bool { return a.Can(PermAll) }

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The route table is ported from cms/server/admin/handlers/__init__.py of
// CMS:
// Copyright © 2015-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Peyman Jabbarzade Ganje <peyman.jabarzade@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package adminweb

// Every route of CMS's AdminWebServer is registered here, with the
// permission of its CMS handler (adapted to the roles of R6.5, see
// perm.go), plus the new pages of SPEC F6. The files that implement them:
//
//	core (shared; changes to these files should stay small):
//	  auth.go           login, logout (CMS LoginHandler, LogoutHandler)
//	  overview.go       / and contest/<id>/overview (OverviewHandler),
//	                    resourceslist, resources (the services and hosts
//	                    status that replaces ResourceService)
//	  rpc.go            rpc/<service>/<shard>/<method> (CMS's RPC proxy
//	                    that web_rpc.js calls; the methods are a table)
//	  notifications.go  notifications (polled by aws_utils.js)
//	  files.go          file/<digest>/<name>, render_markdown
//	  admins.go         admins, admins/add, admin/<id>
//	  serverpage.go     server (new: R6.8)
//	  auditpage.go      audit, contest/<id>/audit (new: R6.6)
//	  events.go         events, contest/<id>/events (new: R6.9)
//	(A) contests, groups, users, teams, participations, ranking and
//	    communication:
//	  contests.go       contests, contests/add, contests/<id>/remove,
//	                    contest/<id>, contest/<id>/groups...
//	  users.go          users, teams, user/<id>, team/<id>,
//	                    contest/<id>/users, contest/<id>/user/<id>/...
//	  ranking.go        contest/<id>/ranking[/<format>]
//	  communication.go  contest/<id>/announcements, questions,
//	                    contest/<id>/user/<id>/message
//	(B) tasks, statements, attachments, datasets, managers, testcases and
//	    problem preparation (R6.2):
//	  tasks.go          tasks, tasks/add, tasks/<id>/remove, task/<id>/...,
//	                    contest/<id>/tasks
//	  datasets.go       dataset/<id>/{clone,rename,delete,activate,
//	                    autojudge,managers,testcases...}
//	  prepare.go        the new problem preparation pages (R6.2)
//	(C) submissions, user tests, reevaluation, judges (R6.7) and live
//	    lists (R6.9):
//	  submissions.go    contest/<id>/submissions, contest/<id>/user_tests,
//	                    dataset/<id> (its submissions), submission/...,
//	                    submission_file, submission_diff, user_test/...,
//	                    user_test_file; the RPC invalidate_submission
//	  judges.go         judges (new) and the RPC enable_worker and
//	                    disable_worker
//
// Until a page is written its handler answers 501 (Ctx.NotImplemented).
// Each work replaces its stubs, adds its routes to this table (only its
// own lines) and its templates under templates/; the shared helpers are
// in the core files:
//
//	ctx.go         Ctx: the request (Admin, Contest, Now, Lang, RC),
//	               PathID, Arg, ArgDefault, URL, Render, RenderStatus,
//	               Error, NotFound, Forbidden, BadRequest, Fail, Redirect,
//	               WriteText (CMS's DELETE answers), JSON, ClientAddr,
//	               LoadContest / ContestFromPath / SetContest (the latter
//	               two set the contest menu), Can, Notify / NotifyError
//	               (the notifications of aws_utils.js), Commit (CMS
//	               try_commit), Invalid (a FieldError), N (a msgid)
//	page.go        Page: the context of base.html, embedded in every
//	               page's data (Ctx.Page)
//	audit.go       Ctx.Write: the transaction of every change, with
//	               Tx.Audit (R6.6) and Tx.Publish (live events after the
//	               commit)
//	forms.go       Ctx.Form: CMS's argument readers (get_string,
//	               get_int, get_bool, get_datetime, get_password...)
//	uploads.go     Ctx.Files / Ctx.File, Ctx.PutUpload (streamed to the
//	               blob store; large parts live in temporary files),
//	               OpenUpload, ReadUpload
//	rpc.go         Server.Jobs (the job queue), RPCMethod (the RPC table)
//	serverpage.go  Server.PublicURL (links to the contestant site and the
//	               scoreboard), TimezoneSetting...
//	files.go       Server.ServeFile (downloads from the blob store)
//	pagination.go  Pager (D24) and the macro pages.selector of CMS
//	funcs.go       template functions (int, form_datetime, form_seconds,
//	               dataset_attrs, public_url, parse_authentication,
//	               current_admin); FormDatetime, FormSeconds
//	static/aws_handlers.js  the inline event handlers of CMS as data-
//	               attributes (data-ajax-delete, data-ajax-post,
//	               data-show-file, data-history-back, data-submit-form,
//	               data-rpc, data-markdown-preview, ...) (D10)
//
// Path values: names ending in "_id" match CMS's [0-9]+ (other values get
// Tornado's 404 before anything runs, so Ctx.PathID always succeeds);
// {digest}, {filename}, {format}, {shard}, {service} and {method} have
// the patterns of CMS (pathRules).

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// Route is one route of the site.
type Route struct {
	// Method is GET, POST or DELETE (HEAD is served by GET routes).
	Method string
	// Path is the pattern below the root ("/contest/{contest_id}/users").
	Path string
	// Perm is the permission the route requires (perm.go); mandatory.
	Perm Perm
	// Handler serves the request.
	Handler func(*Ctx)
	// SelfAllowed lets an admin without Perm use the route on their own
	// {admin_id} (CMS require_permission(self_allowed=True)).
	SelfAllowed bool
	// Upload raises the body limit to MaxBody (files).
	Upload bool
	// NoRefresh leaves the login cookie as it is: requests that pages
	// make by themselves (polling, events) must not extend the session.
	NoRefresh bool
}

// routes is the route table.
func (s *Server) routes() []Route {
	return []Route{
		// Main (core).
		{Method: "GET", Path: "/{$}", Perm: PermRead, Handler: s.overviewPage},
		{Method: "GET", Path: "/login", Perm: PermPublic, Handler: s.loginPage},
		{Method: "POST", Path: "/login", Perm: PermPublic, Handler: s.loginPost, NoRefresh: true},
		{Method: "POST", Path: "/logout", Perm: PermPublic, Handler: s.logoutPost, NoRefresh: true},
		{Method: "GET", Path: "/resourceslist", Perm: PermRead, Handler: s.resourcesListPage},
		{Method: "GET", Path: "/resources", Perm: PermRead, Handler: s.resourcesPage},
		{Method: "GET", Path: "/resources/{shard}", Perm: PermRead, Handler: s.resourcesPage},
		{Method: "GET", Path: "/resources/{shard}/{contest_id}", Perm: PermRead, Handler: s.resourcesPage},
		{Method: "GET", Path: "/notifications", Perm: PermRead, Handler: s.notificationsPage},
		{Method: "GET", Path: "/file/{digest}/{filename}", Perm: PermRead, Handler: s.filePage},
		{Method: "POST", Path: "/render_markdown", Perm: PermRead, Handler: s.renderMarkdownPost},
		// Each RPC method has its own permission (rpc.go).
		{Method: "POST", Path: "/rpc/{service}/{shard}/{method}", Perm: PermRead, Handler: s.rpcPost, NoRefresh: true},
		{Method: "GET", Path: "/events", Perm: PermRead, Handler: s.eventsPage, NoRefresh: true},
		{Method: "GET", Path: "/contest/{contest_id}/events", Perm: PermRead, Handler: s.eventsPage, NoRefresh: true},
		{Method: "GET", Path: "/server", Perm: PermRead, Handler: s.serverPage},
		{Method: "POST", Path: "/server", Perm: PermAll, Handler: s.serverPost},
		{Method: "GET", Path: "/audit", Perm: PermRead, Handler: s.auditPage},
		{Method: "GET", Path: "/contest/{contest_id}/audit", Perm: PermRead, Handler: s.auditPage},

		// Contest (A: contests.go; the overview is core).
		{Method: "GET", Path: "/contests", Perm: PermRead, Handler: s.contestsPage},
		{Method: "POST", Path: "/contests", Perm: PermRead, Handler: s.contestsPost},
		{Method: "GET", Path: "/contests/{contest_id}/remove", Perm: PermAll, Handler: s.removeContestPage},
		{Method: "DELETE", Path: "/contests/{contest_id}/remove", Perm: PermAll, Handler: s.removeContestDelete},
		{Method: "GET", Path: "/contests/add", Perm: PermAll, Handler: s.addContestPage},
		{Method: "POST", Path: "/contests/add", Perm: PermAll, Handler: s.addContestPost},
		{Method: "GET", Path: "/contest/{contest_id}", Perm: PermRead, Handler: s.contestPage},
		{Method: "POST", Path: "/contest/{contest_id}", Perm: PermAll, Handler: s.contestPost},
		{Method: "GET", Path: "/contest/{contest_id}/overview", Perm: PermRead, Handler: s.overviewPage},
		{Method: "GET", Path: "/contest/{contest_id}/resourceslist", Perm: PermRead, Handler: s.resourcesListPage},

		// Contest's users (A: users.go).
		{Method: "GET", Path: "/contest/{contest_id}/users", Perm: PermRead, Handler: s.contestUsersPage},
		{Method: "POST", Path: "/contest/{contest_id}/users", Perm: PermAll, Handler: s.contestUsersPost},
		{Method: "POST", Path: "/contest/{contest_id}/users/add", Perm: PermAll, Handler: s.addContestUserPost},
		{Method: "GET", Path: "/contest/{contest_id}/user/{user_id}/remove", Perm: PermAll, Handler: s.removeParticipationPage},
		{Method: "DELETE", Path: "/contest/{contest_id}/user/{user_id}/remove", Perm: PermAll, Handler: s.removeParticipationDelete},
		{Method: "GET", Path: "/contest/{contest_id}/user/{user_id}/edit", Perm: PermRead, Handler: s.participationPage},
		{Method: "POST", Path: "/contest/{contest_id}/user/{user_id}/edit", Perm: PermAll, Handler: s.participationPost},
		// Communication (A: communication.go).
		{Method: "POST", Path: "/contest/{contest_id}/user/{user_id}/message", Perm: PermMessaging, Handler: s.messagePost},

		// Contest's groups (A: contests.go).
		{Method: "GET", Path: "/contest/{contest_id}/groups", Perm: PermRead, Handler: s.groupsPage},
		{Method: "POST", Path: "/contest/{contest_id}/groups", Perm: PermAll, Handler: s.groupsPost},
		{Method: "POST", Path: "/contest/{contest_id}/groups/add", Perm: PermAll, Handler: s.addGroupPost},
		{Method: "GET", Path: "/contest/{contest_id}/group/{group_id}/edit", Perm: PermAll, Handler: s.groupPage},
		{Method: "POST", Path: "/contest/{contest_id}/group/{group_id}/edit", Perm: PermAll, Handler: s.groupPost},

		// Contest's tasks (B: tasks.go). Adding or removing a task of a
		// contest changes the contest: permission_all.
		{Method: "GET", Path: "/contest/{contest_id}/tasks", Perm: PermRead, Handler: s.contestTasksPage},
		{Method: "POST", Path: "/contest/{contest_id}/tasks", Perm: PermAll, Handler: s.contestTasksPost},
		{Method: "POST", Path: "/contest/{contest_id}/tasks/add", Perm: PermAll, Handler: s.addContestTaskPost},

		// Contest's submissions and user tests (C: submissions.go).
		{Method: "GET", Path: "/contest/{contest_id}/submissions", Perm: PermRead, Handler: s.contestSubmissionsPage},
		{Method: "GET", Path: "/contest/{contest_id}/user_tests", Perm: PermRead, Handler: s.contestUserTestsPage},

		// Contest's announcements (A: communication.go).
		{Method: "GET", Path: "/contest/{contest_id}/announcements", Perm: PermRead, Handler: s.announcementsPage},
		{Method: "POST", Path: "/contest/{contest_id}/announcements/add", Perm: PermMessaging, Handler: s.addAnnouncementPost},
		{Method: "DELETE", Path: "/contest/{contest_id}/announcement/{announcement_id}", Perm: PermMessaging, Handler: s.announcementDelete},

		// Contest's questions (A: communication.go).
		{Method: "GET", Path: "/contest/{contest_id}/questions", Perm: PermRead, Handler: s.questionsPage},
		{Method: "POST", Path: "/contest/{contest_id}/question/{question_id}/reply", Perm: PermMessaging, Handler: s.questionReplyPost},
		{Method: "POST", Path: "/contest/{contest_id}/question/{question_id}/ignore", Perm: PermMessaging, Handler: s.questionIgnorePost},
		{Method: "POST", Path: "/contest/{contest_id}/question/{question_id}/claim", Perm: PermMessaging, Handler: s.questionClaimPost},

		// Contest's ranking (A: ranking.go).
		{Method: "GET", Path: "/contest/{contest_id}/ranking", Perm: PermRead, Handler: s.rankingPage},
		{Method: "GET", Path: "/contest/{contest_id}/ranking/{format}", Perm: PermRead, Handler: s.rankingPage},

		// Tasks (B: tasks.go). The problem preparer may change any task;
		// removing one deletes its submissions: permission_all.
		{Method: "GET", Path: "/tasks", Perm: PermRead, Handler: s.tasksPage},
		{Method: "POST", Path: "/tasks", Perm: PermRead, Handler: s.tasksPost},
		{Method: "GET", Path: "/tasks/{task_id}/remove", Perm: PermAll, Handler: s.removeTaskPage},
		{Method: "DELETE", Path: "/tasks/{task_id}/remove", Perm: PermAll, Handler: s.removeTaskDelete},
		{Method: "GET", Path: "/tasks/add", Perm: PermTasks, Handler: s.addTaskPage},
		{Method: "POST", Path: "/tasks/add", Perm: PermTasks, Handler: s.addTaskPost},
		{Method: "GET", Path: "/task/{task_id}", Perm: PermRead, Handler: s.taskPage},
		{Method: "POST", Path: "/task/{task_id}", Perm: PermTasks, Handler: s.taskPost},
		{Method: "GET", Path: "/task/{task_id}/add_dataset", Perm: PermTasks, Handler: s.addDatasetPage},
		{Method: "POST", Path: "/task/{task_id}/add_dataset", Perm: PermTasks, Handler: s.addDatasetPost},
		{Method: "GET", Path: "/task/{task_id}/statements/add", Perm: PermTasks, Handler: s.addStatementPage},
		{Method: "POST", Path: "/task/{task_id}/statements/add", Perm: PermTasks, Handler: s.addStatementPost, Upload: true},
		{Method: "DELETE", Path: "/task/{task_id}/statement/{statement_id}", Perm: PermTasks, Handler: s.statementDelete},
		{Method: "GET", Path: "/task/{task_id}/attachments/add", Perm: PermTasks, Handler: s.addAttachmentPage},
		{Method: "POST", Path: "/task/{task_id}/attachments/add", Perm: PermTasks, Handler: s.addAttachmentPost, Upload: true},
		{Method: "DELETE", Path: "/task/{task_id}/attachment/{attachment_id}", Perm: PermTasks, Handler: s.attachmentDelete},

		// Datasets (B: datasets.go; its submissions page: C).
		{Method: "GET", Path: "/dataset/{dataset_id}", Perm: PermRead, Handler: s.datasetSubmissionsPage},
		{Method: "GET", Path: "/dataset/{dataset_id}/clone", Perm: PermTasks, Handler: s.cloneDatasetPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/clone", Perm: PermTasks, Handler: s.cloneDatasetPost},
		{Method: "GET", Path: "/dataset/{dataset_id}/rename", Perm: PermTasks, Handler: s.renameDatasetPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/rename", Perm: PermTasks, Handler: s.renameDatasetPost},
		{Method: "GET", Path: "/dataset/{dataset_id}/delete", Perm: PermTasks, Handler: s.deleteDatasetPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/delete", Perm: PermTasks, Handler: s.deleteDatasetPost},
		{Method: "GET", Path: "/dataset/{dataset_id}/activate", Perm: PermTasks, Handler: s.activateDatasetPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/activate", Perm: PermTasks, Handler: s.activateDatasetPost},
		{Method: "POST", Path: "/dataset/{dataset_id}/autojudge", Perm: PermTasks, Handler: s.autojudgeDatasetPost},
		{Method: "GET", Path: "/dataset/{dataset_id}/managers/add", Perm: PermTasks, Handler: s.addManagerPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/managers/add", Perm: PermTasks, Handler: s.addManagerPost, Upload: true},
		{Method: "DELETE", Path: "/dataset/{dataset_id}/manager/{manager_id}/delete", Perm: PermTasks, Handler: s.managerDelete},
		{Method: "GET", Path: "/dataset/{dataset_id}/testcases/add", Perm: PermTasks, Handler: s.addTestcasePage},
		{Method: "POST", Path: "/dataset/{dataset_id}/testcases/add", Perm: PermTasks, Handler: s.addTestcasePost, Upload: true},
		{Method: "GET", Path: "/dataset/{dataset_id}/testcases/add_multiple", Perm: PermTasks, Handler: s.addTestcasesPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/testcases/add_multiple", Perm: PermTasks, Handler: s.addTestcasesPost, Upload: true},
		{Method: "DELETE", Path: "/dataset/{dataset_id}/testcase/{testcase_id}/delete", Perm: PermTasks, Handler: s.testcaseDelete},
		{Method: "GET", Path: "/dataset/{dataset_id}/testcases/download", Perm: PermRead, Handler: s.downloadTestcasesPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/testcases/download", Perm: PermRead, Handler: s.downloadTestcasesPost},

		// Problem preparation (B: prepare*.go, R6.2).
		{Method: "GET", Path: "/tasks/import", Perm: PermTasks, Handler: s.importTaskPage},
		{Method: "POST", Path: "/tasks/import", Perm: PermTasks, Handler: s.importTaskPost, Upload: true},
		{Method: "POST", Path: "/tasks/import/confirm", Perm: PermTasks, Handler: s.importConfirmPost},
		{Method: "GET", Path: "/task/{task_id}/import", Perm: PermTasks, Handler: s.importTaskPage},
		{Method: "POST", Path: "/task/{task_id}/import", Perm: PermTasks, Handler: s.importTaskPost, Upload: true},
		{Method: "POST", Path: "/task/{task_id}/import/confirm", Perm: PermTasks, Handler: s.importConfirmPost},
		{Method: "GET", Path: "/task/{task_id}/export", Perm: PermRead, Handler: s.exportTaskPage},
		{Method: "GET", Path: "/task/{task_id}/problem.yaml", Perm: PermRead, Handler: s.problemYAMLPage},
		{Method: "GET", Path: "/task/{task_id}/problem.yaml/raw", Perm: PermRead, Handler: s.problemYAMLRaw},
		{Method: "POST", Path: "/task/{task_id}/problem.yaml", Perm: PermTasks, Handler: s.problemYAMLPost, Upload: true},
		{Method: "POST", Path: "/task/{task_id}/problem.yaml/confirm", Perm: PermTasks, Handler: s.problemYAMLPost},
		{Method: "GET", Path: "/task/{task_id}/prepare", Perm: PermRead, Handler: s.preparePage},
		{Method: "GET", Path: "/task/{task_id}/files", Perm: PermTasks, Handler: s.taskFilesPage},
		{Method: "POST", Path: "/task/{task_id}/files", Perm: PermTasks, Handler: s.taskFilesPost, Upload: true},
		{Method: "GET", Path: "/task/{task_id}/test_submissions", Perm: PermRead, Handler: s.testSubmissionsPage},
		{Method: "POST", Path: "/task/{task_id}/test_submissions", Perm: PermTasks, Handler: s.testSubmissionsPost, Upload: true},
		{Method: "GET", Path: "/dataset/{dataset_id}/testcase/{testcase_id}", Perm: PermRead, Handler: s.testcasePage},
		{Method: "POST", Path: "/dataset/{dataset_id}/testcase/{testcase_id}", Perm: PermTasks, Handler: s.testcasePost, Upload: true},
		{Method: "GET", Path: "/dataset/{dataset_id}/testcases/new", Perm: PermTasks, Handler: s.testcasePage},
		{Method: "POST", Path: "/dataset/{dataset_id}/testcases/new", Perm: PermTasks, Handler: s.testcasePost, Upload: true},
		{Method: "GET", Path: "/dataset/{dataset_id}/generator", Perm: PermTasks, Handler: s.generatorPage},
		{Method: "POST", Path: "/dataset/{dataset_id}/generator", Perm: PermTasks, Handler: s.generatorPost, Upload: true},

		// Users and teams (A: users.go).
		{Method: "GET", Path: "/users", Perm: PermRead, Handler: s.usersPage},
		{Method: "POST", Path: "/users", Perm: PermRead, Handler: s.usersPost},
		{Method: "GET", Path: "/users/{user_id}/remove", Perm: PermAll, Handler: s.removeUserPage},
		{Method: "DELETE", Path: "/users/{user_id}/remove", Perm: PermAll, Handler: s.removeUserDelete},
		{Method: "GET", Path: "/teams", Perm: PermRead, Handler: s.teamsPage},
		{Method: "POST", Path: "/teams", Perm: PermRead, Handler: s.teamsPost},
		{Method: "GET", Path: "/teams/{team_id}/remove", Perm: PermAll, Handler: s.removeTeamPage},
		{Method: "DELETE", Path: "/teams/{team_id}/remove", Perm: PermAll, Handler: s.removeTeamDelete},
		{Method: "GET", Path: "/users/add", Perm: PermAll, Handler: s.addUserPage},
		{Method: "POST", Path: "/users/add", Perm: PermAll, Handler: s.addUserPost},
		{Method: "GET", Path: "/teams/add", Perm: PermAll, Handler: s.addTeamPage},
		{Method: "POST", Path: "/teams/add", Perm: PermAll, Handler: s.addTeamPost},
		{Method: "POST", Path: "/teams/import", Perm: PermAll, Handler: s.importTeamsPost, Upload: true},
		{Method: "GET", Path: "/user/{user_id}", Perm: PermRead, Handler: s.userPage},
		{Method: "POST", Path: "/user/{user_id}", Perm: PermAll, Handler: s.userPost},
		{Method: "GET", Path: "/team/{team_id}", Perm: PermRead, Handler: s.teamPage},
		{Method: "POST", Path: "/team/{team_id}", Perm: PermAll, Handler: s.teamPost},
		{Method: "POST", Path: "/user/{user_id}/add_participation", Perm: PermAll, Handler: s.addParticipationPost},
		{Method: "POST", Path: "/user/{user_id}/edit_participation", Perm: PermAll, Handler: s.editParticipationPost},

		// Admins (core: admins.go).
		{Method: "GET", Path: "/admins", Perm: PermRead, Handler: s.adminsPage},
		{Method: "GET", Path: "/admins/add", Perm: PermAll, Handler: s.addAdminPage},
		{Method: "POST", Path: "/admins/add", Perm: PermAll, Handler: s.addAdminPost},
		{Method: "GET", Path: "/admin/{admin_id}", Perm: PermRead, Handler: s.adminPage},
		{Method: "POST", Path: "/admin/{admin_id}", Perm: PermAll, Handler: s.adminPost, SelfAllowed: true},
		{Method: "DELETE", Path: "/admin/{admin_id}", Perm: PermAll, Handler: s.adminDelete},

		// Submissions (C: submissions.go). CMS's optional dataset
		// ((?:/([0-9]+))?) is two routes.
		{Method: "GET", Path: "/submission/{submission_id}", Perm: PermRead, Handler: s.submissionPage},
		{Method: "GET", Path: "/submission/{submission_id}/{dataset_id}", Perm: PermRead, Handler: s.submissionPage},
		{Method: "POST", Path: "/submission/{submission_id}/comment", Perm: PermAll, Handler: s.submissionCommentPost},
		{Method: "POST", Path: "/submission/{submission_id}/{dataset_id}/comment", Perm: PermAll, Handler: s.submissionCommentPost},
		{Method: "POST", Path: "/submission/{submission_id}/official", Perm: PermAll, Handler: s.submissionOfficialPost},
		{Method: "POST", Path: "/submission/{submission_id}/{dataset_id}/official", Perm: PermAll, Handler: s.submissionOfficialPost},
		{Method: "GET", Path: "/submission_file/{file_id}", Perm: PermRead, Handler: s.submissionFilePage},
		{Method: "GET", Path: "/submission_diff/{old_id}/{new_id}", Perm: PermRead, Handler: s.submissionDiffPage},

		// User tests (C: submissions.go).
		{Method: "GET", Path: "/user_test/{user_test_id}", Perm: PermRead, Handler: s.userTestPage},
		{Method: "GET", Path: "/user_test/{user_test_id}/{dataset_id}", Perm: PermRead, Handler: s.userTestPage},
		{Method: "GET", Path: "/user_test_file/{file_id}", Perm: PermRead, Handler: s.userTestFilePage},

		// Judges (C: judges.go, R6.7).
		{Method: "GET", Path: "/judges", Perm: PermRead, Handler: s.judgesPage},
		{Method: "POST", Path: "/judges/job", Perm: PermAll, Handler: s.judgesJobPost},
		{Method: "POST", Path: "/judges/dead", Perm: PermAll, Handler: s.judgesDeadPost},
	}
}

// pathRules are the patterns of CMS's URLs for the path values that are
// not ids (names ending in "_id" match [0-9]+).
var pathRules = map[string]*regexp.Regexp{
	"digest":   regexp.MustCompile(`^[a-f0-9]+$`),
	"filename": regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`),
	"format":   regexp.MustCompile(`^[a-z]+$`),
	"shard":    regexp.MustCompile(`^(?:[0-9]+|all)$`),
	"service":  regexp.MustCompile(`^[A-Za-z]+$`),
	"method":   regexp.MustCompile(`^[a-z_]+$`),
}

var (
	idRule      = regexp.MustCompile(`^[0-9]+$`)
	wildcardsRe = regexp.MustCompile(`\{([a-z_]+)(\.\.\.)?\}`)
)

// pathParams returns the names of the path values of a pattern.
func pathParams(path string) []string {
	var out []string
	for _, m := range wildcardsRe.FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}
	return out
}

// checkRoutes refuses a route table with a route that does not declare
// its permission, an unknown method, a path value without a rule or a
// duplicate.
func checkRoutes(routes []Route) error {
	seen := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if seen[key] {
			return fmt.Errorf("adminweb: route %s registered twice", key)
		}
		seen[key] = true
		if rt.Perm == permUndeclared || rt.Perm > PermAll {
			return fmt.Errorf("adminweb: route %s declares no permission", key)
		}
		if rt.Handler == nil {
			return fmt.Errorf("adminweb: route %s has no handler", key)
		}
		switch rt.Method {
		case "GET", "POST", "DELETE":
		default:
			return fmt.Errorf("adminweb: route %s: method not supported", key)
		}
		if rt.Method != "GET" && rt.Perm == PermPublic && rt.Path != "/login" && rt.Path != "/logout" {
			return fmt.Errorf("adminweb: route %s changes data without a login", key)
		}
		for _, p := range pathParams(rt.Path) {
			if !strings.HasSuffix(p, "_id") && pathRules[p] == nil {
				return fmt.Errorf("adminweb: route %s: no rule for {%s}", key, p)
			}
		}
		if rt.SelfAllowed && !strings.Contains(rt.Path, "{admin_id}") {
			return fmt.Errorf("adminweb: route %s: SelfAllowed needs {admin_id}", key)
		}
	}
	return nil
}

// validPath reports whether the path values of a request match their
// rules (CMS's regular expressions: otherwise no handler matches).
func validPath(r *http.Request, params []string) bool {
	for _, p := range params {
		v := r.PathValue(p)
		rule := pathRules[p]
		if rule == nil {
			rule = idRule
		}
		if !rule.MatchString(v) {
			return false
		}
		if rule == idRule {
			// Ids are int32 in the database: larger numbers exist nowhere.
			if _, err := strconv.ParseInt(v, 10, 32); err != nil {
				return false
			}
		}
	}
	return true
}

// router builds the handler of the site.
func (s *Server) router() http.Handler {
	mux := httpx.NewRouter()
	httpx.MountCommon(mux, s.health, s.cfg.SourceURL)
	mux.Handle("GET /static/", http.StripPrefix("/static", s.static))

	allowed := map[string][]string{}
	var paths []string
	for _, rt := range s.routeTab {
		if _, ok := allowed[rt.Path]; !ok {
			paths = append(paths, rt.Path)
		}
		allowed[rt.Path] = append(allowed[rt.Path], rt.Method)
		mux.HandleFunc(rt.Method+" "+rt.Path, s.route(rt))
	}
	// Other methods: CMS answers 405 with its error page. The method-less
	// patterns live in their own router (a method-less pattern next to
	// a method one of a more general path conflicts in http.ServeMux).
	paths405 := http.NewServeMux()
	for _, p := range paths {
		methods := allowed[p]
		params := pathParams(p)
		paths405.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if !validPath(r, params) {
				notFound(w, r)
				return
			}
			c, ok := s.newCtx(w, r, Route{Perm: PermPublic})
			if !ok {
				return
			}
			w.Header().Set("Allow", strings.Join(methods, ", "))
			c.Error(http.StatusMethodNotAllowed)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := paths405.Handler(r); pattern != "" {
			paths405.ServeHTTP(w, r)
			return
		}
		notFound(w, r)
	})
	return mux
}

// notFound is Tornado's answer to a URL no handler matches.
func notFound(w http.ResponseWriter, r *http.Request) {
	plainPage(w, r, http.StatusNotFound)
}

// plainPage answers with Tornado's default error page ("404: Not Found"),
// for errors that happen before CMS has a page to render.
func plainPage(w http.ResponseWriter, _ *http.Request, status int) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=UTF-8")
	h.Del("Content-Length")
	w.WriteHeader(status)
	msg := strconv.Itoa(status) + ": " + http.StatusText(status)
	_, _ = w.Write([]byte("<html><title>" + msg + "</title><body>" + msg + "</body></html>"))
}

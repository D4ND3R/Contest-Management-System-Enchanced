// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package db_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// cmsColumns lists every column of the CMS data model, derived from the
// SQLAlchemy models of cms/db/*.py at schema version 49 (same names,
// nullability as declared there), plus the fork additions. Types follow
// DECISIONS.md: Unicode/String → text, DateTime → timestamptz, Float →
// double precision, Enum and the CMS domains → text with a CHECK.
var cmsColumns = map[string][]string{
	"admins": {
		"id integer NOT NULL",
		"name text NOT NULL",
		"username text NOT NULL",
		"authentication text NOT NULL",
		"enabled boolean NOT NULL",
		"permission_all boolean NOT NULL",
		"permission_messaging boolean NOT NULL",
		"permission_tasks boolean NOT NULL", // fork: problem preparer role
	},
	"contests": {
		"id integer NOT NULL",
		"name text NOT NULL",
		"description text NOT NULL",
		"allowed_localizations text[] NOT NULL",
		"languages text[] NOT NULL",
		"submissions_download_allowed boolean NOT NULL",
		"allow_questions boolean NOT NULL",
		"allow_user_tests boolean NOT NULL",
		"allow_unofficial_submission_before_analysis_mode boolean NOT NULL",
		"block_hidden_participations boolean NOT NULL",
		"allow_password_authentication boolean NOT NULL",
		"allow_registration boolean NOT NULL",
		"ip_restriction boolean NOT NULL",
		"ip_autologin boolean NOT NULL",
		"token_mode text NOT NULL",
		"token_max_number integer",
		"token_min_interval interval NOT NULL",
		"token_gen_initial integer NOT NULL",
		"token_gen_number integer NOT NULL",
		"token_gen_interval interval NOT NULL",
		"token_gen_max integer",
		"timezone text",
		"max_submission_number integer",
		"max_user_test_number integer",
		"min_submission_interval interval",
		"min_submission_interval_grace_period interval",
		"min_user_test_interval interval",
		"score_precision integer NOT NULL",
		"main_group_id integer",
	},
	"groups": {
		"id integer NOT NULL",
		"name text NOT NULL",
		"start timestamptz NOT NULL",
		"stop timestamptz NOT NULL",
		"analysis_enabled boolean NOT NULL",
		"analysis_start timestamptz NOT NULL",
		"analysis_stop timestamptz NOT NULL",
		"per_user_time interval",
		"contest_id integer NOT NULL",
	},
	"announcements": {
		"id integer NOT NULL",
		"timestamp timestamptz NOT NULL",
		"subject text NOT NULL",
		"text text NOT NULL",
		"contest_id integer NOT NULL",
		"admin_id integer",
	},
	"users": {
		"id integer NOT NULL",
		"first_name text NOT NULL",
		"last_name text NOT NULL",
		"username text NOT NULL",
		"password text NOT NULL",
		"email text",
		"timezone text",
		"preferred_languages text[] NOT NULL",
	},
	"teams": {
		"id integer NOT NULL",
		"code text NOT NULL",
		"name text NOT NULL",
	},
	"participations": {
		"id integer NOT NULL",
		"ip cidr[]",
		"starting_time timestamptz",
		"delay_time interval NOT NULL",
		"extra_time interval NOT NULL",
		"password text",
		"hidden boolean NOT NULL",
		"unrestricted boolean NOT NULL",
		"contest_id integer NOT NULL",
		"user_id integer NOT NULL",
		"group_id integer NOT NULL",
		"team_id integer",
	},
	"messages": {
		"id integer NOT NULL",
		"timestamp timestamptz NOT NULL",
		"subject text NOT NULL",
		"text text NOT NULL",
		"participation_id integer NOT NULL",
		"admin_id integer",
	},
	"questions": {
		"id integer NOT NULL",
		"question_timestamp timestamptz NOT NULL",
		"subject text NOT NULL",
		"text text NOT NULL",
		"reply_timestamp timestamptz",
		"ignored boolean NOT NULL",
		"reply_subject text",
		"reply_text text",
		"participation_id integer NOT NULL",
		"admin_id integer",
	},
	"tasks": {
		"id integer NOT NULL",
		"num integer",
		"contest_id integer",
		"name text NOT NULL",
		"title text NOT NULL",
		"submission_format text[] NOT NULL",
		"primary_statements text[] NOT NULL",
		"allowed_languages text[]",
		"token_mode text NOT NULL",
		"token_max_number integer",
		"token_min_interval interval NOT NULL",
		"token_gen_initial integer NOT NULL",
		"token_gen_number integer NOT NULL",
		"token_gen_interval interval NOT NULL",
		"token_gen_max integer",
		"max_submission_number integer",
		"max_user_test_number integer",
		"min_submission_interval interval",
		"min_user_test_interval interval",
		"feedback_level text NOT NULL",
		"score_precision integer NOT NULL",
		"score_mode text NOT NULL",
		"active_dataset_id integer",
	},
	"statements": {
		"id integer NOT NULL",
		"task_id integer NOT NULL",
		"language text NOT NULL",
		"digest text NOT NULL",
	},
	"attachments": {
		"id integer NOT NULL",
		"task_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	"datasets": {
		"id integer NOT NULL",
		"task_id integer NOT NULL",
		"description text NOT NULL",
		"autojudge boolean NOT NULL",
		"time_limit double precision",
		"memory_limit bigint",
		"task_type text NOT NULL",
		"task_type_parameters jsonb NOT NULL",
		"score_type text NOT NULL",
		"score_type_parameters jsonb NOT NULL",
	},
	"managers": {
		"id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	"testcases": {
		"id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"codename text NOT NULL",
		"public boolean NOT NULL",
		"input text NOT NULL",
		"output text NOT NULL",
	},
	"submissions": {
		"id integer NOT NULL",
		"opaque_id bigint NOT NULL",
		"participation_id integer NOT NULL",
		"task_id integer NOT NULL",
		"timestamp timestamptz NOT NULL",
		"language text",
		"comment text NOT NULL",
		"official boolean NOT NULL",
	},
	"files": {
		"id integer NOT NULL",
		"submission_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	"tokens": {
		"id integer NOT NULL",
		"submission_id integer NOT NULL",
		"timestamp timestamptz NOT NULL",
	},
	"submission_results": {
		"submission_id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"compilation_outcome text",
		"compilation_text text[] NOT NULL",
		"compilation_tries integer NOT NULL",
		"compilation_stdout text",
		"compilation_stderr text",
		"compilation_time double precision",
		"compilation_wall_clock_time double precision",
		"compilation_memory bigint",
		"compilation_shard integer",
		"compilation_sandbox_paths text[]",
		"compilation_sandbox_digests text[]",
		"evaluation_outcome text",
		"evaluation_tries integer NOT NULL",
		"score double precision",
		"score_details jsonb",
		"scored_at timestamptz",
		"public_score double precision",
		"public_score_details jsonb",
		"ranking_score_details text[]",
		"generation bigint NOT NULL",  // fork: stale results (D23, migration 0004)
		"subtask_scores jsonb",        // fork: generated, for the task score cache (migration 0004)
		"public_subtask_scores jsonb", // fork: generated, for the task score cache (migration 0004)
	},
	"executables": {
		"id integer NOT NULL",
		"submission_id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	"evaluations": {
		"id integer NOT NULL",
		"submission_id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"testcase_id integer NOT NULL",
		"outcome text",
		"text text[] NOT NULL",
		"admin_text text",
		"execution_time double precision",
		"execution_wall_clock_time double precision",
		"execution_memory bigint",
		"evaluation_shard integer",
		"evaluation_sandbox_paths text[]",
		"evaluation_sandbox_digests text[]",
	},
	"user_tests": {
		"id integer NOT NULL",
		"participation_id integer NOT NULL",
		"task_id integer NOT NULL",
		"timestamp timestamptz NOT NULL",
		"language text",
		"input text NOT NULL",
	},
	"user_test_files": {
		"id integer NOT NULL",
		"user_test_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	"user_test_managers": {
		"id integer NOT NULL",
		"user_test_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	"user_test_results": {
		"user_test_id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"output text",
		"compilation_outcome text",
		"compilation_text text[] NOT NULL",
		"compilation_tries integer NOT NULL",
		"compilation_stdout text",
		"compilation_stderr text",
		"compilation_time double precision",
		"compilation_wall_clock_time double precision",
		"compilation_memory bigint",
		"compilation_shard integer",
		"compilation_sandbox_paths text[]",
		"compilation_sandbox_digests text[]",
		"evaluation_outcome text",
		"evaluation_text text[] NOT NULL",
		"evaluation_tries integer NOT NULL",
		"execution_time double precision",
		"execution_wall_clock_time double precision",
		"execution_memory bigint",
		"evaluation_shard integer",
		"evaluation_sandbox_paths text[]",
		"evaluation_sandbox_digests text[]",
		"generation bigint NOT NULL", // fork: stale results (D23, migration 0004)
	},
	"user_test_executables": {
		"id integer NOT NULL",
		"user_test_id integer NOT NULL",
		"dataset_id integer NOT NULL",
		"filename text NOT NULL",
		"digest text NOT NULL",
	},
	// Fork addition: the audit log of the administration.
	"audit_log": {
		"id bigint NOT NULL",
		"at timestamptz NOT NULL",
		"admin_id integer",
		"action text NOT NULL",
		"target_type text NOT NULL",
		"target_id bigint",
		"contest_id integer",
		"details jsonb NOT NULL",
		"ip inet",
	},
}

// cmsKeys lists the primary keys, UNIQUE constraints and foreign keys
// (pg_get_constraintdef). Names and definitions are those of a fresh CMS
// installation at schema version 49 (compare the dump in
// cmstestsuite/unit_tests/schema_v1.5.sql and the changes in
// cmscontrib/updaters/update_from_1.5.sql), except:
//   - fk_active_dataset_id: ON UPDATE CASCADE and no ON DELETE action
//     instead of SET NULL on both columns, which always failed on tasks.id
//     NOT NULL (DECISIONS.md); deleting the active dataset is still refused;
//   - CMS's submission_results/user_test_results UNIQUE constraints equal
//     to their primary keys are not repeated;
//   - the fork's audit_log.
var cmsKeys = map[string][]string{
	"admins": {
		"admins_pkey PRIMARY KEY (id)",
		"admins_username_key UNIQUE (username)",
	},
	"contests": {
		"contests_pkey PRIMARY KEY (id)",
		"contests_name_key UNIQUE (name)",
		"fk_contest_main_group_id FOREIGN KEY (main_group_id) REFERENCES groups(id) ON UPDATE CASCADE ON DELETE SET NULL",
	},
	"groups": {
		"groups_pkey PRIMARY KEY (id)",
		"groups_contest_id_name_key UNIQUE (contest_id, name)",
		"groups_id_contest_id_key UNIQUE (id, contest_id)",
		"groups_contest_id_fkey FOREIGN KEY (contest_id) REFERENCES contests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"announcements": {
		"announcements_pkey PRIMARY KEY (id)",
		"announcements_admin_id_fkey FOREIGN KEY (admin_id) REFERENCES admins(id) ON UPDATE CASCADE ON DELETE SET NULL",
		"announcements_contest_id_fkey FOREIGN KEY (contest_id) REFERENCES contests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"users": {
		"users_pkey PRIMARY KEY (id)",
		"users_username_key UNIQUE (username)",
	},
	"teams": {
		"teams_pkey PRIMARY KEY (id)",
		"teams_code_key UNIQUE (code)",
	},
	"participations": {
		"participations_pkey PRIMARY KEY (id)",
		"participations_contest_id_user_id_key UNIQUE (contest_id, user_id)",
		"participations_contest_id_fkey FOREIGN KEY (contest_id) REFERENCES contests(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"participations_group_id_contest_id_fkey FOREIGN KEY (group_id, contest_id) REFERENCES groups(id, contest_id)",
		"participations_group_id_fkey FOREIGN KEY (group_id) REFERENCES groups(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"participations_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams(id) ON UPDATE CASCADE ON DELETE SET NULL",
		"participations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"messages": {
		"messages_pkey PRIMARY KEY (id)",
		"messages_admin_id_fkey FOREIGN KEY (admin_id) REFERENCES admins(id) ON UPDATE CASCADE ON DELETE SET NULL",
		"messages_participation_id_fkey FOREIGN KEY (participation_id) REFERENCES participations(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"questions": {
		"questions_pkey PRIMARY KEY (id)",
		"questions_admin_id_fkey FOREIGN KEY (admin_id) REFERENCES admins(id) ON UPDATE CASCADE ON DELETE SET NULL",
		"questions_participation_id_fkey FOREIGN KEY (participation_id) REFERENCES participations(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"tasks": {
		"tasks_pkey PRIMARY KEY (id)",
		"tasks_contest_id_name_key UNIQUE (contest_id, name)",
		"tasks_contest_id_num_key UNIQUE (contest_id, num)",
		"tasks_name_key UNIQUE (name)",
		"fk_active_dataset_id FOREIGN KEY (id, active_dataset_id) REFERENCES datasets(task_id, id) ON UPDATE CASCADE",
		"tasks_contest_id_fkey FOREIGN KEY (contest_id) REFERENCES contests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"statements": {
		"statements_pkey PRIMARY KEY (id)",
		"statements_task_id_language_key UNIQUE (task_id, language)",
		"statements_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"attachments": {
		"attachments_pkey PRIMARY KEY (id)",
		"attachments_task_id_filename_key UNIQUE (task_id, filename)",
		"attachments_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"datasets": {
		"datasets_pkey PRIMARY KEY (id)",
		"datasets_id_task_id_key UNIQUE (id, task_id)",
		"datasets_task_id_description_key UNIQUE (task_id, description)",
		"datasets_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"managers": {
		"managers_pkey PRIMARY KEY (id)",
		"managers_dataset_id_filename_key UNIQUE (dataset_id, filename)",
		"managers_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"testcases": {
		"testcases_pkey PRIMARY KEY (id)",
		"testcases_dataset_id_codename_key UNIQUE (dataset_id, codename)",
		"testcases_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"submissions": {
		"submissions_pkey PRIMARY KEY (id)",
		"participation_opaque_unique UNIQUE (participation_id, opaque_id)",
		"submissions_participation_id_fkey FOREIGN KEY (participation_id) REFERENCES participations(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"submissions_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"files": {
		"files_pkey PRIMARY KEY (id)",
		"files_submission_id_filename_key UNIQUE (submission_id, filename)",
		"files_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES submissions(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"tokens": {
		"tokens_pkey PRIMARY KEY (id)",
		"tokens_submission_id_key UNIQUE (submission_id)",
		"tokens_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES submissions(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"submission_results": {
		"submission_results_pkey PRIMARY KEY (submission_id, dataset_id)",
		"submission_results_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"submission_results_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES submissions(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"executables": {
		"executables_pkey PRIMARY KEY (id)",
		"executables_submission_id_dataset_id_filename_key UNIQUE (submission_id, dataset_id, filename)",
		"executables_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"executables_submission_id_dataset_id_fkey FOREIGN KEY (submission_id, dataset_id) REFERENCES submission_results(submission_id, dataset_id) ON UPDATE CASCADE ON DELETE CASCADE",
		"executables_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES submissions(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"evaluations": {
		"evaluations_pkey PRIMARY KEY (id)",
		"evaluations_submission_id_dataset_id_testcase_id_key UNIQUE (submission_id, dataset_id, testcase_id)",
		"evaluations_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"evaluations_submission_id_dataset_id_fkey FOREIGN KEY (submission_id, dataset_id) REFERENCES submission_results(submission_id, dataset_id) ON UPDATE CASCADE ON DELETE CASCADE",
		"evaluations_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES submissions(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"evaluations_testcase_id_fkey FOREIGN KEY (testcase_id) REFERENCES testcases(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"user_tests": {
		"user_tests_pkey PRIMARY KEY (id)",
		"user_tests_participation_id_fkey FOREIGN KEY (participation_id) REFERENCES participations(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"user_tests_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"user_test_files": {
		"user_test_files_pkey PRIMARY KEY (id)",
		"user_test_files_user_test_id_filename_key UNIQUE (user_test_id, filename)",
		"user_test_files_user_test_id_fkey FOREIGN KEY (user_test_id) REFERENCES user_tests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"user_test_managers": {
		"user_test_managers_pkey PRIMARY KEY (id)",
		"user_test_managers_user_test_id_filename_key UNIQUE (user_test_id, filename)",
		"user_test_managers_user_test_id_fkey FOREIGN KEY (user_test_id) REFERENCES user_tests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"user_test_results": {
		"user_test_results_pkey PRIMARY KEY (user_test_id, dataset_id)",
		"user_test_results_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"user_test_results_user_test_id_fkey FOREIGN KEY (user_test_id) REFERENCES user_tests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"user_test_executables": {
		"user_test_executables_pkey PRIMARY KEY (id)",
		"user_test_executables_user_test_id_dataset_id_filename_key UNIQUE (user_test_id, dataset_id, filename)",
		"user_test_executables_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"user_test_executables_user_test_id_dataset_id_fkey FOREIGN KEY (user_test_id, dataset_id) REFERENCES user_test_results(user_test_id, dataset_id) ON UPDATE CASCADE ON DELETE CASCADE",
		"user_test_executables_user_test_id_fkey FOREIGN KEY (user_test_id) REFERENCES user_tests(id) ON UPDATE CASCADE ON DELETE CASCADE",
	},
	"audit_log": {
		"audit_log_pkey PRIMARY KEY (id)",
		"audit_log_admin_id_fkey FOREIGN KEY (admin_id) REFERENCES admins(id) ON UPDATE CASCADE ON DELETE SET NULL",
		"audit_log_contest_id_fkey FOREIGN KEY (contest_id) REFERENCES contests(id) ON UPDATE CASCADE ON DELETE SET NULL",
	},
}

// cmsChecks lists the CHECK constraints. Those of CMS keep its names
// (<table>_<column>_check, <table>_check[N], datasets_memory_limit_check1);
// the CMS domains and enum types become <table>_<column>_check.
// TestSchemaRejectsInvalidRows triggers each one.
var cmsChecks = map[string][]string{
	"admins":      {"admins_username_check"},
	"attachments": {"attachments_digest_check", "attachments_filename_check"},
	"audit_log":   {"audit_log_action_check", "audit_log_details_check", "audit_log_target_type_check"},
	"contests": {
		"contests_check", "contests_max_submission_number_check", "contests_max_user_test_number_check",
		"contests_min_submission_interval_check", "contests_min_submission_interval_grace_period_check",
		"contests_min_user_test_interval_check", "contests_name_check", "contests_score_precision_check",
		"contests_token_gen_initial_check", "contests_token_gen_interval_check", "contests_token_gen_max_check",
		"contests_token_gen_number_check", "contests_token_max_number_check", "contests_token_min_interval_check",
		"contests_token_mode_check",
	},
	"datasets":           {"datasets_memory_limit_check", "datasets_memory_limit_check1", "datasets_time_limit_check"},
	"executables":        {"executables_digest_check", "executables_filename_check"},
	"files":              {"files_digest_check", "files_filename_check"},
	"groups":             {"groups_check", "groups_check1", "groups_check2", "groups_per_user_time_check"},
	"managers":           {"managers_digest_check", "managers_filename_check"},
	"participations":     {"participations_delay_time_check", "participations_extra_time_check"},
	"statements":         {"statements_digest_check"},
	"submission_results": {"submission_results_compilation_outcome_check", "submission_results_evaluation_outcome_check"},
	"tasks": {
		"tasks_check", "tasks_feedback_level_check", "tasks_max_submission_number_check",
		"tasks_max_user_test_number_check", "tasks_min_submission_interval_check", "tasks_min_user_test_interval_check",
		"tasks_name_check", "tasks_score_mode_check", "tasks_score_precision_check", "tasks_submission_format_check",
		"tasks_token_gen_initial_check", "tasks_token_gen_interval_check", "tasks_token_gen_max_check",
		"tasks_token_gen_number_check", "tasks_token_max_number_check", "tasks_token_min_interval_check",
		"tasks_token_mode_check",
	},
	"teams":                 {"teams_code_check"},
	"testcases":             {"testcases_codename_check", "testcases_input_check", "testcases_output_check"},
	"user_test_executables": {"user_test_executables_digest_check", "user_test_executables_filename_check"},
	"user_test_files":       {"user_test_files_digest_check", "user_test_files_filename_check"},
	"user_test_managers":    {"user_test_managers_digest_check", "user_test_managers_filename_check"},
	"user_test_results":     {"user_test_results_output_check"},
	"user_tests":            {"user_tests_input_check"},
	"users":                 {"users_username_check"},
}

// cmsIndexedColumns are the columns CMS declares with index=True. Each one
// must lead some index (its own, or a primary key or UNIQUE constraint).
var cmsIndexedColumns = []string{
	"contests.main_group_id",
	"announcements.contest_id", "announcements.admin_id",
	"groups.contest_id",
	"participations.contest_id", "participations.user_id", "participations.group_id",
	"messages.participation_id", "messages.admin_id",
	"questions.participation_id", "questions.admin_id",
	"tasks.contest_id",
	"statements.task_id",
	"attachments.task_id",
	"managers.dataset_id",
	"testcases.dataset_id",
	"submissions.participation_id", "submissions.task_id",
	"files.submission_id",
	"tokens.submission_id",
	"executables.submission_id", "executables.dataset_id",
	"evaluations.submission_id", "evaluations.dataset_id", "evaluations.testcase_id",
	"user_tests.participation_id", "user_tests.task_id",
	"user_test_files.user_test_id",
	"user_test_managers.user_test_id",
	"user_test_executables.user_test_id", "user_test_executables.dataset_id",
}

// unindexedForeignKeys are the foreign keys whose referencing rows are
// looked up without an index (only when the referenced row is deleted).
var unindexedForeignKeys = map[string]string{
	"participations_team_id_fkey":       "teams are rarely deleted and participations is small",
	"user_test_results_dataset_id_fkey": "datasets are rarely deleted and user tests are few",
}

const (
	sqlNotNull = "23502"
	sqlFKey    = "23503"
	sqlUnique  = "23505"
	sqlCheck   = "23514"
)

func cmsTables() []string {
	tables := make([]string, 0, len(cmsColumns))
	for t := range cmsColumns {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	return tables
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// diffSets returns the elements only in want and only in got.
func diffSets(want, got []string) (missing, unexpected []string) {
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			unexpected = append(unexpected, g)
		}
	}
	return missing, unexpected
}

func TestSchemaColumns(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT table_name::text, column_name::text,
		       CASE WHEN data_type = 'ARRAY' THEN substr(udt_name, 2) || '[]'
		            WHEN data_type = 'timestamp with time zone' THEN 'timestamptz'
		            ELSE data_type::text END
		       || CASE WHEN is_nullable = 'NO' THEN ' NOT NULL' ELSE '' END,
		       coalesce(is_identity = 'YES' AND identity_generation = 'BY DEFAULT', false)
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name::text = ANY ($1::text[])`, cmsTables())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	identity := map[string]bool{}
	for rows.Next() {
		var table, column, spec string
		var ident bool
		if err := rows.Scan(&table, &column, &spec, &ident); err != nil {
			t.Fatal(err)
		}
		got[table] = append(got[table], column+" "+spec)
		if ident {
			identity[table+"."+column] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, table := range cmsTables() {
		if len(got[table]) == 0 {
			t.Errorf("table %s does not exist", table)
			continue
		}
		missing, unexpected := diffSets(cmsColumns[table], got[table])
		for _, c := range missing {
			t.Errorf("%s: missing column %q", table, c)
		}
		for _, c := range unexpected {
			t.Errorf("%s: unexpected column %q", table, c)
		}
		// Ids are identity columns that still accept explicit values
		// (dump imports keep the original ids).
		hasID := slices.ContainsFunc(cmsColumns[table], func(c string) bool { return strings.HasPrefix(c, "id ") })
		if hasID != identity[table+".id"] {
			t.Errorf("%s.id: GENERATED BY DEFAULT AS IDENTITY = %v, want %v", table, identity[table+".id"], hasID)
		}
	}
}

func TestSchemaConstraints(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT c.relname::text, x.conname::text, x.contype::text, pg_get_constraintdef(x.oid)
		FROM pg_constraint x JOIN pg_class c ON c.oid = x.conrelid
		WHERE c.relnamespace = 'public'::regnamespace AND x.contype IN ('p', 'u', 'f', 'c')
		  AND c.relname::text = ANY ($1::text[])`, cmsTables())
	if err != nil {
		t.Fatal(err)
	}
	keys, checks := map[string][]string{}, map[string][]string{}
	for rows.Next() {
		var table, name, kind, def string
		if err := rows.Scan(&table, &name, &kind, &def); err != nil {
			t.Fatal(err)
		}
		if kind == "c" {
			checks[table] = append(checks[table], name)
		} else {
			keys[table] = append(keys[table], name+" "+def)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, table := range cmsTables() {
		missing, unexpected := diffSets(cmsKeys[table], keys[table])
		for _, c := range missing {
			t.Errorf("%s: missing constraint %q", table, c)
		}
		for _, c := range unexpected {
			t.Errorf("%s: unexpected constraint %q", table, c)
		}
		missing, unexpected = diffSets(cmsChecks[table], checks[table])
		for _, c := range missing {
			t.Errorf("%s: missing CHECK constraint %s", table, c)
		}
		for _, c := range unexpected {
			t.Errorf("%s: unexpected CHECK constraint %s", table, c)
		}
	}
}

// indexes returns, per table, the columns of each index in order
// (primary keys and UNIQUE constraints included).
func indexes(t *testing.T, pool *pgxpool.Pool) map[string][][]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname::text,
		       array(SELECT a.attname::text FROM unnest(i.indkey::int2[]) WITH ORDINALITY k(attnum, n)
		             JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum ORDER BY k.n)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid
		WHERE c.relnamespace = 'public'::regnamespace`)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][][]string{}
	for rows.Next() {
		var table string
		var cols []string
		if err := rows.Scan(&table, &cols); err != nil {
			t.Fatal(err)
		}
		out[table] = append(out[table], cols)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSchemaIndexes(t *testing.T) {
	pool := testutil.DB(t)
	idx := indexes(t, pool)
	leads := func(table string, cols ...string) bool {
		for _, ix := range idx[table] {
			if len(ix) > 0 && slices.Contains(cols, ix[0]) {
				return true
			}
		}
		return false
	}

	// Every lookup CMS indexed is still served by an index.
	for _, tc := range cmsIndexedColumns {
		table, col, _ := strings.Cut(tc, ".")
		if !leads(table, col) {
			t.Errorf("CMS indexes %s, but no index starts with it", tc)
		}
	}

	// Deleting or renumbering a referenced row looks up the referencing
	// rows: without an index that is a scan of the whole table.
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname::text, x.conname::text,
		       array(SELECT a.attname::text FROM unnest(x.conkey) k(attnum)
		             JOIN pg_attribute a ON a.attrelid = x.conrelid AND a.attnum = k.attnum)
		FROM pg_constraint x JOIN pg_class c ON c.oid = x.conrelid
		WHERE x.contype = 'f' AND c.relname::text = ANY ($1::text[])`, cmsTables())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, name string
		var cols []string
		if err := rows.Scan(&table, &name, &cols); err != nil {
			t.Fatal(err)
		}
		_, allowed := unindexedForeignKeys[name]
		if covered := leads(table, cols...); covered == allowed {
			if covered {
				t.Errorf("%s is indexed now: remove it from unindexedForeignKeys", name)
			} else {
				t.Errorf("foreign key %s (%s.%v) has no index starting with its columns", name, table, cols)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// graph holds the ids of the complete contest that seed inserts: one row
// in every table of the model.
type graph struct {
	admin, contest, group, task, dataset, testcase, user, team, participation, submission, userTest int64
}

// placeholders replaces {contest}, {task}… in SQL with the ids of g, and
// {digest} / {sha1} with a valid SHA-256 digest and a (rejected) SHA-1 one.
func (g graph) placeholders() *strings.Replacer {
	id := func(v int64) string { return fmt.Sprint(v) }
	return strings.NewReplacer(
		"{admin}", id(g.admin), "{contest}", id(g.contest), "{group}", id(g.group),
		"{task}", id(g.task), "{dataset}", id(g.dataset), "{testcase}", id(g.testcase),
		"{user_test}", id(g.userTest), "{user}", id(g.user), "{team}", id(g.team),
		"{participation}", id(g.participation), "{submission}", id(g.submission),
		"{digest}", digestOf("another file"),
		"{sha1}", "da39a3ee5e6b4b0d3255bfef95601890afd80709",
	)
}

// seed inserts a complete contest: one row in every table.
func seed(t *testing.T, pool *pgxpool.Pool) graph {
	t.Helper()
	ctx := context.Background()
	var g graph
	insert := func(dst *int64, sql string, args ...any) {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if dst != nil {
			*dst = id
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	d := digestOf

	insert(&g.admin, `INSERT INTO admins (name, username, authentication, permission_tasks)
		VALUES ('Ada Lovelace', 'ada', 'plaintext:secret', true) RETURNING id`)
	insert(&g.contest, `INSERT INTO contests (name, description, token_mode, token_gen_max)
		VALUES ('ioi', 'IOI 2026, day 1', 'finite', 4) RETURNING id`)
	insert(&g.group, `INSERT INTO groups (name, contest_id, start, stop, per_user_time)
		VALUES ('default', $1, '2026-10-07 09:00:00Z', '2026-10-07 14:00:00Z', '5 hours') RETURNING id`, g.contest)
	exec(`UPDATE contests SET main_group_id = $1 WHERE id = $2`, g.group, g.contest)

	insert(&g.task, `INSERT INTO tasks (num, contest_id, name, title, submission_format, primary_statements)
		VALUES (0, $1, 'sum', 'Sum', '{sum.%l}', '{en}') RETURNING id`, g.contest)
	insert(&g.dataset, `INSERT INTO datasets (task_id, description, time_limit, memory_limit,
			task_type, task_type_parameters, score_type, score_type_parameters)
		VALUES ($1, 'default', 1.5, 536870912, 'Batch', '["alone", ["", ""], "comparator"]', 'Sum', '100')
		RETURNING id`, g.task)
	exec(`UPDATE tasks SET active_dataset_id = $1 WHERE id = $2`, g.dataset, g.task)
	insert(&g.testcase, `INSERT INTO testcases (dataset_id, codename, public, input, output)
		VALUES ($1, '001', true, $2, $3) RETURNING id`, g.dataset, d("1 2\n"), d("3\n"))
	insert(nil, `INSERT INTO managers (dataset_id, filename, digest) VALUES ($1, 'checker', $2) RETURNING id`,
		g.dataset, d("checker"))
	insert(nil, `INSERT INTO statements (task_id, language, digest) VALUES ($1, 'es_MX', $2) RETURNING id`,
		g.task, d("statement"))
	insert(nil, `INSERT INTO attachments (task_id, filename, digest) VALUES ($1, 'sum.zip', $2) RETURNING id`,
		g.task, d("attachment"))

	insert(&g.user, `INSERT INTO users (first_name, last_name, username, password, preferred_languages)
		VALUES ('Grace', 'Hopper', 'grace', 'bcrypt:$2b$12$abcdefghijklmnopqrstuv', '{es,en}') RETURNING id`)
	insert(&g.team, `INSERT INTO teams (code, name) VALUES ('MEX', 'México') RETURNING id`)
	insert(&g.participation, `INSERT INTO participations (contest_id, user_id, group_id, team_id, ip, extra_time)
		VALUES ($1, $2, $3, $4, '{10.0.0.0/8,2001:db8::/32}', '10 minutes') RETURNING id`,
		g.contest, g.user, g.group, g.team)

	insert(&g.submission, `INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		VALUES (9223372036854775807, $1, $2, '2026-10-07 10:00:00Z', 'C++20 / g++') RETURNING id`,
		g.participation, g.task)
	insert(nil, `INSERT INTO files (submission_id, filename, digest) VALUES ($1, 'sum.%l', $2) RETURNING id`,
		g.submission, d("int main() {}"))
	insert(nil, `INSERT INTO tokens (submission_id) VALUES ($1) RETURNING id`, g.submission)
	exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_text,
			compilation_tries, compilation_time, compilation_memory, compilation_shard,
			compilation_sandbox_paths, compilation_sandbox_digests, evaluation_outcome, evaluation_tries,
			score, score_details, scored_at, public_score, public_score_details, ranking_score_details)
		VALUES ($1, $2, 'ok', '{"Compilation succeeded"}', 1, 0.42, 33554432, 0,
			'{/var/lib/cms/sandbox/1}', '{}', 'ok', 1,
			100, '[{"idx": "001", "outcome": "Correct"}]', '2026-10-07 10:00:05Z', 100, '[]', '{100}')`,
		g.submission, g.dataset)
	insert(nil, `INSERT INTO executables (submission_id, dataset_id, filename, digest)
		VALUES ($1, $2, 'sum', $3) RETURNING id`, g.submission, g.dataset, d("ELF"))
	insert(nil, `INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome, text, admin_text,
			execution_time, execution_wall_clock_time, execution_memory, evaluation_shard)
		VALUES ($1, $2, $3, '1.0', '{"Output is correct"}', 'checker: ok', 0.01, 0.02, 1048576, 0)
		RETURNING id`, g.submission, g.dataset, g.testcase)

	insert(&g.userTest, `INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		VALUES ($1, $2, '2026-10-07 10:05:00Z', 'C++20 / g++', $3) RETURNING id`,
		g.participation, g.task, d("5 6\n"))
	insert(nil, `INSERT INTO user_test_files (user_test_id, filename, digest) VALUES ($1, 'sum.%l', $2) RETURNING id`,
		g.userTest, d("int main() {}"))
	insert(nil, `INSERT INTO user_test_managers (user_test_id, filename, digest) VALUES ($1, 'grader.cpp', $2) RETURNING id`,
		g.userTest, d("grader"))
	exec(`INSERT INTO user_test_results (user_test_id, dataset_id, output, compilation_outcome,
			evaluation_outcome, evaluation_text, execution_time)
		VALUES ($1, $2, $3, 'ok', 'ok', '{"Execution completed successfully"}', 0.01)`,
		g.userTest, g.dataset, d("11\n"))
	insert(nil, `INSERT INTO user_test_executables (user_test_id, dataset_id, filename, digest)
		VALUES ($1, $2, 'sum', $3) RETURNING id`, g.userTest, g.dataset, d("ELF"))

	insert(nil, `INSERT INTO announcements (timestamp, subject, text, contest_id, admin_id)
		VALUES ('2026-10-07 09:00:00Z', 'Welcome', 'Good luck!', $1, $2) RETURNING id`, g.contest, g.admin)
	insert(nil, `INSERT INTO questions (question_timestamp, subject, text, participation_id,
			reply_timestamp, reply_subject, reply_text, admin_id)
		VALUES ('2026-10-07 09:30:00Z', 'Sum', '¿Puede n ser 0?', $1, '2026-10-07 09:31:00Z', 'No', '', $2)
		RETURNING id`, g.participation, g.admin)
	insert(nil, `INSERT INTO messages (timestamp, subject, text, participation_id, admin_id)
		VALUES ('2026-10-07 09:40:00Z', 'Seat', 'Please move to seat 12', $1, $2) RETURNING id`,
		g.participation, g.admin)
	insert(nil, `INSERT INTO audit_log (admin_id, action, target_type, target_id, contest_id, details, ip)
		VALUES ($1, 'task.update', 'task', $2, $3, '{"field": "title", "old": "Suma", "new": "Sum"}', '192.0.2.10')
		RETURNING id`, g.admin, g.task, g.contest)
	return g
}

func count(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string) int {
	t.Helper()
	if !strings.HasPrefix(sql, "SELECT") {
		sql = "SELECT count(*) FROM " + sql
	}
	var n int
	if err := q.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestSchemaFullGraph(t *testing.T) {
	pool := testutil.DB(t)
	g := seed(t, pool)

	for _, table := range cmsTables() {
		if n := count(t, pool, table); n != 1 {
			t.Errorf("%s has %d rows, want 1", table, n)
		}
	}

	// Walk the whole graph from an evaluation to the contest.
	r := g.placeholders()
	walk := r.Replace(`SELECT count(*)
		FROM evaluations e
		JOIN submission_results sr USING (submission_id, dataset_id)
		JOIN executables x USING (submission_id, dataset_id)
		JOIN testcases tc ON tc.id = e.testcase_id AND tc.dataset_id = e.dataset_id
		JOIN submissions s ON s.id = e.submission_id
		JOIN files f ON f.submission_id = s.id
		JOIN tokens tk ON tk.submission_id = s.id
		JOIN tasks t ON t.id = s.task_id AND t.active_dataset_id = e.dataset_id
		JOIN participations p ON p.id = s.participation_id
		JOIN groups gr ON gr.id = p.group_id AND gr.contest_id = p.contest_id
		JOIN contests c ON c.id = p.contest_id AND c.id = t.contest_id AND c.main_group_id = gr.id
		JOIN users u ON u.id = p.user_id
		JOIN teams tm ON tm.id = p.team_id
		WHERE c.id = {contest} AND sr.score = 100 AND f.filename = 'sum.%l'
		  AND '10.1.2.3'::inet <<= ANY (p.ip) AND '2001:db8::1'::inet <<= ANY (p.ip)`)
	if n := count(t, pool, walk); n != 1 {
		t.Errorf("graph walk found %d rows, want 1", n)
	}
	walk = r.Replace(`SELECT count(*)
		FROM user_test_executables x
		JOIN user_test_results r USING (user_test_id, dataset_id)
		JOIN user_tests ut ON ut.id = r.user_test_id
		JOIN user_test_files f ON f.user_test_id = ut.id
		JOIN user_test_managers m ON m.user_test_id = ut.id
		JOIN datasets d ON d.id = r.dataset_id AND d.task_id = ut.task_id
		JOIN managers dm ON dm.dataset_id = d.id
		JOIN statements st ON st.task_id = d.task_id
		JOIN attachments a ON a.task_id = d.task_id
		WHERE ut.participation_id = {participation} AND d.memory_limit = 512 * 1048576`)
	if n := count(t, pool, walk); n != 1 {
		t.Errorf("user test walk found %d rows, want 1", n)
	}
	walk = r.Replace(`SELECT count(*)
		FROM admins a
		JOIN announcements an ON an.admin_id = a.id AND an.contest_id = {contest}
		JOIN questions q ON q.admin_id = a.id AND q.participation_id = {participation}
		JOIN messages m ON m.admin_id = a.id AND m.participation_id = {participation}
		JOIN audit_log l ON l.admin_id = a.id AND l.contest_id = {contest}
		WHERE a.id = {admin} AND a.permission_tasks AND l.target_type = 'task' AND l.target_id = {task}
		  AND l.details->>'new' = 'Sum' AND l.ip = '192.0.2.10'`)
	if n := count(t, pool, walk); n != 1 {
		t.Errorf("communication walk found %d rows, want 1", n)
	}
}

// insertJSON runs an INSERT … RETURNING to_jsonb(…) and decodes the row.
func insertJSON(t *testing.T, pool *pgxpool.Pool, sql string) map[string]any {
	t.Helper()
	var row map[string]any
	if err := pool.QueryRow(context.Background(), sql).Scan(&row); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return row
}

func TestSchemaDefaults(t *testing.T) {
	pool := testutil.DB(t)
	ids := map[string]string{}
	r := func(s string) string {
		for k, v := range ids {
			s = strings.ReplaceAll(s, "{"+k+"}", v)
		}
		return strings.ReplaceAll(s, "{digest}", digestOf("x"))
	}
	interval0, interval30 := "00:00:00", "00:30:00"
	empty := []any{}
	// The defaults that CMS declares in its models (Python side), in the
	// to_jsonb representation (session timezone UTC).
	cases := []struct {
		table, sql string
		want       map[string]any
	}{
		{"admins", `INSERT INTO admins (name, username, authentication) VALUES ('A', 'a', 'plaintext:a')`,
			map[string]any{"enabled": true, "permission_all": false, "permission_messaging": false, "permission_tasks": false}},
		{"contests", `INSERT INTO contests (name, description) VALUES ('c', 'C')`, map[string]any{
			"allowed_localizations": empty, "languages": []string{"C11 / gcc", "C++20 / g++", "Pascal / fpc"},
			"submissions_download_allowed": true, "allow_questions": true, "allow_user_tests": true,
			"allow_unofficial_submission_before_analysis_mode": false, "block_hidden_participations": false,
			"allow_password_authentication": true, "allow_registration": false, "ip_restriction": true,
			"ip_autologin": false, "token_mode": "infinite", "token_max_number": nil,
			"token_min_interval": interval0, "token_gen_initial": 2, "token_gen_number": 2,
			"token_gen_interval": interval30, "token_gen_max": nil, "timezone": nil,
			"max_submission_number": nil, "max_user_test_number": nil, "min_submission_interval": nil,
			"min_submission_interval_grace_period": nil, "min_user_test_interval": nil,
			"score_precision": 0, "main_group_id": nil,
		}},
		{"groups", `INSERT INTO groups (name, contest_id) VALUES ('g', {contests})`, map[string]any{
			"start": "2000-01-01T00:00:00+00:00", "stop": "2100-01-01T00:00:00+00:00", "analysis_enabled": false,
			"analysis_start": "2100-01-01T00:00:00+00:00", "analysis_stop": "2100-01-01T00:00:00+00:00",
			"per_user_time": nil,
		}},
		{"users", `INSERT INTO users (first_name, last_name, username, password) VALUES ('U', 'V', 'u', 'plaintext:u')`,
			map[string]any{"email": nil, "timezone": nil, "preferred_languages": empty}},
		{"participations", `INSERT INTO participations (contest_id, user_id, group_id) VALUES ({contests}, {users}, {groups})`,
			map[string]any{"ip": nil, "starting_time": nil, "delay_time": interval0, "extra_time": interval0,
				"password": nil, "hidden": false, "unrestricted": false, "team_id": nil}},
		{"questions", `INSERT INTO questions (question_timestamp, subject, text, participation_id) VALUES (now(), 's', 't', {participations})`,
			map[string]any{"reply_timestamp": nil, "ignored": false, "reply_subject": nil, "reply_text": nil, "admin_id": nil}},
		{"tasks", `INSERT INTO tasks (name, title) VALUES ('t', 'T')`, map[string]any{
			"num": nil, "contest_id": nil, "submission_format": empty, "primary_statements": empty,
			"allowed_languages": nil, "token_mode": "disabled", "token_max_number": nil,
			"token_min_interval": interval0, "token_gen_initial": 2, "token_gen_number": 2,
			"token_gen_interval": interval30, "token_gen_max": nil, "max_submission_number": nil,
			"max_user_test_number": nil, "min_submission_interval": nil, "min_user_test_interval": nil,
			"feedback_level": "restricted", "score_precision": 0, "score_mode": "max_tokened_last",
			"active_dataset_id": nil,
		}},
		{"datasets", `INSERT INTO datasets (task_id, description, task_type, task_type_parameters, score_type, score_type_parameters)
			VALUES ({tasks}, 'd', 'Batch', '[]', 'Sum', '0')`,
			map[string]any{"autojudge": false, "time_limit": nil, "memory_limit": nil}},
		{"testcases", `INSERT INTO testcases (dataset_id, codename, input, output) VALUES ({datasets}, 'a', '{digest}', '{digest}')`,
			map[string]any{"public": false}},
		{"submissions", `INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp) VALUES (0, {participations}, {tasks}, now())`,
			map[string]any{"language": nil, "comment": "", "official": true}},
		{"submission_results", `INSERT INTO submission_results (submission_id, dataset_id) VALUES ({submissions}, {datasets})`,
			map[string]any{"compilation_outcome": nil, "compilation_text": empty, "compilation_tries": 0,
				"compilation_stdout": nil, "compilation_stderr": nil, "compilation_time": nil,
				"compilation_wall_clock_time": nil, "compilation_memory": nil, "compilation_shard": nil,
				"compilation_sandbox_paths": nil, "compilation_sandbox_digests": nil,
				"evaluation_outcome": nil, "evaluation_tries": 0, "score": nil, "score_details": nil,
				"scored_at": nil, "public_score": nil, "public_score_details": nil, "ranking_score_details": nil,
				"generation": 0}},
		{"evaluations", `INSERT INTO evaluations (submission_id, dataset_id, testcase_id) VALUES ({submissions}, {datasets}, {testcases})`,
			map[string]any{"outcome": nil, "text": empty, "admin_text": nil, "execution_time": nil,
				"execution_wall_clock_time": nil, "execution_memory": nil, "evaluation_shard": nil,
				"evaluation_sandbox_paths": nil, "evaluation_sandbox_digests": nil}},
		{"user_tests", `INSERT INTO user_tests (participation_id, task_id, timestamp, input) VALUES ({participations}, {tasks}, now(), '{digest}')`,
			map[string]any{"language": nil}},
		{"user_test_results", `INSERT INTO user_test_results (user_test_id, dataset_id) VALUES ({user_tests}, {datasets})`,
			map[string]any{"output": nil, "compilation_outcome": nil, "compilation_text": empty, "compilation_tries": 0,
				"evaluation_outcome": nil, "evaluation_text": empty, "evaluation_tries": 0, "generation": 0}},
		{"audit_log", `INSERT INTO audit_log (action, target_type) VALUES ('server.update', 'server_settings')`,
			map[string]any{"admin_id": nil, "target_id": nil, "contest_id": nil, "details": map[string]any{}, "ip": nil}},
	}
	for _, c := range cases {
		row := insertJSON(t, pool, r(c.sql)+" RETURNING to_jsonb("+c.table+".*)")
		if id, ok := row["id"]; ok {
			ids[c.table] = fmt.Sprint(id)
		}
		keys := make([]string, 0, len(c.want))
		for k := range c.want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			got, ok := row[k]
			if !ok {
				t.Errorf("%s.%s: no such column", c.table, k)
				continue
			}
			g, _ := json.Marshal(got)
			w, _ := json.Marshal(c.want[k])
			if string(g) != string(w) {
				t.Errorf("%s.%s defaults to %s, want %s", c.table, k, g, w)
			}
		}
	}
	// Timestamps that default to the current time.
	if _, err := pool.Exec(context.Background(), r(`INSERT INTO tokens (submission_id) VALUES ({submissions})`)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM tokens WHERE timestamp BETWEEN now() - interval '1 minute' AND now()`,
		`SELECT count(*) FROM audit_log WHERE at BETWEEN now() - interval '1 minute' AND now()`,
	} {
		if n := count(t, pool, q); n != 1 {
			t.Errorf("%s: %d rows, want 1", q, n)
		}
	}
}

// runInTx runs the statements in a transaction that is always rolled back,
// and returns the error of the first failing one with its index.
func runInTx(ctx context.Context, pool *pgxpool.Pool, stmts []string) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return -1, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return i, err
		}
	}
	return len(stmts), nil
}

// datasetSQL inserts dataset 1000 of task {task} with testcase 1000.
var datasetSQL = []string{
	`INSERT INTO datasets (id, task_id, description, task_type, task_type_parameters, score_type, score_type_parameters)
	 VALUES (1000, {task}, 'other', 'Batch', '[]', 'Sum', '0')`,
	`INSERT INTO testcases (id, dataset_id, codename, input, output) VALUES (1000, 1000, '001', '{digest}', '{digest}')`,
}

func TestSchemaRejectsInvalidRows(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	g := seed(t, pool)
	r := g.placeholders()

	type violation struct {
		name  string
		stmts []string // all but the last must succeed
		code  string   // SQLSTATE of the last statement
		// The violated constraint, or the column for NOT NULL violations.
		constraint string
	}
	one := func(name, stmt, code, constraint string) violation {
		return violation{name, []string{stmt}, code, constraint}
	}
	upd := func(table, set, constraint string) violation {
		return one(table+": "+set, "UPDATE "+table+" SET "+set, sqlCheck, constraint)
	}
	dup := func(table, columns, constraint string) violation {
		return one(table+": duplicate ("+columns+")",
			"INSERT INTO "+table+" ("+columns+") SELECT "+columns+" FROM "+table, sqlUnique, constraint)
	}
	cases := []violation{
		// contests
		upd("contests", "token_gen_initial = 5, token_gen_max = 4", "contests_check"),
		upd("contests", "token_max_number = 0", "contests_token_max_number_check"),
		upd("contests", "token_min_interval = '-1 second'", "contests_token_min_interval_check"),
		upd("contests", "token_gen_initial = -1", "contests_token_gen_initial_check"),
		upd("contests", "token_gen_number = -1", "contests_token_gen_number_check"),
		upd("contests", "token_gen_interval = '0 seconds'", "contests_token_gen_interval_check"),
		upd("contests", "token_gen_initial = 0, token_gen_max = 0", "contests_token_gen_max_check"),
		upd("contests", "max_submission_number = 0", "contests_max_submission_number_check"),
		upd("contests", "max_user_test_number = 0", "contests_max_user_test_number_check"),
		upd("contests", "min_submission_interval = '0 seconds'", "contests_min_submission_interval_check"),
		upd("contests", "min_submission_interval_grace_period = '0 seconds'", "contests_min_submission_interval_grace_period_check"),
		upd("contests", "min_user_test_interval = '-1 minute'", "contests_min_user_test_interval_check"),
		upd("contests", "score_precision = -1", "contests_score_precision_check"),
		upd("contests", "token_mode = 'mixed'", "contests_token_mode_check"),
		upd("contests", "name = 'IOI 2026'", "contests_name_check"),
		upd("contests", "name = ''", "contests_name_check"),
		one("contests: duplicate name", `INSERT INTO contests (name, description) VALUES ('ioi', 'again')`, sqlUnique, "contests_name_key"),
		one("contests: no description", `INSERT INTO contests (name) VALUES ('other')`, sqlNotNull, "description"),
		one("contests: main group missing", `UPDATE contests SET main_group_id = 1000`, sqlFKey, "fk_contest_main_group_id"),
		// groups
		upd("groups", "start = stop + interval '1 second'", "groups_check"),
		upd("groups", "analysis_start = stop - interval '1 second'", "groups_check1"),
		upd("groups", "analysis_start = '2100-01-02 00:00:00Z'", "groups_check2"),
		upd("groups", "per_user_time = '-1 second'", "groups_per_user_time_check"),
		dup("groups", "contest_id, name", "groups_contest_id_name_key"),
		// admins, users, teams
		upd("admins", "username = 'ada lovelace'", "admins_username_check"),
		dup("admins", "name, username, authentication", "admins_username_key"),
		upd("users", "username = 'grace.hopper'", "users_username_check"),
		dup("users", "first_name, last_name, username, password", "users_username_key"),
		one("users: no password", `INSERT INTO users (first_name, last_name, username) VALUES ('A', 'B', 'ab')`, sqlNotNull, "password"),
		upd("teams", "code = 'MX!'", "teams_code_check"),
		dup("teams", "code, name", "teams_code_key"),
		// participations
		upd("participations", "delay_time = '-1 second'", "participations_delay_time_check"),
		upd("participations", "extra_time = '-1 second'", "participations_extra_time_check"),
		dup("participations", "contest_id, user_id, group_id", "participations_contest_id_user_id_key"),
		{"participations: group of another contest", []string{
			`INSERT INTO contests (id, name, description) VALUES (1000, 'other', 'Other')`,
			`INSERT INTO groups (id, name, contest_id) VALUES (1000, 'default', 1000)`,
			`UPDATE participations SET group_id = 1000`,
		}, sqlFKey, "participations_group_id_contest_id_fkey"},
		one("participations: missing user", `UPDATE participations SET user_id = 1000`, sqlFKey, "participations_user_id_fkey"),
		// tasks
		upd("tasks", "token_gen_initial = 5, token_gen_max = 4", "tasks_check"),
		upd("tasks", "token_max_number = 0", "tasks_token_max_number_check"),
		upd("tasks", "token_min_interval = '-1 second'", "tasks_token_min_interval_check"),
		upd("tasks", "token_gen_initial = -1", "tasks_token_gen_initial_check"),
		upd("tasks", "token_gen_number = -1", "tasks_token_gen_number_check"),
		upd("tasks", "token_gen_interval = '0 seconds'", "tasks_token_gen_interval_check"),
		upd("tasks", "token_gen_initial = 0, token_gen_max = 0", "tasks_token_gen_max_check"),
		upd("tasks", "max_submission_number = 0", "tasks_max_submission_number_check"),
		upd("tasks", "max_user_test_number = 0", "tasks_max_user_test_number_check"),
		upd("tasks", "min_submission_interval = '0 seconds'", "tasks_min_submission_interval_check"),
		upd("tasks", "min_user_test_interval = '0 seconds'", "tasks_min_user_test_interval_check"),
		upd("tasks", "score_precision = -1", "tasks_score_precision_check"),
		upd("tasks", "token_mode = 'mixed'", "tasks_token_mode_check"),
		upd("tasks", "feedback_level = 'none'", "tasks_feedback_level_check"),
		upd("tasks", "score_mode = 'min'", "tasks_score_mode_check"),
		upd("tasks", "name = 'a+b'", "tasks_name_check"),
		upd("tasks", `submission_format = '{"src/sum.%l"}'`, "tasks_submission_format_check"),
		upd("tasks", `submission_format = '{"my sum.%l"}'`, "tasks_submission_format_check"),
		upd("tasks", `submission_format = '{sum.%l,..}'`, "tasks_submission_format_check"),
		upd("tasks", `submission_format = '{.}'`, "tasks_submission_format_check"),
		upd("tasks", `submission_format = '{sum.%x}'`, "tasks_submission_format_check"),
		upd("tasks", `submission_format = '{sum.%l.bak}'`, "tasks_submission_format_check"),
		one("tasks: duplicate name in another contest", `INSERT INTO tasks (name, title) VALUES ('sum', 'Sum')`, sqlUnique, "tasks_name_key"),
		one("tasks: duplicate position", `INSERT INTO tasks (num, contest_id, name, title) VALUES (0, {contest}, 'other', 'Other')`,
			sqlUnique, "tasks_contest_id_num_key"),
		{"tasks: active dataset of another task", []string{
			`INSERT INTO tasks (id, name, title) VALUES (1000, 'other', 'Other')`,
			`INSERT INTO datasets (id, task_id, description, task_type, task_type_parameters, score_type, score_type_parameters)
			 VALUES (1000, 1000, 'd', 'Batch', '[]', 'Sum', '0')`,
			`UPDATE tasks SET active_dataset_id = 1000 WHERE id = {task}`,
		}, sqlFKey, "fk_active_dataset_id"},
		// As in CMS (where ON DELETE SET NULL failed on tasks.id NOT NULL):
		// the live dataset and its official results cannot be deleted.
		one("tasks: deleting the active dataset", `DELETE FROM datasets WHERE id = {dataset}`,
			sqlFKey, "fk_active_dataset_id"),
		// statements, attachments
		upd("statements", "digest = '{sha1}'", "statements_digest_check"),
		dup("statements", "task_id, language, digest", "statements_task_id_language_key"),
		upd("attachments", "filename = '.'", "attachments_filename_check"),
		upd("attachments", "filename = 'docs/sum.pdf'", "attachments_filename_check"),
		upd("attachments", "digest = upper(digest)", "attachments_digest_check"),
		dup("attachments", "task_id, filename, digest", "attachments_task_id_filename_key"),
		// datasets, managers, testcases
		upd("datasets", "time_limit = 0", "datasets_time_limit_check"),
		upd("datasets", "memory_limit = 0", "datasets_memory_limit_check"),
		upd("datasets", "memory_limit = 1000000", "datasets_memory_limit_check1"),
		dup("datasets", "task_id, description, task_type, task_type_parameters, score_type, score_type_parameters",
			"datasets_task_id_description_key"),
		one("datasets: no task type", `INSERT INTO datasets (task_id, description, task_type_parameters, score_type, score_type_parameters)
			VALUES ({task}, 'x', '[]', 'Sum', '0')`, sqlNotNull, "task_type"),
		upd("managers", "filename = '..'", "managers_filename_check"),
		upd("managers", "digest = digest || '0'", "managers_digest_check"),
		dup("managers", "dataset_id, filename, digest", "managers_dataset_id_filename_key"),
		upd("testcases", "codename = '001.in'", "testcases_codename_check"),
		upd("testcases", "input = 'abc'", "testcases_input_check"),
		upd("testcases", "output = ''", "testcases_output_check"),
		dup("testcases", "dataset_id, codename, input, output", "testcases_dataset_id_codename_key"),
		// submissions
		dup("submissions", "opaque_id, participation_id, task_id, timestamp", "participation_opaque_unique"),
		one("submissions: no opaque id", `INSERT INTO submissions (participation_id, task_id, timestamp) VALUES ({participation}, {task}, now())`,
			sqlNotNull, "opaque_id"),
		upd("files", "filename = 'sum.%l.bak'", "files_filename_check"),
		upd("files", "filename = '..'", "files_filename_check"),
		upd("files", "digest = '{sha1}'", "files_digest_check"),
		dup("files", "submission_id, filename, digest", "files_submission_id_filename_key"),
		dup("tokens", "submission_id", "tokens_submission_id_key"),
		upd("submission_results", "compilation_outcome = 'maybe'", "submission_results_compilation_outcome_check"),
		upd("submission_results", "evaluation_outcome = 'fail'", "submission_results_evaluation_outcome_check"),
		dup("submission_results", "submission_id, dataset_id", "submission_results_pkey"),
		upd("executables", "filename = 'sum.%l'", "executables_filename_check"),
		upd("executables", "digest = 'X'", "executables_digest_check"),
		dup("executables", "submission_id, dataset_id, filename, digest", "executables_submission_id_dataset_id_filename_key"),
		{"executables: no submission result", append(slices.Clone(datasetSQL),
			`INSERT INTO executables (submission_id, dataset_id, filename, digest) VALUES ({submission}, 1000, 'sum', '{digest}')`),
			sqlFKey, "executables_submission_id_dataset_id_fkey"},
		dup("evaluations", "submission_id, dataset_id, testcase_id", "evaluations_submission_id_dataset_id_testcase_id_key"),
		{"evaluations: no submission result", append(slices.Clone(datasetSQL),
			`INSERT INTO evaluations (submission_id, dataset_id, testcase_id) VALUES ({submission}, 1000, 1000)`),
			sqlFKey, "evaluations_submission_id_dataset_id_fkey"},
		// user tests
		upd("user_tests", "input = '{sha1}'", "user_tests_input_check"),
		upd("user_test_files", "filename = 'a b.%l'", "user_test_files_filename_check"),
		upd("user_test_files", "digest = ''", "user_test_files_digest_check"),
		dup("user_test_files", "user_test_id, filename, digest", "user_test_files_user_test_id_filename_key"),
		upd("user_test_managers", "filename = 'grader.%l'", "user_test_managers_filename_check"),
		upd("user_test_managers", "digest = 'xx'", "user_test_managers_digest_check"),
		dup("user_test_managers", "user_test_id, filename, digest", "user_test_managers_user_test_id_filename_key"),
		upd("user_test_results", "output = '{sha1}'", "user_test_results_output_check"),
		dup("user_test_results", "user_test_id, dataset_id", "user_test_results_pkey"),
		upd("user_test_executables", "filename = '.'", "user_test_executables_filename_check"),
		upd("user_test_executables", "digest = '{sha1}'", "user_test_executables_digest_check"),
		dup("user_test_executables", "user_test_id, dataset_id, filename, digest",
			"user_test_executables_user_test_id_dataset_id_filename_key"),
		{"user_test_executables: no user test result", append(slices.Clone(datasetSQL),
			`INSERT INTO user_test_executables (user_test_id, dataset_id, filename, digest) VALUES ({user_test}, 1000, 'sum', '{digest}')`),
			sqlFKey, "user_test_executables_user_test_id_dataset_id_fkey"},
		// audit_log
		upd("audit_log", "action = ''", "audit_log_action_check"),
		upd("audit_log", "target_type = ''", "audit_log_target_type_check"),
		upd("audit_log", "details = '[]'", "audit_log_details_check"),
		one("audit_log: missing admin", `UPDATE audit_log SET admin_id = 1000`, sqlFKey, "audit_log_admin_id_fkey"),
	}

	triggered := map[string]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stmts := make([]string, len(c.stmts))
			for i, s := range c.stmts {
				stmts[i] = r.Replace(s)
			}
			i, err := runInTx(ctx, pool, stmts)
			if i < len(stmts)-1 {
				t.Fatalf("setup statement %d failed: %v", i, err)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("%s\nwas accepted (err = %v), want SQLSTATE %s on %s", stmts[len(stmts)-1], err, c.code, c.constraint)
			}
			got := pgErr.ConstraintName
			if c.code == sqlNotNull {
				got = pgErr.ColumnName
			}
			if pgErr.Code != c.code || got != c.constraint {
				t.Errorf("got SQLSTATE %s on %q (%s), want %s on %q", pgErr.Code, got, pgErr.Message, c.code, c.constraint)
			}
			triggered[pgErr.ConstraintName] = true
		})
	}
	// Every CHECK constraint must reject something.
	for _, table := range cmsTables() {
		for _, name := range cmsChecks[table] {
			if !triggered[name] {
				t.Errorf("no test case violates %s", name)
			}
		}
	}
}

func TestSchemaAcceptsValidEdgeCases(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	g := seed(t, pool)
	r := g.placeholders()
	cases := []struct {
		name  string
		stmts []string
	}{
		{"tombstone digest of a deleted executable", []string{
			`UPDATE executables SET digest = 'x'`, `UPDATE user_test_executables SET digest = 'x'`}},
		{"several submission files and headers", []string{
			`UPDATE tasks SET submission_format = '{encoder.%l,decoder.%l,grader.h,output_001.txt}'`}},
		{"output only task without files to submit", []string{`UPDATE tasks SET submission_format = '{}'`}},
		{"submission files with placeholders", []string{
			`INSERT INTO files (submission_id, filename, digest) VALUES ({submission}, 'grader.%l', '{digest}')`,
			`INSERT INTO user_test_files (user_test_id, filename, digest) VALUES ({user_test}, 'output_001.txt', '{digest}')`}},
		{"filenames with dots, dashes and underscores", []string{
			`UPDATE attachments SET filename = 'sum-1.0_en.tar.gz'`, `UPDATE managers SET filename = '.hidden'`}},
		{"IP lock that forbids every address", []string{`UPDATE participations SET ip = '{}'`}},
		{"same opaque id in another participation", []string{
			`INSERT INTO users (id, first_name, last_name, username, password) VALUES (1000, 'A', 'B', 'ab', 'plaintext:x')`,
			`INSERT INTO participations (id, contest_id, user_id, group_id) VALUES (1000, {contest}, 1000, {group})`,
			`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp)
			 SELECT opaque_id, 1000, task_id, timestamp FROM submissions`,
			`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp) VALUES (0, 1000, {task}, now())`}},
		{"tasks outside any contest without a position", []string{
			`INSERT INTO tasks (name, title) VALUES ('a', 'A'), ('b', 'B')`}},
		{"task without an active dataset", []string{`UPDATE tasks SET active_dataset_id = NULL`}},
		{"dataset without limits and a large memory limit", []string{
			`UPDATE datasets SET time_limit = NULL, memory_limit = NULL`,
			`UPDATE datasets SET memory_limit = 8 * 1073741824::bigint, time_limit = 0.001`}},
		{"zero-length windows and analysis right after the contest", []string{
			`UPDATE groups SET start = stop, analysis_enabled = true, analysis_start = stop, analysis_stop = stop,
			 per_user_time = '0 seconds'`}},
		{"finite tokens without a cap", []string{`UPDATE contests SET token_gen_initial = 100, token_gen_max = NULL`}},
		{"user test outcomes are free text as in CMS", []string{
			`UPDATE user_test_results SET compilation_outcome = 'fail', evaluation_outcome = 'anything'`}},
		{"explicit ids from a dump", []string{
			`INSERT INTO teams (id, code, name) VALUES (424242, 'ITA', 'Italia')`,
			`INSERT INTO audit_log (id, action, target_type) VALUES (9000000000, 'team.import', 'team')`}},
		{"announcement and question without an admin", []string{
			`INSERT INTO announcements (timestamp, subject, text, contest_id) VALUES (now(), 'S', 'T', {contest})`,
			`INSERT INTO questions (question_timestamp, subject, text, participation_id) VALUES (now(), 'S', 'T', {participation})`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stmts := make([]string, len(c.stmts))
			for i, s := range c.stmts {
				stmts[i] = r.Replace(s)
			}
			if i, err := runInTx(ctx, pool, stmts); err != nil {
				t.Errorf("%s\nrejected: %v", stmts[i], err)
			}
		})
	}
}

func TestSchemaCascades(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	g := seed(t, pool)
	r := g.placeholders()

	// setup runs first; after maps a table name (row count) or a SELECT
	// count(*) query to the expected count once stmt ran. Each case runs
	// in its own transaction, rolled back afterwards.
	cases := []struct {
		name  string
		setup []string
		stmt  string
		after map[string]int
	}{
		{"deleting a submission removes its files, token and results", nil,
			`DELETE FROM submissions WHERE id = {submission}`,
			map[string]int{"files": 0, "tokens": 0, "submission_results": 0, "executables": 0, "evaluations": 0,
				"participations": 1, "tasks": 1, "testcases": 1, "user_tests": 1}},
		{"deleting a testcase removes its evaluations", nil,
			`DELETE FROM testcases WHERE id = {testcase}`,
			map[string]int{"evaluations": 0, "submission_results": 1, "executables": 1, "datasets": 1}},
		// The active dataset cannot be deleted (TestSchemaRejectsInvalidRows):
		// another one is made live first, as in the CMS interface.
		{"deleting a dataset that is no longer active removes its files and results",
			append(slices.Clone(datasetSQL), `UPDATE tasks SET active_dataset_id = 1000 WHERE id = {task}`),
			`DELETE FROM datasets WHERE id = {dataset}`,
			map[string]int{"SELECT count(*) FROM tasks WHERE id = {task} AND active_dataset_id = 1000": 1,
				"SELECT count(*) FROM testcases WHERE dataset_id = 1000": 1, "testcases": 1, "datasets": 1,
				"managers": 0, "submission_results": 0, "executables": 0, "evaluations": 0,
				"user_test_results": 0, "user_test_executables": 0, "submissions": 1, "user_tests": 1}},
		{"deleting an admin keeps what they wrote", nil,
			`DELETE FROM admins WHERE id = {admin}`,
			map[string]int{
				"SELECT count(*) FROM announcements WHERE admin_id IS NULL": 1,
				"SELECT count(*) FROM messages WHERE admin_id IS NULL":      1,
				"SELECT count(*) FROM questions WHERE admin_id IS NULL":     1,
				"SELECT count(*) FROM audit_log WHERE admin_id IS NULL":     1,
			}},
		{"deleting a team keeps its participations", nil,
			`DELETE FROM teams WHERE id = {team}`,
			map[string]int{"SELECT count(*) FROM participations WHERE team_id IS NULL": 1}},
		{"deleting the main group clears it and removes its participations", nil,
			`DELETE FROM groups WHERE id = {group}`,
			map[string]int{"SELECT count(*) FROM contests WHERE main_group_id IS NULL": 1,
				"participations": 0, "submissions": 0, "evaluations": 0, "user_tests": 0, "messages": 0,
				"questions": 0, "tasks": 1, "users": 1}},
		{"deleting a user removes their participations and everything below", nil,
			`DELETE FROM users WHERE id = {user}`,
			map[string]int{"participations": 0, "submissions": 0, "files": 0, "tokens": 0, "evaluations": 0,
				"user_tests": 0, "user_test_results": 0, "messages": 0, "questions": 0,
				"contests": 1, "groups": 1, "teams": 1, "tasks": 1}},
		{"deleting a participation removes its submissions, tests and communication", nil,
			`DELETE FROM participations WHERE id = {participation}`,
			map[string]int{"submissions": 0, "user_tests": 0, "messages": 0, "questions": 0, "users": 1, "groups": 1}},
		// The task row goes in the same statement, so fk_active_dataset_id
		// does not block the cascade to its active dataset.
		{"deleting a task removes its statements, datasets and submissions", nil,
			`DELETE FROM tasks WHERE id = {task}`,
			map[string]int{"statements": 0, "attachments": 0, "datasets": 0, "managers": 0, "testcases": 0,
				"submissions": 0, "evaluations": 0, "user_tests": 0, "user_test_executables": 0,
				"contests": 1, "participations": 1}},
		// As in CMS, deleting a contest deletes its tasks too (tasks_contest_id_fkey ON DELETE CASCADE).
		{"deleting a contest removes everything in it but keeps people and the audit log", nil,
			`DELETE FROM contests WHERE id = {contest}`,
			map[string]int{"groups": 0, "participations": 0, "tasks": 0, "datasets": 0, "testcases": 0,
				"submissions": 0, "evaluations": 0, "user_tests": 0, "announcements": 0, "messages": 0, "questions": 0,
				"users": 1, "teams": 1, "admins": 1, "audit_log": 1,
				"SELECT count(*) FROM audit_log WHERE contest_id IS NULL": 1}},
		{"renumbering a contest follows everywhere", nil,
			`UPDATE contests SET id = 900 WHERE id = {contest}`,
			map[string]int{"SELECT count(*) FROM groups WHERE contest_id = 900": 1,
				"SELECT count(*) FROM participations WHERE contest_id = 900": 1,
				"SELECT count(*) FROM tasks WHERE contest_id = 900":          1,
				"SELECT count(*) FROM announcements WHERE contest_id = 900":  1,
				"SELECT count(*) FROM audit_log WHERE contest_id = 900":      1}},
		{"renumbering a group follows everywhere", nil,
			`UPDATE groups SET id = 900 WHERE id = {group}`,
			map[string]int{"SELECT count(*) FROM contests WHERE main_group_id = 900": 1,
				"SELECT count(*) FROM participations WHERE group_id = 900": 1}},
		{"renumbering a task keeps its active dataset", nil,
			`UPDATE tasks SET id = 900 WHERE id = {task}`,
			map[string]int{"SELECT count(*) FROM tasks WHERE id = 900 AND active_dataset_id = {dataset}": 1,
				"SELECT count(*) FROM datasets WHERE task_id = 900":    1,
				"SELECT count(*) FROM submissions WHERE task_id = 900": 1,
				"SELECT count(*) FROM statements WHERE task_id = 900":  1}},
		{"renumbering a dataset follows everywhere", nil,
			`UPDATE datasets SET id = 900 WHERE id = {dataset}`,
			map[string]int{"SELECT count(*) FROM tasks WHERE active_dataset_id = 900": 1,
				"SELECT count(*) FROM testcases WHERE dataset_id = 900":             1,
				"SELECT count(*) FROM managers WHERE dataset_id = 900":              1,
				"SELECT count(*) FROM submission_results WHERE dataset_id = 900":    1,
				"SELECT count(*) FROM executables WHERE dataset_id = 900":           1,
				"SELECT count(*) FROM evaluations WHERE dataset_id = 900":           1,
				"SELECT count(*) FROM user_test_results WHERE dataset_id = 900":     1,
				"SELECT count(*) FROM user_test_executables WHERE dataset_id = 900": 1}},
		{"renumbering a submission follows everywhere", nil,
			`UPDATE submissions SET id = 900 WHERE id = {submission}`,
			map[string]int{"SELECT count(*) FROM files WHERE submission_id = 900": 1,
				"SELECT count(*) FROM tokens WHERE submission_id = 900":             1,
				"SELECT count(*) FROM submission_results WHERE submission_id = 900": 1,
				"SELECT count(*) FROM executables WHERE submission_id = 900":        1,
				"SELECT count(*) FROM evaluations WHERE submission_id = 900":        1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			for _, stmt := range append(slices.Clone(c.setup), c.stmt) {
				if _, err := tx.Exec(ctx, r.Replace(stmt)); err != nil {
					t.Fatalf("%s: %v", stmt, err)
				}
			}
			keys := make([]string, 0, len(c.after))
			for k := range c.after {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if n := count(t, tx, r.Replace(k)); n != c.after[k] {
					t.Errorf("after %q: %s = %d, want %d", c.stmt, k, n, c.after[k])
				}
			}
		})
	}
}

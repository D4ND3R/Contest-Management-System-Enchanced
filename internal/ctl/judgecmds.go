// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// JudgeCommands returns the commands of the judging core: `reevaluate`
// (the reevaluation buttons of the admin and the cmscontrib scripts that
// call invalidate_submission) and `judging-status`.
func JudgeCommands() []*cli.Command {
	return []*cli.Command{reevaluateCommand(), judgingStatusCommand()}
}

// idList is a flag that takes ids separated by commas, and can be repeated.
type idList []int32

func (l *idList) String() string {
	parts := make([]string, len(*l))
	for i, id := range *l {
		parts[i] = strconv.Itoa(int(id))
	}
	return strings.Join(parts, ",")
}

func (l *idList) Set(s string) error {
	for _, part := range strings.Split(s, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 32)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid id %q", part)
		}
		*l = append(*l, int32(id))
	}
	return nil
}

func reevaluateCommand() *cli.Command {
	var (
		contest, task, user, testcase, level string
		dataset, participation               int
		submissions, userTests               idList
		archive                              bool
	)
	return &cli.Command{
		Name:  "reevaluate",
		Short: "recompile, reevaluate or rescore submissions (and user tests)",
		Long: `Throws away part of the results of the selected submissions and has the
dispatcher judge them again (CMS invalidate_submission):

  --level compilation  recompile: compilation, evaluations and score
  --level evaluation   reevaluate: evaluations and score (the compilation stays)
  --level score        rescore: the score only

The selection is every submission that matches all the flags given (at least
one): a contest, a task, a participation, a user (all their participations),
submission ids; --dataset limits the results to one dataset (and the
submissions to its task), --testcase to the evaluations of one testcase (a
codename of --dataset, or else a testcase id). --user-test selects user tests
too (--level compilation or evaluation). Contests, tasks and users are names,
or else ids (a name made of digits wins over the id).

Jobs in flight for the invalidated results are discarded when their results
arrive. The dispatcher is notified through Valkey; if it cannot be reached it
finds the work at its next sweep.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&contest, "contest", "", "contest name or id")
			fs.StringVar(&task, "task", "", "task name or id")
			fs.IntVar(&dataset, "dataset", 0, "dataset id")
			fs.StringVar(&user, "user", "", "username or user id")
			fs.IntVar(&participation, "participation", 0, "participation id")
			fs.Var(&submissions, "submission", "submission ids, separated by commas")
			fs.StringVar(&testcase, "testcase", "", "testcase codename (of --dataset) or id, with --level evaluation")
			fs.Var(&userTests, "user-test", "user test ids, separated by commas")
			fs.StringVar(&level, "level", string(judging.LevelCompilation), "compilation, evaluation or score")
			fs.BoolVar(&archive, "archive-sandbox", false, "ask the workers to archive the sandboxes of the new jobs")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			lv := judging.Level(level)
			if !lv.Valid() {
				return cli.UsageError("--level must be compilation, evaluation or score, not %q", level)
			}
			cfg, pool, err := openJudgingDB(ctx, env)
			if err != nil {
				return err
			}
			defer pool.Close()
			scope := judging.Scope{
				DatasetID: int32(dataset), ParticipationID: int32(participation),
				SubmissionIDs: submissions, UserTestIDs: userTests,
			}
			if scope.ContestID, err = lookupID(ctx, pool, contestLookup, contest); err != nil {
				return err
			}
			if scope.TaskID, err = lookupID(ctx, pool, taskLookup, task); err != nil {
				return err
			}
			if scope.UserID, err = lookupID(ctx, pool, userLookup, user); err != nil {
				return err
			}
			if err := checkExists(ctx, pool, "dataset", `SELECT 1 FROM datasets WHERE id = $1`, scope.DatasetID); err != nil {
				return err
			}
			if err := checkExists(ctx, pool, "participation", `SELECT 1 FROM participations WHERE id = $1`, scope.ParticipationID); err != nil {
				return err
			}
			if scope.TestcaseID, err = lookupTestcase(ctx, pool, testcase, scope.DatasetID); err != nil {
				return err
			}
			j := judging.New(pool, judging.Options{Logger: slog.New(slog.DiscardHandler),
				Tries: judging.TriesFrom(cfg.Dispatcher)})
			inv, err := j.Invalidate(ctx, scope, lv)
			switch {
			case errors.Is(err, judging.ErrEmptyScope):
				return cli.UsageError("select what to reevaluate (--contest, --task, --dataset, --user, --participation, --submission, --user-test)")
			case errors.Is(err, judging.ErrLevel):
				return cli.UsageError("--testcase needs --level evaluation")
			case err != nil:
				return err
			}
			fmt.Fprintf(env.Stdout, "%s: %d results of %d submissions invalidated", lv, len(inv.Results), len(inv.Submissions))
			if len(scope.UserTestIDs) > 0 {
				fmt.Fprintf(env.Stdout, ", %d results of %d user tests", len(inv.UserTestResults), len(inv.UserTests))
			}
			fmt.Fprintf(env.Stdout, "; %d task scores changed\n", len(inv.TaskScores))
			events := inv.Events(archive)
			if len(events) == 0 {
				return nil
			}
			if err := notifyDispatcher(ctx, cfg, events); err != nil {
				fmt.Fprintf(env.Stderr, "warning: could not notify the dispatcher (%v); it will find the work at its next sweep\n", err)
				if lv == judging.LevelScore {
					// The sweep only looks at the datasets to judge; running
					// the command again notifies the same submissions.
					fmt.Fprintln(env.Stderr, "warning: the results on datasets that are neither active nor autojudge are only rescored when notified: run this command again once Valkey is reachable")
				}
				return nil
			}
			fmt.Fprintln(env.Stdout, "the dispatcher was notified")
			return nil
		},
	}
}

func judgingStatusCommand() *cli.Command {
	var contest string
	return &cli.Command{
		Name:  "judging-status",
		Short: "count the submissions and user tests of a contest by judging status",
		Long: `Counts the submissions and user tests of each task of a contest (every
contest without --contest) by the status of their results on each dataset
that is judged (the active one and those with autojudge): compiling,
compilation failed, evaluating, scoring, scored. "Gave up" are results whose
compilation or evaluation failed for infrastructure reasons as many times as
allowed: like CMS, they stay compiling or evaluating until reevaluated.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&contest, "contest", "", "contest name or id (default: every contest)")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			cfg, pool, err := openJudgingDB(ctx, env)
			if err != nil {
				return err
			}
			defer pool.Close()
			id, err := lookupID(ctx, pool, contestLookup, contest)
			if err != nil {
				return err
			}
			r, err := judging.New(pool, judging.Options{Logger: slog.New(slog.DiscardHandler),
				Tries: judging.TriesFrom(cfg.Dispatcher)}).Status(ctx, id)
			if err != nil {
				return err
			}
			return printStatus(env.Stdout, r)
		},
	}
}

func printStatus(w io.Writer, r *judging.StatusReport) error {
	dataset := func(s judging.DatasetStatus) string {
		if s.Active {
			return s.DatasetDescription + " (active)"
		}
		return s.DatasetDescription
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SUBMISSIONS")
	fmt.Fprintln(tw, "TASK\tDATASET\tTOTAL\tCOMPILING\tCOMPILATION FAILED\tEVALUATING\tSCORING\tSCORED\tGAVE UP\t")
	for _, s := range r.Submissions {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t\n", s.TaskName, dataset(s), s.Total, s.Compiling,
			s.CompilationFailed, s.Evaluating, s.Scoring, s.Scored, s.CompilationGaveUp+s.EvaluationGaveUp)
	}
	fmt.Fprintln(tw, "\t")
	fmt.Fprintln(tw, "USER TESTS")
	fmt.Fprintln(tw, "TASK\tDATASET\tTOTAL\tCOMPILING\tCOMPILATION FAILED\tEVALUATING\tEVALUATED\tGAVE UP\t")
	for _, s := range r.UserTests {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t\n", s.TaskName, dataset(s), s.Total, s.Compiling,
			s.CompilationFailed, s.Evaluating, s.Evaluated, s.CompilationGaveUp+s.EvaluationGaveUp)
	}
	return tw.Flush()
}

// openJudgingDB loads the configuration and opens the database, refusing
// a schema that does not match this binary.
func openJudgingDB(ctx context.Context, env *cli.Env) (*config.Config, *pgxpool.Pool, error) {
	cfg, cfgPath, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	pool, err := openDB(ctx, cfg, cfgPath)
	if err != nil {
		return nil, nil, err
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return cfg, pool, nil
}

// lookup says how to find something by name or by id.
type lookup struct {
	what         string
	byName, byID string
}

// The things given by name or id (names are unique).
var (
	contestLookup = lookup{"contest", `SELECT id FROM contests WHERE name = $1`, `SELECT id FROM contests WHERE id = $1`}
	taskLookup    = lookup{"task", `SELECT id FROM tasks WHERE name = $1`, `SELECT id FROM tasks WHERE id = $1`}
	userLookup    = lookup{"user", `SELECT id FROM users WHERE username = $1`, `SELECT id FROM users WHERE id = $1`}
)

// lookupID resolves a name, or else an id that exists; "" is 0 (not
// given). The name is looked up first, so a contest called "2024" is that
// contest and not the one with id 2024.
func lookupID(ctx context.Context, pool *pgxpool.Pool, l lookup, value string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	var id int32
	err := pool.QueryRow(ctx, l.byName, value).Scan(&id)
	if !errors.Is(err, pgx.ErrNoRows) {
		return id, err
	}
	if n, perr := strconv.ParseInt(value, 10, 32); perr == nil && n > 0 {
		err = pool.QueryRow(ctx, l.byID, int32(n)).Scan(&id)
		if !errors.Is(err, pgx.ErrNoRows) {
			return id, err
		}
	}
	return 0, fmt.Errorf("%s %q not found", l.what, value)
}

// checkExists fails if id is set and query (with id as $1) finds nothing.
func checkExists(ctx context.Context, pool *pgxpool.Pool, what, query string, id int32) error {
	if id == 0 {
		return nil
	}
	var one int
	err := pool.QueryRow(ctx, query, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s %d not found", what, id)
	}
	return err
}

// lookupTestcase resolves a codename of the dataset (CMS codenames are
// often numbers: "001"), or else a testcase id, which must belong to the
// dataset when one is given.
func lookupTestcase(ctx context.Context, pool *pgxpool.Pool, value string, dataset int32) (int32, error) {
	if value == "" {
		return 0, nil
	}
	var id int32
	if dataset != 0 {
		err := pool.QueryRow(ctx, `SELECT id FROM testcases WHERE dataset_id = $1 AND codename = $2`, dataset, value).Scan(&id)
		if !errors.Is(err, pgx.ErrNoRows) {
			return id, err
		}
	}
	n, perr := strconv.ParseInt(value, 10, 32)
	if perr != nil || n <= 0 {
		if dataset == 0 {
			return 0, cli.UsageError("--testcase %q needs --dataset (or a testcase id)", value)
		}
		return 0, fmt.Errorf("testcase %q not found in dataset %d", value, dataset)
	}
	var of int32
	err := pool.QueryRow(ctx, `SELECT id, dataset_id FROM testcases WHERE id = $1`, int32(n)).Scan(&id, &of)
	switch {
	case errors.Is(err, pgx.ErrNoRows) && dataset != 0:
		return 0, fmt.Errorf("testcase %q not found in dataset %d (neither a codename nor an id)", value, dataset)
	case errors.Is(err, pgx.ErrNoRows):
		return 0, fmt.Errorf("testcase %d not found", n)
	case err != nil:
		return 0, err
	case dataset != 0 && of != dataset:
		return 0, fmt.Errorf("testcase %q not found in dataset %d (testcase %d is in dataset %d)", value, dataset, n, of)
	}
	return id, nil
}

// notifyDispatcher sends events to the dispatcher through Valkey.
func notifyDispatcher(ctx context.Context, cfg *config.Config, events []judging.Event) error {
	c, err := kv.Open(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer c.Close()
	return judging.Notify(ctx, c, events...)
}

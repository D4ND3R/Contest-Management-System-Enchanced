// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// The loaders are registered in importer.Default by their packages
// (internal/importer/<format>), which the binary imports for that effect.

// ImportCommands returns the commands that bring packages into the
// database (cmsImportContest, cmsImportTask, cmsImportUser, cmsImportTeam
// and cmsImportDataset of CMS), the ones that check a package without
// writing it (validate-task, validate-contest), and export-task and
// import-dump.
func ImportCommands() []*cli.Command {
	return []*cli.Command{
		importContestCommand(),
		importTaskCommand(),
		importUsersCommand(),
		importTeamCommand(),
		importDatasetCommand(),
		validateTaskCommand(),
		validateContestCommand(),
		exportTaskCommand(),
		importDumpCommand(),
	}
}

// errNotImplemented is returned by the commands that are not available
// yet.
var errNotImplemented = errors.New("not implemented")

// importFlags are the flags shared by the import commands.
type importFlags struct {
	fs     *flag.FlagSet
	loader string
	dryRun bool
}

// registerWrite adds the flags of the commands that write: register and
// --dry-run.
func (f *importFlags) registerWrite(fs *flag.FlagSet) {
	f.register(fs)
	fs.BoolVar(&f.dryRun, "dry-run", false, "check everything and print what would change, without writing anything")
}

// register adds -L/--loader to fs and remembers fs to parse the flags
// that follow the directory (see positional).
func (f *importFlags) register(fs *flag.FlagSet) {
	f.fs = fs
	fs.StringVar(&f.loader, "L", "", "loader of the package (default: autodetect)")
	fs.StringVar(&f.loader, "loader", "", "same as -L")
}

// positional returns the positional arguments, parsing the flags found
// among them: like the argparse commands of CMS, "import-task sum -U"
// works (the flag package stops at the first argument that is not a flag).
func (f *importFlags) positional(args []string) ([]string, error) {
	var out []string
	if f.fs == nil {
		return args, nil
	}
	f.fs.SetOutput(io.Discard)
	f.fs.Usage = func() {}
	for len(args) > 0 {
		if args[0] == "--" {
			return append(out, args[1:]...), nil
		}
		out = append(out, args[0])
		if err := f.fs.Parse(args[1:]); err != nil {
			return nil, cli.UsageError("%v", err)
		}
		args = f.fs.Args()
	}
	return out, nil
}

// boolFlags defines a flag with several names (-u and --update-contest).
func boolFlags(fs *flag.FlagSet, p *bool, usage string, names ...string) {
	for i, n := range names {
		u := usage
		if i > 0 {
			u = "same as -" + names[0]
		}
		fs.BoolVar(p, n, false, u)
	}
}

// stringFlags is boolFlags for strings.
func stringFlags(fs *flag.FlagSet, p *string, usage string, names ...string) {
	for i, n := range names {
		u := usage
		if i > 0 {
			u = "same as -" + names[0]
		}
		fs.StringVar(p, n, "", u)
	}
}

// loadersHelp is the list of loaders for the help of the commands.
func loadersHelp() string {
	return "\n\n" + importer.Default.Help()
}

// importEnv is what an import needs: the configuration, the database, the
// blob store and the languages of the installation.
type importEnv struct {
	cfg    *config.Config
	pool   *pgxpool.Pool
	blobs  blob.Store
	writer *importer.Writer
}

func openImport(ctx context.Context, env *cli.Env, f *importFlags) (*importEnv, error) {
	cfg, pool, err := openJudgingDB(ctx, env)
	if err != nil {
		return nil, err
	}
	langs, err := language.Load(cfg.LanguagesDir)
	if err != nil {
		pool.Close()
		return nil, err
	}
	// No cache: a command stores each file once, and the cache directory
	// would be locked against a second command running at the same time.
	blobs, err := blob.OpenBackend(cfg.Storage, "ctl")
	if err != nil {
		pool.Close()
		return nil, err
	}
	w := importer.NewWriter(pool, blobs, langs)
	w.DryRun = f.dryRun
	return &importEnv{cfg: cfg, pool: pool, blobs: blobs, writer: w}, nil
}

func (e *importEnv) close() {
	_ = blob.Close(e.blobs)
	e.pool.Close()
}

// installedLanguages returns the languages of the installation for the commands
// that do not open the database.
func installedLanguages(env *cli.Env) (*language.Registry, error) {
	cfg, _, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, err
	}
	return language.Load(cfg.LanguagesDir)
}

// packageDir checks the directory argument and makes it absolute (loaders and
// messages name files by their full path).
func packageDir(args []string, extra string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, cli.UsageError("missing the directory of the package")
	}
	if extra == "" && len(args) > 1 {
		return "", nil, cli.UsageError("unexpected arguments %q", args[1:])
	}
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return "", nil, err
	}
	return dir, args[1:], nil
}

// finishImport prints what an import did and notifies the dispatcher. A failed
// import wrote nothing: its report (errors and warnings) is printed.
func finishImport(ctx context.Context, env *cli.Env, ie *importEnv, res *importer.Result, err error) error {
	if res == nil {
		res = &importer.Result{Report: &importer.Report{}}
	}
	var ve *importer.ValidationError
	if errors.As(err, &ve) {
		_, _ = res.Report.WriteTo(env.Stderr)
		return fmt.Errorf("%s in the package; nothing was written", countOf(len(ve.Report.Errors()), "error"))
	}
	for _, w := range res.Report.Warnings() {
		fmt.Fprintln(env.Stderr, w.String())
	}
	if err != nil {
		for _, e := range res.Report.Errors() {
			fmt.Fprintln(env.Stderr, e.String())
		}
		return fmt.Errorf("%w (nothing was written)", err)
	}
	printChanges(env.Stdout, res.Changes)
	if ie.writer.DryRun {
		fmt.Fprintln(env.Stdout, "dry run: nothing was written")
		return nil
	}
	if len(res.Events) == 0 {
		return nil
	}
	if err := notifyDispatcher(ctx, ie.cfg, res.Events); err != nil {
		fmt.Fprintf(env.Stderr, "warning: could not notify the dispatcher (%v); it will find the work at its next sweep\n", err)
		return nil
	}
	fmt.Fprintln(env.Stdout, "the dispatcher was notified")
	return nil
}

// printChanges prints one line per change, or one per kind and action
// when there are many (a contest with a thousand participants).
func printChanges(w io.Writer, changes []importer.Change) {
	type key struct {
		action importer.Action
		kind   string
	}
	counts := map[key]int{}
	for _, c := range changes {
		counts[key{c.Action, c.Kind}]++
	}
	done := map[key]bool{}
	for _, c := range changes {
		k := key{c.Action, c.Kind}
		if n := counts[k]; n > 10 {
			if !done[k] {
				done[k] = true
				fmt.Fprintf(w, "%s %s\n", c.Action, countOf(n, c.Kind))
			}
			continue
		}
		fmt.Fprintln(w, c.String())
	}
}

func countOf(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

func importContestCommand() *cli.Command {
	var (
		f    importFlags
		opts importer.ContestOptions
		yes  bool
	)
	return &cli.Command{
		Name:  "import-contest",
		Args:  "[flags] DIR",
		Short: "import a contest, its tasks and its participants (cmsImportContest)",
		Long: `Imports the contest at DIR, in one transaction: nothing is written if
anything fails. The files are stored in the blob store first.

A new contest is created with its groups and attached to its tasks, in the
order of the package; tasks that are not in the database are imported with
-i, and tasks that are updated with -U keep their submissions (see
import-task). An existing contest is an error unless -u (update its
settings and groups; groups not in the package are kept) or -U.
Participations are created or updated (only what the package says
changes); their users and teams must exist (import-users, import-team) or
be in the package and imported with --users. The participations of users
that are not in the package are kept, unless --delete-stale-participations
(which deletes them with their submissions).

The dispatcher is notified to judge what is new.` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.registerWrite(fs)
			boolFlags(fs, &opts.ImportTasks, "import the tasks that are not in the database", "i", "import-tasks")
			boolFlags(fs, &opts.UpdateContest, "update an existing contest", "u", "update-contest")
			boolFlags(fs, &opts.UpdateTasks, "update the existing tasks", "U", "update-tasks")
			boolFlags(fs, &opts.SkipStatements, "do not import or update the statements", "S", "no-statements")
			boolFlags(fs, &opts.ZeroTime, "set the start and stop of every group to 1970-01-01", "z", "zero-time")
			fs.BoolVar(&opts.ImportUsers, "users", false, "also create the users and teams of the package that are not in the database")
			fs.BoolVar(&opts.DeleteStaleParticipations, "delete-stale-participations", false,
				"delete the participations (and submissions) of users that are not in the package")
			boolFlags(fs, &yes, "accepted for compatibility with cmsImportContest (nothing is asked)", "y", "yes")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, _, err := packageDir(args, "")
			if err != nil {
				return err
			}
			l, err := importer.Default.ChooseContest(f.loader, dir)
			if err != nil {
				return err
			}
			ie, err := openImport(ctx, env, &f)
			if err != nil {
				return err
			}
			defer ie.close()
			res, err := ie.writer.ImportContest(ctx, l, dir, opts)
			return finishImport(ctx, env, ie, res, err)
		},
	}
}

func importTaskCommand() *cli.Command {
	var (
		f       importFlags
		opts    importer.TaskImport
		contest string
	)
	return &cli.Command{
		Name:  "import-task",
		Args:  "[flags] DIR",
		Short: "import a task, or update it keeping its submissions (cmsImportTask)",
		Long: `Imports the task at DIR, in one transaction (nothing is written if anything
fails), attached at the end of the contest given with -c.

An existing task is an error unless -u, which updates it as cmsImportTask
does: its settings, statements and attachments become those of the
package; a dataset with the description of an existing one updates it in
place (its testcases by codename: a changed testcase keeps its
evaluations), a dataset with a new description is added, not active; the
active dataset and the submissions are kept. Results on a dataset whose
judging data changed are kept too: reevaluate them (reevaluate --dataset).` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.registerWrite(fs)
			boolFlags(fs, &opts.Update, "update the task if it exists", "u", "U", "update")
			boolFlags(fs, &opts.SkipStatements, "do not import or update the statements", "S", "no-statement", "no-statements")
			stringFlags(fs, &contest, "contest (name or id) to attach the task to", "c", "contest", "contest-id")
			stringFlags(fs, &opts.Prefix, "prefix added to the name of the task", "p", "prefix")
			stringFlags(fs, &opts.Name, "name of the task, instead of the one of the package", "n", "name")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, _, err := packageDir(args, "")
			if err != nil {
				return err
			}
			l, err := importer.Default.ChooseTask(f.loader, dir)
			if err != nil {
				return err
			}
			ie, err := openImport(ctx, env, &f)
			if err != nil {
				return err
			}
			defer ie.close()
			if opts.ContestID, err = lookupID(ctx, ie.pool, contestLookup, contest); err != nil {
				return err
			}
			res, err := ie.writer.ImportTask(ctx, l, dir, opts)
			return finishImport(ctx, env, ie, res, err)
		},
	}
}

func importUsersCommand() *cli.Command {
	var (
		f       importFlags
		opts    importer.UserOptions
		contest string
		all     bool
	)
	return &cli.Command{
		Name:  "import-users",
		Args:  "[flags] DIR [USERNAME...]",
		Short: "import the users of a package (cmsImportUser)",
		Long: `Imports the users of the package at DIR (a contest whose package lists
them, for the formats that do), or only those named, in one transaction.
Existing users are left alone, or updated with -u (a user without a
password in the package keeps hers). Users created without a password get
a random one. With -c, every user named gets a participation in the main
group of that contest if she has none.` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.registerWrite(fs)
			boolFlags(fs, &opts.Update, "update the users that exist", "u", "update")
			stringFlags(fs, &contest, "contest (name or id) to add the users to", "c", "contest", "contest-id")
			boolFlags(fs, &all, "accepted for compatibility with cmsImportUser (every user is imported unless named)", "A", "all")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, names, err := packageDir(args, "USERNAME")
			if err != nil {
				return err
			}
			l, err := importer.Default.ChooseUsers(f.loader, dir)
			if err != nil {
				return err
			}
			ie, err := openImport(ctx, env, &f)
			if err != nil {
				return err
			}
			defer ie.close()
			if opts.ContestID, err = lookupID(ctx, ie.pool, contestLookup, contest); err != nil {
				return err
			}
			opts.Report = &importer.Report{}
			users, err := l.LoadUsers(dir, &importer.Options{Languages: ie.writer.Languages, Report: opts.Report})
			if err != nil {
				return finishImport(ctx, env, ie, &importer.Result{Report: opts.Report}, fmt.Errorf("loading the users at %s: %w", dir, err))
			}
			if users, err = pickNamed(users, names, func(u importer.User) string { return u.Username }, "user"); err != nil {
				return err
			}
			res, err := ie.writer.WriteUsers(ctx, users, opts)
			return finishImport(ctx, env, ie, res, err)
		},
	}
}

func importTeamCommand() *cli.Command {
	var (
		f    importFlags
		opts importer.TeamOptions
		all  bool
	)
	return &cli.Command{
		Name:  "import-team",
		Args:  "[flags] DIR [CODE...]",
		Short: "import the teams of a package (cmsImportTeam)",
		Long: `Imports the teams of the package at DIR, or only those named, in one
transaction. Existing teams are left alone, or renamed with -u.` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.registerWrite(fs)
			boolFlags(fs, &opts.Update, "rename the teams that exist", "u", "update")
			boolFlags(fs, &all, "accepted for compatibility with cmsImportTeam (every team is imported unless named)", "A", "all")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, codes, err := packageDir(args, "CODE")
			if err != nil {
				return err
			}
			l, err := importer.Default.ChooseTeams(f.loader, dir)
			if err != nil {
				return err
			}
			ie, err := openImport(ctx, env, &f)
			if err != nil {
				return err
			}
			defer ie.close()
			opts.Report = &importer.Report{}
			teams, err := l.LoadTeams(dir, &importer.Options{Languages: ie.writer.Languages, Report: opts.Report})
			if err != nil {
				return finishImport(ctx, env, ie, &importer.Result{Report: opts.Report}, fmt.Errorf("loading the teams at %s: %w", dir, err))
			}
			if teams, err = pickNamed(teams, codes, func(t importer.Team) string { return t.Code }, "team"); err != nil {
				return err
			}
			res, err := ie.writer.WriteTeams(ctx, teams, opts)
			return finishImport(ctx, env, ie, res, err)
		},
	}
}

// pickNamed keeps the items named (all of them if names is empty).
func pickNamed[T any](items []T, names []string, name func(T) string, what string) ([]T, error) {
	if len(names) == 0 {
		return items, nil
	}
	byName := make(map[string]T, len(items))
	for _, it := range items {
		byName[name(it)] = it
	}
	out := make([]T, 0, len(names))
	var missing []string
	for _, n := range names {
		it, ok := byName[n]
		if !ok {
			missing = append(missing, n)
			continue
		}
		out = append(out, it)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("not in the package: %s %s", what, strings.Join(missing, ", "))
	}
	return out, nil
}

func importDatasetCommand() *cli.Command {
	var (
		f           importFlags
		description string
	)
	return &cli.Command{
		Name:  "import-dataset",
		Args:  "[flags] -d DESCRIPTION DIR",
		Short: "add the judging data of a package as a new dataset of its task (cmsImportDataset)",
		Long: `Reads the task at DIR and adds its active dataset, with the description
given, to the task of the same name in the database. The new dataset is
not active: compare it with the active one and activate it from the
administration.` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.registerWrite(fs)
			stringFlags(fs, &description, "description of the new dataset (required)", "d", "description")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, _, err := packageDir(args, "")
			if err != nil {
				return err
			}
			if strings.TrimSpace(description) == "" {
				return cli.UsageError("-d DESCRIPTION is required")
			}
			l, err := importer.Default.ChooseTask(f.loader, dir)
			if err != nil {
				return err
			}
			ie, err := openImport(ctx, env, &f)
			if err != nil {
				return err
			}
			defer ie.close()
			res, err := ie.writer.ImportDataset(ctx, l, dir, description, nil)
			return finishImport(ctx, env, ie, res, err)
		},
	}
}

// validation is the JSON output of the validate commands.
type validation struct {
	Loader   string           `json:"loader"`
	Kind     string           `json:"kind"`
	Name     string           `json:"name,omitempty"`
	Errors   int              `json:"errors"`
	Warnings int              `json:"warnings"`
	Issues   []importer.Issue `json:"issues"`
}

// printValidation prints a report and fails if it has errors.
func printValidation(env *cli.Env, asJSON bool, v validation, rep *importer.Report, summary []string) error {
	v.Issues = rep.Issues()
	if v.Issues == nil {
		v.Issues = []importer.Issue{}
	}
	v.Errors, v.Warnings = len(rep.Errors()), len(rep.Warnings())
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return err
		}
	} else {
		for _, s := range summary {
			fmt.Fprintln(env.Stdout, s)
		}
		if _, err := rep.WriteTo(env.Stdout); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%s, %s\n", countOf(v.Errors, "error"), countOf(v.Warnings, "warning"))
	}
	if v.Errors > 0 {
		return fmt.Errorf("the %s cannot be imported: %s", v.Kind, countOf(v.Errors, "error"))
	}
	return nil
}

// describeTask summarizes a task for validate-task.
func describeTask(t *importer.Task) []string {
	attachments := len(t.Attachments)
	out := []string{fmt.Sprintf("task %s %q: %s, %s, %s", t.Name, t.Title,
		countOf(len(t.Statements), "statement"), countOf(attachments, "attachment"), countOf(len(t.Datasets), "dataset"))}
	active := t.Active()
	for _, d := range t.Datasets {
		if d == nil {
			continue
		}
		public := 0
		for _, tc := range d.Testcases {
			if tc.Public {
				public++
			}
		}
		limits := "no time limit"
		if d.TimeLimit != nil {
			limits = fmt.Sprintf("%g s", *d.TimeLimit)
		}
		if d.MemoryLimit != nil {
			limits += fmt.Sprintf(", %d MiB", *d.MemoryLimit/importer.MiB)
		} else {
			limits += ", no memory limit"
		}
		mark := ""
		if d == active {
			mark = " (active)"
		}
		out = append(out, fmt.Sprintf("  dataset %q%s: %s, %s, %s, %s (%d public), %s", d.Description, mark,
			d.TaskType, d.ScoreType, limits, countOf(len(d.Testcases), "testcase"), public, countOf(len(d.Managers), "manager")))
	}
	return out
}

func validateTaskCommand() *cli.Command {
	var (
		f      importFlags
		asJSON bool
	)
	return &cli.Command{
		Name:  "validate-task",
		Args:  "[flags] DIR",
		Short: "check a task package without importing it",
		Long: `Reads the task at DIR and checks it as an import would, without a
database: the names and files, the limits, the task type with its
parameters and the managers it needs, the score type with its parameters
against the testcases, the languages. Prints the errors (the task cannot be
imported) and warnings, and fails if there are errors.` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.register(fs)
			fs.BoolVar(&asJSON, "json", false, "print the report as JSON")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, _, err := packageDir(args, "")
			if err != nil {
				return err
			}
			l, err := importer.Default.ChooseTask(f.loader, dir)
			if err != nil {
				return err
			}
			langs, err := installedLanguages(env)
			if err != nil {
				return err
			}
			t, rep, err := importer.ValidateTaskDir(l, dir, langs)
			if err != nil {
				rep.Errorf(dir, "%v", err)
			}
			v := validation{Loader: l.Name(), Kind: "task"}
			var summary []string
			if t != nil {
				v.Name = t.Name
				summary = describeTask(t)
			}
			return printValidation(env, asJSON, v, rep, summary)
		},
	}
}

func validateContestCommand() *cli.Command {
	var (
		f      importFlags
		asJSON bool
	)
	return &cli.Command{
		Name:  "validate-contest",
		Args:  "[flags] DIR",
		Short: "check a contest package and all its tasks without importing them",
		Long: `Reads the contest at DIR and every task of it, and checks them as an
import would, without a database (see validate-task). Prints the errors
and warnings, and fails if there are errors.` + loadersHelp(),
		Flags: func(fs *flag.FlagSet) {
			f.register(fs)
			fs.BoolVar(&asJSON, "json", false, "print the report as JSON")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			args, err := f.positional(args)
			if err != nil {
				return err
			}
			dir, _, err := packageDir(args, "")
			if err != nil {
				return err
			}
			l, err := importer.Default.ChooseContest(f.loader, dir)
			if err != nil {
				return err
			}
			langs, err := installedLanguages(env)
			if err != nil {
				return err
			}
			c, tasks, rep, err := importer.ValidateContestDir(l, dir, langs)
			if err != nil {
				rep.Errorf(dir, "%v", err)
			}
			v := validation{Loader: l.Name(), Kind: "contest"}
			var summary []string
			if c != nil {
				v.Name = c.Name
				summary = append(summary, fmt.Sprintf("contest %s %q: %s, %s, %s, %s", c.Name, c.Description,
					countOf(len(c.Groups), "group"), countOf(len(c.Tasks), "task"),
					countOf(len(c.Users), "user"), countOf(len(c.Participations), "participation")))
				for _, name := range c.Tasks {
					if t := tasks[name]; t != nil {
						summary = append(summary, describeTask(t)...)
					}
				}
			}
			return printValidation(env, asJSON, v, rep, summary)
		},
	}
}

func exportTaskCommand() *cli.Command {
	return &cli.Command{
		Name:  "export-task",
		Args:  "[flags] TASK DIR",
		Short: "export a task as a package of the own format",
		Run: func(context.Context, *cli.Env, []string) error {
			return fmt.Errorf("export-task: %w", errNotImplemented)
		},
	}
}

func importDumpCommand() *cli.Command {
	return &cli.Command{
		Name:  "import-dump",
		Args:  "[flags] DIR",
		Short: "import a dump of CMS (cmsDumpImporter)",
		Run: func(context.Context, *cli.Env, []string) error {
			return fmt.Errorf("import-dump: %w", errNotImplemented)
		},
	}
}

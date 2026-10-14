// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
)

// The domains of CMS (cms/db/types.py), as the CHECK constraints of
// migration 0002 spell them.
var (
	codenameRE       = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	filenameRE       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	filenameSchemaRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+(.%l)?$`)
	schemaCharsRE    = regexp.MustCompile(`^[A-Za-z0-9_.%-]*$`)
)

// ValidCodename reports whether s is a CMS codename (names of contests,
// tasks and groups... usernames, team codes, testcase codenames).
func ValidCodename(s string) bool { return codenameRE.MatchString(s) }

// ValidFilename reports whether s is a CMS file name (attachments,
// managers).
func ValidFilename(s string) bool { return filenameRE.MatchString(s) && s != "." && s != ".." }

// ValidFilenameSchema reports whether s is an element of a submission
// format ("sum.%l", "output_001.txt").
func ValidFilenameSchema(s string) bool {
	return filenameSchemaRE.MatchString(s) && schemaCharsRE.MatchString(s) && s != "." && s != ".."
}

// validator checks models and writes what it finds in a report.
type validator struct {
	langs *language.Registry
	rep   *Report
	stat  *statCache
	// skipStatements: the statements were not loaded (-S).
	skipStatements bool
}

func newValidator(langs *language.Registry, rep *Report) *validator {
	if langs == nil {
		langs = language.Builtin()
	}
	return &validator{langs: langs, rep: rep, stat: &statCache{}}
}

// ValidateTask checks a task against the rules of CMS and what the judge
// can run, adding errors and warnings to rep: the database constraints
// (names, files, limits, tokens), the task type with its parameters and
// managers, the score type with its parameters against the testcases, and
// the allowed languages. A task without errors can be written and judged.
// langs nil means the built-in languages.
func ValidateTask(t *Task, langs *language.Registry, rep *Report) {
	newValidator(langs, rep).task(t)
}

// ValidateContest checks a contest (not its tasks: see ValidateTask and
// ValidateTaskInContest): columns, groups, participations, and the users
// and teams of the package.
func ValidateContest(c *Contest, langs *language.Registry, rep *Report) {
	newValidator(langs, rep).contest(c)
}

// ValidateTaskInContest checks what depends on both a contest and one of
// its tasks: graders for the languages of the contest.
func ValidateTaskInContest(c *Contest, t *Task, langs *language.Registry, rep *Report) {
	newValidator(langs, rep).taskInContest(c, t)
}

// ValidateUsers checks users: usernames and passwords.
func ValidateUsers(users []User, rep *Report) {
	newValidator(nil, rep).users(users)
}

// ValidateTeams checks teams: codes.
func ValidateTeams(teams []Team, rep *Report) {
	newValidator(nil, rep).teams(teams)
}

func taskWhere(name string) string { return "task " + name }

func datasetWhere(task, description string) string {
	return fmt.Sprintf("task %s: dataset %q", task, description)
}

func (v *validator) task(t *Task) {
	where := taskWhere(t.Name)
	switch {
	case t.Name == "":
		v.rep.Errorf("task", "the task has no name")
		where = "task"
	case !ValidCodename(t.Name):
		v.rep.Errorf(where, "invalid task name %q: only letters, digits, _ and - are allowed", t.Name)
	}
	if strings.TrimSpace(t.Title) == "" {
		v.rep.Warnf(where, "the task has no title")
	}
	v.unique(where, "submission format file", t.SubmissionFormat, func(s string) string {
		if !ValidFilenameSchema(s) {
			return fmt.Sprintf("invalid submission format file %q", s)
		}
		return ""
	})
	for _, l := range t.AllowedLanguages {
		if _, ok := v.langs.Lookup(l); !ok {
			v.rep.Errorf(where, "unknown allowed language %q (known: %s)", l, strings.Join(v.langs.Names(), ", "))
		}
	}
	v.tokens(where, t.Tokens)
	v.positive(where, "max_submission_number", t.MaxSubmissionNumber)
	v.positive(where, "max_user_test_number", t.MaxUserTestNumber)
	v.positiveInterval(where, "min_submission_interval", t.MinSubmissionInterval)
	v.positiveInterval(where, "min_user_test_interval", t.MinUserTestInterval)
	switch t.FeedbackLevel {
	case FeedbackFull, FeedbackRestricted, FeedbackOIRestricted:
	default:
		v.rep.Errorf(where, "invalid feedback level %q (want %s, %s or %s)", t.FeedbackLevel,
			FeedbackFull, FeedbackRestricted, FeedbackOIRestricted)
	}
	switch t.ScoreMode {
	case ScoreModeMax, ScoreModeMaxSubtask, ScoreModeMaxTokenedLast:
	default:
		v.rep.Errorf(where, "invalid score mode %q (want %s, %s or %s)", t.ScoreMode,
			ScoreModeMax, ScoreModeMaxSubtask, ScoreModeMaxTokenedLast)
	}
	if t.ScorePrecision < 0 {
		v.rep.Errorf(where, "negative score precision %d", t.ScorePrecision)
	}

	// Statements and attachments.
	languages := make([]string, len(t.Statements))
	for i, s := range t.Statements {
		languages[i] = s.Language
		if s.Language == "" {
			v.rep.Errorf(where, "a statement has no language")
		}
		v.file(where+": statement "+s.Language, s.File)
	}
	v.unique(where, "statement language", languages, nil)
	if !v.skipStatements {
		for _, p := range t.PrimaryStatements {
			if !slices.Contains(languages, p) {
				v.rep.Warnf(where, "primary statement language %q has no statement", p)
			}
		}
	}
	filenames := make([]string, len(t.Attachments))
	for i, a := range t.Attachments {
		filenames[i] = a.Filename
		v.file(where+": attachment "+a.Filename, a.File)
	}
	v.unique(where, "attachment", filenames, func(s string) string {
		if !ValidFilename(s) {
			return fmt.Sprintf("invalid attachment file name %q", s)
		}
		return ""
	})

	// Datasets.
	if len(t.Datasets) == 0 {
		v.rep.Errorf(where, "the task has no dataset: it cannot be judged")
		return
	}
	descriptions := make([]string, len(t.Datasets))
	for i, d := range t.Datasets {
		if d == nil {
			v.rep.Errorf(where, "dataset %d is missing", i)
			continue
		}
		descriptions[i] = d.Description
		v.dataset(t, d)
	}
	v.unique(where, "dataset", descriptions, func(s string) string {
		if s == "" {
			return "a dataset has no description"
		}
		return ""
	})
	if t.Active() == nil {
		v.rep.Errorf(where, "the active dataset %q does not exist", t.ActiveDataset)
	}
}

func (v *validator) dataset(t *Task, d *Dataset) {
	where := datasetWhere(t.Name, d.Description)
	if d.TimeLimit != nil && (!(*d.TimeLimit > 0) || math.IsInf(*d.TimeLimit, 0)) {
		v.rep.Errorf(where, "invalid time limit %v: it must be a positive number of seconds", *d.TimeLimit)
	}
	if d.MemoryLimit != nil && (*d.MemoryLimit <= 0 || *d.MemoryLimit%MiB != 0) {
		v.rep.Errorf(where, "invalid memory limit %d bytes: it must be a positive multiple of 1 MiB", *d.MemoryLimit)
	}

	// Managers.
	managers := make([]string, len(d.Managers))
	for i, m := range d.Managers {
		managers[i] = m.Filename
		v.file(where+": manager "+m.Filename, m.File)
	}
	v.unique(where, "manager", managers, func(s string) string {
		if !ValidFilename(s) {
			return fmt.Sprintf("invalid manager file name %q", s)
		}
		return ""
	})

	// Testcases.
	codenames := make([]string, len(d.Testcases))
	public := make(map[string]bool, len(d.Testcases))
	for i, tc := range d.Testcases {
		codenames[i] = tc.Codename
		public[tc.Codename] = tc.Public
		tw := where + ": testcase " + tc.Codename
		v.file(tw+" input", tc.Input)
		v.file(tw+" output", tc.Output)
	}
	v.unique(where, "testcase", codenames, func(s string) string {
		if !ValidCodename(s) {
			return fmt.Sprintf("invalid testcase codename %q: only letters, digits, _ and - are allowed", s)
		}
		return ""
	})
	if len(d.Testcases) == 0 {
		v.rep.Warnf(where, "the dataset has no testcases")
	}

	// Task type, its parameters and the managers it needs.
	tt, err := tasktype.Get(d.TaskType, d.TaskTypeParameters)
	if err != nil {
		if d.TaskType == "" {
			v.rep.Errorf(where, "no task type (known: %s)", strings.Join(tasktype.Names(), ", "))
		} else if !tasktype.Valid(d.TaskType) {
			v.rep.Errorf(where, "unknown task type %q (known: %s)", d.TaskType, strings.Join(tasktype.Names(), ", "))
		} else {
			v.rep.Errorf(where, "invalid parameters %s for task type %s: %v", compact(d.TaskTypeParameters), d.TaskType, unwrapPrefix(err))
		}
	} else {
		v.managersOf(t, d, tt, where)
		if d.TaskType != tasktype.NameOutputOnly {
			if d.TimeLimit == nil {
				v.rep.Warnf(where, "no time limit")
			}
			if d.MemoryLimit == nil {
				v.rep.Warnf(where, "no memory limit")
			}
		}
		if d.TaskType == tasktype.NameOutputOnly {
			for _, c := range codenames {
				if f := tasktype.UserOutputFilename(c); !slices.Contains(t.SubmissionFormat, f) {
					v.rep.Warnf(where, "testcase %s: %s is not in the submission format, contestants cannot send its output", c, f)
				}
			}
		}
	}

	// Score type and its parameters, resolved against the testcases.
	if !scoretype.Valid(d.ScoreType) {
		v.rep.Errorf(where, "unknown score type %q (known: %s)", d.ScoreType, strings.Join(scoretype.Names(), ", "))
	} else if _, err := scoretype.New(d.ScoreType, d.ScoreTypeParameters, public, int(max(t.ScorePrecision, 0))); err != nil {
		v.rep.Errorf(where, "invalid parameters %s for score type %s: %v", compact(d.ScoreTypeParameters), d.ScoreType, err)
	}
}

// managersOf checks that the dataset has the managers its task type needs:
// a missing checker or manager can never be judged; a grader (grader.%l)
// is needed for at least one language.
func (v *validator) managersOf(t *Task, d *Dataset, tt tasktype.TaskType, where string) {
	have := make(map[string]bool, len(d.Managers))
	for _, m := range d.Managers {
		have[m.Filename] = true
	}
	for _, m := range tt.Managers() {
		base, perLanguage := strings.CutSuffix(m, ".%l")
		if !perLanguage {
			if !have[m] {
				v.rep.Errorf(where, "task type %s needs the manager %q", d.TaskType, m)
			}
			continue
		}
		var found bool
		for _, l := range v.candidateLanguages(t) {
			if have[base+l.SourceExtension()] {
				found = true
				break
			}
		}
		if !found {
			v.rep.Errorf(where, "task type %s needs %s for the languages of the contest (e.g. %s.cpp): none found", d.TaskType, m, base)
		}
	}
}

// candidateLanguages are the languages a submission to t may use: its
// allowed languages, or every language.
func (v *validator) candidateLanguages(t *Task) []*language.Language {
	if t.AllowedLanguages == nil {
		return v.langs.All()
	}
	var out []*language.Language
	for _, name := range t.AllowedLanguages {
		if l, ok := v.langs.Lookup(name); ok {
			out = append(out, l)
		}
	}
	return out
}

func (v *validator) taskInContest(c *Contest, t *Task) {
	langs := c.Languages
	if t.AllowedLanguages != nil {
		langs = slices.DeleteFunc(slices.Clone(langs), func(l string) bool { return !slices.Contains(t.AllowedLanguages, l) })
	}
	for _, d := range t.Datasets {
		if d == nil {
			continue
		}
		tt, err := tasktype.Get(d.TaskType, d.TaskTypeParameters)
		if err != nil {
			continue
		}
		have := make(map[string]bool, len(d.Managers))
		for _, m := range d.Managers {
			have[m.Filename] = true
		}
		for _, m := range tt.Managers() {
			base, perLanguage := strings.CutSuffix(m, ".%l")
			if !perLanguage {
				continue
			}
			var missing []string
			for _, name := range langs {
				if l, ok := v.langs.Lookup(name); ok && !have[base+l.SourceExtension()] {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				v.rep.Warnf(datasetWhere(t.Name, d.Description), "no %s for %s: submissions in these languages cannot be judged",
					m, strings.Join(missing, ", "))
			}
		}
	}
}

func (v *validator) contest(c *Contest) {
	where := "contest " + c.Name
	switch {
	case c.Name == "":
		v.rep.Errorf("contest", "the contest has no name")
		where = "contest"
	case !ValidCodename(c.Name):
		v.rep.Errorf(where, "invalid contest name %q: only letters, digits, _ and - are allowed", c.Name)
	}
	v.unique(where, "language", c.Languages, func(s string) string {
		if _, ok := v.langs.Lookup(s); !ok {
			return fmt.Sprintf("unknown language %q (known: %s)", s, strings.Join(v.langs.Names(), ", "))
		}
		return ""
	})
	if len(c.Languages) == 0 {
		v.rep.Warnf(where, "the contest allows no language")
	}
	v.tokens(where, c.Tokens)
	if c.Timezone != nil && *c.Timezone != "" {
		if _, err := time.LoadLocation(*c.Timezone); err != nil {
			v.rep.Warnf(where, "unknown timezone %q", *c.Timezone)
		}
	}
	v.positive(where, "max_submission_number", c.MaxSubmissionNumber)
	v.positive(where, "max_user_test_number", c.MaxUserTestNumber)
	v.positiveInterval(where, "min_submission_interval", c.MinSubmissionInterval)
	v.positiveInterval(where, "min_submission_interval_grace_period", c.MinSubmissionIntervalGracePeriod)
	v.positiveInterval(where, "min_user_test_interval", c.MinUserTestInterval)
	if c.ScorePrecision < 0 {
		v.rep.Errorf(where, "negative score precision %d", c.ScorePrecision)
	}

	// Groups.
	if len(c.Groups) == 0 {
		v.rep.Errorf(where, "the contest has no group")
	}
	names := make([]string, len(c.Groups))
	for i, g := range c.Groups {
		names[i] = g.Name
		gw := fmt.Sprintf("%s: group %q", where, g.Name)
		if g.Start.After(g.Stop) || g.Stop.After(g.AnalysisStart) || g.AnalysisStart.After(g.AnalysisStop) {
			v.rep.Errorf(gw, "the times are not in order (start %s, stop %s, analysis start %s, analysis stop %s)",
				fmtTime(g.Start), fmtTime(g.Stop), fmtTime(g.AnalysisStart), fmtTime(g.AnalysisStop))
		}
		if g.PerUserTime != nil && *g.PerUserTime < 0 {
			v.rep.Errorf(gw, "negative per-user time %s", *g.PerUserTime)
		}
	}
	v.unique(where, "group", names, func(s string) string {
		if s == "" {
			return "a group has no name"
		}
		return ""
	})
	if main := c.MainGroupName(); len(c.Groups) > 0 && c.Group(main) == nil {
		v.rep.Errorf(where, "the main group %q does not exist", main)
	}

	// Tasks.
	v.unique(where, "task", c.Tasks, func(s string) string {
		if !ValidCodename(s) {
			return fmt.Sprintf("invalid task name %q", s)
		}
		return ""
	})
	if len(c.Tasks) == 0 {
		v.rep.Warnf(where, "the contest has no task")
	}

	// People.
	v.users(c.Users)
	v.teams(c.Teams)
	usernames := make([]string, len(c.Participations))
	for i, p := range c.Participations {
		usernames[i] = p.Username
		v.participation(c, where, p)
	}
	v.unique(where, "participation of", usernames, func(s string) string {
		if !ValidCodename(s) {
			return fmt.Sprintf("invalid username %q in a participation", s)
		}
		return ""
	})
}

func (v *validator) participation(c *Contest, where string, p Participation) {
	pw := fmt.Sprintf("%s: participation of %s", where, p.Username)
	if p.Group != "" && c.Group(p.Group) == nil {
		// An update keeps the groups of the contest that are not in the
		// package: the writer checks that it exists.
		v.rep.Warnf(pw, "group %q is not in the package: it must exist in the contest already", p.Group)
	}
	if p.Team != nil && *p.Team != "" && !ValidCodename(*p.Team) {
		v.rep.Errorf(pw, "invalid team code %q", *p.Team)
	}
	if p.Password != nil && *p.Password != "" {
		if _, _, err := ParseAuthentication(*p.Password); err != nil {
			v.rep.Errorf(pw, "%v", err)
		}
	}
	for _, n := range p.IP {
		if !n.IsValid() || n.Masked() != n {
			v.rep.Errorf(pw, "invalid network %s", n)
		}
	}
	if p.DelayTime != nil && *p.DelayTime < 0 {
		v.rep.Errorf(pw, "negative delay time %s", *p.DelayTime)
	}
	if p.ExtraTime != nil && *p.ExtraTime < 0 {
		v.rep.Errorf(pw, "negative extra time %s", *p.ExtraTime)
	}
}

func (v *validator) users(users []User) {
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Username
		if u.Password != "" {
			if _, _, err := ParseAuthentication(u.Password); err != nil {
				v.rep.Errorf("user "+u.Username, "%v", err)
			}
		}
	}
	v.unique("users", "user", names, func(s string) string {
		if !ValidCodename(s) {
			return fmt.Sprintf("invalid username %q: only letters, digits, _ and - are allowed", s)
		}
		return ""
	})
}

func (v *validator) teams(teams []Team) {
	codes := make([]string, len(teams))
	for i, t := range teams {
		codes[i] = t.Code
		if strings.TrimSpace(t.Name) == "" {
			v.rep.Warnf("team "+t.Code, "the team has no name")
		}
	}
	v.unique("teams", "team", codes, func(s string) string {
		if !ValidCodename(s) {
			return fmt.Sprintf("invalid team code %q: only letters, digits, _ and - are allowed", s)
		}
		return ""
	})
}

// tokens checks token rules against the CHECK constraints of CMS.
func (v *validator) tokens(where string, tk Tokens) {
	switch tk.Mode {
	case TokenModeDisabled, TokenModeFinite, TokenModeInfinite:
	default:
		v.rep.Errorf(where, "invalid token mode %q (want %s, %s or %s)", tk.Mode, TokenModeDisabled, TokenModeFinite, TokenModeInfinite)
	}
	v.positive(where, "token_max_number", tk.MaxNumber)
	if tk.MinInterval < 0 {
		v.rep.Errorf(where, "negative token_min_interval %s", tk.MinInterval)
	}
	if tk.GenInitial < 0 {
		v.rep.Errorf(where, "negative token_gen_initial %d", tk.GenInitial)
	}
	if tk.GenNumber < 0 {
		v.rep.Errorf(where, "negative token_gen_number %d", tk.GenNumber)
	}
	if tk.GenInterval <= 0 {
		v.rep.Errorf(where, "token_gen_interval must be positive, not %s", tk.GenInterval)
	}
	v.positive(where, "token_gen_max", tk.GenMax)
	if tk.GenMax != nil && *tk.GenMax > 0 && tk.GenInitial > *tk.GenMax {
		v.rep.Errorf(where, "token_gen_initial (%d) is larger than token_gen_max (%d)", tk.GenInitial, *tk.GenMax)
	}
}

func (v *validator) positive(where, what string, n *int32) {
	if n != nil && *n <= 0 {
		v.rep.Errorf(where, "%s must be positive, not %d", what, *n)
	}
}

func (v *validator) positiveInterval(where, what string, d *time.Duration) {
	if d != nil && *d <= 0 {
		v.rep.Errorf(where, "%s must be positive, not %s", what, *d)
	}
}

// unique reports invalid values (check returns a message, "" if valid)
// and duplicates.
func (v *validator) unique(where, what string, values []string, check func(string) string) {
	seen := make(map[string]bool, len(values))
	for _, s := range values {
		if check != nil {
			if msg := check(s); msg != "" {
				v.rep.Errorf(where, "%s", msg)
			}
		}
		if seen[s] {
			v.rep.Errorf(where, "duplicate %s %q", what, s)
		}
		seen[s] = true
	}
}

// file checks that a file of the model can be read: a path that exists,
// or a valid digest.
func (v *validator) file(where string, f *File) {
	switch {
	case f == nil:
		v.rep.Errorf(where, "no content")
	case f.path != "":
		if err := v.stat.check(f.path); err != nil {
			v.rep.Errorf(where, "cannot read %s: %v", f.path, unwrapPath(err))
		}
	case f.digest != "" && !blob.ValidDigest(f.digest):
		v.rep.Errorf(where, "invalid digest %q", f.digest)
	}
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// compact prints JSON parameters on one line.
func compact(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "(none)"
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// unwrapPrefix drops the "tasktype: invalid parameters: " prefix of an
// error of tasktype.Get.
func unwrapPrefix(err error) string {
	msg := err.Error()
	if _, rest, ok := strings.Cut(msg, tasktype.ErrParameters.Error()+": "); ok {
		return rest
	}
	return msg
}

// unwrapPath shortens *fs.PathError messages (the path is already said).
func unwrapPath(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok && u.Unwrap() != nil {
		return u.Unwrap()
	}
	return err
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
)

// The objects of a dump become rows of this installation: one record per
// object with its columns converted (timestamps of CMS are naive UTC
// seconds, intervals seconds, digests SHA-1 until the files are hashed)
// and its parents as indexes into Dump.keys (none: -1). Records are small:
// a dump can have millions of evaluations.

// none is a missing reference.
const none int32 = -1

type tokens struct {
	mode        string
	maxNumber   *int32
	minInterval time.Duration
	genInitial  int32
	genNumber   int32
	genInterval time.Duration
	genMax      *int32
}

type admin struct {
	key                                         int32
	name, username, auth                        string
	enabled, permissionAll, permissionMessaging bool
	// used: an imported row names it.
	used bool
	// existing is the id of the admin with its username (0: created).
	existing int32
}

type contest struct {
	key                                    int32
	name, description                      string
	allowedLocalizations, languages        []string
	downloadAllowed, allowQuestions        bool
	allowUserTests, allowUnofficial        bool
	blockHidden, allowPassword             bool
	allowRegistration, ipRestriction       bool
	ipAutologin                            bool
	tokens                                 tokens
	timezone                               *string
	maxSubmissionNumber, maxUserTestNumber *int32
	minSubmissionInterval                  *time.Duration
	minSubmissionIntervalGrace             *time.Duration
	minUserTestInterval                    *time.Duration
	scorePrecision                         int32
	mainGroup                              int32
}

type group struct {
	key, contest                int32
	name                        string
	start, stop                 time.Time
	analysisEnabled             bool
	analysisStart, analysisStop time.Time
	perUserTime                 *time.Duration
}

type announcement struct {
	key, contest, admin int32
	timestamp           time.Time
	subject, text       string
}

type user struct {
	key                                   int32
	firstName, lastName, username, passwd string
	email, timezone                       *string
	preferredLanguages                    []string
	existing                              int32 // id of the user reused (ReuseUsers)
}

type team struct {
	key        int32
	code, name string
	existing   int32
}

type participation struct {
	key, contest, user, group, team int32
	ip                              []netip.Prefix // nil: NULL
	startingTime                    *time.Time
	delayTime, extraTime            time.Duration
	password                        *string
	hidden, unrestricted            bool
}

type message struct {
	key, participation, admin int32
	timestamp                 time.Time
	subject, text             string
}

type question struct {
	key, participation, admin int32
	questionTimestamp         time.Time
	subject, text             string
	replyTimestamp            *time.Time
	ignored                   bool
	replySubject, replyText   *string
}

type task struct {
	key, contest, activeDataset            int32
	num                                    *int32
	name, title                            string
	submissionFormat, primaryStatements    []string
	allowedLanguages                       []string // nil: NULL
	tokens                                 tokens
	maxSubmissionNumber, maxUserTestNumber *int32
	minSubmissionInterval                  *time.Duration
	minUserTestInterval                    *time.Duration
	feedbackLevel, scoreMode               string
	scorePrecision                         int32
}

// fileRow is a statement, an attachment, a manager, a submitted file, an
// executable, a file or manager of a user test or an executable of a user
// test: a name (a language for statements) and a digest, under a parent
// (and a dataset for executables, given by their result when they name
// it).
type fileRow struct {
	key, parent, dataset, result int32
	name, digest                 string
}

type dataset struct {
	key, task               int32
	description             string
	autojudge               bool
	timeLimit               *float64
	memoryLimit             *int64
	taskType, scoreType     string
	taskParams, scoreParams json.RawMessage
}

type testcase struct {
	key, dataset  int32
	codename      string
	public        bool
	input, output string
}

type submission struct {
	key, participation, task int32
	opaqueID                 int64
	timestamp                time.Time
	language                 *string
	comment                  string
	official                 bool
}

type token struct {
	key, submission int32
	timestamp       time.Time
}

// compilation are the compilation columns of submission and user test
// results.
type compilation struct {
	outcome         *string
	text            []string
	tries           int32
	stdout, stderr  *string
	time, wallTime  *float64
	memory          *int64
	shard           *int32
	paths, digests  []string // nil: NULL
	evaluationTries int32
	evaluation      *string
}

type submissionResult struct {
	key, submission, dataset int32
	compilation
	score, publicScore          *float64
	scoreDetails, publicDetails json.RawMessage // nil: NULL
	scoredAt                    *time.Time
	rankingDetails              []string // nil: NULL
}

// execution are the execution columns of evaluations and user test
// results.
type execution struct {
	time, wallTime *float64
	memory         *int64
	shard          *int32
	paths, digests []string // nil: NULL
}

type evaluation struct {
	key, submission, dataset, testcase, result int32
	outcome                                    *string
	text                                       []string
	adminText                                  *string
	execution
}

type userTest struct {
	key, participation, task int32
	timestamp                time.Time
	language                 *string
	input                    string
}

type userTestResult struct {
	key, userTest, dataset int32
	output                 *string
	compilation
	evaluationText []string
	execution
}

// Dump is a dump of CMS read, updated to the current data model and
// converted to the rows of this installation (Read). Import writes it.
type Dump struct {
	// Version is the version of the data model the dump was written with.
	Version int

	keys  []string         // by index
	index map[string]int32 // key → index
	class []string         // class of each object; "" for keys not in the dump
	skip  []bool           // not imported (Options.Skip*, or a parent skipped)
	roots []string

	admins          []*admin
	contests        []*contest
	groups          []*group
	announcements   []*announcement
	users           []*user
	teams           []*team
	participations  []*participation
	messages        []*message
	questions       []*question
	tasks           []*task
	statements      []*fileRow
	attachments     []*fileRow
	datasets        []*dataset
	managers        []*fileRow
	testcases       []*testcase
	submissions     []*submission
	files           []*fileRow
	tokens          []*token
	results         []*submissionResult
	executables     []*fileRow
	evaluations     []*evaluation
	userTests       []*userTest
	userTestFiles   []*fileRow
	userTestManager []*fileRow
	userTestResults []*userTestResult
	userTestExecs   []*fileRow

	// need are the files the rows name, by SHA-1.
	need map[string]*use
	// exported are the contests whose files the dump has (exportedContests).
	exported map[int32]bool
	// unknown counts the objects of classes this importer does not know.
	unknown map[string]int

	rep  *importer.Report
	opts *Options
}

// fileKind says what a missing file means.
type fileKind int

const (
	// optional: the digest is dropped (archived sandboxes, which dumps
	// never carry).
	optional fileKind = iota
	// generated: the digest becomes the tombstone "x" (an executable is
	// compiled again).
	generated
	// outside: a file of a task in no contest, or in a contest that is not
	// a root of the dump (it came with the users of an exported contest:
	// cmsDumpExporter -c), or of its submissions and user tests, which
	// cmsDumpExporter does not export: the digest becomes the tombstone,
	// and the task must be imported again from its package.
	outside
	// required: the dump cannot be imported without it.
	required
)

// use is how the rows use a file.
type use struct {
	kind fileKind
	// where is the row that names it, for messages.
	where string
	// task is the task of an outside file.
	task int32
}

// tombstone is the digest of a file whose content is gone (CMS
// Digest.TOMBSTONE).
const tombstone = "x"

func newDump(version int, roots []string, rep *importer.Report, opts *Options) *Dump {
	return &Dump{
		Version: version, roots: roots, index: map[string]int32{},
		need: map[string]*use{}, unknown: map[string]int{}, rep: rep, opts: opts,
	}
}

// ref interns a key.
func (d *Dump) ref(key string) int32 {
	if i, ok := d.index[key]; ok {
		return i
	}
	i := int32(len(d.keys))
	d.index[key] = i
	d.keys = append(d.keys, key)
	d.class = append(d.class, "")
	d.skip = append(d.skip, false)
	return i
}

// where locates an object in messages: "Task 2", or "Task 2 (sum)".
func (d *Dump) where(i int32, name string) string {
	s := d.class[i] + " " + d.keys[i]
	if name != "" {
		s += fmt.Sprintf(" (%s)", name)
	}
	return s
}

// conv converts the fields of one object, reporting what is wrong.
type conv struct {
	d      *Dump
	i      int32
	name   string
	failed bool
}

func (c *conv) where() string { return c.d.where(c.i, c.name) }

func (c *conv) errorf(format string, args ...any) {
	c.d.rep.Errorf(c.where(), format, args...)
	c.failed = true
}

func (c *conv) str(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func (c *conv) required(field string, p *string) string {
	if p == nil {
		c.errorf("no %s", field)
		return ""
	}
	return *p
}

func (c *conv) flag(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func (c *conv) list(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (c *conv) int64Of(field string, n *number) (int64, bool) {
	if n == nil {
		return 0, false
	}
	s := n.raw
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		c.errorf("%s: %s is not an integer", field, s)
		return 0, false
	}
	return int64(f), true
}

func (c *conv) int64p(field string, n *number) *int64 {
	if v, ok := c.int64Of(field, n); ok {
		return &v
	}
	return nil
}

func (c *conv) int32p(field string, n *number) *int32 {
	v, ok := c.int64Of(field, n)
	if !ok {
		return nil
	}
	if v < math.MinInt32 || v > math.MaxInt32 {
		c.errorf("%s: %d is too large", field, v)
		return nil
	}
	w := int32(v)
	return &w
}

func (c *conv) int32d(field string, n *number, def int32) int32 {
	if p := c.int32p(field, n); p != nil {
		return *p
	}
	return def
}

func (c *conv) float(field string, n *number) (float64, bool) {
	if n == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(n.raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		c.errorf("%s: %s is not a number", field, n.raw)
		return 0, false
	}
	return f, true
}

func (c *conv) floatp(field string, n *number) *float64 {
	if f, ok := c.float(field, n); ok {
		return &f
	}
	return nil
}

// micros splits seconds into whole seconds and microseconds as Python's
// datetime.fromtimestamp and timedelta do (rounding half to even).
func micros(f float64) (int64, int64) {
	sec, frac := math.Modf(f)
	us := int64(math.RoundToEven(frac * 1e6))
	s := int64(sec)
	switch {
	case us >= 1e6:
		s, us = s+1, us-1e6
	case us < 0:
		s, us = s-1, us+1e6
	}
	return s, us
}

// timeOf converts a timestamp of CMS (seconds since the epoch, UTC).
func (c *conv) timeOf(field string, n *number) (time.Time, bool) {
	f, ok := c.float(field, n)
	if !ok {
		return time.Time{}, false
	}
	if math.Abs(f) > 1e13 {
		c.errorf("%s: timestamp %s is out of range", field, n.raw)
		return time.Time{}, false
	}
	s, us := micros(f)
	return time.Unix(s, us*1000).UTC(), true
}

func (c *conv) timeReq(field string, n *number) time.Time {
	if n == nil {
		c.errorf("no %s", field)
		return time.Time{}
	}
	t, _ := c.timeOf(field, n)
	return t
}

func (c *conv) timeDef(field string, n *number, def time.Time) time.Time {
	if t, ok := c.timeOf(field, n); ok {
		return t
	}
	return def
}

func (c *conv) timep(field string, n *number) *time.Time {
	if t, ok := c.timeOf(field, n); ok {
		return &t
	}
	return nil
}

// duration converts an interval of CMS (seconds).
func (c *conv) duration(field string, n *number) (time.Duration, bool) {
	f, ok := c.float(field, n)
	if !ok {
		return 0, false
	}
	if math.Abs(f) > 1e11 {
		c.errorf("%s: interval %s is out of range", field, n.raw)
		return 0, false
	}
	s, us := micros(f)
	return time.Duration(s)*time.Second + time.Duration(us)*time.Microsecond, true
}

func (c *conv) durationDef(field string, n *number, def time.Duration) time.Duration {
	if d, ok := c.duration(field, n); ok {
		return d
	}
	return def
}

func (c *conv) durationp(field string, n *number) *time.Duration {
	if d, ok := c.duration(field, n); ok {
		return &d
	}
	return nil
}

// jsonValue returns a JSON column: nil (NULL) for null or missing.
func (c *conv) jsonValue(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// parent interns the key of a parent object (none if null or missing).
func (c *conv) parent(p *string) int32 {
	if p == nil {
		return none
	}
	return c.d.ref(*p)
}

// digest checks a SHA-1 digest of CMS (or the tombstone); the files the
// rows need are collected once they are linked (collectFiles).
func (c *conv) digest(field string, p *string, kind fileKind) string {
	if p == nil {
		if kind == required {
			c.errorf("no %s", field)
		}
		return ""
	}
	d := *p
	if d == tombstone {
		if kind == required {
			c.d.rep.Warnf(c.where(), "%s: the content was deleted in CMS (tombstone): the file cannot be used", field)
		}
		return d
	}
	if !sha1Name.MatchString(d) {
		c.errorf("%s: invalid digest %q (not a SHA-1)", field, d)
		return ""
	}
	return d
}

func (c *conv) tokens(o *object, defMode string) tokens {
	return tokens{
		mode:        c.str(o.TokenMode, defMode),
		maxNumber:   c.int32p("token_max_number", o.TokenMaxNumber),
		minInterval: c.durationDef("token_min_interval", o.TokenMinInterval, 0),
		genInitial:  c.int32d("token_gen_initial", o.TokenGenInitial, 2),
		genNumber:   c.int32d("token_gen_number", o.TokenGenNumber, 2),
		genInterval: c.durationDef("token_gen_interval", o.TokenGenInterval, 30*time.Minute),
		genMax:      c.int32p("token_gen_max", o.TokenGenMax),
	}
}

func (c *conv) compilation(o *object) compilation {
	return compilation{
		outcome:         o.CompilationOutcome,
		text:            c.list(o.CompilationText),
		tries:           c.int32d("compilation_tries", o.CompilationTries, 0),
		stdout:          o.CompilationStdout,
		stderr:          o.CompilationStderr,
		time:            c.floatp("compilation_time", o.CompilationTime),
		wallTime:        c.floatp("compilation_wall_clock_time", o.CompilationWallClockTime),
		memory:          c.int64p("compilation_memory", o.CompilationMemory),
		shard:           c.int32p("compilation_shard", o.CompilationShard),
		paths:           o.CompilationSandboxPaths,
		digests:         c.sandboxDigests("compilation_sandbox_digests", o.CompilationSandboxDigests),
		evaluationTries: c.int32d("evaluation_tries", o.EvaluationTries, 0),
		evaluation:      o.EvaluationOutcome,
	}
}

func (c *conv) execution(o *object) execution {
	return execution{
		time:     c.floatp("execution_time", o.ExecutionTime),
		wallTime: c.floatp("execution_wall_clock_time", o.ExecutionWallClockTime),
		memory:   c.int64p("execution_memory", o.ExecutionMemory),
		shard:    c.int32p("evaluation_shard", o.EvaluationShard),
		paths:    o.EvaluationSandboxPaths,
		digests:  c.sandboxDigests("evaluation_sandbox_digests", o.EvaluationSandboxDigests),
	}
}

// sandboxDigests notes the archived sandboxes of a result: dumps do not
// carry them (enumerate_files leaves them out), so they are usually
// dropped.
func (c *conv) sandboxDigests(field string, l []string) []string {
	for i := range l {
		l[i] = c.digest(field, &l[i], optional)
	}
	return l
}

// text is the text of an announcement, message or question (a string).
func (c *conv) text(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		c.errorf("no text")
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		c.errorf("text: not a string")
	}
	return s
}

// textList is the text of an evaluation (a list of strings).
func (c *conv) textList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}
	}
	var l []string
	if err := json.Unmarshal(raw, &l); err != nil {
		c.errorf("text: not a list of strings")
	}
	return c.list(l)
}

// skipClasses are the classes Options leave out.
func (o *Options) skipClasses() map[string]bool {
	s := map[string]bool{}
	add := func(classes ...string) {
		for _, c := range classes {
			s[c] = true
		}
	}
	if o.SkipSubmissions {
		add("Submission", "File", "Token", "SubmissionResult", "Executable", "Evaluation")
	}
	if o.SkipUserTests {
		add("UserTest", "UserTestFile", "UserTestManager", "UserTestResult", "UserTestExecutable")
	}
	if o.SkipUsers {
		add("User", "Team", "Participation", "Announcement", "Message", "Question",
			"Submission", "File", "Token", "SubmissionResult", "Executable", "Evaluation",
			"UserTest", "UserTestFile", "UserTestManager", "UserTestResult", "UserTestExecutable")
	}
	if o.SkipGenerated {
		add("SubmissionResult", "Executable", "Evaluation", "UserTestResult", "UserTestExecutable")
	}
	return s
}

// add converts one object.
func (d *Dump) add(key string, o *object, skip map[string]bool) {
	i := d.ref(key)
	if d.class[i] != "" {
		d.rep.Errorf(modelName, "object %s appears twice", key)
		return
	}
	d.class[i] = o.Class
	if o.Class == "" {
		d.class[i] = "?"
		d.rep.Errorf("object "+key, "no _class")
		return
	}
	if skip[o.Class] {
		d.skip[i] = true
		return
	}
	c := &conv{d: d, i: i}
	switch o.Class {
	case "Admin":
		c.name = c.str(o.Username, "")
		d.admins = append(d.admins, &admin{
			key: i, name: c.required("name", o.Name), username: c.required("username", o.Username),
			auth: c.required("authentication", o.Authentication), enabled: c.flag(o.Enabled, true),
			permissionAll: c.flag(o.PermissionAll, false), permissionMessaging: c.flag(o.PermissionMessaging, false),
		})
	case "Contest":
		c.name = c.str(o.Name, "")
		x := &contest{
			key: i, name: c.required("name", o.Name), description: c.required("description", o.Description),
			allowedLocalizations:  c.list(o.AllowedLocalizations),
			downloadAllowed:       c.flag(o.SubmissionsDownloadAllowed, true),
			allowQuestions:        c.flag(o.AllowQuestions, true),
			allowUserTests:        c.flag(o.AllowUserTests, true),
			allowUnofficial:       c.flag(o.AllowUnofficialSubmissionBeforeAnalysisMode, false),
			blockHidden:           c.flag(o.BlockHiddenParticipations, false),
			allowPassword:         c.flag(o.AllowPasswordAuthentication, true),
			allowRegistration:     c.flag(o.AllowRegistration, false),
			ipRestriction:         c.flag(o.IPRestriction, true),
			ipAutologin:           c.flag(o.IPAutologin, false),
			tokens:                c.tokens(o, importer.TokenModeInfinite),
			timezone:              o.Timezone,
			maxSubmissionNumber:   c.int32p("max_submission_number", o.MaxSubmissionNumber),
			maxUserTestNumber:     c.int32p("max_user_test_number", o.MaxUserTestNumber),
			minSubmissionInterval: c.durationp("min_submission_interval", o.MinSubmissionInterval),
			minSubmissionIntervalGrace: c.durationp("min_submission_interval_grace_period",
				o.MinSubmissionIntervalGracePeriod),
			minUserTestInterval: c.durationp("min_user_test_interval", o.MinUserTestInterval),
			scorePrecision:      c.int32d("score_precision", o.ScorePrecision, 0),
			mainGroup:           c.parent(o.MainGroup),
		}
		x.languages = o.Languages
		if x.languages == nil {
			x.languages = append([]string(nil), importer.DefaultContestLanguages...)
		}
		d.contests = append(d.contests, x)
	case "Group":
		c.name = c.str(o.Name, "")
		d.groups = append(d.groups, &group{
			key: i, contest: c.parent(o.Contest), name: c.required("name", o.Name),
			start:           c.timeDef("start", o.Start, importer.DefaultGroupStart),
			stop:            c.timeDef("stop", o.Stop, importer.DefaultGroupStop),
			analysisEnabled: c.flag(o.AnalysisEnabled, false),
			analysisStart:   c.timeDef("analysis_start", o.AnalysisStart, importer.DefaultGroupStop),
			analysisStop:    c.timeDef("analysis_stop", o.AnalysisStop, importer.DefaultGroupStop),
			perUserTime:     c.durationp("per_user_time", o.PerUserTime),
		})
	case "Announcement":
		d.announcements = append(d.announcements, &announcement{
			key: i, contest: c.parent(o.Contest), admin: c.parent(o.Admin),
			timestamp: c.timeReq("timestamp", o.Timestamp), subject: c.required("subject", o.Subject),
			text: c.text(o.Text),
		})
	case "User":
		c.name = c.str(o.Username, "")
		pw := c.str(o.Password, "")
		if o.Password == nil {
			pw = importer.BuildPassword(importer.RandomPassword(), importer.PasswordPlaintext)
		}
		d.users = append(d.users, &user{
			key: i, firstName: c.required("first_name", o.FirstName), lastName: c.required("last_name", o.LastName),
			username: c.required("username", o.Username), passwd: pw, email: o.Email, timezone: o.Timezone,
			preferredLanguages: c.list(o.PreferredLanguages),
		})
	case "Team":
		c.name = c.str(o.Code, "")
		d.teams = append(d.teams, &team{key: i, code: c.required("code", o.Code), name: c.required("name", o.Name)})
	case "Participation":
		p := &participation{
			key: i, contest: c.parent(o.Contest), user: c.parent(o.User), group: c.parent(o.Group),
			team: c.parent(o.Team), startingTime: c.timep("starting_time", o.StartingTime),
			delayTime: c.durationDef("delay_time", o.DelayTime, 0), extraTime: c.durationDef("extra_time", o.ExtraTime, 0),
			password: o.Password, hidden: c.flag(o.Hidden, false), unrestricted: c.flag(o.Unrestricted, false),
		}
		if o.IP != nil {
			p.ip = make([]netip.Prefix, 0, len(o.IP))
			for _, s := range o.IP {
				n, err := parseNetwork(strings.TrimSpace(s))
				if err != nil {
					c.errorf("ip: invalid network %q", s)
					continue
				}
				p.ip = append(p.ip, n)
			}
		}
		d.participations = append(d.participations, p)
	case "Message":
		d.messages = append(d.messages, &message{
			key: i, participation: c.parent(o.Participation), admin: c.parent(o.Admin),
			timestamp: c.timeReq("timestamp", o.Timestamp), subject: c.required("subject", o.Subject),
			text: c.text(o.Text),
		})
	case "Question":
		d.questions = append(d.questions, &question{
			key: i, participation: c.parent(o.Participation), admin: c.parent(o.Admin),
			questionTimestamp: c.timeReq("question_timestamp", o.QuestionTimestamp),
			subject:           c.required("subject", o.Subject), text: c.text(o.Text),
			replyTimestamp: c.timep("reply_timestamp", o.ReplyTimestamp), ignored: c.flag(o.Ignored, false),
			replySubject: o.ReplySubject, replyText: o.ReplyText,
		})
	case "Task":
		c.name = c.str(o.Name, "")
		d.tasks = append(d.tasks, &task{
			key: i, contest: c.parent(o.Contest), activeDataset: c.parent(o.ActiveDataset),
			num: c.int32p("num", o.Num), name: c.required("name", o.Name), title: c.required("title", o.Title),
			submissionFormat: c.list(o.SubmissionFormat), primaryStatements: c.list(o.PrimaryStatements),
			allowedLanguages: o.AllowedLanguages, tokens: c.tokens(o, importer.TokenModeDisabled),
			maxSubmissionNumber:   c.int32p("max_submission_number", o.MaxSubmissionNumber),
			maxUserTestNumber:     c.int32p("max_user_test_number", o.MaxUserTestNumber),
			minSubmissionInterval: c.durationp("min_submission_interval", o.MinSubmissionInterval),
			minUserTestInterval:   c.durationp("min_user_test_interval", o.MinUserTestInterval),
			feedbackLevel:         c.str(o.FeedbackLevel, importer.FeedbackRestricted),
			scoreMode:             c.str(o.ScoreMode, importer.ScoreModeMaxTokenedLast),
			scorePrecision:        c.int32d("score_precision", o.ScorePrecision, 0),
		})
	case "Statement":
		c.name = c.str(o.Language, "")
		d.statements = append(d.statements, &fileRow{key: i, parent: c.parent(o.Task), dataset: none, result: none,
			name: c.required("language", o.Language), digest: c.digest("digest", o.Digest, required)})
	case "Attachment":
		c.name = c.str(o.Filename, "")
		d.attachments = append(d.attachments, &fileRow{key: i, parent: c.parent(o.Task), dataset: none, result: none,
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, required)})
	case "Dataset":
		c.name = c.str(o.Description, "")
		x := &dataset{
			key: i, task: c.parent(o.Task), description: c.required("description", o.Description),
			autojudge: c.flag(o.Autojudge, false), timeLimit: c.floatp("time_limit", o.TimeLimit),
			memoryLimit: c.int64p("memory_limit", o.MemoryLimit),
			taskType:    c.required("task_type", o.TaskType), scoreType: c.required("score_type", o.ScoreType),
			taskParams: c.jsonValue(o.TaskTypeParameters), scoreParams: c.jsonValue(o.ScoreTypeParameters),
		}
		if x.taskParams == nil {
			c.errorf("no task_type_parameters")
		}
		if x.scoreParams == nil {
			c.errorf("no score_type_parameters")
		}
		d.datasets = append(d.datasets, x)
	case "Manager":
		c.name = c.str(o.Filename, "")
		d.managers = append(d.managers, &fileRow{key: i, parent: c.parent(o.Dataset), dataset: none, result: none,
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, required)})
	case "Testcase":
		c.name = c.str(o.Codename, "")
		d.testcases = append(d.testcases, &testcase{
			key: i, dataset: c.parent(o.Dataset), codename: c.required("codename", o.Codename),
			public: c.flag(o.Public, false), input: c.digest("input", o.Input, required),
			output: c.digest("output", o.Output, required),
		})
	case "Submission":
		s := &submission{
			key: i, participation: c.parent(o.Participation), task: c.parent(o.Task),
			timestamp: c.timeReq("timestamp", o.Timestamp), language: o.Language,
			comment: c.str(o.Comment, ""), official: c.flag(o.Official, true),
		}
		if id, ok := c.int64Of("opaque_id", o.OpaqueID); ok {
			if id < 0 {
				c.errorf("opaque_id: %d is negative", id)
			}
			s.opaqueID = id
		} else if o.OpaqueID == nil {
			s.opaqueID = store.NewOpaqueID()
		}
		d.submissions = append(d.submissions, s)
	case "File":
		c.name = c.str(o.Filename, "")
		d.files = append(d.files, &fileRow{key: i, parent: c.parent(o.Submission), dataset: none, result: none,
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, required)})
	case "Token":
		ts := time.Now().UTC().Truncate(time.Microsecond)
		if o.Timestamp != nil {
			ts = c.timeReq("timestamp", o.Timestamp)
		}
		d.tokens = append(d.tokens, &token{key: i, submission: c.parent(o.Submission), timestamp: ts})
	case "SubmissionResult":
		d.results = append(d.results, &submissionResult{
			key: i, submission: c.parent(o.Submission), dataset: c.parent(o.Dataset),
			compilation: c.compilation(o),
			score:       c.floatp("score", o.Score), scoreDetails: c.jsonValue(o.ScoreDetails),
			scoredAt:    c.timep("scored_at", o.ScoredAt),
			publicScore: c.floatp("public_score", o.PublicScore), publicDetails: c.jsonValue(o.PublicScoreDetails),
			rankingDetails: o.RankingScoreDetails,
		})
	case "Executable":
		c.name = c.str(o.Filename, "")
		d.executables = append(d.executables, &fileRow{key: i, parent: c.parent(o.Submission),
			dataset: c.parent(o.Dataset), result: c.parent(o.SubmissionResult),
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, generated)})
	case "Evaluation":
		d.evaluations = append(d.evaluations, &evaluation{
			key: i, submission: c.parent(o.Submission), dataset: c.parent(o.Dataset), testcase: c.parent(o.Testcase),
			result: c.parent(o.SubmissionResult), outcome: o.Outcome, text: c.textList(o.Text),
			adminText: o.AdminText, execution: c.execution(o),
		})
	case "UserTest":
		d.userTests = append(d.userTests, &userTest{
			key: i, participation: c.parent(o.Participation), task: c.parent(o.Task),
			timestamp: c.timeReq("timestamp", o.Timestamp), language: o.Language,
			input: c.digest("input", o.Input, required),
		})
	case "UserTestFile":
		c.name = c.str(o.Filename, "")
		d.userTestFiles = append(d.userTestFiles, &fileRow{key: i, parent: c.parent(o.UserTest), dataset: none, result: none,
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, required)})
	case "UserTestManager":
		c.name = c.str(o.Filename, "")
		d.userTestManager = append(d.userTestManager, &fileRow{key: i, parent: c.parent(o.UserTest), dataset: none, result: none,
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, required)})
	case "UserTestResult":
		r := &userTestResult{
			key: i, userTest: c.parent(o.UserTest), dataset: c.parent(o.Dataset),
			compilation: c.compilation(o), evaluationText: c.list(o.EvaluationText), execution: c.execution(o),
		}
		if o.Output != nil {
			out := c.digest("output", o.Output, generated)
			r.output = &out
		}
		d.userTestResults = append(d.userTestResults, r)
	case "UserTestExecutable":
		c.name = c.str(o.Filename, "")
		d.userTestExecs = append(d.userTestExecs, &fileRow{key: i, parent: c.parent(o.UserTest),
			dataset: c.parent(o.Dataset), result: c.parent(o.UserTestResult),
			name: c.required("filename", o.Filename), digest: c.digest("digest", o.Digest, generated)})
	default:
		d.unknown[o.Class]++
		d.skip[i] = true
	}
}

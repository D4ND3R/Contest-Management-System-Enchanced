// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"strings"
	"time"
)

// Values of the enumerated columns of CMS (cms/__init__.py,
// cmscommon/constants.py).
const (
	TokenModeDisabled = "disabled"
	TokenModeFinite   = "finite"
	TokenModeInfinite = "infinite"

	FeedbackFull         = "full"
	FeedbackRestricted   = "restricted"
	FeedbackOIRestricted = "oi_restricted"

	ScoreModeMax            = "max"
	ScoreModeMaxSubtask     = "max_subtask"
	ScoreModeMaxTokenedLast = "max_tokened_last"
)

// DefaultContestLanguages are the languages of a contest that does not
// list any (upstream Contest.languages default).
var DefaultContestLanguages = []string{"C11 / gcc", "C++20 / g++", "Pascal / fpc"}

// The default times of a group (upstream Group defaults).
var (
	DefaultGroupStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	DefaultGroupStop  = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Tokens are the token rules of a contest or of a task (the token_*
// columns of CMS).
type Tokens struct {
	Mode        string
	MaxNumber   *int32
	MinInterval time.Duration
	GenInitial  int32
	GenNumber   int32
	GenInterval time.Duration
	GenMax      *int32
}

// defaultTokens are the upstream defaults with the given mode (contests
// default to infinite, tasks to disabled).
func defaultTokens(mode string) Tokens {
	return Tokens{Mode: mode, GenInitial: 2, GenNumber: 2, GenInterval: 30 * time.Minute}
}

// Contest is a contest as a loader reads it (upstream
// ContestLoader.get_contest): the columns of the contests table, its
// groups, and what the importer needs to link it to tasks and people.
// Build it with NewContest, which fills the defaults of CMS: the writer
// stores every field, so a field the format does not have must keep its
// default (an update overwrites the contest with them, like upstream
// update_contest).
type Contest struct {
	Name                                        string
	Description                                 string
	AllowedLocalizations                        []string
	Languages                                   []string
	SubmissionsDownloadAllowed                  bool
	AllowQuestions                              bool
	AllowUserTests                              bool
	AllowUnofficialSubmissionBeforeAnalysisMode bool
	BlockHiddenParticipations                   bool
	AllowPasswordAuthentication                 bool
	AllowRegistration                           bool
	IPRestriction                               bool
	IPAutologin                                 bool
	Tokens                                      Tokens
	Timezone                                    *string
	MaxSubmissionNumber                         *int32
	MaxUserTestNumber                           *int32
	MinSubmissionInterval                       *time.Duration
	MinSubmissionIntervalGracePeriod            *time.Duration
	MinUserTestInterval                         *time.Duration
	ScorePrecision                              int32

	// Groups of participants with their times; at least one.
	Groups []Group
	// MainGroup is the name of the group of the participations that do
	// not name one ("" means the first group).
	MainGroup string
	// Tasks are the names of the tasks, in their order in the contest.
	// The importer loads them with ContestLoader.TaskLoader when it needs
	// them (missing tasks with -i, all of them with -U).
	Tasks []string
	// Participations of the contest. Their users (and teams) must exist,
	// or be in Users (and Teams) when the import creates them (--users).
	Participations []Participation
	// Users and Teams described by the contest package, if the format has
	// them (italy_yaml keeps the users in contest.yaml).
	Users []User
	Teams []Team
}

// NewContest returns a contest with the defaults of CMS and no group.
func NewContest(name string) *Contest {
	return &Contest{
		Name:                        name,
		Description:                 name,
		AllowedLocalizations:        []string{},
		Languages:                   append([]string(nil), DefaultContestLanguages...),
		SubmissionsDownloadAllowed:  true,
		AllowQuestions:              true,
		AllowUserTests:              true,
		AllowPasswordAuthentication: true,
		IPRestriction:               true,
		Tokens:                      defaultTokens(TokenModeInfinite),
	}
}

// Group returns the group called name, or nil.
func (c *Contest) Group(name string) *Group {
	for i := range c.Groups {
		if c.Groups[i].Name == name {
			return &c.Groups[i]
		}
	}
	return nil
}

// MainGroupName returns the name of the main group: MainGroup, or else
// the first group.
func (c *Contest) MainGroupName() string {
	if c.MainGroup != "" || len(c.Groups) == 0 {
		return c.MainGroup
	}
	return c.Groups[0].Name
}

// Group is a group of participants of a contest with its own times.
type Group struct {
	Name            string
	Start, Stop     time.Time
	AnalysisEnabled bool
	AnalysisStart   time.Time
	AnalysisStop    time.Time
	// PerUserTime is the length of the personal window of each
	// participant (USACO-like contests), nil if none.
	PerUserTime *time.Duration
}

// NewGroup returns a group with the default times of CMS.
func NewGroup(name string) Group {
	return Group{
		Name: name, Start: DefaultGroupStart, Stop: DefaultGroupStop,
		AnalysisStart: DefaultGroupStop, AnalysisStop: DefaultGroupStop,
	}
}

// Participation is a user taking part in the contest. Like the
// dictionaries of upstream loaders, only the fields that are set change an
// existing participation; Group is always set (to the main group when
// empty), as upstream ImportContest does.
type Participation struct {
	Username string
	// Group is the name of the group; "" is the main group.
	Group string
	// Team is the code of the team; a pointer to "" removes the team.
	Team *string
	// Password overrides the password of the user in this contest
	// ("<method>:<payload>", see BuildPassword); a pointer to "" removes
	// it (NULL).
	Password *string
	// IP are the networks the participant may connect from (nil: not
	// given).
	IP           []netip.Prefix
	Hidden       *bool
	Unrestricted *bool
	DelayTime    *time.Duration
	ExtraTime    *time.Duration
}

// User is a person who can take part in contests.
type User struct {
	Username  string
	FirstName string
	LastName  string
	// Password is "<method>:<payload>" (BuildPassword); "" gives a new
	// user a random plaintext password, as CMS does, and leaves the
	// password of an existing user alone.
	Password           string
	Email              *string
	Timezone           *string
	PreferredLanguages []string
}

// Team is a team (only shown in rankings).
type Team struct {
	Code string
	Name string
}

// Task is a task as a loader reads it (upstream TaskLoader.get_task): the
// columns of the tasks table with its statements, attachments and
// datasets. Build it with NewTask, which fills the defaults of CMS: the
// writer stores every field, and an update overwrites the task with them
// (upstream update_task).
type Task struct {
	Name             string
	Title            string
	SubmissionFormat []string
	// PrimaryStatements are the languages of the statements highlighted to
	// every participant.
	PrimaryStatements []string
	// AllowedLanguages restricts the languages of the contest for this
	// task; nil allows all of them.
	AllowedLanguages      []string
	Tokens                Tokens
	MaxSubmissionNumber   *int32
	MaxUserTestNumber     *int32
	MinSubmissionInterval *time.Duration
	MinUserTestInterval   *time.Duration
	FeedbackLevel         string
	ScorePrecision        int32
	ScoreMode             string
	Statements            []Statement
	Attachments           []Attachment
	Datasets              []*Dataset
	// ActiveDataset is the description of the dataset used for scoring
	// when the task is created ("" means the first one). Updating a task
	// never changes its active dataset, as upstream.
	ActiveDataset string
}

// NewTask returns a task with the defaults of CMS and no dataset.
func NewTask(name, title string) *Task {
	return &Task{
		Name:              name,
		Title:             title,
		SubmissionFormat:  []string{},
		PrimaryStatements: []string{},
		Tokens:            defaultTokens(TokenModeDisabled),
		FeedbackLevel:     FeedbackRestricted,
		ScoreMode:         ScoreModeMaxTokenedLast,
	}
}

// Dataset returns the dataset with that description, or nil.
func (t *Task) Dataset(description string) *Dataset {
	for _, d := range t.Datasets {
		if d.Description == description {
			return d
		}
	}
	return nil
}

// Active returns the dataset ActiveDataset names (the first dataset when
// it is empty), or nil.
func (t *Task) Active() *Dataset {
	if t.ActiveDataset == "" {
		if len(t.Datasets) == 0 {
			return nil
		}
		return t.Datasets[0]
	}
	return t.Dataset(t.ActiveDataset)
}

// Files returns every file of the task (statements, attachments,
// managers, testcases), in that order.
func (t *Task) Files() []*File {
	var out []*File
	for _, s := range t.Statements {
		out = append(out, s.File)
	}
	for _, a := range t.Attachments {
		out = append(out, a.File)
	}
	for _, d := range t.Datasets {
		for _, m := range d.Managers {
			out = append(out, m.File)
		}
		for _, tc := range d.Testcases {
			out = append(out, tc.Input, tc.Output)
		}
	}
	return out
}

// Statement is the statement of a task in one language.
type Statement struct {
	// Language is a code such as "en" or "es_MX" (LanguageCodes maps the
	// names some formats use).
	Language string
	File     *File
}

// Attachment is a file given to the contestants with the statement.
type Attachment struct {
	Filename string
	File     *File
}

// Dataset is a version of the judging data of a task.
type Dataset struct {
	Description string
	// Autojudge: judged in the background together with the active
	// dataset.
	Autojudge bool
	// TimeLimit is in seconds, MemoryLimit in bytes (a multiple of MiB);
	// nil means none.
	TimeLimit   *float64
	MemoryLimit *int64
	// TaskType and ScoreType are names of CMS (Batch, GroupMin...) and
	// their parameters the JSON stored in datasets.task_type_parameters
	// and score_type_parameters.
	TaskType            string
	TaskTypeParameters  json.RawMessage
	ScoreType           string
	ScoreTypeParameters json.RawMessage
	Managers            []Manager
	Testcases           []Testcase
}

// NewDataset returns a dataset without limits, managers nor testcases.
func NewDataset(description string) *Dataset {
	return &Dataset{Description: description}
}

// Manager is a file needed to compile or evaluate (grader, checker,
// manager, header).
type Manager struct {
	Filename string
	File     *File
}

// Testcase is one testcase of a dataset.
type Testcase struct {
	Codename string
	// Public: its outcome is shown without playing a token.
	Public bool
	Input  *File
	Output *File
}

// MiB is the unit of memory limits.
const MiB = 1 << 20

// Ptr returns a pointer to v (for the optional fields of the model).
func Ptr[T any](v T) *T { return &v }

// Password methods of CMS (cmscommon/crypto.py).
const (
	PasswordPlaintext = "plaintext"
	PasswordBcrypt    = "bcrypt"
)

// BuildPassword returns the stored form of a password: "<method>:<payload>"
// (upstream build_password; the payload of bcrypt is the hash).
func BuildPassword(payload, method string) string {
	return method + ":" + payload
}

// ParseAuthentication splits a stored password into method and payload
// (upstream parse_authentication) and checks the method.
func ParseAuthentication(s string) (method, payload string, err error) {
	method, payload, ok := strings.Cut(s, ":")
	if !ok {
		// The password itself is never shown.
		return "", "", errors.New("the password is not <method>:<password> (plaintext:… or bcrypt:$2b$…)")
	}
	switch method {
	case PasswordPlaintext:
	case PasswordBcrypt:
		if !validBcrypt(payload) {
			return "", "", fmt.Errorf("the bcrypt password is not a bcrypt hash ($2b$12$ followed by 53 characters)")
		}
	default:
		// Not even the method: a password with a colon would show its start.
		return "", "", fmt.Errorf("unknown password method (want %s:… or %s:…)", PasswordPlaintext, PasswordBcrypt)
	}
	return method, payload, nil
}

// validBcrypt reports whether s looks like a bcrypt hash:
// $2a$, $2b$ or $2y$, a two-digit cost and 53 characters of salt and hash.
func validBcrypt(s string) bool {
	if len(s) != 60 || s[0] != '$' || s[1] != '2' || s[3] != '$' || s[6] != '$' {
		return false
	}
	if s[2] != 'a' && s[2] != 'b' && s[2] != 'y' {
		return false
	}
	if s[4] < '0' || s[4] > '9' || s[5] < '0' || s[5] > '9' {
		return false
	}
	for _, c := range s[7:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '/') {
			return false
		}
	}
	return true
}

// RandomPassword returns the password CMS gives users created without one:
// "plaintext:" and six random lowercase letters (upstream
// generate_random_password).
func RandomPassword() string {
	var b [6]byte
	for i := range b {
		b[i] = byte('a' + rand.IntN(26))
	}
	return BuildPassword(string(b[:]), PasswordPlaintext)
}

// ParseIPNetworks parses a comma-separated list of networks or addresses
// (an address is a /32 or /128 network), as upstream ImportContest does
// with ipaddress.ip_network: host bits must be zero.
func ParseIPNetworks(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		var p netip.Prefix
		if strings.Contains(part, "/") {
			var err error
			if p, err = netip.ParsePrefix(part); err != nil {
				return nil, fmt.Errorf("invalid network %q", part)
			}
		} else {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q", part)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Masked() != p {
			return nil, fmt.Errorf("network %q has host bits set", part)
		}
		out = append(out, p)
	}
	return out, nil
}

// LanguageCodes maps the language names some formats use for statements
// (italy_yaml "<language>.pdf") to their codes (upstream LANGUAGE_MAP of
// cmscontrib/loaders/base_loader.py).
var LanguageCodes = map[string]string{
	"afrikaans": "af", "arabic": "ar", "armenian": "hy", "azerbaijani": "az",
	"belarusian": "be", "bengali": "bn", "bosnian": "bs", "bulgarian": "bg",
	"catalan": "ca", "chinese": "zh", "croatian": "hr", "czech": "cs",
	"danish": "da", "dutch": "nl", "english": "en", "estonian": "et",
	"filipino": "fil", "finnish": "fi", "french": "fr", "georgian": "ka",
	"german": "de", "greek": "el", "hebrew": "he", "hindi": "hi",
	"hungarian": "hu", "icelandic": "is", "indonesian": "id", "irish": "ga",
	"italian": "it", "japanese": "ja", "kazakh": "kk", "korean": "ko",
	"kyrgyz": "ky", "latvian": "lv", "lithuanian": "lt", "luxembourgish": "lb",
	"macedonian": "mk", "malay": "ms", "mongolian": "mn", "norwegian": "no",
	"persian": "fa", "polish": "pl", "portuguese": "pt", "romanian": "ro",
	"russian": "ru", "serbian": "sr", "sinhala": "si", "slovak": "sk",
	"slovene": "sl", "spanish": "es", "swedish": "sv", "tajik": "tg",
	"tamil": "ta", "thai": "th", "turkish": "tr", "turkmen": "tk",
	"ukrainian": "uk", "urdu": "ur", "uzbek": "uz", "vietnamese": "vi",
	"other": "other",
}

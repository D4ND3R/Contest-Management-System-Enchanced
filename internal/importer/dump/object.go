// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import "encoding/json"

// object is any object of contest.json at data model version 49: the
// union of the columns of every class of cms/db (the keys DumpExporter
// writes: no ids nor foreign keys) and of the relationships this importer
// follows, which are those from a child to its parent (a testcase names
// its dataset; the collections of the parents are not needed). Numbers
// are kept as written (number) and converted with the type of their
// column; missing keys and nulls are nil.
type object struct {
	Class string `json:"_class"`

	// Names and texts.
	Name           *string         `json:"name"`
	Description    *string         `json:"description"`
	Username       *string         `json:"username"`
	Authentication *string         `json:"authentication"`
	FirstName      *string         `json:"first_name"`
	LastName       *string         `json:"last_name"`
	Password       *string         `json:"password"`
	Email          *string         `json:"email"`
	Timezone       *string         `json:"timezone"`
	Code           *string         `json:"code"`
	Title          *string         `json:"title"`
	Subject        *string         `json:"subject"`
	Text           json.RawMessage `json:"text"` // a string, or a list of strings (Evaluation)
	ReplySubject   *string         `json:"reply_subject"`
	ReplyText      *string         `json:"reply_text"`
	Comment        *string         `json:"comment"`
	Language       *string         `json:"language"`
	Codename       *string         `json:"codename"`
	Filename       *string         `json:"filename"`
	Digest         *string         `json:"digest"`
	Input          *string         `json:"input"`
	Output         *string         `json:"output"`
	Outcome        *string         `json:"outcome"`
	AdminText      *string         `json:"admin_text"`

	// Admin.
	Enabled             *bool `json:"enabled"`
	PermissionAll       *bool `json:"permission_all"`
	PermissionMessaging *bool `json:"permission_messaging"`

	// Contest.
	AllowedLocalizations                        []string `json:"allowed_localizations"`
	Languages                                   []string `json:"languages"`
	SubmissionsDownloadAllowed                  *bool    `json:"submissions_download_allowed"`
	AllowQuestions                              *bool    `json:"allow_questions"`
	AllowUserTests                              *bool    `json:"allow_user_tests"`
	AllowUnofficialSubmissionBeforeAnalysisMode *bool    `json:"allow_unofficial_submission_before_analysis_mode"`
	BlockHiddenParticipations                   *bool    `json:"block_hidden_participations"`
	AllowPasswordAuthentication                 *bool    `json:"allow_password_authentication"`
	AllowRegistration                           *bool    `json:"allow_registration"`
	IPRestriction                               *bool    `json:"ip_restriction"`
	IPAutologin                                 *bool    `json:"ip_autologin"`
	MinSubmissionIntervalGracePeriod            *number  `json:"min_submission_interval_grace_period"`
	MainGroup                                   *string  `json:"main_group"`

	// Contest and Task.
	TokenMode             *string `json:"token_mode"`
	TokenMaxNumber        *number `json:"token_max_number"`
	TokenMinInterval      *number `json:"token_min_interval"`
	TokenGenInitial       *number `json:"token_gen_initial"`
	TokenGenNumber        *number `json:"token_gen_number"`
	TokenGenInterval      *number `json:"token_gen_interval"`
	TokenGenMax           *number `json:"token_gen_max"`
	MaxSubmissionNumber   *number `json:"max_submission_number"`
	MaxUserTestNumber     *number `json:"max_user_test_number"`
	MinSubmissionInterval *number `json:"min_submission_interval"`
	MinUserTestInterval   *number `json:"min_user_test_interval"`
	ScorePrecision        *number `json:"score_precision"`

	// Group.
	Start           *number `json:"start"`
	Stop            *number `json:"stop"`
	AnalysisEnabled *bool   `json:"analysis_enabled"`
	AnalysisStart   *number `json:"analysis_start"`
	AnalysisStop    *number `json:"analysis_stop"`
	PerUserTime     *number `json:"per_user_time"`

	// User.
	PreferredLanguages []string `json:"preferred_languages"`

	// Participation.
	IP           []string `json:"ip"`
	StartingTime *number  `json:"starting_time"`
	DelayTime    *number  `json:"delay_time"`
	ExtraTime    *number  `json:"extra_time"`
	Hidden       *bool    `json:"hidden"`
	Unrestricted *bool    `json:"unrestricted"`

	// Announcement, Message, Question, Submission, Token, UserTest.
	Timestamp         *number `json:"timestamp"`
	QuestionTimestamp *number `json:"question_timestamp"`
	ReplyTimestamp    *number `json:"reply_timestamp"`
	Ignored           *bool   `json:"ignored"`

	// Task.
	Num               *number  `json:"num"`
	SubmissionFormat  []string `json:"submission_format"`
	PrimaryStatements []string `json:"primary_statements"`
	AllowedLanguages  []string `json:"allowed_languages"`
	FeedbackLevel     *string  `json:"feedback_level"`
	ScoreMode         *string  `json:"score_mode"`
	ActiveDataset     *string  `json:"active_dataset"`

	// Dataset and Testcase.
	Autojudge           *bool           `json:"autojudge"`
	TimeLimit           *number         `json:"time_limit"`
	MemoryLimit         *number         `json:"memory_limit"`
	TaskType            *string         `json:"task_type"`
	TaskTypeParameters  json.RawMessage `json:"task_type_parameters"`
	ScoreType           *string         `json:"score_type"`
	ScoreTypeParameters json.RawMessage `json:"score_type_parameters"`
	Public              *bool           `json:"public"`

	// Submission.
	OpaqueID *number `json:"opaque_id"`
	Official *bool   `json:"official"`

	// SubmissionResult and UserTestResult.
	CompilationOutcome        *string         `json:"compilation_outcome"`
	CompilationText           []string        `json:"compilation_text"`
	CompilationTries          *number         `json:"compilation_tries"`
	CompilationStdout         *string         `json:"compilation_stdout"`
	CompilationStderr         *string         `json:"compilation_stderr"`
	CompilationTime           *number         `json:"compilation_time"`
	CompilationWallClockTime  *number         `json:"compilation_wall_clock_time"`
	CompilationMemory         *number         `json:"compilation_memory"`
	CompilationShard          *number         `json:"compilation_shard"`
	CompilationSandboxPaths   []string        `json:"compilation_sandbox_paths"`
	CompilationSandboxDigests []string        `json:"compilation_sandbox_digests"`
	EvaluationOutcome         *string         `json:"evaluation_outcome"`
	EvaluationText            []string        `json:"evaluation_text"`
	EvaluationTries           *number         `json:"evaluation_tries"`
	Score                     *number         `json:"score"`
	ScoreDetails              json.RawMessage `json:"score_details"`
	ScoredAt                  *number         `json:"scored_at"`
	PublicScore               *number         `json:"public_score"`
	PublicScoreDetails        json.RawMessage `json:"public_score_details"`
	RankingScoreDetails       []string        `json:"ranking_score_details"`

	// Evaluation and UserTestResult.
	ExecutionTime            *number  `json:"execution_time"`
	ExecutionWallClockTime   *number  `json:"execution_wall_clock_time"`
	ExecutionMemory          *number  `json:"execution_memory"`
	EvaluationShard          *number  `json:"evaluation_shard"`
	EvaluationSandboxPaths   []string `json:"evaluation_sandbox_paths"`
	EvaluationSandboxDigests []string `json:"evaluation_sandbox_digests"`

	// Relationships to the parent objects (keys of other objects).
	Contest          *string `json:"contest"`
	Group            *string `json:"group"`
	User             *string `json:"user"`
	Team             *string `json:"team"`
	Participation    *string `json:"participation"`
	Admin            *string `json:"admin"`
	Task             *string `json:"task"`
	Dataset          *string `json:"dataset"`
	Testcase         *string `json:"testcase"`
	Submission       *string `json:"submission"`
	SubmissionResult *string `json:"submission_result"`
	UserTest         *string `json:"user_test"`
	UserTestResult   *string `json:"user_test_result"`
}

// number is a JSON value kept as written, for a numeric column: the
// conversion reports a value of the wrong type with the name of its field.
type number struct{ raw string }

func (n *number) UnmarshalJSON(b []byte) error {
	n.raw = string(b)
	return nil
}

func (n *number) String() string { return n.raw }

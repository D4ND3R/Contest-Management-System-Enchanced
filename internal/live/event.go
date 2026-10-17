// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"slices"
	"strconv"
	"time"
)

// Version is the version of the event payloads: every payload carries it
// as "v". Adding a field keeps the version; renaming or removing one, or
// changing its meaning, increments it.
const Version = 1

// Topic is what a subscriber listens to. On Valkey, topic t of an
// installation is the channel <key prefix>live:<t>.
type Topic string

// AdminTopic carries the events of every administrator page that are not
// about one contest: judging counters of the whole installation and
// worker changes.
const AdminTopic Topic = "a"

// ParticipationTopic carries what only one contestant (and their open
// pages) may see: their submissions, user tests, task scores, private
// messages and answers.
func ParticipationTopic(participation int64) Topic { return idTopic("p:", participation) }

// ContestTopic carries what every contestant of a contest sees:
// announcements and changes of the contest itself.
func ContestTopic(contest int64) Topic { return idTopic("c:", contest) }

// ScoreboardTopic carries the changes of the scoreboard of a contest.
// Only pages that show the scoreboard subscribe to it.
func ScoreboardTopic(contest int64) Topic { return idTopic("s:", contest) }

// AdminContestTopic carries the administrator events of a contest: every
// submission and user test, questions, messages and announcements.
func AdminContestTopic(contest int64) Topic { return idTopic("a:", contest) }

func idTopic(kind string, id int64) Topic {
	return Topic(kind + strconv.FormatInt(id, 10))
}

// Event names: the "event:" field of the server-sent events. Names are
// lowercase ASCII letters, digits and underscores.
const (
	// EventPing is sent as soon as a stream opens and then every
	// ping interval (PingPayload).
	EventPing = "ping"
	// EventResync tells the page that events were lost: it must reload
	// the state of its live regions (ResyncPayload).
	EventResync = "resync"

	EventSubmission    = "submission"
	EventUserTest      = "user_test"
	EventTaskScore     = "task_score"
	EventAnnouncement  = "announcement"
	EventMessage       = "message"
	EventAnswer        = "answer"
	EventQuestion      = "question"
	EventContest       = "contest"
	EventParticipation = "participation"
	EventScoreboard    = "scoreboard"
	EventJudging       = "judging"
	EventWorker        = "worker"
)

// Event is one live event: an event name, the topics it goes to and a
// payload marshalled as JSON. The constructors (the Event methods of the
// payload types) fill everything; Data must not change after Publish.
type Event struct {
	Name   string
	Topics []Topic
	Data   any
	// Throttle groups events: per (Name, Topics) a Publisher sends at
	// most one every PublisherOptions.GroupInterval, merging the ones in
	// between (scoreboard: union of participations; judging: the latest
	// counters). The first event of a quiet group goes out at once.
	Throttle bool

	at time.Time // when Publish was called (lag metrics)
}

// Submission statuses (store.SubmissionStatus, as CMS
// SubmissionResult.get_status).
const (
	SubmissionCompiling         = "compiling"
	SubmissionCompilationFailed = "compilation_failed"
	SubmissionEvaluating        = "evaluating"
	SubmissionScoring           = "scoring"
	SubmissionScored            = "scored"
)

// User test statuses (CMS UserTestResult.get_status).
const (
	UserTestCompiling         = "compiling"
	UserTestCompilationFailed = "compilation_failed"
	UserTestEvaluating        = "evaluating"
	UserTestEvaluated         = "evaluated"
)

// Submission: a submission was received or its result on a dataset
// changed state. The administrators of the contest always get it; the
// contestant only for the active dataset.
type Submission struct {
	V             int   `json:"v"`
	Contest       int64 `json:"contest"`
	Participation int64 `json:"participation"`
	Task          int64 `json:"task"`
	Dataset       int64 `json:"dataset"`
	ID            int64 `json:"submission"`
	// Opaque is the opaque_id of the contestant URLs. It is a string in
	// JSON: it does not fit in a JavaScript number.
	Opaque int64  `json:"opaque,string,omitempty"`
	Status string `json:"status"`
	// New: the submission was just received (first event).
	New bool `json:"new,omitempty"`
	// ActiveDataset sends the event to the contestant too. Results on
	// other datasets (background judging) only reach the administrators.
	ActiveDataset bool `json:"-"`
}

// Event returns the event "submission".
func (p Submission) Event() Event {
	p.V = Version
	topics := []Topic{AdminContestTopic(p.Contest)}
	if p.ActiveDataset {
		topics = append(topics, ParticipationTopic(p.Participation))
	}
	return Event{Name: EventSubmission, Topics: topics, Data: p}
}

// UserTest: a user test was received or changed state (user tests are
// only run on the active dataset).
type UserTest struct {
	V             int    `json:"v"`
	Contest       int64  `json:"contest"`
	Participation int64  `json:"participation"`
	Task          int64  `json:"task"`
	ID            int64  `json:"user_test"`
	Status        string `json:"status"`
	New           bool   `json:"new,omitempty"`
}

// Event returns the event "user_test" for the contestant and the
// administrators.
func (p UserTest) Event() Event {
	p.V = Version
	return Event{Name: EventUserTest, Data: p,
		Topics: []Topic{ParticipationTopic(p.Participation), AdminContestTopic(p.Contest)}}
}

// TaskScore: the score of a contestant on a task changed. The page
// fetches the new score (which part it may show depends on tokens and
// the contest phase).
type TaskScore struct {
	V             int   `json:"v"`
	Contest       int64 `json:"contest"`
	Participation int64 `json:"participation"`
	Task          int64 `json:"task"`
}

// Event returns the event "task_score" for the contestant.
func (p TaskScore) Event() Event {
	p.V = Version
	return Event{Name: EventTaskScore, Data: p, Topics: []Topic{ParticipationTopic(p.Participation)}}
}

// Announcement: an announcement was published in a contest.
type Announcement struct {
	V       int   `json:"v"`
	Contest int64 `json:"contest"`
	ID      int64 `json:"announcement"`
}

// Event returns the event "announcement" for every contestant and the
// administrators of the contest.
func (p Announcement) Event() Event {
	p.V = Version
	return Event{Name: EventAnnouncement, Data: p,
		Topics: []Topic{ContestTopic(p.Contest), AdminContestTopic(p.Contest)}}
}

// PrivateMessage: an administrator sent a private message to a
// contestant.
type PrivateMessage struct {
	V             int   `json:"v"`
	Contest       int64 `json:"contest"`
	Participation int64 `json:"participation"`
	ID            int64 `json:"message"`
}

// Event returns the event "message" for the contestant and the
// administrators of the contest.
func (p PrivateMessage) Event() Event {
	p.V = Version
	return Event{Name: EventMessage, Data: p,
		Topics: []Topic{ParticipationTopic(p.Participation), AdminContestTopic(p.Contest)}}
}

// Answer: a question of the contestant was answered.
type Answer struct {
	V             int   `json:"v"`
	Contest       int64 `json:"contest"`
	Participation int64 `json:"participation"`
	Question      int64 `json:"question"`
}

// Event returns the event "answer" for the contestant. The admin site
// publishes Question{Change: QuestionAnswered} for the other
// administrators along with it.
func (p Answer) Event() Event {
	p.V = Version
	return Event{Name: EventAnswer, Data: p, Topics: []Topic{ParticipationTopic(p.Participation)}}
}

// Question changes.
const (
	QuestionNew      = "new"
	QuestionAnswered = "answered"
	QuestionIgnored  = "ignored"
	// QuestionClaimed and QuestionReleased: an administrator took the
	// question or gave it back (R6.3).
	QuestionClaimed  = "claimed"
	QuestionReleased = "released"
)

// Question: a question was asked or changed state (administrators).
type Question struct {
	V             int    `json:"v"`
	Contest       int64  `json:"contest"`
	Participation int64  `json:"participation"`
	ID            int64  `json:"question"`
	Change        string `json:"change"`
}

// Event returns the event "question" for the administrators of the
// contest.
func (p Question) Event() Event {
	p.V = Version
	return Event{Name: EventQuestion, Data: p, Topics: []Topic{AdminContestTopic(p.Contest)}}
}

// ContestChange: the settings of a contest changed (times, tasks,
// limits...). Pages reload what depends on them (the timer, the task
// list).
type ContestChange struct {
	V       int   `json:"v"`
	Contest int64 `json:"contest"`
}

// Event returns the event "contest" for every contestant and the
// administrators of the contest.
func (p ContestChange) Event() Event {
	p.V = Version
	return Event{Name: EventContest, Data: p,
		Topics: []Topic{ContestTopic(p.Contest), AdminContestTopic(p.Contest)}}
}

// ParticipationChange: the settings of one participation changed (extra
// time, delay, starting time, hidden, unrestricted...). The contestant's
// pages reload what depends on them (the timer, the task list).
type ParticipationChange struct {
	V             int   `json:"v"`
	Contest       int64 `json:"contest"`
	Participation int64 `json:"participation"`
}

// Event returns the event "participation" for the contestant and the
// administrators of the contest.
func (p ParticipationChange) Event() Event {
	p.V = Version
	return Event{Name: EventParticipation, Data: p,
		Topics: []Topic{ParticipationTopic(p.Participation), AdminContestTopic(p.Contest)}}
}

// MaxScoreboardIDs bounds the participations listed in one scoreboard
// event; beyond it the event says All.
const MaxScoreboardIDs = 256

// ScoreboardChange: the scores of some participations changed. Events
// carry ids, never scores: the page fetches the rows it may show.
type ScoreboardChange struct {
	V              int     `json:"v"`
	Contest        int64   `json:"contest"`
	Participations []int64 `json:"participations,omitempty"`
	// All: too many rows changed (or unknown which); reload the whole
	// scoreboard.
	All bool `json:"all,omitempty"`
}

// Event returns the throttled event "scoreboard" (at most one per
// second per contest, merging the participations).
func (p ScoreboardChange) Event() Event {
	p.V = Version
	p.Participations = mergeIDs(nil, p.Participations)
	if len(p.Participations) > MaxScoreboardIDs {
		p.All = true
	}
	if p.All {
		p.Participations = nil
	}
	return Event{Name: EventScoreboard, Data: p, Topics: []Topic{ScoreboardTopic(p.Contest)}, Throttle: true}
}

func (p ScoreboardChange) merge(newer any) any {
	n, ok := newer.(ScoreboardChange)
	if !ok || n.Contest != p.Contest {
		return newer
	}
	if p.All || n.All {
		n.All, n.Participations = true, nil
		return n
	}
	n.Participations = mergeIDs(p.Participations, n.Participations)
	if len(n.Participations) > MaxScoreboardIDs {
		n.All, n.Participations = true, nil
	}
	return n
}

// mergeIDs returns the sorted union of a and b without duplicates, in a
// new slice.
func mergeIDs(a, b []int64) []int64 {
	out := make([]int64, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// Recommended keys of Judging.Counters (the admin pages show the ones
// they know).
const (
	CounterQueued     = "queued"     // jobs waiting in the queue
	CounterInFlight   = "in_flight"  // jobs taken by a worker
	CounterRetrying   = "retrying"   // jobs waiting for a retry
	CounterDead       = "dead"       // dead-lettered jobs
	CounterCompiling  = "compiling"  // submissions without a compilation outcome
	CounterEvaluating = "evaluating" // compiled, not evaluated
	CounterScoring    = "scoring"    // evaluated, not scored
	CounterWorkers    = "workers"    // live workers
)

// Judging is a snapshot of the judging progress counters.
type Judging struct {
	V int `json:"v"`
	// Contest is 0 for the counters of the whole installation.
	Contest  int64            `json:"contest,omitempty"`
	Counters map[string]int64 `json:"counters"`
}

// Event returns the throttled event "judging" (the latest snapshot at
// most once per second) for the administrators: of the contest, or of
// every page (AdminTopic) when Contest is 0.
func (p Judging) Event() Event {
	p.V = Version
	topic := AdminTopic
	if p.Contest != 0 {
		topic = AdminContestTopic(p.Contest)
	}
	return Event{Name: EventJudging, Data: p, Topics: []Topic{topic}, Throttle: true}
}

// Worker states.
const (
	WorkerUp       = "up"
	WorkerDown     = "down"
	WorkerDraining = "draining"
	// WorkerBroken: the worker runs but takes no jobs (its sandbox does
	// not work).
	WorkerBroken = "broken"
	// WorkerDisabled: an administrator disabled the worker: it finishes
	// its jobs and takes no new ones.
	WorkerDisabled = "disabled"
)

// Worker: a worker appeared, stopped or changed state.
type Worker struct {
	V     int    `json:"v"`
	Name  string `json:"worker"`
	State string `json:"state"`
}

// Event returns the event "worker" for every administrator page.
func (p Worker) Event() Event {
	p.V = Version
	return Event{Name: EventWorker, Data: p, Topics: []Topic{AdminTopic}}
}

// PingPayload is the data of EventPing.
type PingPayload struct {
	V int `json:"v"`
	// Interval is the ping interval in milliseconds: the page counts the
	// connection as late after 2 intervals without a ping and as lost
	// after 3.
	Interval int64 `json:"interval"`
	// Live is false while this server cannot receive events (its Valkey
	// subscription is down): the page polls until a ping says true again
	// (a resync comes first).
	Live bool `json:"live"`
	// Time is the server clock in Unix milliseconds (contest timers).
	Time int64 `json:"time"`
	// Spread is in milliseconds: pages spread over a random delay below
	// it the requests they all make on a broadcast event (contestweb's
	// notifications on an announcement), so that they do not arrive at
	// once. ResyncSpread for the server's open streams.
	Spread int64 `json:"spread"`
}

// Resync reasons.
const (
	// ReasonOverflow: the page did not keep up and events were dropped.
	ReasonOverflow = "overflow"
	// ReasonReconnect: the server reconnected to Valkey.
	ReasonReconnect = "reconnect"
	// ReasonGap: Last-Event-ID is unknown, too old, or events were lost
	// on the way.
	ReasonGap = "gap"
)

// ResyncPayload is the data of EventResync.
type ResyncPayload struct {
	V      int    `json:"v"`
	Reason string `json:"reason"`
	// Spread is in milliseconds: the page reloads its live regions after
	// a random delay below it, so that the pages of a server that
	// reconnected (or restarted) do not all reload at once. It grows with
	// the number of open streams (ResyncSpread).
	Spread int64 `json:"spread"`
}

// Resync spread: MinResyncSpread, or ResyncSpreadPerStream for every open
// stream of the hub when every page may get the marker at once (reasons
// reconnect and gap), up to MaxResyncSpread. 5 ms per stream keeps the
// reloads after a reconnection of 1500 pages around 200 per second (7.5 s).
const (
	MinResyncSpread       = 2 * time.Second
	ResyncSpreadPerStream = 5 * time.Millisecond
	MaxResyncSpread       = 30 * time.Second
)

// ResyncSpread returns the spread of a resync marker for a hub with
// streams open streams.
func ResyncSpread(reason string, streams int) time.Duration {
	if reason == ReasonOverflow {
		// One page that did not keep up.
		return MinResyncSpread
	}
	return min(max(MinResyncSpread, time.Duration(streams)*ResyncSpreadPerStream), MaxResyncSpread)
}

// validName reports whether s can be an event name.
func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// validTopic reports whether t can be a topic (no glob characters, no
// spaces).
func validTopic(t Topic) bool {
	if t == "" || len(t) > 128 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c <= ' ' || c == 0x7f || c == '*' || c == '?' || c == '[' || c == ']' || c == '\\' {
			return false
		}
	}
	return true
}

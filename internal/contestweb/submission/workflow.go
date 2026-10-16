// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/submission/workflow.py and utils.py of
// CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015-2016 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package submission

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"slices"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
)

// Unacceptable is a submission or a user test the site refuses (CMS
// UnacceptableSubmission and UnacceptableUserTest): Subject and Text are
// msgids of the "cms" catalogs, Text formatted with Params (Python's %).
// The pages notify the contestant with them, translated; the API answers
// with the English ones (FormattedText).
type Unacceptable struct {
	Subject string
	Text    string
	Params  []any
	// Err is the cause, for the logs (the storage failure...).
	Err error
}

func (e *Unacceptable) Error() string {
	s := e.Subject + " - " + e.FormattedText()
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *Unacceptable) Unwrap() error { return e.Err }

// FormattedText is Text formatted with Params, in English (CMS
// formatted_text).
func (e *Unacceptable) FormattedText() string {
	if len(e.Params) == 0 {
		return e.Text
	}
	return pyPercent(e.Text, e.Params...)
}

// pyPercent formats the %d and %s placeholders of the messages below as
// Python's % does (a float for %d is truncated: CMS passes
// total_seconds()).
func pyPercent(msg string, args ...any) string {
	var b strings.Builder
	i := 0
	for j := 0; j < len(msg); j++ {
		c := msg[j]
		if c != '%' || j+1 >= len(msg) {
			b.WriteByte(c)
			continue
		}
		j++
		switch msg[j] {
		case '%':
			b.WriteByte('%')
		case 'd', 's':
			if i >= len(args) {
				b.WriteByte('%')
				b.WriteByte(msg[j])
				continue
			}
			a := args[i]
			i++
			if msg[j] == 'd' {
				switch v := a.(type) {
				case float64:
					a = int64(v)
				case int32:
					a = int64(v)
				}
			}
			fmt.Fprint(&b, a)
		default:
			b.WriteByte('%')
			b.WriteByte(msg[j])
		}
	}
	return b.String()
}

func unacceptable(subject, text string, params ...any) *Unacceptable {
	return &Unacceptable{Subject: subject, Text: text, Params: params}
}

// The messages of CMS (msgids of the "cms" catalogs).
const (
	MsgTooManySubmissions      = "Too many submissions!"
	MsgMaxSubmissionsContest   = "You have reached the maximum limit of at most %d submissions among all tasks."
	MsgMaxSubmissionsTask      = "You have reached the maximum limit of at most %d submissions on this task."
	MsgSubmissionsTooFrequent  = "Submissions too frequent!"
	MsgMinIntervalContest      = "Among all tasks, you can submit again after %d seconds from last submission."
	MsgMinIntervalTask         = "For this task, you can submit again after %d seconds from last submission."
	MsgSubmissionTooBig        = "Submission too big!"
	MsgSourceTooBig            = "Each source file must be at most %d bytes long."
	MsgTooManyFiles            = "The submission should contain at most %d files."
	MsgInvalidArchive          = "Invalid archive format!"
	MsgArchiveNotOpened        = "The submitted archive could not be opened."
	MsgInvalidSubmissionFormat = "Invalid submission format!"
	MsgSelectCorrectFiles      = "Please select the correct files."
	MsgSubmissionStorageFailed = "Submission storage failed!"
	MsgTryAgain                = "Please try again."
	MsgTooManyTests            = "Too many tests!"
	MsgMaxTestsContest         = "You have reached the maximum limit of at most %d tests among all tasks."
	MsgMaxTestsTask            = "You have reached the maximum limit of at most %d tests on this task."
	MsgTestsTooFrequent        = "Tests too frequent!"
	MsgTestMinIntervalContest  = "Among all tasks, you can test again after %d seconds from last test."
	MsgTestMinIntervalTask     = "For this task, you can test again after %d seconds from last test."
	MsgInvalidTestFormat       = "Invalid test format!"
	MsgTestTooBig              = "Test too big!"
	MsgInputTooBig             = "Input too big!"
	MsgInputMaxLength          = "The input file must be at most %d bytes long."
	MsgTestStorageFailed       = "Test storage failed!"
	MsgSubmissionReceived      = "Submission received"
	MsgSubmissionReceivedText  = "Your submission has been received and is currently being evaluated."
	MsgTestReceived            = "Test received"
	MsgTestReceivedText        = "Your test has been received and is currently being executed."
	userTestInputCodename      = "input"
	submissionComment          = ","
	maxArchiveFilesPerCodename = 2
	defaultMaxSubmissionLength = 100_000
	defaultMaxInputLength      = 5_000_000
)

// ErrTestingNotAllowed: the task type cannot run user tests (CMS
// TestingNotAllowed; the page answers 404).
var ErrTestingNotAllowed = errors.New("submission: the task does not allow tests")

// Rules are the limits of the contest and of the task on submissions
// (or on user tests) for a participant.
type Rules struct {
	ContestMaxNumber, TaskMaxNumber     *int32
	ContestMinInterval, TaskMinInterval *time.Duration
	// Unrestricted participants have no limits.
	Unrestricted bool
	// LastMinutes: the participant is in the grace period at the end of
	// their time (IsLastMinutes), when the minimum intervals between
	// submissions do not apply (not for user tests).
	LastMinutes bool
}

// CheckSubmission applies the limits on submissions in CMS's order
// (accept_submission): the number on the contest and on the task (unless
// overrideMax, an administrator through the API), then the intervals
// (unless overrideMin or in the grace period). nil if the participant
// may submit.
func CheckSubmission(r Rules, st Stats, now time.Time, overrideMax, overrideMin bool) *Unacceptable {
	if !overrideMax {
		if !CheckMaxNumber(r.ContestMaxNumber, st.ContestCount, r.Unrestricted) {
			return unacceptable(MsgTooManySubmissions, MsgMaxSubmissionsContest, *r.ContestMaxNumber)
		}
		if !CheckMaxNumber(r.TaskMaxNumber, st.TaskCount, r.Unrestricted) {
			return unacceptable(MsgTooManySubmissions, MsgMaxSubmissionsTask, *r.TaskMaxNumber)
		}
	}
	if !overrideMin && !r.LastMinutes {
		if !CheckMinInterval(r.ContestMinInterval, now, st.ContestLast, r.Unrestricted) {
			return unacceptable(MsgSubmissionsTooFrequent, MsgMinIntervalContest, r.ContestMinInterval.Seconds())
		}
		if !CheckMinInterval(r.TaskMinInterval, now, st.TaskLast, r.Unrestricted) {
			return unacceptable(MsgSubmissionsTooFrequent, MsgMinIntervalTask, r.TaskMinInterval.Seconds())
		}
	}
	return nil
}

// CheckUserTest applies the limits on user tests (accept_user_test).
func CheckUserTest(r Rules, st Stats, now time.Time) *Unacceptable {
	if !CheckMaxNumber(r.ContestMaxNumber, st.ContestCount, r.Unrestricted) {
		return unacceptable(MsgTooManyTests, MsgMaxTestsContest, *r.ContestMaxNumber)
	}
	if !CheckMaxNumber(r.TaskMaxNumber, st.TaskCount, r.Unrestricted) {
		return unacceptable(MsgTooManyTests, MsgMaxTestsTask, *r.TaskMaxNumber)
	}
	if !CheckMinInterval(r.ContestMinInterval, now, st.ContestLast, r.Unrestricted) {
		return unacceptable(MsgTestsTooFrequent, MsgTestMinIntervalContest, r.ContestMinInterval.Seconds())
	}
	if !CheckMinInterval(r.TaskMinInterval, now, st.TaskLast, r.Unrestricted) {
		return unacceptable(MsgTestsTooFrequent, MsgTestMinIntervalTask, r.TaskMinInterval.Seconds())
	}
	return nil
}

// TaskType is what the workflow needs of the task type of the active
// dataset (tasktype.TaskType implements it).
type TaskType interface {
	Testable() bool
	AllowPartialSubmission() bool
	ReusePreviousSubmission() bool
	UserManagers() []string
}

// Previous is the latest submission or user test of the participant on
// the task (CMS get_latest_submission), whose files fill in the ones a
// partial submission lacks.
type Previous struct {
	// Language is the language of the previous submission (nil: none).
	Language *string
	// Files are its files by codename ("foo.%l").
	Files map[string]string
	// Input and Managers (by file name, "grader.c") of a user test.
	Input    string
	Managers map[string]string
}

// PreviousDigests returns the digests of the codenames taken from the
// previous submission (CMS fetch_file_digests_from_previous_submission):
// nothing if there is none or it is in another language; for user tests
// also the input and the managers (stored with the language's
// extension).
func PreviousDigests(prev *Previous, lang *language.Language, codenames []string, userTest bool) map[string]string {
	out := map[string]string{}
	if prev == nil {
		return out
	}
	if lang == nil && prev.Language != nil || lang != nil && (prev.Language == nil || *prev.Language != lang.Name) {
		return out
	}
	for _, codename := range codenames {
		if d, ok := prev.Files[codename]; ok {
			out[codename] = d
			continue
		}
		if !userTest {
			continue
		}
		if codename == userTestInputCodename {
			if prev.Input != "" {
				out[userTestInputCodename] = prev.Input
			}
			continue
		}
		filename := codename
		if strings.HasSuffix(codename, langSuffix) && lang != nil {
			filename = pySplitextBase(codename) + lang.SourceExtension()
		}
		if d, ok := prev.Managers[filename]; ok {
			out[codename] = d
		}
	}
	return out
}

// Store is what the workflow needs of the blob store.
type Store interface {
	PutBytes(ctx context.Context, b []byte) (string, error)
}

// Request is a submission or a user test sent by a contestant: the files
// of the request and what the task says about them.
type Request struct {
	// Form holds the files of the request (multipart.Form.File).
	Form map[string][]*multipart.FileHeader
	// Language chosen by the contestant (nil: guess it).
	Language *string
	// SubmissionFormat of the task; AllowedLanguages of the task (or of
	// the contest); AnyLanguage: no limit on languages.
	SubmissionFormat []string
	AllowedLanguages []string
	AnyLanguage      bool
	// TaskType of the active dataset.
	TaskType TaskType
	// MaxSubmissionLength (each source file) and MaxInputLength (the
	// input of a user test) in bytes (contest_web.max_submission_length
	// and max_input_length; 0: CMS's defaults).
	MaxSubmissionLength, MaxInputLength int64
	Languages                           Languages
	// Previous loads the latest submission (or user test) of the
	// participant on the task; called only when files are missing and
	// the task type reuses them. Nil: there is none.
	Previous func(ctx context.Context) (*Previous, error)
	// Store receives the files.
	Store Store
}

// Accepted is a submission or user test ready to be inserted: its files
// are in the store.
type Accepted struct {
	// Language of the files (nil: the format has no language).
	Language *language.Language
	// Files are the digests of the submission files by codename
	// ("foo.%l"), including those taken from the previous submission.
	Files map[string]string
	// Input is the digest of the input of a user test; Managers the
	// digests of the managers it brought, by file name ("grader.c").
	Input    string
	Managers map[string]string
	// Comment lists the names of the files received, as CMS writes it in
	// the comment of a submission.
	Comment string
}

// LanguageName returns the name of the language, nil without one (the
// language column).
func (a *Accepted) LanguageName() *string {
	if a.Language == nil {
		return nil
	}
	n := a.Language.Name
	return &n
}

func (r *Request) lengths() (int64, int64) {
	sub, in := r.MaxSubmissionLength, r.MaxInputLength
	if sub <= 0 {
		sub = defaultMaxSubmissionLength
	}
	if in <= 0 {
		in = defaultMaxInputLength
	}
	return sub, in
}

// AcceptSubmission validates the files of a submission and stores them
// (CMS accept_submission after the checks of the limits, which
// CheckSubmission does): the archive or the files of the form, matched to
// the submission format and the language, the files the task type takes
// from the previous submission, and the size of each file. It returns an
// *Unacceptable with CMS's messages when the submission is refused.
func AcceptSubmission(ctx context.Context, r *Request) (*Accepted, error) {
	maxLen, _ := r.lengths()
	required := uniq(r.SubmissionFormat)
	received, err := ExtractFilesFromForm(r.Form, required, maxLen*int64(len(required)), maxArchiveFilesPerCodename*len(required))
	if err != nil {
		var ia *InvalidArchive
		if !errors.As(err, &ia) {
			return nil, err
		}
		switch {
		case ia.TooBig:
			return nil, unacceptable(MsgSubmissionTooBig, MsgSourceTooBig, maxLen)
		case ia.TooManyFiles:
			return nil, unacceptable(MsgSubmissionTooBig, MsgTooManyFiles, len(required))
		}
		return nil, unacceptable(MsgInvalidArchive, MsgArchiveNotOpened)
	}
	files, lang, err := MatchFilesAndLanguage(received, r.Language, required, r.AllowedLanguages, r.AnyLanguage, r.Languages)
	if err != nil {
		return nil, &Unacceptable{Subject: MsgInvalidSubmissionFormat, Text: MsgSelectCorrectFiles, Err: err}
	}
	digests := map[string]string{}
	if missing := missingCodenames(required, files); len(missing) > 0 {
		if !r.TaskType.AllowPartialSubmission() {
			return nil, unacceptable(MsgInvalidSubmissionFormat, MsgSelectCorrectFiles)
		}
		if r.TaskType.ReusePreviousSubmission() {
			if digests, err = previousDigests(ctx, r, lang, missing, false); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range files {
		if f.Size > maxLen {
			return nil, unacceptable(MsgSubmissionTooBig, MsgSourceTooBig, maxLen)
		}
	}
	if err := storeFiles(ctx, r.Store, files, digests); err != nil {
		return nil, &Unacceptable{Subject: MsgSubmissionStorageFailed, Text: MsgTryAgain, Err: err}
	}
	return &Accepted{Language: lang, Files: digests, Comment: comment(received)}, nil
}

// AcceptUserTest validates and stores the files of a user test (CMS
// accept_user_test after the limits, which CheckUserTest does): the
// submission format plus the managers the task type lets contestants
// provide plus the input. ErrTestingNotAllowed if the task type has no
// user tests.
func AcceptUserTest(ctx context.Context, r *Request) (*Accepted, error) {
	if !r.TaskType.Testable() {
		return nil, ErrTestingNotAllowed
	}
	maxLen, maxInput := r.lengths()
	required := uniq(append(append(slices.Clone(r.SubmissionFormat), r.TaskType.UserManagers()...), userTestInputCodename))
	received, err := ExtractFilesFromForm(r.Form, required, maxLen*int64(len(required)), maxArchiveFilesPerCodename*len(required))
	if err != nil {
		var ia *InvalidArchive
		if !errors.As(err, &ia) {
			return nil, err
		}
		return nil, unacceptable(MsgInvalidArchive, MsgArchiveNotOpened)
	}
	files, lang, err := MatchFilesAndLanguage(received, r.Language, required, r.AllowedLanguages, r.AnyLanguage, r.Languages)
	if err != nil {
		return nil, &Unacceptable{Subject: MsgInvalidTestFormat, Text: MsgSelectCorrectFiles, Err: err}
	}
	digests := map[string]string{}
	if missing := missingCodenames(required, files); len(missing) > 0 {
		if !r.TaskType.AllowPartialSubmission() {
			return nil, unacceptable(MsgInvalidTestFormat, MsgSelectCorrectFiles)
		}
		if r.TaskType.ReusePreviousSubmission() {
			if digests, err = previousDigests(ctx, r, lang, missing, true); err != nil {
				return nil, err
			}
		}
	}
	if _, ok := files[userTestInputCodename]; !ok {
		if _, ok := digests[userTestInputCodename]; !ok {
			return nil, unacceptable(MsgInvalidTestFormat, MsgSelectCorrectFiles)
		}
	}
	for codename, f := range files {
		if codename != userTestInputCodename && f.Size > maxLen {
			return nil, unacceptable(MsgTestTooBig, MsgSourceTooBig, maxLen)
		}
	}
	if f, ok := files[userTestInputCodename]; ok && f.Size > maxInput {
		return nil, unacceptable(MsgInputTooBig, MsgInputMaxLength, maxInput)
	}
	if err := storeFiles(ctx, r.Store, files, digests); err != nil {
		return nil, &Unacceptable{Subject: MsgTestStorageFailed, Text: MsgTryAgain, Err: err}
	}
	a := &Accepted{Language: lang, Files: map[string]string{}, Managers: map[string]string{},
		Input: digests[userTestInputCodename], Comment: comment(received)}
	for codename, d := range digests {
		switch {
		case codename == userTestInputCodename:
		case slices.Contains(r.SubmissionFormat, codename):
			a.Files[codename] = d
		default:
			// A manager: stored under its name in the language.
			filename := codename
			if lang != nil {
				filename = strings.ReplaceAll(codename, langSuffix, lang.SourceExtension())
			}
			a.Managers[filename] = d
		}
	}
	return a, nil
}

func missingCodenames(required []string, files map[string]*ReceivedFile) []string {
	var missing []string
	for _, c := range required {
		if _, ok := files[c]; !ok {
			missing = append(missing, c)
		}
	}
	return missing
}

func previousDigests(ctx context.Context, r *Request, lang *language.Language, missing []string, userTest bool) (map[string]string, error) {
	if r.Previous == nil {
		return map[string]string{}, nil
	}
	prev, err := r.Previous(ctx)
	if err != nil {
		return nil, err
	}
	return PreviousDigests(prev, lang, missing, userTest), nil
}

// storeFiles puts the files in the store and adds their digests.
func storeFiles(ctx context.Context, st Store, files map[string]*ReceivedFile, digests map[string]string) error {
	for codename, f := range files {
		b, err := f.Content()
		if err != nil {
			return err
		}
		d, err := st.PutBytes(ctx, b)
		if err != nil {
			return err
		}
		digests[codename] = d
	}
	return nil
}

// comment joins the names of the files received (CMS uses them as the
// default comment of a submission).
func comment(received []ReceivedFile) string {
	names := make([]string, len(received))
	for i, f := range received {
		names[i] = f.Filename
	}
	return strings.Join(names, submissionComment)
}

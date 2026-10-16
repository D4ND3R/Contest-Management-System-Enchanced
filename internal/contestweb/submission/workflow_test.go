// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/server/contest/submission/
// workflow_test.py and utils_test.py of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package submission

import (
	"archive/zip"
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
)

// memStore is a blob store in memory.
type memStore struct {
	blobs map[string][]byte
	fail  bool
}

func (m *memStore) PutBytes(_ context.Context, b []byte) (string, error) {
	if m.fail {
		return "", errors.New("disk full")
	}
	if m.blobs == nil {
		m.blobs = map[string][]byte{}
	}
	d := blob.Digest(b)
	m.blobs[d] = b
	return d, nil
}

// fakeTaskType is a task type with the flags of the tests.
type fakeTaskType struct {
	testable, partial, reuse bool
	managers                 []string
}

func (f fakeTaskType) Testable() bool                { return f.testable }
func (f fakeTaskType) AllowPartialSubmission() bool  { return f.partial }
func (f fakeTaskType) ReusePreviousSubmission() bool { return f.reuse }
func (f fakeTaskType) UserManagers() []string        { return f.managers }

func newRequest(t *testing.T, st *memStore, parts ...[3]string) *Request {
	return &Request{
		Form:             multipartForm(t, parts...),
		SubmissionFormat: []string{"foo.%l", "bar.txt"},
		AllowedLanguages: []string{"C", "C++"},
		TaskType:         fakeTaskType{testable: true, managers: []string{"grader.%l"}},
		Languages:        fakeLangs{cLang, cppLang, py2Lang},
		Store:            st,
	}
}

func wantUnacceptable(t *testing.T, err error, subject, text string) {
	t.Helper()
	var u *Unacceptable
	if !errors.As(err, &u) || u.Subject != subject || u.Text != text {
		t.Errorf("got %v, want %q - %q", err, subject, text)
	}
}

func TestAcceptSubmissionSuccess(t *testing.T) {
	st := &memStore{}
	r := newRequest(t, st, [3]string{"foo.%l", "foo.c", "int main"}, [3]string{"bar.txt", "bar.txt", "data"})
	a, err := AcceptSubmission(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Language != cLang || *a.LanguageName() != "C" {
		t.Errorf("language %v", a.Language)
	}
	want := map[string]string{"foo.%l": blob.Digest([]byte("int main")), "bar.txt": blob.Digest([]byte("data"))}
	if !maps.Equal(a.Files, want) {
		t.Errorf("files %v", a.Files)
	}
	if string(st.blobs[want["foo.%l"]]) != "int main" {
		t.Error("not stored")
	}
	if a.Comment != "foo.c,bar.txt" {
		t.Errorf("comment %q", a.Comment)
	}

	// The language chosen by the contestant, and an archive.
	r = newRequest(t, st, [3]string{"submission", "s.zip", string(zipOf(t, zip.Deflate, [2]string{"dir/foo.cpp", "x"}, [2]string{"bar.txt", "y"}))})
	lang := "C++"
	r.Language = &lang
	if a, err = AcceptSubmission(context.Background(), r); err != nil || a.Language != cppLang || a.Comment != "foo.cpp,bar.txt" {
		t.Errorf("archive: %+v %v", a, err)
	}
}

func TestAcceptSubmissionLanguageAgnostic(t *testing.T) {
	st := &memStore{}
	r := newRequest(t, st, [3]string{"out.txt", "anything", "42"})
	r.SubmissionFormat = []string{"out.txt"}
	a, err := AcceptSubmission(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Language != nil || a.LanguageName() != nil || len(a.Files) != 1 {
		t.Errorf("got %+v", a)
	}
}

func TestAcceptSubmissionFailures(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}

	r := newRequest(t, st, [3]string{"submission", "s.zip", "this is not an archive"})
	_, err := AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgInvalidArchive, MsgArchiveNotOpened)

	// An archive larger than max length × files, or with more than twice
	// the files of the format.
	r = newRequest(t, st, [3]string{"submission", "s.zip", string(zipOf(t, zip.Deflate, [2]string{"foo.c", strings.Repeat("x", 21)}))})
	r.MaxSubmissionLength = 10
	_, err = AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgSubmissionTooBig, MsgSourceTooBig)
	var u *Unacceptable
	if errors.As(err, &u) && u.FormattedText() != "Each source file must be at most 10 bytes long." {
		t.Errorf("text %q", u.FormattedText())
	}
	r = newRequest(t, st, [3]string{"submission", "s.zip", string(zipOf(t, zip.Deflate,
		[2]string{"a", ""}, [2]string{"b", ""}, [2]string{"c", ""}, [2]string{"d", ""}, [2]string{"e", ""}))})
	_, err = AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgSubmissionTooBig, MsgTooManyFiles)
	if errors.As(err, &u) && u.FormattedText() != "The submission should contain at most 2 files." {
		t.Errorf("text %q", u.FormattedText())
	}

	// Files that do not match.
	r = newRequest(t, st, [3]string{"foo.%l", "foo.py", "x"}, [3]string{"bar.txt", "bar.txt", "y"})
	_, err = AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgInvalidSubmissionFormat, MsgSelectCorrectFiles)

	// Missing files without partial submissions.
	r = newRequest(t, st, [3]string{"foo.%l", "foo.c", "x"})
	_, err = AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgInvalidSubmissionFormat, MsgSelectCorrectFiles)

	// A file too large (checked after matching).
	r = newRequest(t, st, [3]string{"foo.%l", "foo.c", strings.Repeat("x", 11)}, [3]string{"bar.txt", "bar.txt", "y"})
	r.MaxSubmissionLength = 10
	_, err = AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgSubmissionTooBig, MsgSourceTooBig)

	// The store fails.
	r = newRequest(t, &memStore{fail: true}, [3]string{"foo.%l", "foo.c", "x"}, [3]string{"bar.txt", "bar.txt", "y"})
	_, err = AcceptSubmission(ctx, r)
	wantUnacceptable(t, err, MsgSubmissionStorageFailed, MsgTryAgain)
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("the cause is lost: %v", err)
	}
}

func TestAcceptSubmissionPartial(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	lang := "C"
	prev := &Previous{Language: &lang, Files: map[string]string{"bar.txt": "d-bar", "other": "d-other"}}
	calls := 0
	r := newRequest(t, st, [3]string{"foo.%l", "foo.c", "x"})
	r.TaskType = fakeTaskType{partial: true, reuse: true}
	r.Previous = func(context.Context) (*Previous, error) { calls++; return prev, nil }
	a, err := AcceptSubmission(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Files["bar.txt"] != "d-bar" || len(a.Files) != 2 || calls != 1 {
		t.Errorf("files %v, %d calls", a.Files, calls)
	}
	// Partial without reuse: only the files sent.
	r = newRequest(t, st, [3]string{"foo.%l", "foo.c", "x"})
	r.TaskType = fakeTaskType{partial: true}
	r.Previous = func(context.Context) (*Previous, error) { t.Error("called"); return prev, nil }
	if a, err = AcceptSubmission(ctx, r); err != nil || len(a.Files) != 1 {
		t.Errorf("got %v %v", a, err)
	}
}

func TestAcceptUserTest(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	r := newRequest(t, st, [3]string{"foo.%l", "foo.cpp", "src"}, [3]string{"bar.txt", "bar.txt", "data"},
		[3]string{"grader.%l", "grader.cpp", "grader"}, [3]string{"input", "in.txt", "1 2"})
	a, err := AcceptUserTest(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Language != cppLang || a.Input != blob.Digest([]byte("1 2")) {
		t.Errorf("got %+v", a)
	}
	if !maps.Equal(a.Files, map[string]string{"foo.%l": blob.Digest([]byte("src")), "bar.txt": blob.Digest([]byte("data"))}) {
		t.Errorf("files %v", a.Files)
	}
	if !maps.Equal(a.Managers, map[string]string{"grader.cpp": blob.Digest([]byte("grader"))}) {
		t.Errorf("managers %v", a.Managers)
	}

	// Not testable.
	r.TaskType = fakeTaskType{}
	if _, err := AcceptUserTest(ctx, r); !errors.Is(err, ErrTestingNotAllowed) {
		t.Errorf("got %v", err)
	}
}

func TestAcceptUserTestInput(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	lang := "C"
	tt := fakeTaskType{testable: true, partial: true, reuse: true, managers: []string{"grader.%l"}}

	// The input filled in from the previous test.
	r := newRequest(t, st, [3]string{"foo.%l", "foo.c", "src"}, [3]string{"bar.txt", "bar.txt", "data"})
	r.TaskType = tt
	r.Previous = func(context.Context) (*Previous, error) {
		return &Previous{Language: &lang, Input: "d-input", Managers: map[string]string{"grader.c": "d-grader"}}, nil
	}
	a, err := AcceptUserTest(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Input != "d-input" || a.Managers["grader.c"] != "d-grader" {
		t.Errorf("got %+v", a)
	}

	// No input at all.
	r = newRequest(t, st, [3]string{"foo.%l", "foo.c", "src"}, [3]string{"bar.txt", "bar.txt", "data"}, [3]string{"grader.%l", "grader.c", "g"})
	r.TaskType = fakeTaskType{testable: true, partial: true, managers: []string{"grader.%l"}}
	_, err = AcceptUserTest(ctx, r)
	wantUnacceptable(t, err, MsgInvalidTestFormat, MsgSelectCorrectFiles)

	// Missing files without partial submissions.
	r = newRequest(t, st, [3]string{"foo.%l", "foo.c", "src"}, [3]string{"input", "in", "1"})
	_, err = AcceptUserTest(ctx, r)
	wantUnacceptable(t, err, MsgInvalidTestFormat, MsgSelectCorrectFiles)

	full := func() *Request {
		return newRequest(t, st, [3]string{"foo.%l", "foo.c", "src"}, [3]string{"bar.txt", "bar.txt", "data"},
			[3]string{"grader.%l", "grader.c", "grader"}, [3]string{"input", "in", "12345"})
	}
	r = full()
	r.MaxSubmissionLength = 4
	_, err = AcceptUserTest(ctx, r)
	wantUnacceptable(t, err, MsgTestTooBig, MsgSourceTooBig)

	r = full()
	r.MaxInputLength = 4
	_, err = AcceptUserTest(ctx, r)
	wantUnacceptable(t, err, MsgInputTooBig, MsgInputMaxLength)
	var u *Unacceptable
	if errors.As(err, &u) && u.FormattedText() != "The input file must be at most 4 bytes long." {
		t.Errorf("text %q", u.FormattedText())
	}

	r = full()
	r.Store = &memStore{fail: true}
	_, err = AcceptUserTest(ctx, r)
	wantUnacceptable(t, err, MsgTestStorageFailed, MsgTryAgain)

	r = newRequest(t, st, [3]string{"submission", "s.zip", "not an archive"})
	_, err = AcceptUserTest(ctx, r)
	wantUnacceptable(t, err, MsgInvalidArchive, MsgArchiveNotOpened)
}

// TestPreviousDigests ports TestFetchFileDigestsFromPreviousSubmission.
func TestPreviousDigests(t *testing.T) {
	str := func(s string) *string { return &s }
	sub := func(lang *string, files map[string]string) *Previous { return &Previous{Language: lang, Files: files} }

	got := PreviousDigests(sub(str("C"), map[string]string{"foo.%l": "f", "baz.txt": "b"}), cLang, []string{"foo.%l", "bar.txt"}, false)
	if !maps.Equal(got, map[string]string{"foo.%l": "f"}) {
		t.Errorf("submission: %v", got)
	}
	if got := PreviousDigests(nil, cLang, []string{"foo.%l"}, false); len(got) != 0 {
		t.Errorf("no submission: %v", got)
	}
	if got := PreviousDigests(sub(str("Py2"), map[string]string{"foo.%l": "f"}), cLang, []string{"foo.%l"}, false); len(got) != 0 {
		t.Errorf("wrong language: %v", got)
	}
	got = PreviousDigests(sub(nil, map[string]string{"foo.txt": "f", "baz": "b"}), nil, []string{"foo.txt", "bar.zip"}, false)
	if !maps.Equal(got, map[string]string{"foo.txt": "f"}) {
		t.Errorf("language agnostic: %v", got)
	}

	ut := &Previous{Language: str("C"), Input: "in", Files: map[string]string{"foo.%l": "f", "baz.txt": "b"},
		Managers: map[string]string{"spam.c": "s", "eggs.h": "e"}}
	got = PreviousDigests(ut, cLang, []string{"input", "foo.%l", "bar.txt", "spam.%l", "ham"}, true)
	if !maps.Equal(got, map[string]string{"input": "in", "foo.%l": "f", "spam.%l": "s"}) {
		t.Errorf("user test: %v", got)
	}
	ut.Language = str("Py2")
	if got := PreviousDigests(ut, cLang, []string{"input", "foo.%l"}, true); len(got) != 0 {
		t.Errorf("user test, wrong language: %v", got)
	}
	cpp := &Previous{Language: str("C++"), Input: "in", Managers: map[string]string{"foo.cpp": "f", "bar.cc": "b"}}
	if got := PreviousDigests(cpp, cppLang, []string{"foo.%l", "bar.%l"}, true); !maps.Equal(got, map[string]string{"foo.%l": "f"}) {
		t.Errorf("only the primary extension: %v", got)
	}
	pas := &Previous{Language: str("Pascal"), Input: "in", Managers: map[string]string{"foolib.pas": "f", "foo.lib.pas": "x", "foo.pas": "x"}}
	if got := PreviousDigests(pas, pascal, []string{"foo.%l"}, true); !maps.Equal(got, map[string]string{"foo.%l": "f"}) {
		t.Errorf("extension replacement: %v", got)
	}
}

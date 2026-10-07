// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The cases of TestWhiteDiff, TestParseManagerOutput and TestRunProgram are
// ported from cmstestsuite/unit_tests/grading/steps (whitediff_test.py,
// trusted_test.py) of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package checker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

func diff(a, b string) bool {
	eq, _, err := WhiteDiff(strings.NewReader(a), strings.NewReader(b))
	if err != nil {
		panic(err)
	}
	return eq
}

const whiteChars = " \t\n\v\f\r"

func TestWhiteDiff(t *testing.T) {
	for _, c := range [][2]string{
		// One token.
		{"", ""}, {"1", "1"}, {"a", "a"}, {"你好", "你好"},
		// One token and whites.
		{"1   ", "1"}, {"   1", "1"}, {"1" + whiteChars, "1"},
		// Trailing blank lines.
		{"1\n", "1"}, {"1\n\n\n\n", "1"}, {"1\n\n\n\n", "1\n"}, {"1\n\r\r \n  \n\n", "1   \n\r  "},
		// Several tokens.
		{"1 asd\n\n\n", "   1\tasd  \n"}, {"1 2\n\n\n", "1 2\n"}, {"1\t\r2", "1 2"}, {"1 2", "1 2" + whiteChars},
	} {
		if !diff(c[0], c[1]) {
			t.Errorf("%q and %q should be equal", c[0], c[1])
		}
	}
	for _, c := range [][2]string{
		// Wrong tokens.
		{"1 2", "12"}, {"1 23", "12 3"}, {"1", "01"}, {"1.0", "1"},
		// Wrong lines.
		{"\n1", "1"}, {"1 2", "1\n2"}, {"1\n\n2", "1\n2"},
		// Long lines.
		{strings.Repeat("1", 1000), strings.Repeat("1", 1000) + "0"},
	} {
		if diff(c[0], c[1]) {
			t.Errorf("%q and %q should differ", c[0], c[1])
		}
	}
	// NUL is escaped in the admin text.
	eq, admin, _ := WhiteDiff(strings.NewReader("hello\x00world"), strings.NewReader("helloworld"))
	if eq || strings.ContainsRune(admin, 0) || !strings.Contains(admin, `hello\x00world`) {
		t.Errorf("NUL: %v %q", eq, admin)
	}
}

type golden struct {
	WhiteDiff []struct {
		Output, Correct string
		Equal           bool
		AdminText       *string `json:"admin_text"`
	} `json:"white_diff"`
	ManagerOutput []struct {
		Stdout, Stderr string
		Outcome        *float64
		Text           []string
		AdminText      *string `json:"admin_text"`
		Error          string
	} `json:"manager_output"`
}

func loadGolden(t *testing.T) *golden {
	t.Helper()
	data, err := os.ReadFile("testdata/upstream_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWhiteDiffUpstream compares with the answers of upstream _white_diff
// (testdata/gen_golden.py), reading whole files and byte by byte.
func TestWhiteDiffUpstream(t *testing.T) {
	g := loadGolden(t)
	if len(g.WhiteDiff) < 400 {
		t.Fatalf("%d cases", len(g.WhiteDiff))
	}
	for i, c := range g.WhiteDiff {
		o, r := unb64(t, c.Output), unb64(t, c.Correct)
		want := ""
		if c.AdminText != nil {
			want = *c.AdminText
		}
		for _, wrap := range []func(io.Reader) io.Reader{
			func(r io.Reader) io.Reader { return r }, iotest.OneByteReader, iotest.HalfReader,
		} {
			eq, admin, err := WhiteDiff(wrap(bytes.NewReader(o)), wrap(bytes.NewReader(r)))
			if err != nil || eq != c.Equal || admin != want {
				t.Fatalf("case %d (%q vs %q): %v %q %v, want %v %q", i, o, r, eq, admin, err, c.Equal, want)
			}
		}
	}
}

func TestWhiteDiffReadError(t *testing.T) {
	broken := iotest.ErrReader(io.ErrUnexpectedEOF)
	if _, _, err := WhiteDiff(broken, strings.NewReader("1")); err == nil {
		t.Error("read error of the output: want an error")
	}
	if _, _, err := WhiteDiff(strings.NewReader("1\n2"), io.MultiReader(strings.NewReader("1\n"), iotest.ErrReader(io.ErrClosedPipe))); err == nil {
		t.Error("read error of the correct output: want an error")
	}
}

// TestWhiteDiffLongLines checks that lines longer than the buffers work
// and are not kept in memory.
func TestWhiteDiffLongLines(t *testing.T) {
	long := strings.Repeat("ab ", 1<<20) // 3 MiB on one line
	eq, admin, err := WhiteDiff(strings.NewReader(long+"\n"), strings.NewReader(strings.ReplaceAll(long, " ", "\t\t")))
	if err != nil || !eq {
		t.Fatalf("%v %q %v", eq, admin, err)
	}
	eq, admin, _ = WhiteDiff(strings.NewReader(long+"x"), strings.NewReader(long+"y"))
	if eq || admin != "Expected `"+long[:100]+"...`, found `"+long[:100]+"...` on line 1" {
		t.Errorf("%v %q", eq, admin)
	}
}

func TestExact(t *testing.T) {
	for _, c := range []struct {
		out, cor string
		eq       bool
		admin    string
	}{
		{"", "", true, ""},
		{"1 2\n3\n", "1 2\n3\n", true, ""},
		{"1 2\n3", "1 2\n3\n", false, "Contestant output too short"},
		{"1 2\n3\n\n", "1 2\n3\n", false, "Contestant output too long"},
		{"1  2\n", "1 2\n", false, "Expected `1 2`, found `1  2` on line 1"},
		{"a\nb\nc d\n", "a\nb\nc e\nf\n", false, "Expected `c e`, found `c d` on line 3"},
		{"a\r\n", "a\n", false, "Expected `a`, found `a\r` on line 1"},
		{"x" + strings.Repeat("y", 200) + "\n", "x" + strings.Repeat("z", 200) + "\n", false,
			"Expected `x" + strings.Repeat("z", 99) + "...`, found `x" + strings.Repeat("y", 99) + "...` on line 1"},
		{"\xff\x00\n", "\xfe\x00\n", false, "Expected `\\xfe\\x00`, found `\\xff\\x00` on line 1"},
	} {
		for _, wrap := range []func(io.Reader) io.Reader{
			func(r io.Reader) io.Reader { return r }, iotest.OneByteReader,
		} {
			eq, admin, err := Exact(wrap(strings.NewReader(c.out)), wrap(strings.NewReader(c.cor)))
			if err != nil || eq != c.eq || admin != c.admin {
				t.Errorf("Exact(%q, %q) = %v %q %v, want %v %q", c.out, c.cor, eq, admin, err, c.eq, c.admin)
			}
		}
	}
	// A difference after the first buffer: the line is shown whole.
	big := strings.Repeat("0123456789\n", 20000) // 220 kB
	eq, admin, err := Exact(strings.NewReader(big+"same start, then A\n"), strings.NewReader(big+"same start, then B\n"))
	if err != nil || eq || admin != "Expected `same start, then B`, found `same start, then A` on line 20001" {
		t.Errorf("%v %q %v", eq, admin, err)
	}
	if _, _, err := Exact(iotest.ErrReader(io.ErrClosedPipe), strings.NewReader("")); err == nil {
		t.Error("read error: want an error")
	}
}

func TestRealNumbers(t *testing.T) {
	for _, c := range []struct {
		out, cor   string
		abs, rel   float64
		eq         bool
		admin      string
		wantErrStr string
	}{
		{"1.0 2", "1 2.000", 0, 0, true, "", ""},
		{"1.0\n2\n\n", "1 2", 0, 0, true, "", ""},
		{"0.30000001", "0.3", 1e-6, 0, true, "", ""},
		{"0.3001", "0.3", 1e-6, 0, false, "Expected `0.3`, found `0.3001` on line 1", ""},
		{"1000001", "1000000", 0, 1e-6, true, "", ""},
		{"1000002", "1000000", 0, 1e-6, false, "Expected `1000000`, found `1000002` on line 1", ""},
		{"-0", "0", 0, 0, true, "", ""},
		{"1e3", "1000", 0, 0, true, "", ""},
		{"yes 1.5", "yes 1.5000001", 1e-6, 0, true, "", ""},
		{"Yes 1.5", "yes 1.5", 1e-6, 0, false, "Expected `yes`, found `Yes` on line 1", ""},
		{"nan", "1", 1, 1, false, "Expected `1`, found `nan` on line 1", ""},
		{"inf", "inf", 0, 0, true, "", ""}, // identical tokens
		{"0x10", "16", 0, 0, false, "Expected `16`, found `0x10` on line 1", ""},
		{"1\n2\n3", "1\n2", 0, 0, false, "Contestant output too long", ""},
		{"1\n\n", "1 2", 0, 0, false, "Contestant output too short", ""},
		{"a\n\nb c\nd", "a b c e", 0, 0, false, "Expected `e`, found `d` on line 4", ""},
		{"1", "1", -1, 0, false, "", "invalid tolerance"},
	} {
		eq, admin, err := RealNumbers(strings.NewReader(c.out), strings.NewReader(c.cor), c.abs, c.rel)
		if c.wantErrStr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrStr) {
				t.Errorf("RealNumbers(%q, %q): error %v", c.out, c.cor, err)
			}
			continue
		}
		if err != nil || eq != c.eq || admin != c.admin {
			t.Errorf("RealNumbers(%q, %q, %g, %g) = %v %q %v, want %v %q", c.out, c.cor, c.abs, c.rel, eq, admin, err, c.eq, c.admin)
		}
	}
	// Tokens longer than maxToken are compared whole.
	long := strings.Repeat("7", maxToken+10)
	if eq, _, _ := RealNumbers(strings.NewReader(long+" 1"), strings.NewReader(long+" 1.0"), 0, 0); !eq {
		t.Error("equal long tokens differ")
	}
	if eq, admin, _ := RealNumbers(strings.NewReader(long+"8 1"), strings.NewReader(long+"9 1"), 0, 0); eq ||
		!strings.HasSuffix(admin, "...` on line 1") {
		t.Errorf("different long tokens: %v %q", eq, admin)
	}
	if eq, _, _ := RealNumbers(iotest.OneByteReader(strings.NewReader("1.5\n2")), iotest.OneByteReader(strings.NewReader("1.50 2")), 0, 0); !eq {
		t.Error("byte by byte")
	}
}

// TestParseManagerOutput runs the cases of upstream trusted_test.py and
// compares with the answers of upstream extract_outcome_and_text.
func TestParseManagerOutput(t *testing.T) {
	o, err := ParseManagerOutput([]byte("0.45\n"), []byte("你好.\n"))
	if err != nil || o.Outcome != 0.45 || !slices.Equal(o.Text, []string{"你好."}) || o.AdminText != "" {
		t.Errorf("%+v %v", o, err)
	}
	o, _ = ParseManagerOutput([]byte("0.45\n"), []byte("Text to return.\nADMIN_MESSAGE: admin"))
	if o.AdminText != "admin" {
		t.Errorf("admin text %q", o.AdminText)
	}
	o, _ = ParseManagerOutput([]byte("0.45\n"), []byte("translate:success\n"))
	if !slices.Equal(o.Text, []string{"Output is correct"}) {
		t.Errorf("translated text %q", o.Text)
	}
	for _, bad := range [][2]string{{"not a float\n", "Text\n"}, {"0.45", "\xc3\x28"}, {"nan", "x"}, {"inf\n", "x"}} {
		if _, err := ParseManagerOutput([]byte(bad[0]), []byte(bad[1])); err == nil {
			t.Errorf("%q %q: want an error", bad[0], bad[1])
		}
	}

	g := loadGolden(t)
	for i, c := range g.ManagerOutput {
		o, err := ParseManagerOutput(unb64(t, c.Stdout), unb64(t, c.Stderr))
		if c.Error != "" {
			if err == nil {
				t.Errorf("case %d: %+v, want an error like %q", i, o, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: %v", i, err)
			continue
		}
		admin := ""
		if c.AdminText != nil {
			admin = *c.AdminText
		}
		if o.Outcome != *c.Outcome || !slices.Equal(o.Text, c.Text) || o.AdminText != admin {
			t.Errorf("case %d: %+v, want %v %q %q", i, o, *c.Outcome, c.Text, admin)
		}
	}
}

func TestTestlibOutcome(t *testing.T) {
	for _, c := range []struct {
		code    int
		stderr  string
		outcome float64
		text    string
		admin   string
		err     bool
	}{
		{0, "ok 3 numbers\n", 1, "Output is correct", "ok 3 numbers", false},
		{1, "wrong answer expected 3, found 4\n", 0, "Output isn't correct", "wrong answer expected 3, found 4", false},
		{2, "wrong output format Expected int32\n", 0, "Output isn't correct", "wrong output format Expected int32", false},
		{4, "wrong output format x", 0, "Output isn't correct", "wrong output format x", false},
		{8, "unexpected eof", 0, "Output isn't correct", "unexpected eof", false},
		{7, "points 0.25 almost\n", 0.25, "Output is partially correct", "points 0.25 almost", false},
		{7, "points 1", 1, "Output is partially correct", "points 1", false},
		{7, "nothing", 0, "", "", true},
		{7, "points x", 0, "", "", true},
		{3, "FAIL cannot open file", 0, "", "", true},
		{5, "", 0, "", "", true},
		{-1, "", 0, "", "", true},
	} {
		o, err := TestlibOutcome(c.code, []byte(c.stderr))
		if c.err {
			if err == nil {
				t.Errorf("exit %d %q: %+v, want an error", c.code, c.stderr, o)
			}
			continue
		}
		if err != nil || o.Outcome != c.outcome || !slices.Equal(o.Text, []string{c.text}) || o.AdminText != c.admin {
			t.Errorf("exit %d %q: %+v %v", c.code, c.stderr, o, err)
		}
	}
	if o, _ := TestlibOutcome(1, []byte("bad \xff\x00 bytes")); o.AdminText != "bad �\\x00 bytes" {
		t.Errorf("admin text %q", o.AdminText)
	}
	if !bytes.Contains(TestlibHeader, []byte("#ifdef CMS")) {
		t.Error("TestlibHeader is not the testlib.h patched for CMS")
	}
}

func testConfig() *steps.Config {
	return &steps.Config{
		Trusted: config.SandboxLimit{MaxProcesses: 1000, MaxTimeSec: 10, MaxMemoryKiB: 4 << 20},
		Cgroups: true,
	}
}

func TestCheckComparisons(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		checker Checker
		user    string
		outcome float64
		text    string
		admin   string
	}{
		{Checker{Kind: KindDiff}, "1  2\n\n", 1, "Output is correct", ""},
		{Checker{Kind: KindDiff}, "1 3", 0, "Output isn't correct", "Expected `1 2`, found `1 3` on line 1"},
		{Checker{Kind: KindExact}, "1 2\n", 1, "Output is correct", ""},
		{Checker{Kind: KindExact}, "1  2\n", 0, "Output isn't correct", "Expected `1 2`, found `1  2` on line 1"},
		{Checker{Kind: KindReal, AbsTolerance: 0.1}, "1.05 2", 1, "Output is correct", ""},
	} {
		box := stepstest.New(t, nil)
		if err := box.WriteFile("output.txt", []byte(c.user)); err != nil {
			t.Fatal(err)
		}
		o, err := c.checker.Check(ctx, testConfig(), box, Files{UserOutput: "output.txt", CorrectOutput: strings.NewReader("1 2\n")})
		if err != nil || o.Outcome != c.outcome || !slices.Equal(o.Text, []string{c.text}) || o.AdminText != c.admin {
			t.Errorf("%s %q: %+v %v", c.checker.Kind, c.user, o, err)
		}
		if len(box.Specs()) != 0 {
			t.Error("a comparison ran a program")
		}
	}

	// No output, or a link instead of it.
	box := stepstest.New(t, nil)
	if err := os.Symlink("/etc/passwd", filepath.Join(box.Dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing.txt", "link.txt"} {
		for _, kind := range []Kind{KindDiff, KindComparator} {
			o, err := Checker{Kind: kind}.Check(ctx, testConfig(), box, Files{UserOutput: name, OutputFilename: "out.txt"})
			if err != nil || o.Outcome != 0 || !slices.Equal(o.Text, []string{"Evaluation didn't produce file %s", "out.txt"}) {
				t.Errorf("%s %s: %+v %v", kind, name, o, err)
			}
		}
	}
	if _, err := (Checker{Kind: "magic"}).Check(ctx, testConfig(), box, Files{}); err == nil {
		t.Error("unknown kind: want an error")
	}
	if _, err := (Checker{Kind: KindReal, RelTolerance: -1}).Check(ctx, testConfig(), box, Files{}); err == nil {
		t.Error("negative tolerance: want an error")
	}
	if _, err := (Checker{Kind: KindComparator}).Compare(strings.NewReader(""), strings.NewReader("")); err == nil {
		t.Error("Compare with a program kind: want an error")
	}
}

// fakeChecker plays a checker program writing stdout and stderr and
// exiting with result.
func fakeChecker(result *sandbox.Result, stdout, stderr string) stepstest.RunFunc {
	return func(b *stepstest.FakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		_ = b.WriteFile(spec.Stdout, []byte(stdout))
		_ = b.WriteFile(spec.Stderr, []byte(stderr))
		return result, nil
	}
}

func programFiles() Files {
	return Files{
		UserOutput:    "user_output.txt",
		Program:       strings.NewReader("#!/bin/true\n"),
		Input:         strings.NewReader("1 2\n"),
		CorrectOutput: strings.NewReader("3\n"),
	}
}

func TestRunProgram(t *testing.T) {
	ctx := context.Background()
	newBox := func(run stepstest.RunFunc) *stepstest.FakeBox {
		b := stepstest.New(t, run)
		if err := b.WriteFile("user_output.txt", []byte("3\n")); err != nil {
			t.Fatal(err)
		}
		return b
	}

	// Success: the files are in place and the arguments in CMS order.
	box := newBox(fakeChecker(stepstest.OK(), "0.123\n", "Text.\n"))
	f := programFiles()
	f.ExtraArgs = []string{"batch"}
	o, err := Checker{Kind: KindComparator}.Check(ctx, testConfig(), box, f)
	if err != nil || o.Outcome != 0.123 || !slices.Equal(o.Text, []string{"Text."}) || o.AdminText != "" {
		t.Fatalf("%+v %v", o, err)
	}
	specs := box.Specs()
	if len(specs) != 1 || !slices.Equal(specs[0].Argv, []string{"./checker", "input.txt", "correct_output.txt", "user_output.txt", "batch"}) ||
		specs[0].Seccomp || specs[0].CPUTime != 10 {
		t.Errorf("runs %+v", specs)
	}
	if box.Read("input.txt") != "1 2\n" || box.Read("correct_output.txt") != "3\n" || box.Read("checker") != "#!/bin/true\n" {
		t.Error("files not in the box")
	}
	if st, _ := box.Stat("checker"); st.Mode().Perm()&0o111 == 0 {
		t.Error("the checker is not executable")
	}

	// Failures of the checker are errors, never verdicts.
	for name, run := range map[string]stepstest.RunFunc{
		"nonzero return":    fakeChecker(&sandbox.Result{Status: sandbox.StatusRuntimeError, ExitCode: 1}, "0.1\n", "x\n"),
		"timeout":           fakeChecker(&sandbox.Result{Status: sandbox.StatusTimedOut}, "", ""),
		"invalid outcome":   fakeChecker(stepstest.OK(), "A0.123\n", "Text.\n"),
		"invalid text":      fakeChecker(stepstest.OK(), "0.123\n", "\xc3\x28"),
		"missing outcome":   fakeChecker(stepstest.OK(), "", "Text.\n"),
		"control character": fakeChecker(stepstest.OK(), "1\n", "a\x01b\n"),
		"sandbox error": func(*stepstest.FakeBox, sandbox.RunSpec) (*sandbox.Result, error) {
			return &sandbox.Result{Status: sandbox.StatusInternalError}, nil
		},
		"missing stdout": func(b *stepstest.FakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
			_ = os.Remove(filepath.Join(b.Dir, spec.Stdout))
			return stepstest.OK(), nil
		},
	} {
		box := newBox(run)
		if o, err := RunProgram(ctx, testConfig(), box, KindComparator, programFiles()); err == nil {
			t.Errorf("%s: %+v, want an error", name, o)
		}
	}

	// Missing checker, files already in the box.
	f = programFiles()
	f.Program = nil
	if _, err := RunProgram(ctx, testConfig(), newBox(nil), KindComparator, f); err == nil || !strings.Contains(err.Error(), "missing checker") {
		t.Errorf("missing checker: %v", err)
	}
	for _, name := range []string{"checker", "input.txt", "correct_output.txt"} {
		box := newBox(nil)
		_ = box.WriteFile(name, []byte("something"))
		if _, err := RunProgram(ctx, testConfig(), box, KindComparator, programFiles()); err == nil || len(box.Specs()) != 0 {
			t.Errorf("%s already in the box: %v", name, err)
		}
	}
	if _, err := RunProgram(ctx, testConfig(), newBox(nil), KindDiff, programFiles()); err == nil {
		t.Error("RunProgram with a comparison: want an error")
	}
}

func TestRunProgramTestlib(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		res     *sandbox.Result
		stderr  string
		outcome float64
		err     bool
	}{
		{stepstest.OK(), "ok fine\n", 1, false},
		{&sandbox.Result{Status: sandbox.StatusRuntimeError, ExitCode: 1}, "wrong answer 4 != 3\n", 0, false},
		{&sandbox.Result{Status: sandbox.StatusRuntimeError, ExitCode: 7}, "points 0.5\n", 0.5, false},
		{&sandbox.Result{Status: sandbox.StatusRuntimeError, ExitCode: 3}, "FAIL bad\n", 0, true},
		{&sandbox.Result{Status: sandbox.StatusSignal, Signal: 11}, "", 0, true},
		{&sandbox.Result{Status: sandbox.StatusTimedOut}, "", 0, true},
	} {
		box := stepstest.New(t, fakeChecker(c.res, "", c.stderr))
		_ = box.WriteFile("user_output.txt", []byte("3\n"))
		f := programFiles()
		f.ExtraArgs = []string{"ignored"}
		o, err := Checker{Kind: KindTestlib}.Check(ctx, testConfig(), box, f)
		if c.err {
			if err == nil {
				t.Errorf("%+v: %+v, want an error", c.res, o)
			}
			continue
		}
		if err != nil || o.Outcome != c.outcome || o.AdminText != strings.TrimSpace(c.stderr) {
			t.Errorf("%+v: %+v %v", c.res, o, err)
		}
		// testlib's argument order: input, output, answer.
		if argv := box.Specs()[0].Argv; !slices.Equal(argv, []string{"./checker", "input.txt", "user_output.txt", "correct_output.txt"}) {
			t.Errorf("argv %q", argv)
		}
	}
}

func TestKinds(t *testing.T) {
	for _, k := range []Kind{KindDiff, KindExact, KindReal, KindComparator, KindTestlib} {
		if !k.Valid() || (Checker{Kind: k}).Validate() != nil {
			t.Errorf("%s invalid", k)
		}
	}
	if Kind("").Valid() || KindDiff.IsProgram() || !KindTestlib.IsProgram() {
		t.Error("Kind")
	}
}

// benchOutput is a large correct output: 10^6 numbers on 10^5 lines.
func benchOutput() []byte {
	var b bytes.Buffer
	for i := 0; i < 100000; i++ {
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("1234567")
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func BenchmarkWhiteDiff(b *testing.B) {
	data := benchOutput()
	spaced := bytes.ReplaceAll(data, []byte(" "), []byte("  "))
	for _, c := range []struct {
		name string
		out  []byte
	}{{"identical", data}, {"other spacing", spaced}} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.out)))
			for b.Loop() {
				if eq, _, _ := WhiteDiff(bytes.NewReader(c.out), bytes.NewReader(data)); !eq {
					b.Fatal("not equal")
				}
			}
		})
	}
}

func BenchmarkExact(b *testing.B) {
	data := benchOutput()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if eq, _, _ := Exact(bytes.NewReader(data), bytes.NewReader(data)); !eq {
			b.Fatal("not equal")
		}
	}
}

func BenchmarkRealNumbers(b *testing.B) {
	data := benchOutput()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if eq, _, _ := RealNumbers(bytes.NewReader(data), bytes.NewReader(data), 1e-6, 1e-6); !eq {
			b.Fatal("not equal")
		}
	}
}

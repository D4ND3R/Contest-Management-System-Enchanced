// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package job_test

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

var (
	dSrc   = blob.Digest([]byte("int main() {}"))
	dGrad  = blob.Digest([]byte("grader"))
	dCheck = blob.Digest([]byte("checker"))
	dExe   = blob.Digest([]byte("ELF"))
	dIn    = blob.Digest([]byte("1 2\n"))
	dOut   = blob.Digest([]byte("3\n"))
	dUser  = blob.Digest([]byte("3"))
)

// jobs returns a valid job of each kind.
func jobs() map[job.Kind]*job.Job {
	params := json.RawMessage(`["grader",["input.txt","output.txt"],"comparator"]`)
	base := func(k job.Kind, codename string) *job.Job {
		j := &job.Job{
			Operation:          job.Operation{Type: k, ObjectID: 12, DatasetID: 3, TestcaseCodename: codename},
			Attempt:            1,
			Generation:         4,
			TaskType:           "Batch",
			TaskTypeParameters: params,
			Language:           "C++17 / g++",
			Files:              map[string]string{"sum.%l": dSrc},
			Managers:           map[string]string{"grader.cpp": dGrad, "checker": dCheck},
		}
		j.ID = j.DedupKey()
		return j
	}
	c := base(job.Compilation, "")
	e := base(job.Evaluation, "tc_01-a")
	e.Executables = map[string]string{"sum": dExe}
	e.Input, e.Output = dIn, dOut
	e.TimeLimit, e.MemoryLimit = 1.5, 256<<20
	ct := base(job.UserTestCompilation, "")
	et := base(job.UserTestEvaluation, "")
	et.Executables = map[string]string{"sum": dExe}
	et.Input = dIn
	et.TimeLimit, et.MemoryLimit = 1, 64<<20
	et.OnlyExecution, et.GetOutput = true, true
	return map[job.Kind]*job.Job{c.Operation.Type: c, e.Operation.Type: e, ct.Operation.Type: ct, et.Operation.Type: et}
}

func TestJobRoundTrip(t *testing.T) {
	for k, j := range jobs() {
		t.Run(string(k), func(t *testing.T) {
			b, err := j.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if j.V != 0 {
				t.Error("Encode modified the job")
			}
			got, err := job.DecodeJob(b)
			if err != nil {
				t.Fatalf("decode %s: %v", b, err)
			}
			want := *j
			want.V = job.Version
			if !reflect.DeepEqual(got, &want) {
				t.Fatalf("round trip:\n got %+v\nwant %+v", got, &want)
			}
			// Deterministic encoding (map keys are sorted).
			if b2, _ := got.Encode(); string(b2) != string(b) {
				t.Errorf("encodings differ:\n%s\n%s", b, b2)
			}
		})
	}
}

func TestOperationKeys(t *testing.T) {
	js := jobs()
	for k, want := range map[job.Kind][2]string{
		job.Compilation:         {"compile/12/3@4", "compile submission 12 (dataset 3)"},
		job.Evaluation:          {"evaluate/12/3/tc_01-a@4", "evaluate submission 12 on testcase tc_01-a (dataset 3)"},
		job.UserTestCompilation: {"compile_test/12/3@4", "compile user test 12 (dataset 3)"},
		job.UserTestEvaluation:  {"evaluate_test/12/3@4", "evaluate user test 12 (dataset 3)"},
	} {
		j := js[k]
		if got := j.DedupKey(); got != want[0] {
			t.Errorf("%s: dedup key %q, want %q", k, got, want[0])
		}
		if got := j.Describe(); got != want[1] {
			t.Errorf("%s: description %q, want %q", k, got, want[1])
		}
	}
	// Archiving does not change the identity of the operation.
	o := js[job.Evaluation].Operation
	o2 := o
	o2.ArchiveSandbox = true
	if o.Key() != o2.Key() || o == o2 {
		t.Error("archive_sandbox: same key, different operation expected")
	}
	j := js[job.Compilation]
	j.Info = "custom"
	if j.Describe() != "custom" {
		t.Error("Info not used")
	}
	for k, want := range map[job.Kind][3]bool{
		job.Compilation:         {true, true, false},
		job.Evaluation:          {true, false, true},
		job.UserTestCompilation: {false, true, false},
		job.UserTestEvaluation:  {false, false, true},
	} {
		if got := [3]bool{k.ForSubmission(), k.IsCompilation(), k.IsEvaluation()}; got != want || !k.Valid() {
			t.Errorf("%s: %v, want %v", k, got, want)
		}
	}
	if job.Kind("compilation").Valid() {
		t.Error("upstream job type accepted as an operation type")
	}
}

func TestJobValidation(t *testing.T) {
	cases := []struct {
		name string
		kind job.Kind
		mut  func(*job.Job)
	}{
		{"unknown kind", job.Compilation, func(j *job.Job) { j.Operation.Type = "compilation" }},
		{"no object", job.Compilation, func(j *job.Job) { j.Operation.ObjectID = 0 }},
		{"negative dataset", job.Evaluation, func(j *job.Job) { j.Operation.DatasetID = -1 }},
		{"evaluation without testcase", job.Evaluation, func(j *job.Job) { j.Operation.TestcaseCodename = "" }},
		{"bad codename", job.Evaluation, func(j *job.Job) { j.Operation.TestcaseCodename = "a/b" }},
		{"testcase on a compilation", job.Compilation, func(j *job.Job) { j.Operation.TestcaseCodename = "t1" }},
		{"testcase on a user test", job.UserTestEvaluation, func(j *job.Job) { j.Operation.TestcaseCodename = "t1" }},
		{"no id", job.Compilation, func(j *job.Job) { j.ID = "" }},
		{"id with space", job.Compilation, func(j *job.Job) { j.ID = "a b" }},
		{"attempt 0", job.Compilation, func(j *job.Job) { j.Attempt = 0 }},
		{"negative generation", job.Compilation, func(j *job.Job) { j.Generation = -1 }},
		{"no task type", job.Compilation, func(j *job.Job) { j.TaskType = "" }},
		{"bad task type", job.Compilation, func(j *job.Job) { j.TaskType = "../Batch" }},
		{"no parameters", job.Compilation, func(j *job.Job) { j.TaskTypeParameters = nil }},
		{"bad parameters", job.Compilation, func(j *job.Job) { j.TaskTypeParameters = json.RawMessage(`[1,`) }},
		{"huge parameters", job.Compilation, func(j *job.Job) {
			j.TaskTypeParameters = json.RawMessage(`"` + strings.Repeat("a", job.MaxParametersLen) + `"`)
		}},
		{"control character in language", job.Compilation, func(j *job.Job) { j.Language = "C\n" }},
		{"bad digest", job.Compilation, func(j *job.Job) { j.Files["sum.%l"] = strings.ToUpper(dSrc) }},
		{"short digest", job.Compilation, func(j *job.Job) { j.Managers["grader.cpp"] = dSrc[:63] }},
		{"path in file name", job.Compilation, func(j *job.Job) { j.Files["../sum.%l"] = dSrc }},
		{"dot dot", job.Compilation, func(j *job.Job) { j.Managers[".."] = dSrc }},
		{"%l in a manager", job.Compilation, func(j *job.Job) { j.Managers["grader.%l"] = dSrc }},
		{"empty file name", job.Compilation, func(j *job.Job) { j.Files[""] = dSrc }},
		{"only %l", job.Compilation, func(j *job.Job) { j.Files[".%l"] = dSrc }},
		{"too many files", job.Compilation, func(j *job.Job) {
			for i := range job.MaxFiles + 1 {
				j.Managers["m"+strconv.Itoa(i)] = dSrc
			}
		}},
		{"executables for a compilation", job.Compilation, func(j *job.Job) { j.Executables = map[string]string{"sum": dExe} }},
		{"input for a compilation", job.UserTestCompilation, func(j *job.Job) { j.Input = dIn }},
		{"no input", job.Evaluation, func(j *job.Job) { j.Input = "" }},
		{"no output", job.Evaluation, func(j *job.Job) { j.Output = "" }},
		{"output of a user test", job.UserTestEvaluation, func(j *job.Job) { j.Output = dOut }},
		{"user test not only executed", job.UserTestEvaluation, func(j *job.Job) { j.OnlyExecution = false }},
		{"negative time limit", job.Evaluation, func(j *job.Job) { j.TimeLimit = -1 }},
		{"NaN time limit", job.Evaluation, func(j *job.Job) { j.TimeLimit = nan() }},
		{"huge time limit", job.Evaluation, func(j *job.Job) { j.TimeLimit = job.MaxTimeLimit + 1 }},
		{"memory limit in KiB", job.Evaluation, func(j *job.Job) { j.MemoryLimit = 262144 }},
		{"negative memory limit", job.Evaluation, func(j *job.Job) { j.MemoryLimit = -1 }},
		{"huge memory limit", job.Evaluation, func(j *job.Job) { j.MemoryLimit = job.MaxMemoryLimit + 1 }},
		{"invalid UTF-8 info", job.Evaluation, func(j *job.Job) { j.Info = "\xff" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := jobs()[c.kind]
			c.mut(j)
			err := j.Validate()
			if !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("validate: %v", err)
			}
			if _, err := j.Encode(); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("encode: %v", err)
			}
		})
	}

	// Valid variations.
	for name, mut := range map[job.Kind]func(*job.Job){
		job.Evaluation: func(j *job.Job) {
			j.TimeLimit, j.MemoryLimit = 0, 0 // no limits
			j.Language = ""                   // OutputOnly
			j.Executables = nil
			j.Files = map[string]string{"output_01.txt": job.TombstoneDigest}
		},
		job.Compilation: func(j *job.Job) {
			j.Managers = nil
			j.TaskTypeParameters = json.RawMessage(`null`)
			j.Generation = 0
		},
	} {
		j := jobs()[name]
		mut(j)
		if err := j.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func nan() float64 {
	var zero float64
	return zero / zero
}

func TestDecodeErrors(t *testing.T) {
	good, err := jobs()[job.Evaluation].Encode()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(good, &m); err != nil {
		t.Fatal(err)
	}
	with := func(k string, v any) []byte {
		c := maps.Clone(m)
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
		b, _ := json.Marshal(c)
		return b
	}
	for name, c := range map[string]struct {
		in   []byte
		want error
	}{
		"no version":    {with("v", nil), job.ErrVersion},
		"newer version": {with("v", 2), job.ErrVersion},
		"not JSON":      {[]byte("{"), job.ErrInvalid},
		"not an object": {[]byte(`[1]`), job.ErrInvalid},
		"wrong type":    {with("attempt", "one"), job.ErrInvalid},
		"invalid job":   {with("task_type", ""), job.ErrInvalid},
	} {
		if _, err := job.DecodeJob(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if _, err := job.DecodeResult(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s (as a result): %v, want %v", name, err, c.want)
		}
	}
	// Fields added by a newer release in a compatible way are ignored.
	j, err := job.DecodeJob(with("priority_hint", "high"))
	if err != nil || j.Operation.TestcaseCodename != "tc_01-a" {
		t.Fatalf("unknown field: %+v %v", j, err)
	}
}

// upstreamJob is the export_to_dict of an EvaluationJob after a Worker
// ran it, with the shape of cms/grading/Job.py (Job, EvaluationJob) and
// cms/service/esoperations.py (ESOperation.to_dict) of CMS.
const upstreamJob = `{
  "operation": {"type": "evaluate", "object_id": 12, "dataset_id": 3,
                "testcase_codename": "tc_01-a", "archive_sandbox": true},
  "task_type": "Batch",
  "task_type_parameters": ["grader", ["input.txt", "output.txt"], "comparator"],
  "language": "C++17 / g++",
  "multithreaded_sandbox": true,
  "archive_sandbox": true,
  "shard": 0,
  "keep_sandbox": true,
  "sandboxes": ["/var/local/cache/cms/cms-sandbox-1"],
  "sandbox_digests": {"/var/local/cache/cms/cms-sandbox-1": "` + "DUSER" + `"},
  "info": "evaluate submission 12 on testcase tc_01-a",
  "success": true,
  "text": ["Output is correct"],
  "admin_text": "checker said ok",
  "files": {"sum.%l": "DSRC"},
  "managers": {"grader.cpp": "DGRAD", "checker": "DCHECK"},
  "executables": {"sum": "DEXE"},
  "type": "evaluation",
  "input": "DIN",
  "output": "DOUT",
  "time_limit": 1.5,
  "memory_limit": 268435456,
  "outcome": "1.0",
  "user_output": null,
  "plus": {"execution_time": 0.25, "execution_wall_clock_time": 0.5,
           "execution_memory": 1048576, "exit_status": "signal", "signal": 11},
  "only_execution": false,
  "get_output": false
}`

func upstream(t *testing.T) []byte {
	t.Helper()
	return []byte(strings.NewReplacer("DSRC", dSrc, "DGRAD", dGrad, "DCHECK", dCheck, "DEXE", dExe,
		"DIN", dIn, "DOUT", dOut, "DUSER", dUser).Replace(upstreamJob))
}

// The fields shared with upstream have upstream's JSON names: an upstream
// job dict fills both the job and the result.
func TestUpstreamFieldNames(t *testing.T) {
	var j job.Job
	if err := json.Unmarshal(upstream(t), &j); err != nil {
		t.Fatal(err)
	}
	want := jobs()[job.Evaluation]
	want.ID, want.Attempt, want.Generation = "", 0, 0
	want.Operation.ArchiveSandbox = true
	want.MultithreadedSandbox, want.KeepSandbox = true, true
	want.Info = "evaluate submission 12 on testcase tc_01-a"
	j.TaskTypeParameters = compact(t, j.TaskTypeParameters)
	if !reflect.DeepEqual(&j, want) {
		t.Errorf("job from an upstream dict:\n got %+v\nwant %+v", &j, want)
	}

	var r job.Result
	if err := json.Unmarshal(upstream(t), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Success || r.Outcome != "1.0" || !slices.Equal(r.Text, []string{"Output is correct"}) ||
		r.AdminText != "checker said ok" || r.UserOutput != "" || r.Operation != want.Operation ||
		!slices.Equal(r.Sandboxes, []string{"/var/local/cache/cms/cms-sandbox-1"}) ||
		r.SandboxDigests["/var/local/cache/cms/cms-sandbox-1"] != dUser {
		t.Errorf("result from an upstream dict: %+v", r)
	}
	s := r.Stats
	if s == nil || *s.ExecutionTime != 0.25 || *s.ExecutionWallClockTime != 0.5 || *s.ExecutionMemory != 1<<20 ||
		s.ExitStatus != job.ExitSignal || s.Signal != 11 {
		t.Errorf("plus: %+v", s)
	}

	// Every key we write is an upstream key, except for this fork's own.
	var up map[string]any
	if err := json.Unmarshal(upstream(t), &up); err != nil {
		t.Fatal(err)
	}
	ours := map[string]bool{"v": true, "id": true, "job_id": true, "attempt": true, "generation": true,
		"worker": true, "error": true, "tombstone": true, "compilation_outcome": true, "durations": true}
	res := r
	res.JobID, res.Attempt, res.Worker = "x", 1, "w"
	res.Executables = nil // inputs of an upstream evaluation, never outputs
	res.Durations.Total = 1
	for name, enc := range map[string]func() ([]byte, error){
		"job":    jobs()[job.Evaluation].Encode,
		"result": res.Encode,
	} {
		b, err := enc()
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		for k := range got {
			if _, ok := up[k]; !ok && !ours[k] {
				t.Errorf("%s key %q is neither upstream's nor in the list of this fork's", name, k)
			}
		}
		if op, _ := got["operation"].(map[string]any); !reflect.DeepEqual(keys(op), keys(up["operation"].(map[string]any))) {
			t.Errorf("%s: operation keys %v", name, keys(op))
		}
	}
}

func keys(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }

func compact(t *testing.T, b json.RawMessage) json.RawMessage {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return out
}

func BenchmarkJobEncode(b *testing.B) {
	j := jobs()[job.Evaluation]
	b.ReportAllocs()
	for b.Loop() {
		if _, err := j.Encode(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJobDecode(b *testing.B) {
	enc, err := jobs()[job.Evaluation].Encode()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(enc)))
	for b.Loop() {
		if _, err := job.DecodeJob(enc); err != nil {
			b.Fatal(err)
		}
	}
}

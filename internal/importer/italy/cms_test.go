// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package italy

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// syntheticTasks are three tasks that cover what the demo contest does
// not (graders and headers, stubs, cor/, the deprecated token format,
// BatchAndOutput, gen/GEN with #COPY: lines, statements per language,
// Communication, Interactive, an explicit score type). The original
// cmsImportTask -L italy_yaml imported them; cmsTaskRows are the rows it
// stored, with SHA-1 digests. The score type of zinter mixes a regular
// expression and a count: CMS stored it, but could never score it; the
// validation rejects it.
func syntheticTasks(t testing.TB) string {
	t.Helper()
	files := map[string]string{
		"zgr/task.yaml": `name: zgr
title: Graders
timeout: 2
memlimit: 64
version: v2
feedback_level: full
score_mode: max_subtask
score_precision: 2
max_submission_number: 7
min_user_test_interval: 60
token_initial: 3
token_total: 10
token_gen_number: 1
token_gen_time: 0
token_max: 5
token_min_interval: 1.5
output_only_testcases: "1"
output_optional_testcases: "2, 3"
public_testcases: "0,2"
primary_language: en
`,
		"zgr/gen/GEN":               "#ST: 30\ngen 1\n#COPY: x\n# comment\n\n#ST: 70\ngen 3\ngen 4\n",
		"zgr/sol/grader.cpp":        "grader cpp\n",
		"zgr/sol/grader.c":          "grader c\n",
		"zgr/sol/g.h":               "header\n",
		"zgr/sol/glib.pas":          "pas\n",
		"zgr/sol/sol.cpp":           "sol\n",
		"zgr/cor/correttore":        "checker\n",
		"zgr/statement/english.pdf": "en\n",
		"zgr/statement/italian.pdf": "it\n",
		"zgr/att/sample.txt":        "sample\n",
		"zgr/att/input_001.txt":     "overridden\n",

		"zcomm/task.yaml":       "name: zcomm\ntitle: Comm\nn_input: 2\ntotal_value: 30\nnum_processes: 2\ntoken_mode: finite\ntoken_max_number: 4\ntoken_gen_interval: 90\n",
		"zcomm/sol/stub.cpp":    "stub\n",
		"zcomm/sol/stub.h":      "stub h\n",
		"zcomm/check/manager":   "manager\n",
		"zcomm/testo/testo.pdf": "testo\n",

		"zinter/task.yaml": "name: zinter\ntitle: Inter\nn_input: 1\ncontroller_process_limit: 3\ninteractive_concurrent: false\n" +
			"controller_memory_limit: 256\ncontroller_time_limit: 10\ncontroller_wall_time_limit: 20.5\ntoken_mode: disabled\n" +
			"score_type: GroupMul\nscore_type_parameters: [[50.0, \"00[01]\"], [50, 1]]\ninfile: in.txt\noutfile: \"\"\n",
		"zinter/input/input0.txt":        "i\n",
		"zinter/output/output0.txt":      "o\n",
		"zinter/cor/controller":          "controller\n",
		"zinter/statement/statement.pdf": "s\n",
	}
	for i := range 4 {
		files[fmt.Sprintf("zgr/input/input%d.txt", i)] = fmt.Sprintf("in %d\n", i)
		files[fmt.Sprintf("zgr/output/output%d.txt", i)] = fmt.Sprintf("out %d\n", i)
	}
	for i := range 2 {
		files[fmt.Sprintf("zcomm/input/input%d.txt", i)] = fmt.Sprintf("cin %d\n", i)
		files[fmt.Sprintf("zcomm/output/output%d.txt", i)] = fmt.Sprintf("cout %d\n", i)
	}
	return writeFiles(t, t.TempDir(), files)
}

type cmsTaskRow struct {
	submissionFormat, primary []string
	tokens                    importer.Tokens
	maxSubmissions            *int32
	minUserTestInterval       *time.Duration
	feedback, scoreMode       string
	precision                 int32
	description               string
	timeLimit                 *float64
	memoryLimit               *int64
	taskType, ttParams        string
	scoreType, stParams       string
	testcases                 []cmsTestcase
	managers, statements      map[string]string
	attachments               map[string]string
	// invalid is an error of the validation: CMS stored the task but
	// could never judge it.
	invalid string
}

var cmsTaskRows = map[string]cmsTaskRow{
	"zgr": {
		submissionFormat: []string{"zgr.%l", "output_001.txt", "output_002.txt", "output_003.txt"}, primary: []string{"en"},
		tokens: importer.Tokens{Mode: "infinite", MaxNumber: importer.Ptr(int32(10)), MinInterval: 1500 * time.Millisecond,
			GenInitial: 3, GenNumber: 1, GenInterval: time.Minute, GenMax: importer.Ptr(int32(5))},
		maxSubmissions: importer.Ptr(int32(7)), minUserTestInterval: importer.Ptr(time.Minute),
		feedback: "full", scoreMode: "max_subtask", precision: 2,
		description: "v2", timeLimit: importer.Ptr(2.0), memoryLimit: importer.Ptr(int64(67108864)),
		taskType: "BatchAndOutput", ttParams: `["grader", ["input.txt", "output.txt"], "comparator", "001"]`,
		scoreType: "GroupMin", stParams: `[[30, 2], [70, 2]]`,
		testcases: []cmsTestcase{
			{"000", true, "284bdfb96806f8cc8a705685fa16308ea3fda5e8", "f2def35bdf5b4258066c7953ff0669fbb3e9f862"},
			{"001", false, "8e9ae53a34423b7a45337756729786b8cd6e7586", "cd6b2f1818608f3c4110734633400be358e6fac7"},
			{"002", true, "a60309e97f8fa44a7a67d67f1a89b4faaaa58f38", "405d4378de9856db8677d1a8c22ca9285c0dc283"},
			{"003", false, "e5226925ac2b8d1f847b695548bb6b0579029866", "5e77509e305a5fd09f03025992b8fca8688ce5cf"},
		},
		managers: map[string]string{
			"checker":    "8a19f4c13af02e8921d270bbd642624b6667bcea",
			"g.h":        "f9d9cfc153eda2dc08095ad52bf43d55c48568c0",
			"glib.pas":   "d3902e622ed64cea963844de17fceb3926e132ce",
			"grader.c":   "c7fa7a1dea9db5366026350ed95feac936c561ce",
			"grader.cpp": "01d848a52e37660e3106627e28e1c8dca026555b",
		},
		statements: map[string]string{"en": "b6c075ba25de379e9fc1952f4b0078094aefccdb", "it": "41d56e8e832ca17c00402afab07dcd36d45d601d"},
		attachments: map[string]string{
			"input_001.txt": "8e9ae53a34423b7a45337756729786b8cd6e7586",
			"input_002.txt": "a60309e97f8fa44a7a67d67f1a89b4faaaa58f38",
			"input_003.txt": "e5226925ac2b8d1f847b695548bb6b0579029866",
			"sample.txt":    "5f34ec0bfeac225b1c854340257a65b106f70ea6",
		},
	},
	"zcomm": {
		submissionFormat: []string{"zcomm.%l"}, primary: []string{"it"},
		tokens:   importer.Tokens{Mode: "finite", MaxNumber: importer.Ptr(int32(4)), GenInitial: 2, GenNumber: 2, GenInterval: 90 * time.Second},
		feedback: "restricted", scoreMode: "max_tokened_last", description: "Default",
		taskType: "Communication", ttParams: `[2, "grader", "fifo_io"]`, scoreType: "Sum", stParams: `15.0`,
		testcases: []cmsTestcase{
			{"000", false, "c7d3ec8dc8cffd288a52b306e1ee16d7510fa9f6", "29c8f07265979fd17a07e578306380f52d731294"},
			{"001", false, "be3ff71c15e9bedc8bf93d29e47508cb62b4279e", "ac4197890ecd2df735d546956582ca4d38df8825"},
		},
		managers: map[string]string{
			"grader.cpp": "34a6fe12ac58d090610ad9b78feb3f75049fb3db",
			"manager":    "5d637b7f40154329a4aba7095214c90868a3a167",
			"stub.h":     "c8fb2a1d430a60eb0b296b05f01e8af4af3387d9",
		},
		statements:  map[string]string{"it": "19629d9eeefc5ef5498f474249125d3d8e9b64d7"},
		attachments: map[string]string{},
	},
	"zinter": {
		submissionFormat: []string{"zinter.%l"}, primary: []string{"it"},
		tokens:   importer.Tokens{Mode: "disabled", GenInitial: 2, GenNumber: 2, GenInterval: 30 * time.Minute},
		feedback: "restricted", scoreMode: "max_tokened_last", description: "Default",
		taskType: "Interactive", ttParams: `[3, "alone", false, 256, 10, 20.5]`,
		scoreType: "GroupMul", stParams: `[[50.0, "00[01]"], [50, 1]]`,
		testcases: []cmsTestcase{
			{"000", false, "397d543883c5cb5019a0ed08acba13fcb26261c2", "aa0a18d9cd83593d2a3af50c1089adbab4bda7b0"},
		},
		managers:    map[string]string{"controller": "a08777dfef81210bbe1d7b277aa0099713e58249"},
		statements:  map[string]string{"it": "ded5ba42480fe75dcebba1ce068489ff7be2186a"},
		attachments: map[string]string{},
		invalid:     "must have the same type",
	},
}

func TestSyntheticTasksMatchCMS(t *testing.T) {
	root := syntheticTasks(t)
	for name, w := range cmsTaskRows {
		t.Run(name, func(t *testing.T) {
			tk, rep := loadTask(t, filepath.Join(root, name))
			if tk == nil || rep.HasErrors() {
				t.Fatalf("%v\n%s", tk, issues(rep))
			}
			importer.ValidateTask(tk, nil, rep)
			if w.invalid == "" && rep.HasErrors() {
				t.Errorf("invalid task:\n%s", issues(rep))
			}
			if w.invalid != "" && !hasIssue(rep, importer.SeverityError, w.invalid) {
				t.Errorf("no error %q:\n%s", w.invalid, issues(rep))
			}
			if !slices.Equal(tk.SubmissionFormat, w.submissionFormat) || !slices.Equal(tk.PrimaryStatements, w.primary) ||
				!reflect.DeepEqual(tk.Tokens, w.tokens) || !reflect.DeepEqual(tk.MaxSubmissionNumber, w.maxSubmissions) ||
				!reflect.DeepEqual(tk.MinUserTestInterval, w.minUserTestInterval) || tk.FeedbackLevel != w.feedback ||
				tk.ScoreMode != w.scoreMode || tk.ScorePrecision != w.precision {
				t.Errorf("task %+v", tk)
			}
			d := tk.Datasets[0]
			if d.Description != w.description || !reflect.DeepEqual(d.TimeLimit, w.timeLimit) ||
				!reflect.DeepEqual(d.MemoryLimit, w.memoryLimit) || d.TaskType != w.taskType ||
				!sameJSON(t, d.TaskTypeParameters, w.ttParams) || d.ScoreType != w.scoreType ||
				!sameJSON(t, d.ScoreTypeParameters, w.stParams) {
				t.Errorf("dataset %+v %s %s", d, d.TaskTypeParameters, d.ScoreTypeParameters)
			}
			var tcs []cmsTestcase
			for _, tc := range d.Testcases {
				tcs = append(tcs, cmsTestcase{tc.Codename, tc.Public, sha1Of(t, tc.Input), sha1Of(t, tc.Output)})
			}
			if !reflect.DeepEqual(tcs, w.testcases) {
				t.Errorf("testcases\n got %v\nwant %v", tcs, w.testcases)
			}
			files := func(n int, get func(int) (string, *importer.File)) map[string]string {
				out := map[string]string{}
				for i := range n {
					k, f := get(i)
					out[k] = sha1Of(t, f)
				}
				return out
			}
			if got := files(len(d.Managers), func(i int) (string, *importer.File) { return d.Managers[i].Filename, d.Managers[i].File }); !reflect.DeepEqual(got, w.managers) {
				t.Errorf("managers %v, want %v", got, w.managers)
			}
			if got := files(len(tk.Statements), func(i int) (string, *importer.File) { return tk.Statements[i].Language, tk.Statements[i].File }); !reflect.DeepEqual(got, w.statements) {
				t.Errorf("statements %v, want %v", got, w.statements)
			}
			if got := files(len(tk.Attachments), func(i int) (string, *importer.File) { return tk.Attachments[i].Filename, tk.Attachments[i].File }); !reflect.DeepEqual(got, w.attachments) {
				t.Errorf("attachments %v, want %v", got, w.attachments)
			}
		})
	}
}

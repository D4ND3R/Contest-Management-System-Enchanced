// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/tasksubmission.py and
// cmscommon/crypto.py (encrypt_number) of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// scoreLocale is the language of a request as the score types need it
// (scoretype.Translation).
type scoreLocale struct{ l *web.Lang }

func (s scoreLocale) T(msgid string) string { return s.l.T(msgid) }

func (s scoreLocale) FormatDecimal(x float64) string { return s.locale().FormatDecimal(x) }

func (s scoreLocale) FormatDuration(seconds float64) string {
	return s.locale().FormatDuration(seconds, "short")
}

func (s scoreLocale) FormatSize(n int64) string { return s.locale().FormatSize(n) }

func (s scoreLocale) FormatStatusText(text []string) string { return web.FormatStatusText(text, s.l) }

func (s scoreLocale) locale() web.Locale {
	if s.l != nil && s.l.Locale != nil {
		return s.l.Locale
	}
	return web.BasicLocale{Code: "en"}
}

// ScoreLocale returns the language of the request for the score types
// (score details and formatted scores).
func (c *Ctx) ScoreLocale() scoretype.Translation { return scoreLocale{c.Lang} }

// ScoreType returns the score type of the active dataset of a task (CMS
// task.active_dataset.score_type_object), nil if the task has no active
// dataset; one query (the parameters and the testcases of the dataset).
// Invalid parameters are an error, as in CMS.
func (c *Ctx) ScoreType(t *Task) (scoretype.ScoreType, error) {
	if t == nil || t.ActiveDatasetID == nil || !t.HasDataset {
		return nil, nil
	}
	row, err := c.S.st.Q.CWSDatasetScoring(c.Context(), *t.ActiveDatasetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	public := make(map[string]bool, len(row.Codenames))
	for i, cn := range row.Codenames {
		public[cn] = i < len(row.Publics) && row.Publics[i]
	}
	st, err := scoretype.New(row.ScoreType, row.ScoreTypeParameters, public, int(t.ScorePrecision))
	if err != nil {
		return nil, fmt.Errorf("task %s: %w", t.Name, err)
	}
	return st, nil
}

// The statuses of submission results as CMS numbers them
// (SubmissionResult.COMPILING...): the data-status attributes of the
// rows and the status JSON use these numbers.
const (
	statusCompiling         = 1
	statusCompilationFailed = 2
	statusEvaluating        = 3
	statusScoring           = 4
	statusScored            = 5
)

// cmsStatus returns the CMS number of a status.
func cmsStatus(s store.SubmissionStatus) int {
	switch s {
	case store.StatusCompilationFailed:
		return statusCompilationFailed
	case store.StatusEvaluating:
		return statusEvaluating
	case store.StatusScoring:
		return statusScoring
	case store.StatusScored:
		return statusScored
	}
	return statusCompiling
}

// submissionStatusText are the texts of the statuses (CMS
// SubmissionStatusHandler.STATUS_TEXT).
var submissionStatusText = map[int]string{
	statusCompiling:         "Compiling...",
	statusCompilationFailed: "Compilation failed",
	statusEvaluating:        "Evaluating...",
	statusScoring:           "Scoring...",
	statusScored:            "Evaluated",
}

// User test statuses (CMS UserTestResult.COMPILING...).
const (
	utCompiling         = 1
	utCompilationFailed = 2
	utEvaluating        = 3
	utEvaluated         = 4
)

// statusNumbers are the statuses as the templates name them (CMS
// SubmissionResult and UserTestResult constants).
type statusNumbers struct {
	Compiling, CompilationFailed, Evaluating, Scoring, Scored, Evaluated int
}

// JS writes a status number in a script as CMS does ("2", where
// html/template would write " 2 ").
func (statusNumbers) JS(n int) template.JS { return template.JS(strconv.Itoa(n)) }

var statuses = statusNumbers{Compiling: statusCompiling, CompilationFailed: statusCompilationFailed,
	Evaluating: statusEvaluating, Scoring: statusScoring, Scored: statusScored, Evaluated: utEvaluated}

// userTestStatus is CMS's UserTestResult.get_status (no result:
// compiling).
func userTestStatus(hasResult bool, compilation, evaluation *string) int {
	switch {
	case !hasResult || compilation == nil:
		return utCompiling
	case *compilation != "ok":
		return utCompilationFailed
	case evaluation == nil:
		return utEvaluating
	}
	return utEvaluated
}

// encryptNumber is CMS's encrypt_number: n in hex, padded and encrypted
// with AES-CBC and a random IV, in URL-safe base64 ("a-zA-Z0-9.-_"). The
// submit redirection carries it (submission_id) for automatic tests, as
// CMS; the key is derived from secret_key.
func encryptNumber(n int64, key []byte) string {
	pt := []byte(strconv.FormatInt(n, 16))
	pad := 16 - (len(pt)+1)%16
	pt = append(pt, 1)
	pt = append(pt, make([]byte, pad)...)
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return ""
	}
	iv := make([]byte, aes.BlockSize)
	_, _ = rand.Read(iv)
	ct := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, pt)
	s := base64.StdEncoding.EncodeToString(append(iv, ct...))
	return strings.NewReplacer("+", "-", "/", "_", "=", ".").Replace(s)
}

// decryptNumber reverses encryptNumber (for the tests).
func decryptNumber(s string, key []byte) (int64, error) {
	b, err := base64.StdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/", ".", "=").Replace(s))
	if err != nil || len(b) < 32 || len(b)%16 != 0 {
		return 0, errors.New("invalid ciphertext")
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return 0, err
	}
	pt := make([]byte, len(b)-16)
	cipher.NewCBCDecrypter(block, b[:16]).CryptBlocks(pt, b[16:])
	pt = []byte(strings.TrimRight(string(pt), "\x00"))
	if len(pt) == 0 {
		return 0, errors.New("invalid padding")
	}
	return strconv.ParseInt(string(pt[:len(pt)-1]), 16, 64)
}

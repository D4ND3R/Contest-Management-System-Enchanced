// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/formatting.py and
// cms/server/contest/jinja2_toolbox.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// funcs are the functions the contest templates add to those of
// internal/web (CMS CWS_ENVIRONMENT and the parameters of render_params):
//
//	contest_url "a" "b"          URL below the contest (CMS contest_url)
//	contest_time t               contest time of t for the participant
//	                             ("01:23:45", R5.9); "" if unknown
//	format_contest_time t        the time to show where CMS shows a
//	                             time of the contest (submissions,
//	                             messages): the contest time when known,
//	                             else CMS's format_datetime_smart; put
//	                             the absolute time in the title:
//	                             <td title="{{format_datetime .T}}">{{format_contest_time .T}}</td>
//	get_score_class score max    "score_0", "score_0_100" or "score_100"
//	extract_token_params x       the token rules of a *Contest or *Task
//	format_token_rules [type] r  the rules in words; type "contest",
//	                             "task" or none (pipeline:
//	                             {{.Contest | extract_token_params | format_token_rules "contest"}})
//	to_language name             the *language.Language (nil if unknown)
//	submission_files task        the files of a task as the overview shows
//	                             them ("sum[.cpp|.c]")
//	make_timestamp t             CMS make_timestamp: nil means now
//	timezoned_timestamp t        t moved by the server time zone's offset
//	                             (the clock of cws_utils.js)
//	TOKEN_MODE_DISABLED, TOKEN_MODE_FINITE, TOKEN_MODE_INFINITE,
//	TOKEN_MODE_MIXED             the token modes
func (s *Server) funcs(cur func() *web.Render) template.FuncMap {
	ctx := func() *Ctx {
		if rc := cur(); rc != nil {
			if c, ok := rc.Extra.(*Ctx); ok {
				return c
			}
		}
		return nil
	}
	lang := func() *web.Lang {
		if rc := cur(); rc != nil {
			return rc.Lang
		}
		return nil
	}
	now := func() time.Time {
		if rc := cur(); rc != nil && !rc.Now.IsZero() {
			return rc.Now
		}
		return s.now()
	}
	return template.FuncMap{
		"contest_url": func(components ...any) string {
			if c := ctx(); c != nil {
				return c.URL.Join(components...)
			}
			return "."
		},
		"contest_time": func(v any) string {
			t, ok := asTime(v)
			if !ok {
				return ""
			}
			d, ok := ctx().ContestTime(t)
			if !ok {
				return ""
			}
			return FormatContestTime(d)
		},
		"format_contest_time": func(v any) string {
			t, ok := asTime(v)
			if !ok {
				return ""
			}
			if d, ok := ctx().ContestTime(t); ok {
				return FormatContestTime(d)
			}
			rc := cur()
			if rc == nil || rc.Lang == nil || rc.Lang.Locale == nil {
				return t.UTC().Format(time.DateTime)
			}
			loc := rc.TZ
			if loc == nil {
				loc = time.UTC
			}
			return rc.Lang.Locale.FormatDatetimeSmart(t, now(), loc)
		},
		"get_score_class": func(score, maxScore any) string {
			return GetScoreClass(toFloat(score), toFloat(maxScore))
		},
		"extract_token_params": ExtractTokenParams,
		"format_token_rules": func(args ...any) (string, error) {
			if len(args) == 0 || len(args) > 2 {
				return "", fmt.Errorf("format_token_rules: want [type] rules, got %d arguments", len(args))
			}
			rules, ok := args[len(args)-1].(TokenRules)
			if !ok {
				return "", fmt.Errorf("format_token_rules: not token rules: %T", args[len(args)-1])
			}
			tType := ""
			if len(args) == 2 {
				tType, _ = args[0].(string)
			}
			return FormatTokenRules(rules, tType, lang()), nil
		},
		"to_language": func(name string) *language.Language {
			l, _ := s.langs.Lookup(name)
			return l
		},
		"submission_files": func(t *Task) string {
			c := ctx()
			if c == nil || t == nil {
				return ""
			}
			return s.submissionFiles(c.Contest, t)
		},
		"make_timestamp": func(v any) web.Timestamp {
			t, ok := asTime(v)
			if !ok {
				t = now()
			}
			return timestamp(t)
		},
		"timezoned_timestamp": func(v any) web.Timestamp {
			t, ok := asTime(v)
			if !ok {
				t = now()
			}
			loc := time.UTC
			if rc := cur(); rc != nil && rc.TZ != nil {
				loc = rc.TZ
			}
			_, off := t.In(loc).Zone()
			return timestamp(t.Add(time.Duration(off) * time.Second))
		},
		"TOKEN_MODE_DISABLED": func() string { return TokenModeDisabled },
		"TOKEN_MODE_FINITE":   func() string { return TokenModeFinite },
		"TOKEN_MODE_INFINITE": func() string { return TokenModeInfinite },
		"TOKEN_MODE_MIXED":    func() string { return TokenModeMixed },
	}
}

func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, !t.IsZero()
	case *time.Time:
		if t != nil {
			return *t, true
		}
	}
	return time.Time{}, false
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case *float64:
		if x != nil {
			return *x
		}
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// FormatContestTime writes a contest time as the countdown of
// cws_utils.js does ("HH:MM:SS"), with a minus sign before the start.
func FormatContestTime(d time.Duration) string {
	sign := ""
	if d < 0 {
		sign, d = "-", -d
	}
	secs := int64(d / time.Second)
	return fmt.Sprintf("%s%02d:%02d:%02d", sign, secs/3600, secs/60%60, secs%60)
}

// GetScoreClass returns the CSS class of a score (CMS get_score_class).
func GetScoreClass(score, maxScore float64) string {
	switch {
	case score <= 0:
		return "score_0"
	case score >= maxScore:
		return "score_100"
	default:
		return "score_0_100"
	}
}

// TokenRules are the token parameters of a contest or a task (CMS
// extract_token_params: the token_* attributes without the prefix).
type TokenRules struct {
	Mode        string
	MaxNumber   *int32
	MinInterval time.Duration
	GenInitial  int32
	GenNumber   int32
	GenInterval time.Duration
	GenMax      *int32
}

// ExtractTokenParams returns the token rules of a *Contest or a *Task.
func ExtractTokenParams(v any) (TokenRules, error) {
	switch x := v.(type) {
	case *Contest:
		return TokenRules{Mode: x.TokenMode, MaxNumber: x.TokenMaxNumber, MinInterval: x.TokenMinInterval,
			GenInitial: x.TokenGenInitial, GenNumber: x.TokenGenNumber, GenInterval: x.TokenGenInterval,
			GenMax: x.TokenGenMax}, nil
	case *Task:
		return TokenRules{Mode: x.TokenMode, MaxNumber: x.TokenMaxNumber, MinInterval: x.TokenMinInterval,
			GenInitial: x.TokenGenInitial, GenNumber: x.TokenGenNumber, GenInterval: x.TokenGenInterval,
			GenMax: x.TokenGenMax}, nil
	}
	return TokenRules{}, fmt.Errorf("extract_token_params: no token rules in %T", v)
}

// FormatTokenRules describes token rules in words (CMS
// format_token_rules), with the quirks of CMS: the first sentence's
// plural is chosen with gen_initial == 1 (1 or 0), and the intervals are
// floats (minutes, seconds), which choose the plural form as floats and
// are printed with %g.
func FormatTokenRules(t TokenRules, tType string, l *web.Lang) string {
	tr := func(msgid string) string { return l.T(msgid) }
	args := map[string]any{}
	switch tType {
	case "contest":
		args["type_s"], args["type_pl"] = tr("contest-token"), tr("contest-tokens")
	case "task":
		args["type_s"], args["type_pl"] = tr("task-token"), tr("task-tokens")
	default:
		args["type_s"], args["type_pl"] = tr("token"), tr("tokens")
	}
	minInterval := t.MinInterval.Seconds()
	genInterval := t.GenInterval.Seconds() / 60
	args["mode"] = t.Mode
	args["min_interval"] = minInterval
	args["gen_interval"] = genInterval
	args["gen_initial"] = int64(t.GenInitial)
	args["gen_number"] = int64(t.GenNumber)
	if t.MaxNumber != nil {
		args["max_number"] = int64(*t.MaxNumber)
	}
	if t.GenMax != nil {
		args["gen_max"] = int64(*t.GenMax)
	}
	f := func(msg string) string { return i18n.Format(msg, args) }
	// CMS passes floats (and a boolean) to ngettext: the plural form is
	// chosen on them as Python's gettext does.
	tn := func(one, many string, n float64) string { return f(l.TnFloat(one, many, n)) }

	var b strings.Builder
	switch t.Mode {
	case TokenModeDisabled:
		// Only shown on tasks when the modes are mixed.
		b.WriteString(f(tr("You don't have %(type_pl)s available for this task.")))
	case TokenModeInfinite:
		b.WriteString(f(tr("You have an infinite number of %(type_pl)s for this task.")))
	case TokenModeFinite:
		if t.GenInitial == 0 {
			b.WriteString(f(tr("You start with no %(type_pl)s.")))
		} else {
			n := 0.0
			if t.GenInitial == 1 {
				n = 1
			}
			b.WriteString(tn("You start with one %(type_s)s.", "You start with %(gen_initial)d %(type_pl)s.", n))
		}
		b.WriteString(" ")
		if t.GenNumber > 0 {
			b.WriteString(tn("Every minute ", "Every %(gen_interval)g minutes ", genInterval))
			if t.GenMax != nil {
				b.WriteString(tn("you get another %(type_s)s, ", "you get %(gen_number)d other %(type_pl)s, ", float64(t.GenNumber)))
				b.WriteString(tn("up to a maximum of one %(type_s)s.", "up to a maximum of %(gen_max)d %(type_pl)s.", float64(*t.GenMax)))
			} else {
				b.WriteString(tn("you get another %(type_s)s.", "you get %(gen_number)d other %(type_pl)s.", float64(t.GenNumber)))
			}
		} else {
			b.WriteString(f(tr("You don't get other %(type_pl)s.")))
		}
		b.WriteString(" ")
		switch {
		case minInterval > 0 && t.MaxNumber != nil:
			b.WriteString(tn("You can use a %(type_s)s every second ", "You can use a %(type_s)s every %(min_interval)g seconds ", minInterval))
			b.WriteString(tn("and no more than one %(type_s)s in total.", "and no more than %(max_number)d %(type_pl)s in total.", float64(*t.MaxNumber)))
		case minInterval > 0:
			b.WriteString(tn("You can use a %(type_s)s every second.", "You can use a %(type_s)s every %(min_interval)g seconds.", minInterval))
		case t.MaxNumber != nil:
			b.WriteString(tn("You can use no more than one %(type_s)s in total.", "You can use no more than %(max_number)d %(type_pl)s in total.", float64(*t.MaxNumber)))
		default:
			b.WriteString(f(tr("You have no limitations on how you use them.")))
		}
	}
	return b.String()
}

// submissionFiles is the "Files" column of the overview: the submission
// format with ".%l" replaced by the extensions of the task's languages
// ("sum[.cpp|.c|.py]").
func (s *Server) submissionFiles(c *Contest, t *Task) string {
	var exts []string
	seen := map[string]bool{}
	for _, name := range t.AllowedLanguages(c) {
		l, ok := s.langs.Lookup(name)
		if !ok {
			continue
		}
		e := l.SourceExtension()
		if !seen[e] {
			seen[e] = true
			exts = append(exts, e)
		}
	}
	repl := "[" + strings.Join(exts, "|") + "]"
	files := make([]string, len(t.SubmissionFormat))
	for i, f := range t.SubmissionFormat {
		files[i] = strings.ReplaceAll(f, ".%l", repl)
	}
	return strings.Join(files, " ")
}

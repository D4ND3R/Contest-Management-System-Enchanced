// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/__init__.py of CMS (format_status_text):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package web

// Translator translates msgids (Lang implements it).
type Translator interface {
	T(msgid string) string
}

// FormatStatusText formats a status text (CMS format_status_text): the
// compilation text of a submission result, the text of an evaluation or
// the texts of a user test. The first element is a msgid with
// printf-like placeholders ("Evaluation didn't produce file %s"), the
// others fill them. The msgid is translated by tr (nil: English); an
// empty msgid stays empty. Anything malformed (no elements, placeholders
// and arguments that do not match) gives the translation of "N/A", as in
// CMS.
func FormatStatusText(status []string, tr Translator) string {
	t := func(s string) string {
		if tr == nil {
			return s
		}
		return tr.T(s)
	}
	if len(status) == 0 {
		return t("N/A")
	}
	text := ""
	if status[0] != "" {
		text = t(status[0])
	}
	args := make([]any, len(status)-1)
	for i, a := range status[1:] {
		args[i] = a
	}
	s, err := PyFormat(text, args...)
	if err != nil {
		return t("N/A")
	}
	return s
}

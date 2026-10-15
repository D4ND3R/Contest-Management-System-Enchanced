// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package l10n formats dates, times, durations, sizes, numbers and
// language names exactly as CMS does: it is a port of the Translation
// class of cms/locale/locale.py, which delegates to Babel 2.12.1 (CLDR 42).
//
// One *Locale exists per language of the contestant site: English plus the
// 25 catalogs of locale/. Get accepts a catalog code or a CMS identifier
// ("es_CL", "es-CL") and falls back to the same language and script, then
// to the base language and finally to English, so it never returns nil;
// the data of a language is parsed on first use and shared. The methods
// match CMS's one to one (they satisfy internal/web.Locale):
//
//	Identifier, Name            translation.identifier, translation.name
//	FormatDatetime, FormatTime  format_datetime, format_time (medium)
//	FormatDatetimeSmart         format_datetime_smart (time only if today)
//	FormatTimedelta             format_timedelta ("1 hour and 5 minutes")
//	FormatDuration              format_duration ("0.123 sec", "1.000 second")
//	FormatSize                  format_size ("512 bytes", "1.50 MiB")
//	FormatDecimal               format_decimal ("1,234.568")
//	FormatLocale                format_locale ("Spanish (Chile)")
//	PluralForm, PluralFormInt   locale.plural_form: the CLDR category
//
// Times are instants; they are shown in the time zone passed to each call
// (the single server time zone of D6).
//
// # Data
//
// data/*.json holds only the CLDR data those methods read (medium date and
// time patterns, month, day and period names, day period rules, decimal
// pattern and symbols, plural rules, unit and list patterns, display names
// of the languages, territories, scripts and variants Babel has locales
// for, and the alias and likely-subtag tables Locale.parse uses). It is
// generated from Babel by tools/l10n/extract.py and committed:
//
//	make l10n                              # needs babel==2.12.1
//	make l10n L10N_PYTHON=/path/to/python  # e.g. the virtualenv of a CMS
//
// The same script writes testdata/golden.json with Babel's own outputs for
// a grid of values in every locale (dates of several years in four server
// time zones, every hour of a day, durations from 0.001 to 3700 s,
// timedeltas, sizes from 0 to 10 GiB and beyond, decimals, locale codes,
// plural operands); TestGolden must reproduce it byte for byte.
// `extract.py --check-cms` additionally compares that grid with CMS's
// Translation class, and `extract.py --random N PATH` writes a larger
// random grid for a one-off run with L10N_GOLDEN=PATH. The data is under
// the Unicode license (CLDR) and the algorithms follow Babel's
// (BSD-3-Clause): see data/README.
//
// # Faithfulness
//
// Babel quirks are reproduced on purpose, since they are what CMS shows:
// numbers use the repr of the float and round half to even; there is no
// minimum grouping (Spanish "1.234"); negative numbers take an ASCII
// hyphen; plural operands v/w/f/t come from Babel's slicing of the decimal
// digits (0.05 has v = 1); a negative number that rounds to zero keeps
// its sign ("-0"); the datetime glue pattern loses its quotes; a locale
// name gets parentheses whenever the code has a territory, script or
// variant, even if none of them has a name ("Name ()").
// The deliberate differences are where CMS fails: when Babel has no name
// for a parseable locale ("root") CMS prints None and FormatLocale returns
// the code; Babel raises for the plural form of NaN and infinities
// (PluralForm returns "other") and for numbers that need more than 28
// digits (they are formatted exactly).
//
// The size units ("%s KiB", ...) go through gettext in CMS, but no catalog
// translates them (they are marked N_ and never extracted), so FormatSize
// uses the msgids as they are; the generator fails if that ever changes.
package l10n

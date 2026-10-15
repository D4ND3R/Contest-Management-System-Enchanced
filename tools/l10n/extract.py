#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# The reference implementation below (class Reference) is ported from the
# Translation class in cms/locale/locale.py of CMS:
# Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
# Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
# Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
# Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
# Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
# Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
# Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
# Copyright © 2015 William Di Luigi <williamdiluigi@gmail.com>
# Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
# Copyright © 2018 Edoardo Morassutto <edoardo.morassutto@gmail.com>

"""Generate the CLDR tables of internal/l10n and their golden outputs.

CMS formats dates, durations, sizes and numbers with Babel 2.12.1 (CLDR 42)
in cms/locale/locale.py. This script dumps exactly the CLDR data those
methods read, for English and every language in locale/, into
internal/l10n/data/*.json, and writes internal/l10n/testdata/golden.json
with Babel's outputs for a grid of values: the Go tests must reproduce it
byte for byte.

Run it with `make l10n` (L10N_PYTHON=/path/to/python if babel 2.12.1 is
not in the default python3). Its output is committed; it is not part of
`make generate`.

    --check-cms   also run the grid through CMS's own Translation class
                  (needs CMS installed in the same Python, and CMS_CONFIG)
                  and fail if the reference copy below disagrees with it.
    --random N PATH
                  write only a random grid of N values of each kind to PATH
                  (dates up to 2100, more time zones, random numbers, sizes
                  and locale codes), for a one-off check that is not
                  committed: L10N_GOLDEN=PATH go test -run TestGolden
                  ./internal/l10n
"""

import argparse
import copy
import datetime
import json
import math
import pathlib
import random
import sys

import babel
import babel.core
import babel.dates
import babel.lists
import babel.localedata
import babel.numbers
import babel.units
from babel.messages.pofile import read_po

BABEL_VERSION = "2.12.1"
CLDR_VERSION = "42"

REPO = pathlib.Path(__file__).resolve().parents[2]
DATA_DIR = REPO / "internal" / "l10n" / "data"
GOLDEN = REPO / "internal" / "l10n" / "testdata" / "golden.json"

# Units read by format_timedelta, format_duration and format_size.
UNITS = ["duration-day", "duration-hour", "duration-minute",
         "duration-second", "digital-byte"]
LENGTHS = ["long", "short", "narrow"]
PLURAL_TAGS = ("zero", "one", "two", "few", "many", "other")
# Fields the Go pattern engine implements (internal/l10n/dates.go).
DATE_FIELDS = set("yYuMLdDFEabBhHKkms")
# Size units of Translation.format_size: msgids marked with N_ but never
# extracted to cms.pot, so no catalog translates them.
SIZE_UNITS = ["%s KiB", "%s MiB", "%s GiB", "%s TiB"]


# ---------------------------------------------------------------- reference

class Reference:
    """The formatting methods of cms.locale.locale.Translation, unchanged
    except that gettext is the identity (see check_size_units)."""

    def __init__(self, lang_code):
        self.locale = babel.core.Locale.parse(lang_code)

    @property
    def identifier(self):
        return babel.core.get_locale_identifier(
            (self.locale.language, self.locale.territory,
             self.locale.script, self.locale.variant), sep="-")

    @property
    def name(self):
        return self.locale.display_name

    def gettext(self, msgid):
        return msgid

    def format_datetime(self, dt, timezone):
        return babel.dates.format_datetime(dt, tzinfo=timezone,
                                           locale=self.locale)

    def format_time(self, dt, timezone):
        return babel.dates.format_time(dt, tzinfo=timezone, locale=self.locale)

    def format_datetime_smart(self, dt, now, timezone):
        utc = datetime.timezone.utc
        dt_date = dt.replace(tzinfo=utc).astimezone(timezone).date()
        now_date = now.replace(tzinfo=utc).astimezone(timezone).date()
        if dt_date == now_date:
            return self.format_time(dt, timezone)
        else:
            return self.format_datetime(dt, timezone)

    SECONDS_PER_HOUR = 60 * 60
    SECONDS_PER_MINUTE = 60

    def format_timedelta(self, td):
        td = abs(td)
        res = []
        if td.days > 0:
            res.append(babel.units.format_unit(td.days, "duration-day",
                                               locale=self.locale))
        secs = td.seconds
        if secs >= self.SECONDS_PER_HOUR:
            res.append(babel.units.format_unit(secs // self.SECONDS_PER_HOUR,
                                               "duration-hour",
                                               locale=self.locale))
            secs %= self.SECONDS_PER_HOUR
        if secs >= self.SECONDS_PER_MINUTE:
            res.append(babel.units.format_unit(secs // self.SECONDS_PER_MINUTE,
                                               "duration-minute",
                                               locale=self.locale))
            secs %= self.SECONDS_PER_MINUTE
        if secs > 0:
            res.append(babel.units.format_unit(secs, "duration-second",
                                               locale=self.locale))
        if len(res) == 0:
            res.append(babel.units.format_unit(0, "duration-second",
                                               locale=self.locale))
        return babel.lists.format_list(res, locale=self.locale)

    def format_duration(self, d, length="short"):
        d = abs(d)
        f = copy.copy(self.locale.decimal_formats[None])
        f.frac_prec = (3, 3)
        return babel.units.format_unit(
            d, "duration-second", length=length, format=f, locale=self.locale)

    PREFIX_FACTOR = 1024
    SIZE_UNITS = [None] + SIZE_UNITS

    def format_size(self, n):
        n = abs(n)
        if n < self.PREFIX_FACTOR:
            return babel.units.format_unit(
                round(n), "digital-byte", locale=self.locale)
        for unit in self.SIZE_UNITS[1:]:
            n /= self.PREFIX_FACTOR
            if n < self.PREFIX_FACTOR:
                f = copy.copy(self.locale.decimal_formats[None])
                d = max(0, 2 - math.floor(math.log10(n)))
                f.frac_prec = (d, d)
                return (self.gettext(unit)
                        % babel.numbers.format_decimal(n, format=f,
                                                       locale=self.locale))
        return (self.gettext(self.SIZE_UNITS[-1])
                % babel.numbers.format_decimal(round(n), locale=self.locale))

    def format_decimal(self, n):
        return babel.numbers.format_decimal(n, locale=self.locale)

    def format_locale(self, code):
        try:
            return babel.core.Locale.parse(code).get_display_name(self.locale)
        except (ValueError, babel.core.UnknownLocaleError):
            return code


# ------------------------------------------------------------------- inputs

UTC = datetime.timezone.utc
ZONES = ["UTC", "Europe/Rome", "America/Santiago", "Asia/Kolkata"]


def naive(s):
    """A naive UTC datetime, as CMS stores them."""
    return datetime.datetime.fromisoformat(s)


DATETIMES = [naive(s) for s in [
    "2026-10-14T09:05:03.250000", "2026-01-01T00:00:00",
    "2025-12-31T23:59:59.999999", "2026-03-29T01:30:00",
    "2026-07-04T12:00:00", "2026-07-04T06:27:00", "2026-02-28T18:45:30",
    "2026-11-15T21:10:00", "1999-05-09T03:04:05", "2031-08-20T14:00:00",
    "2026-04-05T15:30:00", "2026-09-10T11:59:59", "2008-02-29T23:00:00",
    "2026-12-31T13:00:01",
]]
# One per hour of a day, in UTC only: every day period of every locale.
HOURS = [datetime.datetime(2026, 6, 1, h, 30, 15) for h in range(24)] + [
    datetime.datetime(2026, 6, 1, 0, 0, 0),
    datetime.datetime(2026, 6, 1, 12, 0, 0)]
SMART_NOW = [naive("2026-10-14T10:00:00"), naive("2026-10-14T23:30:00"),
             naive("2026-01-01T02:00:00")]
SMART_DTS = [naive(s) for s in [
    "2026-10-14T08:00:00", "2026-10-14T23:59:59", "2026-10-13T23:59:59",
    "2026-10-15T00:00:00", "2026-10-14T00:00:00", "2025-10-14T10:00:00",
    "2025-12-31T23:30:00", "2026-01-01T00:30:00",
]]
TIMEDELTAS = [  # microseconds
    0, 1, 500000, -1500000, 1000000, 2000000, 59000000, 60000000,
    61000000, 119000000, 3600000000, 3601000000, 3661000000, 7322000000,
    18000000000, 86399000000, 86400000000, 86401000000, 90061000000,
    172800000000, 180000000000, 1814400000000, 8726461000000,
    -90000000, 5 * 3600 * 10**6 + 30 * 60 * 10**6,
]
DURATIONS = [
    0.0, 0.001, 0.0005, 0.0015, 0.0025, 0.002, 0.01, 0.1, 0.123, 0.1234,
    0.999, 0.9995, 0.9994, 1.0, 1.0005, 1.5, 2.0, 2.5, 3.0, 4.0, 5.0, 7.0,
    10.0, 11.0, 21.0, 22.0, 59.999, 60.0, 100.5, 101.0, 1000.0,
    1234.5678, 3700.0, -1.5, 0.1 + 0.2, 1e-05, 2.0000000001, 0.05,
    1.25, 0.02, 3.14159, 1.1,
]
SIZES = [
    0, 1, 2, 3, 5, 11, 21, 22, 100, 999, 1000, 1023, 1024, 1025, 1152, 1264,
    1408, 1536, 10000, 10240, 10496, 102399, 102400, 102912, 103936,
    1023 * 1024, 1048575, 1048576, 1048577, 5767168, 67108864, 268435456,
    1073741824, 1610612736, 10737418240, 1099511627775, 1099511627776,
    5 * 2**40, 1023 * 2**40, 2**50, 2**50 + 2**39, 3 * 2**50 + 2**49,
    2**62, -1024,
]
DECIMALS = [
    0.0, -0.0, 1.0, -1.0, 0.5, 1.5, 2.5, 0.0005, 0.0015, 1.0005, 1.0015,
    2.675, 0.1 + 0.2, 33.333333333333336, 66.66666666666667, 99.9995,
    99.9994, 100.0, 1234.0, 1234.5678, 12345.6789, 1234567.891, 1e6, 1e15,
    1e16, 1e19, 123456789012.345, 1e-05, -0.0001, -1234.5,
    float("inf"), float("-inf"), float("nan"), 7.0, 42.5, 0.123456,
]
LOCALE_CODES = [
    "en", "it", "es", "es_CL", "es-CL", "es_cl", "zh_CN", "zh_TW", "zh",
    "zh_Hant", "zh_HK", "zh_SG", "pt_BR", "pt", "en_US", "en_GB", "en_150",
    "es_419", "fr_CA", "de_AT", "de_CH", "nl_BE", "sr_Latn", "sr_ME", "sh",
    "und_IT", "und", "root", "", "xx", "EN", "en_us", "ca_ES_VALENCIA",
    "en_US_POSIX", "it_IT@euro", "it@euro", "de_DE.UTF-8", "ar", "he", "iw",
    "in", "no", "tl", "mo", "ja_JP", "ko_KR", "ru_RU", "uk", "fy", "az_Cyrl",
    "uz_Arab", "ckb", "fil", "haw", "yue", "yue_Hans", "en_XX", "ita", "eng",
    "spa", "deu", "ger", "fre", "fra", "chi", "zho", "bs", "bs_Cyrl", "et",
    "lt", "lv", "ro", "sl", "th", "vi", "hu", "cs", "bg", "it_CH", "it_SM",
    "fr_BE", "en_AU", "en_IN", "es_MX", "es_US", "kk", "hy", "ka", "sq",
    "mk", "be", "ms", "id", "fa", "hi", "bn", "ta", "te", "ur", "sw", "tr",
    "pl", "sv", "da", "nb", "fi", "el", "is", "ga", "cy", "mn", "ky", "tg",
    "1en", "en_US_x", "en-US", "EN_us", "zh_Hans_CN", "zh_Hant_TW", "sr",
    "sr_Cyrl_BA", "ha_NE", "pa_Arab", "en_001",
]
PLURALS = [
    0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 11.0, 12.0, 14.0, 19.0, 20.0,
    21.0, 22.0, 25.0, 100.0, 101.0, 102.0, 111.0, 1000000.0, 0.5, 1.5,
    0.1, 1.25, 0.001, 0.05, 2.1, 10.5, 1e-05,
]


# --------------------------------------------------------------------- data

def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def write_json(path, obj):
    """One top-level key per line: compact but reviewable in diffs."""
    lines = [f"{compact(k)}:{compact(v)}" for k, v in obj.items()]
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{\n" + ",\n".join(lines) + "\n}\n", encoding="utf-8")


def locale_codes():
    codes = sorted(p.parent.parent.name
                   for p in (REPO / "locale").glob("*/LC_MESSAGES/cms.po"))
    return ["en"] + codes


def check_size_units(codes):
    """CMS passes the size units through gettext: make sure no catalog
    translates them, so that the Go side can use the msgids as they are."""
    for code in codes[1:]:
        path = REPO / "locale" / code / "LC_MESSAGES" / "cms.po"
        with path.open("rb") as f:
            catalog = read_po(f)
        for msgid in SIZE_UNITS:
            msg = catalog.get(msgid)
            if msg is not None and msg.string and not msg.fuzzy:
                sys.exit(f"{path}: {msgid!r} is translated; teach "
                         "internal/l10n to use the cms catalog first")


def plural_rule(ast):
    """Serialize a babel plural AST back to CLDR syntax (a subset that
    internal/l10n/plural.go parses)."""
    op, args = ast
    if op == "or":
        return f"{plural_rule(args[0])} or {plural_rule(args[1])}"
    if op == "and":
        return f"{plural_rule(args[0])} and {plural_rule(args[1])}"
    if op == "not":
        inner = args[0]
        if inner[0] != "relation":
            raise ValueError(f"unexpected negation of {inner!r}")
        method, expr, ranges = inner[1]
        word = "!=" if method == "in" else "not within"
        return f"{plural_expr(expr)} {word} {plural_ranges(ranges)}"
    if op == "relation":
        method, expr, ranges = args
        word = "=" if method == "in" else "within"
        return f"{plural_expr(expr)} {word} {plural_ranges(ranges)}"
    if op in ("is", "isnot"):
        expr, value = args
        word = "=" if op == "is" else "!="
        return f"{plural_expr(expr)} {word} {value[1][0]}"
    raise ValueError(f"unexpected plural node {op!r}")


def plural_expr(expr):
    if expr[0] == "mod":
        (name, _), value = expr[1]
        return f"{name} % {value[1][0]}"
    return expr[0]


def plural_ranges(node):
    assert node[0] == "range_list"
    out = []
    for lo, hi in node[1]:
        lo, hi = lo[1][0], hi[1][0]
        out.append(str(lo) if lo == hi else f"{lo}..{hi}")
    return ",".join(out)


def check_date_pattern(code, pattern):
    for kind, value in babel.dates.tokenize_pattern(pattern):
        if kind == "field" and value[0] not in DATE_FIELDS:
            sys.exit(f"{code}: pattern {pattern!r} uses field {value[0]!r}, "
                     "which internal/l10n does not implement")


def check_placeholders(code, what, pattern, allowed):
    stripped = pattern
    for p in allowed:
        stripped = stripped.replace(p, "")
    if "{" in stripped or "}" in stripped:
        sys.exit(f"{code}: {what} {pattern!r} has unexpected placeholders")


def name_keys():
    """The language, territory, script and variant codes that
    Locale.parse can return: those of the locales Babel has data for."""
    langs, terrs, scripts, variants = set(), set(), set(), set()
    for ident in babel.localedata.locale_identifiers():
        lang, terr, script, variant = babel.core.parse_locale(ident)[:4]
        langs.add(lang)
        if terr:
            terrs.add(terr)
        if script:
            scripts.add(script)
        if variant:
            variants.add(variant)
    return {"languages": sorted(langs), "territories": sorted(terrs),
            "scripts": sorted(scripts), "variants": sorted(variants)}


def global_data(keys):
    ids = sorted(babel.localedata.locale_identifiers() + ["root"])
    known_langs = set(keys["languages"])
    likely = {}
    for key, value in babel.core.get_global("likely_subtags").items():
        # Entries whose language has no data can never load a locale:
        # dropping them does not change what Locale.parse returns.
        if babel.core.parse_locale(value)[0] in known_langs:
            likely[key] = value
    territory_aliases = {k: v[0] for k, v in
                         babel.core.get_global("territory_aliases").items()}
    return {
        "babel": BABEL_VERSION,
        "cldr": CLDR_VERSION,
        "locales": ids,
        "languageAliases": dict(sorted(
            babel.core.get_global("language_aliases").items())),
        "territoryAliases": dict(sorted(territory_aliases.items())),
        "scriptAliases": dict(sorted(
            babel.core.get_global("script_aliases").items())),
        "variantAliases": dict(sorted(
            babel.core.get_global("variant_aliases").items())),
        "likelySubtags": dict(sorted(likely.items())),
        **keys,
    }


def locale_data(code, keys):
    loc = babel.core.Locale.parse(code)
    ref = Reference(code)
    date_fmt = loc.date_formats["medium"].pattern
    time_fmt = loc.time_formats["medium"].pattern
    dt_fmt = loc.datetime_formats["medium"]
    for p in (date_fmt, time_fmt):
        check_date_pattern(code, p)
    check_placeholders(code, "datetime format", dt_fmt, ["{0}", "{1}"])

    months = {ctx: {w: [loc.months[ctx][w][m] for m in range(1, 13)]
                    for w in ("abbreviated", "wide", "narrow")}
              for ctx in ("format", "stand-alone")}
    days = {"format": {w: [loc.days["format"][w][d] for d in range(7)]
                       for w in ("abbreviated", "wide", "narrow", "short")}}
    periods = {ctx: {w: dict(loc.day_periods[ctx][w].items())
                     for w in ("abbreviated", "wide", "narrow")}
               for ctx in ("format", "stand-alone")}
    period_rules = [[rid, [dict(sorted(r.items())) for r in rules]]
                    for rid, rules in loc.day_period_rules.get(None, {}).items()]

    plural = [[tag, plural_rule(ast)] for tag, ast in loc.plural_form.abstract]

    units, unit_names = {}, {}
    for unit in UNITS:
        q = babel.units._find_unit_pattern(unit, locale=loc)
        if q != unit:
            sys.exit(f"{code}: unit {unit} resolves to {q}")
        units[unit] = {}
        unit_names[unit] = {}
        for length in LENGTHS:
            pats = loc._data["unit_patterns"][unit].get(length, {})
            forms = {t: pats[t] for t in PLURAL_TAGS if t in pats}
            for p in forms.values():
                check_placeholders(code, "unit pattern", p, ["{0}"])
            if forms:
                units[unit][length] = forms
            name = babel.units.get_unit_name(unit, length=length, locale=loc)
            if name is not None:
                unit_names[unit][length] = name

    lists = dict(loc.list_patterns["standard"].items())
    for p in lists.values():
        check_placeholders(code, "list pattern", p, ["{0}", "{1}"])

    def names(table, key_list):
        return [table.get(k) for k in key_list]

    return {
        "code": code,
        "identifier": ref.identifier,
        "name": ref.name,
        "babel": str(loc),
        "datetimeFormat": dt_fmt,
        "dateFormat": date_fmt,
        "timeFormat": time_fmt,
        "months": months,
        "days": days,
        "periods": periods,
        "periodRules": period_rules,
        "decimalFormat": loc.decimal_formats[None].pattern,
        "symbols": {
            "decimal": babel.numbers.get_decimal_symbol(loc),
            "group": babel.numbers.get_group_symbol(loc),
            "infinity": babel.numbers.get_infinity_symbol(loc),
        },
        "plural": plural,
        "units": units,
        "unitNames": unit_names,
        "list": {k: lists[k] for k in ("start", "middle", "end", "2")},
        "languages": names(loc.languages, keys["languages"]),
        "territories": names(loc.territories, keys["territories"]),
        "scripts": names(loc.scripts, keys["scripts"]),
        "variants": names(loc.variants, keys["variants"]),
    }


# ------------------------------------------------------------------- golden

def iso(dt):
    return dt.replace(tzinfo=UTC).isoformat().replace("+00:00", "Z")


def default_grid():
    return {
        "zones": ZONES, "datetimes": DATETIMES, "hours": HOURS,
        "smartNow": SMART_NOW, "smart": SMART_DTS, "timedeltas": TIMEDELTAS,
        "durations": DURATIONS, "lengths": LENGTHS, "sizes": SIZES,
        "decimals": DECIMALS, "localeCodes": LOCALE_CODES, "plurals": PLURALS,
    }


def random_grid(n, seed):
    """A larger random grid, for a one-off check that is not committed:
    extract.py --random N PATH, then L10N_GOLDEN=PATH go test -run
    TestGolden ./internal/l10n."""
    rnd = random.Random(seed)
    epoch = datetime.datetime(1970, 1, 1)

    def instant(lo, hi):
        return epoch + datetime.timedelta(
            seconds=rnd.randint(lo, hi), microseconds=rnd.randint(0, 999999))

    def number(top):
        # Few decimals (ties of the rounding included) or any float.
        x = 10 ** rnd.uniform(-7, top)
        kind = rnd.random()
        if kind < 0.3:
            x = round(x, rnd.randint(0, 6))
        elif kind < 0.5:
            x = math.floor(x * 1000) / 1000 + 0.0005
        elif kind < 0.6:
            x = float(rnd.randint(0, 30))
        return x

    keys = name_keys()
    parts = [keys["languages"], keys["scripts"] + list(
        babel.core.get_global("script_aliases")), keys["territories"] + list(
        babel.core.get_global("territory_aliases"))[:60], keys["variants"]]
    codes = []
    for _ in range(n):
        code = rnd.choice(parts[0] + list(
            babel.core.get_global("language_aliases"))[:80])
        for i, p in enumerate(parts[1:], 1):
            if rnd.random() < (0.3, 0.6, 0.1)[i - 1]:
                code += "_" + rnd.choice(p)
        if rnd.random() < 0.05:
            code += "@euro"
        codes.append(code)

    now = [instant(0, 4102444800) for _ in range(6)]
    smart = [t + datetime.timedelta(hours=rnd.uniform(-50, 50))
             for t in now for _ in range(4)]
    sizes = [rnd.randint(0, 2 ** rnd.randint(0, 63) - 1) for _ in range(n)]
    sizes += [1024 ** k + d for k in range(1, 6) for d in (-1, 0, 1)]
    return {
        "zones": ZONES + ["America/New_York", "Australia/Lord_Howe",
                          "Pacific/Chatham", "Asia/Kathmandu"],
        "datetimes": [instant(0, 4102444800) for _ in range(n)],
        "hours": HOURS,
        "smartNow": now,
        "smart": smart,
        "timedeltas": [rnd.choice((1, -1)) * int(10 ** rnd.uniform(0, 13))
                       for _ in range(n)],
        "durations": [rnd.choice((1, -1)) * number(5) for _ in range(n)],
        "lengths": LENGTHS,
        "sizes": sizes,
        "decimals": [rnd.choice((1, -1)) * number(16) for _ in range(n)],
        "localeCodes": codes,
        "plurals": [number(7) for _ in range(n)],
    }


def golden(codes, impl_for, grid):
    inputs = {
        "zones": grid["zones"],
        "datetimes": [iso(d) for d in grid["datetimes"]],
        "hours": [iso(d) for d in grid["hours"]],
        "smartNow": [iso(d) for d in grid["smartNow"]],
        "smart": [iso(d) for d in grid["smart"]],
        "timedeltas": grid["timedeltas"],
        "durations": [repr(x) for x in grid["durations"]],
        "lengths": grid["lengths"],
        "sizes": grid["sizes"],
        "decimals": [repr(x) for x in grid["decimals"]],
        "localeCodes": grid["localeCodes"],
        "plurals": [repr(x) for x in grid["plurals"]],
    }
    out = {"babel": BABEL_VERSION, "inputs": inputs, "locales": {}}
    zones = {z: babel.dates.get_timezone(z) for z in grid["zones"]}
    for code in codes:
        t = impl_for(code)
        loc = babel.core.Locale.parse(code)
        res = {
            "identifier": t.identifier,
            "name": t.name,
            "datetime": [[t.format_datetime(d, zones[z])
                          for d in grid["datetimes"]] for z in grid["zones"]],
            "time": [[t.format_time(d, zones[z]) for d in grid["datetimes"]]
                     for z in grid["zones"]],
            "hoursDatetime": [t.format_datetime(d, UTC)
                              for d in grid["hours"]],
            "hoursTime": [t.format_time(d, UTC) for d in grid["hours"]],
            "smart": [[[t.format_datetime_smart(d, now, zones[z])
                        for d in grid["smart"]] for now in grid["smartNow"]]
                      for z in grid["zones"]],
            "timedelta": [t.format_timedelta(
                datetime.timedelta(microseconds=us))
                for us in grid["timedeltas"]],
            "duration": [[t.format_duration(x, length)
                          for x in grid["durations"]]
                         for length in grid["lengths"]],
            "durationDefault": [t.format_duration(x)
                                for x in grid["durations"][:5]],
            "size": [t.format_size(n) for n in grid["sizes"]],
            "decimal": [t.format_decimal(x) for x in grid["decimals"]],
            "locale": [t.format_locale(c) for c in grid["localeCodes"]],
            "plural": [loc.plural_form(x) for x in grid["plurals"]],
        }
        out["locales"][code] = res
    return out


def write_golden(obj, path):
    lines = []
    lines.append(f'"babel":{compact(obj["babel"])}')
    lines.append('"inputs":{\n' + ",\n".join(
        f" {compact(k)}:{compact(v)}" for k, v in obj["inputs"].items())
        + "\n}")
    locs = []
    for code, res in obj["locales"].items():
        body = ",\n".join(f"  {compact(k)}:{compact(v)}"
                          for k, v in res.items())
        locs.append(f" {compact(code)}:{{\n{body}\n }}")
    lines.append('"locales":{\n' + ",\n".join(locs) + "\n}")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{\n" + ",\n".join(lines) + "\n}\n", encoding="utf-8")


def check_cms(codes, mine, grid):
    """Compare the reference copy with CMS's own Translation class."""
    from cms.locale.locale import Translation  # noqa: needs CMS installed

    theirs = golden(codes, Translation, grid)
    if theirs != mine:
        for code in codes:
            a, b = mine["locales"][code], theirs["locales"][code]
            for key in a:
                if a[key] != b[key]:
                    print(f"{code}.{key}: reference {a[key]!r}\n"
                          f"  CMS {b[key]!r}", file=sys.stderr)
        sys.exit("the reference copy disagrees with CMS's Translation")
    print("reference copy matches cms.locale.locale.Translation")


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--check-cms", action="store_true",
                        help="compare with CMS's Translation class")
    parser.add_argument("--random", nargs=2, metavar=("N", "PATH"),
                        help="only write a random grid of N values per "
                        "kind to PATH (for L10N_GOLDEN)")
    parser.add_argument("--seed", type=int, default=1,
                        help="seed of --random")
    args = parser.parse_args()
    if babel.__version__ != BABEL_VERSION:
        sys.exit(f"babel {BABEL_VERSION} is required (CMS pins it), "
                 f"found {babel.__version__}")

    codes = locale_codes()
    if args.random:
        grid = random_grid(int(args.random[0]), args.seed)
        result = golden(codes, Reference, grid)
        write_golden(result, pathlib.Path(args.random[1]))
        if args.check_cms:
            check_cms(codes, result, grid)
        print(f"wrote a random grid to {args.random[1]}")
        return

    check_size_units(codes)
    keys = name_keys()
    write_json(DATA_DIR / "global.json", global_data(keys))
    index = {}
    for code in codes:
        data = locale_data(code, keys)
        index[code] = data["identifier"]
        write_json(DATA_DIR / f"{code}.json", data)
    write_json(DATA_DIR / "locales.json", index)

    grid = default_grid()
    result = golden(codes, Reference, grid)
    write_golden(result, GOLDEN)
    if args.check_cms:
        check_cms(codes, result, grid)
    print(f"wrote {len(codes)} locales to {DATA_DIR.relative_to(REPO)} "
          f"and {GOLDEN.relative_to(REPO)}")


if __name__ == "__main__":
    main()

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package l10n

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// golden is testdata/golden.json, written by tools/l10n/extract.py with
// Babel's (and so CMS's) outputs.
type golden struct {
	Babel  string `json:"babel"`
	Inputs struct {
		Zones       []string `json:"zones"`
		Datetimes   []string `json:"datetimes"`
		Hours       []string `json:"hours"`
		SmartNow    []string `json:"smartNow"`
		Smart       []string `json:"smart"`
		Timedeltas  []int64  `json:"timedeltas"`
		Durations   []string `json:"durations"`
		Lengths     []string `json:"lengths"`
		Sizes       []int64  `json:"sizes"`
		Decimals    []string `json:"decimals"`
		LocaleCodes []string `json:"localeCodes"`
		Plurals     []string `json:"plurals"`
	} `json:"inputs"`
	Locales map[string]goldenLocale `json:"locales"`
}

type goldenLocale struct {
	Identifier      string       `json:"identifier"`
	Name            string       `json:"name"`
	Datetime        [][]string   `json:"datetime"`
	Time            [][]string   `json:"time"`
	HoursDatetime   []string     `json:"hoursDatetime"`
	HoursTime       []string     `json:"hoursTime"`
	Smart           [][][]string `json:"smart"`
	Timedelta       []string     `json:"timedelta"`
	Duration        [][]string   `json:"duration"`
	DurationDefault []string     `json:"durationDefault"`
	Size            []string     `json:"size"`
	Decimal         []string     `json:"decimal"`
	Locale          []*string    `json:"locale"`
	Plural          []string     `json:"plural"`
}

func loadGolden(t *testing.T, path string) *golden {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func parseTimes(t *testing.T, in []string) []time.Time {
	t.Helper()
	out := make([]time.Time, len(in))
	for i, s := range in {
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

func parseFloats(t *testing.T, in []string) []float64 {
	t.Helper()
	out := make([]float64, len(in))
	for i, s := range in {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

// check compares one output with Babel's and counts the mismatches, so
// that a broken locale reports its first few differences only.
type checker struct {
	t    *testing.T
	fail int
}

func (c *checker) eq(what string, got, want string) {
	c.t.Helper()
	if got == want {
		return
	}
	c.fail++
	if c.fail <= 20 {
		c.t.Errorf("%s = %q, Babel %q", what, got, want)
	}
}

// TestGolden checks every method against Babel 2.12.1 on the whole grid
// of testdata/golden.json, for every locale. L10N_GOLDEN names another
// grid (tools/l10n/extract.py --random).
func TestGolden(t *testing.T) {
	path := os.Getenv("L10N_GOLDEN")
	if path == "" {
		path = "testdata/golden.json"
	}
	g := loadGolden(t, path)
	in := g.Inputs
	zones := make([]*time.Location, len(in.Zones))
	for i, z := range in.Zones {
		loc, err := time.LoadLocation(z)
		if err != nil {
			t.Fatalf("time zone %s: %v", z, err)
		}
		zones[i] = loc
	}
	datetimes := parseTimes(t, in.Datetimes)
	hours := parseTimes(t, in.Hours)
	smartNow := parseTimes(t, in.SmartNow)
	smart := parseTimes(t, in.Smart)
	durations := parseFloats(t, in.Durations)
	decimals := parseFloats(t, in.Decimals)
	plurals := parseFloats(t, in.Plurals)

	codes := Available()
	var goldenCodes []string
	for code := range g.Locales {
		goldenCodes = append(goldenCodes, code)
	}
	slices.Sort(goldenCodes)
	if !slices.Equal(codes, goldenCodes) {
		t.Fatalf("Available() = %v, golden has %v", codes, goldenCodes)
	}

	for _, code := range codes {
		want := g.Locales[code]
		t.Run(code, func(t *testing.T) {
			c := &checker{t: t}
			l := Get(code)
			c.eq("Code", l.Code(), code)
			c.eq("Identifier", l.Identifier(), want.Identifier)
			c.eq("Name", l.Name(), want.Name)
			for zi, z := range zones {
				for i, d := range datetimes {
					c.eq(fmt.Sprintf("FormatDatetime(%s, %s)", in.Datetimes[i], in.Zones[zi]),
						l.FormatDatetime(d, z), want.Datetime[zi][i])
					c.eq(fmt.Sprintf("FormatTime(%s, %s)", in.Datetimes[i], in.Zones[zi]),
						l.FormatTime(d, z), want.Time[zi][i])
				}
				for ni, now := range smartNow {
					for i, d := range smart {
						c.eq(fmt.Sprintf("FormatDatetimeSmart(%s, %s, %s)", in.Smart[i], in.SmartNow[ni], in.Zones[zi]),
							l.FormatDatetimeSmart(d, now, z), want.Smart[zi][ni][i])
					}
				}
			}
			for i, d := range hours {
				c.eq(fmt.Sprintf("FormatDatetime(%s, UTC)", in.Hours[i]), l.FormatDatetime(d, time.UTC), want.HoursDatetime[i])
				c.eq(fmt.Sprintf("FormatTime(%s, nil)", in.Hours[i]), l.FormatTime(d, nil), want.HoursTime[i])
			}
			for i, us := range in.Timedeltas {
				d := time.Duration(us) * time.Microsecond
				c.eq(fmt.Sprintf("FormatTimedelta(%v)", d), l.FormatTimedelta(d), want.Timedelta[i])
			}
			for li, length := range in.Lengths {
				for i, x := range durations {
					c.eq(fmt.Sprintf("FormatDuration(%s, %s)", in.Durations[i], length),
						l.FormatDuration(x, length), want.Duration[li][i])
				}
			}
			for i, s := range want.DurationDefault {
				c.eq(fmt.Sprintf("FormatDuration(%s, \"\")", in.Durations[i]), l.FormatDuration(durations[i], ""), s)
			}
			for i, n := range in.Sizes {
				c.eq(fmt.Sprintf("FormatSize(%d)", n), l.FormatSize(n), want.Size[i])
			}
			for i, x := range decimals {
				c.eq(fmt.Sprintf("FormatDecimal(%s)", in.Decimals[i]), l.FormatDecimal(x), want.Decimal[i])
			}
			for i, code := range in.LocaleCodes {
				// Where CMS would print None, FormatLocale returns the code.
				exp := code
				if want.Locale[i] != nil {
					exp = *want.Locale[i]
				}
				c.eq(fmt.Sprintf("FormatLocale(%q)", code), l.FormatLocale(code), exp)
			}
			for i, x := range plurals {
				c.eq(fmt.Sprintf("PluralForm(%s)", in.Plurals[i]), l.PluralForm(x), want.Plural[i])
				if x == math.Trunc(x) {
					c.eq(fmt.Sprintf("PluralFormInt(%s)", in.Plurals[i]), l.PluralFormInt(-int64(x)), want.Plural[i])
				}
			}
			if c.fail > 20 {
				t.Errorf("... %d mismatches in total", c.fail)
			}
		})
	}
}

// TestGoldenCovers makes sure the grid exercises the cases that need care.
func TestGoldenCovers(t *testing.T) {
	g := loadGolden(t, "testdata/golden.json")
	if g.Babel != "2.12.1" {
		t.Errorf("golden written by Babel %s, CMS pins 2.12.1", g.Babel)
	}
	en := g.Locales["en"]
	for _, s := range []string{"1.000 second", "1.000 seconds"} {
		if !slices.Contains(en.Duration[0], s) {
			t.Errorf("no %q in the long durations", s)
		}
	}
	for _, s := range []string{"100.0 KiB", "100 KiB", "1,023 bytes", "4,194,304 TiB"} {
		if !slices.Contains(en.Size, s) {
			t.Errorf("no %q in the sizes", s)
		}
	}
	nameless := 0
	for _, s := range en.Locale {
		if s == nil {
			nameless++
		}
	}
	if nameless == 0 {
		t.Error("no locale code without a name (root) in the grid")
	}
}

func TestGetFallback(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "": "en", "EN": "en", "en-US": "en", "en_GB": "en",
		"es": "es", "es-CL": "es_CL", "es_cl": "es_CL", "ES-cl": "es_CL", "es-MX": "es", "es-419": "es",
		"zh": "zh_CN", "zh-CN": "zh_CN", "zh-Hans": "zh_CN", "zh-SG": "zh_CN", "zh-Hans-CN": "zh_CN",
		"zh-TW": "zh_TW", "zh-HK": "zh_TW", "zh-Hant": "zh_TW", "zh-Hant-TW": "zh_TW", "zh_Hant_HK": "zh_TW",
		"pt-BR": "en", "xx": "en", "1": "en", "@": "en", "it_IT@euro": "it", "de_DE.UTF-8": "de",
		"bs-Cyrl": "bs", "sr": "en",
	} {
		if got := Get(in).Code(); got != want {
			t.Errorf("Get(%q) = %s, want %s", in, got, want)
		}
	}
	if Get("es_CL") != Get("es-CL") {
		t.Error("Get does not share the locales")
	}
}

func TestDefaults(t *testing.T) {
	l := Get("en")
	d := time.Date(2026, 10, 14, 9, 5, 3, 0, time.UTC)
	if got, want := l.FormatDatetime(d, nil), "Oct 14, 2026, 9:05:03 AM"; got != want {
		t.Errorf("FormatDatetime(nil tz) = %q, want %q", got, want)
	}
	if got := l.FormatTimedelta(time.Duration(math.MinInt64)); got == "" {
		t.Error("FormatTimedelta(MinInt64) is empty")
	}
	if got, want := l.FormatSize(math.MinInt64), "8,388,608 TiB"; got != want {
		t.Errorf("FormatSize(MinInt64) = %q, want %q", got, want)
	}
	if got, want := l.FormatDuration(1, "bogus"), "1.000 duration-second"; got != want {
		t.Errorf("FormatDuration(1, bogus) = %q, want %q (Babel's fallback)", got, want)
	}
	if got, want := l.FormatDuration(math.NaN(), ""), "NaN.000 sec"; got != want {
		t.Errorf("FormatDuration(NaN) = %q, want %q", got, want)
	}
	if got, want := l.PluralForm(math.Inf(1)), PluralOther; got != want {
		t.Errorf("PluralForm(+Inf) = %q, want %q", got, want)
	}
	if got, want := Get("ru").PluralFormInt(1e18+1), PluralOne; got != want {
		t.Errorf("ru PluralFormInt(1e18+1) = %q, want %q", got, want)
	}
	if got, want := Get("ru").PluralForm(1e21+0), PluralMany; got != want {
		t.Errorf("ru PluralForm(1e21) = %q, want %q", got, want)
	}
}

// TestNumberPatterns checks pattern features no CLDR decimal pattern of the
// 26 locales uses, against babel.numbers.parse_pattern and format_decimal.
func TestNumberPatterns(t *testing.T) {
	for _, tc := range []struct {
		pattern            string
		prefix, suffix     [2]string
		grouping, int, frc [2]int
	}{
		{"#,##0.###", [2]string{"", "-"}, [2]string{"", ""}, [2]int{3, 3}, [2]int{1, 4}, [2]int{0, 3}},
		{"#,##,##0.00", [2]string{"", "-"}, [2]string{"", ""}, [2]int{3, 2}, [2]int{1, 6}, [2]int{2, 2}},
		{"'#'0;(0)", [2]string{"'#'", "("}, [2]string{"", ")"}, [2]int{1000, 1000}, [2]int{1, 1}, [2]int{0, 0}},
	} {
		p, err := parseNumberPattern(tc.pattern)
		if err != nil {
			t.Fatalf("%q: %v", tc.pattern, err)
		}
		if p.prefix != tc.prefix || p.suffix != tc.suffix || p.grouping != tc.grouping || p.intPrec != tc.int || p.fracPrec != tc.frc {
			t.Errorf("%q: got %+v", tc.pattern, *p)
		}
	}
	if _, err := parseNumberPattern("#E0"); err == nil {
		t.Error("scientific pattern accepted")
	}
	l := Get("en")
	p, _ := parseNumberPattern("#,##,##0.00;(#)")
	if got, want := l.formatNumber(p, decimalFromFloat(-1234567.891), p.fracPrec), "(12,34,567.89)"; got != want {
		t.Errorf("formatNumber = %q, want %q", got, want)
	}
	p, _ = parseNumberPattern("'#'0''")
	if got, want := l.formatNumber(p, decimalFromFloat(7), p.fracPrec), "#7'"; got != want {
		t.Errorf("formatNumber = %q, want %q", got, want)
	}
}

func TestDatePatterns(t *testing.T) {
	toks := tokenizeDatePattern("d 'de' MMMM 'o''clock' y''")
	// The tokens of babel.dates.tokenize_pattern.
	want := []dtToken{{field: 'd', n: 1}, {lit: " "}, {lit: "de "}, {field: 'M', n: 4}, {lit: " "}, {lit: "o'clock "}, {field: 'y', n: 1}, {lit: "'"}}
	if !slices.Equal(toks, want) {
		t.Errorf("tokenizeDatePattern = %+v, want %+v", toks, want)
	}
	if _, err := compileDatePattern("GGGG y"); err == nil {
		t.Error("era accepted")
	}
	if _, err := compileDatePattern("ddd"); err == nil {
		t.Error("invalid field length accepted")
	}
	// Expected values: babel.dates.format_datetime(d, pattern, locale="en").
	l := Get("en")
	d := time.Date(2009, 1, 3, 0, 7, 9, 0, time.UTC)
	for pattern, want := range map[string]string{
		"yy yyyy YYYY": "09 2009 2009", "M MM MMM MMMM MMMMM LLL": "1 01 Jan January J Jan",
		"d dd D DDD F": "3 03 3 003 1", "E EEEE EEEEE EEEEEE": "Sat Saturday S Sa",
		"h hh H HH K k": "12 12 0 00 0 24", "m mm s ss": "7 07 9 09", "a b B": "AM night at night",
	} {
		toks, err := compileDatePattern(pattern)
		if err != nil {
			t.Fatalf("%q: %v", pattern, err)
		}
		if got := l.formatPattern(toks, d); got != want {
			t.Errorf("%q = %q, want %q", pattern, got, want)
		}
	}
}

func TestPluralRules(t *testing.T) {
	for _, bad := range []string{"", "x = 1", "n = ", "n % 0 = 1", "n not 1", "n = 1 or", "n = 1 1"} {
		if _, err := parsePluralRule(bad); err == nil {
			t.Errorf("parsePluralRule(%q) accepted", bad)
		}
	}
	c, err := parsePluralRule("n within 1..2 and n not within 1..1")
	if err != nil {
		t.Fatal(err)
	}
	for x, want := range map[float64]bool{1: false, 1.5: true, 2: true, 2.5: false, 0.5: false} {
		op, _ := operandsFromFloat(x)
		if got := c.eval(&op); got != want {
			t.Errorf("%v: got %v, want %v", x, got, want)
		}
	}
}

func TestConcurrentGet(t *testing.T) {
	var wg sync.WaitGroup
	for _, code := range Available() {
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				l := Get(code)
				_ = l.FormatDatetime(time.Now(), time.UTC)
				_ = l.FormatLocale("it_IT")
				_ = l.FormatSize(123456)
			}()
		}
	}
	wg.Wait()
}

func BenchmarkFormatDatetime(b *testing.B) {
	l := Get("es")
	tz, err := time.LoadLocation("America/Santiago")
	if err != nil {
		b.Skip(err)
	}
	d := time.Date(2026, 10, 14, 9, 5, 3, 0, time.UTC)
	b.ReportAllocs()
	for b.Loop() {
		_ = l.FormatDatetime(d, tz)
	}
}

func BenchmarkFormatDuration(b *testing.B) {
	l := Get("en")
	b.ReportAllocs()
	for b.Loop() {
		_ = l.FormatDuration(1.234, "")
	}
}

func BenchmarkFormatSize(b *testing.B) {
	l := Get("en")
	b.ReportAllocs()
	for b.Loop() {
		_ = l.FormatSize(268435456)
	}
}

func BenchmarkFormatTimedelta(b *testing.B) {
	l := Get("en")
	d := 5*time.Hour + 3*time.Minute + 7*time.Second
	b.ReportAllocs()
	for b.Loop() {
		_ = l.FormatTimedelta(d)
	}
}

func BenchmarkGet(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Get("es-CL")
	}
}

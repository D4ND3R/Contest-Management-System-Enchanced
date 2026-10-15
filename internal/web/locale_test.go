// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"testing"
	"time"
)

// TestBasicLocale checks the English fallback against the English
// expectations of cmstestsuite/unit_tests/locale/locale_test.py (CMS).
func TestBasicLocale(t *testing.T) {
	en := BasicLocale{Code: "en"}
	for _, c := range []struct{ got, want string }{
		{en.FormatTimedelta(0), "0 seconds"},
		{en.FormatTimedelta(123 * time.Millisecond), "0 seconds"},
		{en.FormatTimedelta(-time.Second), "1 second"},
		{en.FormatTimedelta(2 * time.Second), "2 seconds"},
		{en.FormatTimedelta(3 * time.Minute), "3 minutes"},
		{en.FormatTimedelta(4 * time.Hour), "4 hours"},
		{en.FormatTimedelta(5 * 24 * time.Hour), "5 days"},
		{en.FormatTimedelta(42 * 24 * time.Hour), "42 days"},
		{en.FormatTimedelta(25*time.Hour + time.Minute + time.Second), "1 day, 1 hour, 1 minute, and 1 second"},
		{en.FormatDuration(0, "short"), "0.000 sec"},
		{en.FormatDuration(0, "long"), "0.000 seconds"},
		{en.FormatDuration(1, "short"), "1.000 sec"},
		{en.FormatDuration(1, "long"), "1.000 second"},
		{en.FormatDuration(2, "long"), "2.000 seconds"},
		{en.FormatDuration(0.123456789, "short"), "0.123 sec"},
		{en.FormatDuration(1.23456789, "short"), "1.235 sec"},
		{en.FormatDuration(1234.56789, "short"), "1,234.568 sec"},
		{en.FormatDuration(12345.6789, "long"), "12,345.679 seconds"},
		{en.FormatSize(0), "0 bytes"},
		{en.FormatSize(1), "1 byte"},
		{en.FormatSize(2), "2 bytes"},
		{en.FormatSize(999), "999 bytes"},
		{en.FormatSize(1000), "1,000 bytes"},
		{en.FormatSize(1023), "1,023 bytes"},
		{en.FormatSize(1024), "1.00 KiB"},
		{en.FormatSize(1025), "1.00 KiB"},
		{en.FormatSize(999 * 1024), "999 KiB"},
		{en.FormatSize(1000 * 1024), "1,000 KiB"},
		{en.FormatSize(1023 * 1024), "1,023 KiB"},
		{en.FormatSize(1024 * 1024), "1.00 MiB"},
		{en.FormatSize(2_345_000), "2.24 MiB"},
		{en.FormatSize(34_567_000_000), "32.2 GiB"},
		{en.FormatSize(456_789_000_000_000), "415 TiB"},
		{en.FormatSize(5_678_900_000_000_000), "5,165 TiB"},
		{en.FormatDecimal(0), "0"},
		{en.FormatDecimal(1234), "1,234"},
		{en.FormatDecimal(1_234_567_890), "1,234,567,890"},
		{en.FormatDecimal(1.23456789), "1.235"},
		{en.FormatDecimal(-1234.5), "-1,234.5"},
		{en.Identifier(), "en"},
		{BasicLocale{Code: "zh_TW"}.Identifier(), "zh-Hant-TW"},
		{BasicLocale{Code: "es_CL"}.Name(), "español (Chile)"},
		{BasicLocale{Code: "xx_YY"}.Identifier(), "xx-YY"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	utc := time.Date(2026, 10, 14, 13, 4, 5, 0, time.UTC)
	if got := en.FormatDatetime(utc, nil); got != "Oct 14, 2026, 1:04:05\u202fPM" {
		t.Errorf("FormatDatetime: %q", got)
	}
	if got := en.FormatDatetimeSmart(utc, utc.Add(time.Hour), time.UTC); got != "1:04:05\u202fPM" {
		t.Errorf("FormatDatetimeSmart: %q", got)
	}
}

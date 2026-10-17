// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

const testKey = "00112233445566778899aabbccddeeff"

func rankingConfig(key string) *Config {
	c := Default()
	c.SecretKey = key
	return c
}

func TestRankingPushPassword(t *testing.T) {
	a := rankingConfig(testKey)
	p := a.RankingPushPassword()
	if len(p) != 32 {
		t.Fatalf("password %q: want 32 hex characters", p)
	}
	if _, err := hex.DecodeString(p); err != nil {
		t.Fatalf("password %q is not hex: %v", p, err)
	}
	if p != rankingConfig(testKey).RankingPushPassword() {
		t.Error("the derived password is not deterministic")
	}
	if p == rankingConfig("ffeeddccbbaa99887766554433221100").RankingPushPassword() {
		t.Error("the derived password does not depend on secret_key")
	}
	if p == hex.EncodeToString(a.DeriveKey(RankingLinkPurpose)[:16]) {
		t.Error("the push password equals another derived key")
	}
}

func TestRankingWebCredentials(t *testing.T) {
	c := rankingConfig(testKey)
	u, p := c.RankingWebCredentials()
	if u != "cms" || p != c.RankingPushPassword() {
		t.Errorf("defaults: got %q %q", u, p)
	}
	c.RankingWeb.Username, c.RankingWeb.Password = "admin", "pw"
	if u, p := c.RankingWebCredentials(); u != "admin" || p != "pw" {
		t.Errorf("explicit: got %q %q", u, p)
	}
}

func TestRankingTargetCredentials(t *testing.T) {
	c := rankingConfig(testKey)
	derived := c.RankingPushPassword()
	cases := []struct {
		target     RankingTarget
		user, pass string
	}{
		{RankingTarget{URL: "http://127.0.0.1:8890/"}, "cms", derived},
		{RankingTarget{URL: "http://u:p%40ss@127.0.0.1:8890/"}, "u", "p@ss"},
		{RankingTarget{URL: "http://u@127.0.0.1:8890/"}, "u", derived},
		{RankingTarget{URL: "http://u:@127.0.0.1:8890/"}, "u", derived},
		{RankingTarget{URL: "http://u:x@127.0.0.1:8890/", Username: "v", Password: "y"}, "v", "y"},
		{RankingTarget{URL: "http://127.0.0.1:8890/", Password: "y"}, "cms", "y"},
	}
	for _, tc := range cases {
		u, p := tc.target.Credentials(c)
		if u != tc.user || p != tc.pass {
			t.Errorf("%+v: got %q %q, want %q %q", tc.target, u, p, tc.user, tc.pass)
		}
	}
	// A dispatcher and a ranking sharing secret_key agree without
	// credentials in the file.
	wu, wp := c.RankingWebCredentials()
	if u, p := (RankingTarget{URL: "http://r/"}).Credentials(c); u != wu || p != wp {
		t.Error("the dispatcher and ranking-web defaults differ")
	}
}

func TestRankingAdminToken(t *testing.T) {
	c := rankingConfig(testKey)
	now := time.Unix(1_700_000_000, 0)
	tok := c.RankingAdminToken(now.Add(RankingAdminTokenTTL))
	if !c.CheckRankingAdminToken(tok, now) {
		t.Fatal("a fresh token is rejected")
	}
	if c.CheckRankingAdminToken(tok, now.Add(RankingAdminTokenTTL+time.Second)) {
		t.Error("an expired token is accepted")
	}
	if rankingConfig("ffeeddccbbaa99887766554433221100").CheckRankingAdminToken(tok, now) {
		t.Error("a token of another secret_key is accepted")
	}
	exp, mac, _ := strings.Cut(tok, ".")
	bad := []string{
		"", tok + "0", exp + "." + strings.Repeat("0", len(mac)), exp, "x." + mac,
		// Moving the expiry invalidates the MAC.
		"9" + exp + "." + mac,
		// A valid MAC for an expiry too far ahead.
		c.RankingAdminToken(now.Add(48 * time.Hour)),
	}
	for _, b := range bad {
		if c.CheckRankingAdminToken(b, now) {
			t.Errorf("token %q accepted", b)
		}
	}
	if rankingConfig("").CheckRankingAdminToken(rankingConfig("").RankingAdminToken(now.Add(time.Minute)), now) {
		t.Error("a token is accepted without secret_key")
	}
}

func TestRankingLinkToken(t *testing.T) {
	c := rankingConfig(testKey)
	tok := c.RankingLinkToken("ioi")
	if len(tok) != 32 || strings.ToLower(tok) != tok {
		t.Fatalf("token %q: want 32 lowercase hex characters", tok)
	}
	if tok != rankingConfig(testKey).RankingLinkToken("ioi") {
		t.Error("the link token is not deterministic")
	}
	if tok == c.RankingLinkToken("ioi2") {
		t.Error("the link token does not depend on the contest")
	}
	if tok == rankingConfig("ffeeddccbbaa99887766554433221100").RankingLinkToken("ioi") {
		t.Error("the link token does not depend on secret_key")
	}
}

func TestRankingValidation(t *testing.T) {
	cases := map[string]string{
		"ranking_web:\n  visibility: friends":                                       "ranking_web.visibility",
		"ranking_web:\n  username: \"a:b\"":                                         "colon",
		"dispatcher:\n  ranking_refresh: 100ms":                                     "ranking_refresh",
		"dispatcher:\n  ranking_refresh: 2h":                                        "ranking_refresh",
		"dispatcher:\n  rankings:\n    - url: ftp://x/":                             "rankings[0].url",
		"dispatcher:\n  rankings:\n    - url: /relative":                            "rankings[0].url",
		"dispatcher:\n  rankings:\n    - url: http://r/\n      freeze: -1m":         "rankings[0].freeze",
		"dispatcher:\n  rankings:\n    - url: http://r/\n      contests: [\"a b\"]": "rankings[0].contests",
	}
	for yml, want := range cases {
		_, _, err := load(writeFile(t, yml), env(nil), os.ReadFile)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error containing %q, got %v", yml, want, err)
		}
	}
	ok := "ranking_web:\n  visibility: secret\ndispatcher:\n  rankings:\n    - url: https://u:p@r.example/sb/\n      freeze: 1h\n      contests: [ioi-2026_day1]\n"
	if _, _, err := load(writeFile(t, ok), env(nil), os.ReadFile); err != nil {
		t.Errorf("valid ranking settings rejected: %v", err)
	}
}

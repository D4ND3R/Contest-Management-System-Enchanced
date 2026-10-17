// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Visibility of the public scoreboard (ranking_web.visibility, R7.4).
const (
	// RankingPublic: everyone sees the scoreboard (CMS).
	RankingPublic = "public"
	// RankingSecret: whoever opened the secret link of one of its
	// contests (RankingLinkToken), and administrators.
	RankingSecret = "secret"
	// RankingAdmins: administrators only (RankingAdminToken).
	RankingAdmins = "admins"
)

// Purposes of the keys of the scoreboard derived from secret_key.
const (
	rankingPushPurpose  = "ranking-web/push"
	rankingAdminPurpose = "ranking-web/admin-link"
)

// RankingPushPassword is the password of the writes to ranking-web when
// ranking_web.password (and the password of a dispatcher.rankings entry)
// is empty: 32 hex characters derived from secret_key, so a dispatcher
// and a ranking sharing secret_key need no credentials in the file.
func (c *Config) RankingPushPassword() string {
	return hex.EncodeToString(c.DeriveKey(rankingPushPurpose)[:16])
}

// RankingWebCredentials returns the credentials ranking-web accepts for
// writes.
func (c *Config) RankingWebCredentials() (user, password string) {
	user, password = c.RankingWeb.Username, c.RankingWeb.Password
	if user == "" {
		user = "cms"
	}
	if password == "" {
		password = c.RankingPushPassword()
	}
	return user, password
}

// Credentials returns the credentials the dispatcher sends to the
// ranking t: those of the entry, of its URL, or "cms" and the password
// derived from secret_key.
func (t RankingTarget) Credentials(c *Config) (user, password string) {
	user, password = t.Username, t.Password
	if u, err := url.Parse(t.URL); err == nil && u.User != nil {
		if user == "" {
			user = u.User.Username()
		}
		if p, ok := u.User.Password(); ok && password == "" {
			password = p
		}
	}
	if user == "" {
		user = "cms"
	}
	if password == "" {
		password = c.RankingPushPassword()
	}
	return user, password
}

// RankingAdminTokenTTL is the lifetime of the admin links the admin site
// prints (RankingAdminToken): long enough to click, short enough that a
// forwarded link soon stops working. The scoreboard then keeps the access
// in a signed cookie.
const RankingAdminTokenTTL = 10 * time.Minute

// RankingAdminToken returns the token of an administrator link to the
// scoreboard (<scoreboard address>?admin=<token>), valid until expires:
// "<unix expiry>.<hex HMAC-SHA256 of it, 128 bits>" with a key derived
// from secret_key. The admin site prints it for logged-in administrators;
// ranking-web checks it (CheckRankingAdminToken) and lets the browser see
// the scoreboard as an administrator (every visibility, scores hidden by
// a freeze).
func (c *Config) RankingAdminToken(expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	return exp + "." + c.rankingAdminMAC(exp)
}

func (c *Config) rankingAdminMAC(exp string) string {
	m := hmac.New(sha256.New, c.DeriveKey(rankingAdminPurpose))
	m.Write([]byte(exp))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// CheckRankingAdminToken reports whether tok is a RankingAdminToken that
// has not expired at now (and does not claim an expiry more than a day
// ahead).
func (c *Config) CheckRankingAdminToken(tok string, now time.Time) bool {
	exp, mac, ok := strings.Cut(tok, ".")
	if !ok || c.SecretKey == "" {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	if !hmac.Equal([]byte(mac), []byte(c.rankingAdminMAC(exp))) {
		return false
	}
	at := time.Unix(n, 0)
	return !now.After(at) && at.Before(now.Add(24*time.Hour))
}

var rankingContestName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (c *Config) validateRankings() []error {
	var errs []error
	switch c.RankingWeb.Visibility {
	case RankingPublic, RankingSecret, RankingAdmins:
	default:
		errs = append(errs, fmt.Errorf("ranking_web.visibility: %q is not public, secret or admins", c.RankingWeb.Visibility))
	}
	if strings.Contains(c.RankingWeb.Username, ":") {
		errs = append(errs, errors.New("ranking_web.username cannot contain a colon"))
	}
	if d := c.Dispatcher.RankingRefresh.D(); d < time.Second || d > time.Hour {
		errs = append(errs, errors.New("dispatcher.ranking_refresh must be between 1s and 1h"))
	}
	for i, t := range c.Dispatcher.Rankings {
		name := fmt.Sprintf("dispatcher.rankings[%d]", i)
		if u, err := url.Parse(t.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s.url is not an http(s) URL", name))
		}
		if t.Freeze < 0 {
			errs = append(errs, fmt.Errorf("%s.freeze must not be negative", name))
		}
		for _, n := range t.Contests {
			if !rankingContestName.MatchString(n) {
				errs = append(errs, fmt.Errorf("%s.contests: %q is not a contest name", name, n))
			}
		}
	}
	return errs
}

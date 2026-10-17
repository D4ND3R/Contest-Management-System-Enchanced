// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// The Server page (R6.8; new): the server time zone (D6) and the public
// addresses of the contestant site and of the scoreboard, stored in
// server_settings. The administration does not know them otherwise (it
// may be reached by another name or port than contestants use); pages
// use them to build links (PublicURL). An address ":PORT" means "the host
// this page was loaded from, on that port", which makes links work in
// installations by ports (CLAUDE.md lessons).

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Keys of server_settings (JSON strings). TimezoneSetting is shared with
// the contestant site (internal/contestweb/tz.go).
const (
	TimezoneSetting      = "timezone"
	ContestURLSetting    = "public_url.contest"
	RankingURLSetting    = "public_url.ranking"
	settingsRefreshEvery = 30 * time.Second
)

// Site names for PublicURL.
const (
	SiteContest = "contest"
	SiteRanking = "ranking"
)

// serverSettings are the values of the Server page.
type serverSettings struct {
	// Timezone is the name stored ("" : the one of cms.yaml).
	Timezone string
	Location *time.Location
	// ContestURL and RankingURL are the public addresses ("": unknown).
	ContestURL, RankingURL string
}

// settingsCache keeps the server settings, read again in the background
// at most every settingsRefreshEvery (pages never wait for it) and at
// once after the Server page changes them.
type settingsCache struct {
	def  *time.Location
	load func(context.Context) (*serverSettings, error)

	cur        atomic.Pointer[serverSettings]
	mu         sync.Mutex
	at         time.Time
	refreshing bool
}

func newSettingsCache(cfg *config.Config, load func(context.Context) (*serverSettings, error)) *settingsCache {
	c := &settingsCache{def: cfg.Location(), load: load}
	c.cur.Store(&serverSettings{Location: c.def})
	return c
}

// get returns the settings, starting a refresh when they are old.
func (c *settingsCache) get() *serverSettings {
	c.mu.Lock()
	stale := !c.refreshing && time.Since(c.at) > settingsRefreshEvery
	if stale {
		c.refreshing = true
	}
	c.mu.Unlock()
	if stale {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.refresh(ctx)
		}()
	}
	return c.cur.Load()
}

// Location returns the server time zone (D6).
func (c *settingsCache) Location() *time.Location { return c.get().Location }

// refresh reads the settings now; on errors the current ones stay.
func (c *settingsCache) refresh(ctx context.Context) {
	st, err := c.load(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	c.at = time.Now()
	if err == nil {
		if st.Location == nil {
			st.Location = c.def
		}
		c.cur.Store(st)
	}
}

// loadSettings reads the settings of the Server page.
func (s *Server) loadSettings(ctx context.Context) (*serverSettings, error) {
	rows, err := s.st.Q.ListServerSettings(ctx)
	if err != nil {
		return nil, err
	}
	st := &serverSettings{}
	for _, r := range rows {
		var v string
		if json.Unmarshal(r.Value, &v) != nil {
			continue
		}
		switch r.Key {
		case TimezoneSetting:
			st.Timezone = v
			if v != "" {
				if loc, err := time.LoadLocation(v); err == nil {
					st.Location = loc
				} else {
					s.log.Warn("invalid server time zone in server_settings", "timezone", v, "err", err)
				}
			}
		case ContestURLSetting:
			st.ContestURL = v
		case RankingURLSetting:
			st.RankingURL = v
		}
	}
	return st, nil
}

// PublicURL returns the public address of a site (SiteContest,
// SiteRanking) as seen from the request r, with a trailing slash: the
// stored address, or for ":PORT" the host of r on that port (with the
// scheme of r). "" if the address is not configured.
func (s *Server) PublicURL(r *http.Request, site string) string {
	st := s.tz.get()
	v := st.ContestURL
	if site == SiteRanking {
		v = st.RankingURL
	}
	return resolvePublicURL(v, r)
}

func resolvePublicURL(v string, r *http.Request) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, ":") {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		scheme := "http"
		if httpx.IsHTTPS(r) {
			scheme = "https"
		}
		v = scheme + "://" + host + v + "/"
	}
	if !strings.HasSuffix(v, "/") {
		v += "/"
	}
	return v
}

// validPublicURL accepts ":PORT" and absolute http(s) URLs without a
// query or a fragment.
func validPublicURL(v string) bool {
	if v == "" {
		return true
	}
	if strings.HasPrefix(v, ":") {
		n, err := strconv.Atoi(v[1:])
		return err == nil && n > 0 && n < 65536
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

// serverData is the data of server.html.
type serverData struct {
	*Page
	// Timezone is the stored zone ("" : cms.yaml's), ConfigTimezone the
	// one of cms.yaml, Current the zone in use.
	Timezone, ConfigTimezone, Current string
	ContestURL, RankingURL            string
	// ContestLink and RankingLink are the addresses resolved for this
	// request.
	ContestLink, RankingLink string
}

// serverPage is GET /server.
func (s *Server) serverPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	st := s.tz.get()
	c.Render("server.html", serverData{
		Page:           p,
		Timezone:       st.Timezone,
		ConfigTimezone: s.cfg.Timezone,
		Current:        st.Location.String(),
		ContestURL:     st.ContestURL,
		RankingURL:     st.RankingURL,
		ContestLink:    resolvePublicURL(st.ContestURL, c.R),
		RankingLink:    resolvePublicURL(st.RankingURL, c.R),
	})
}

// serverPost is POST /server: it stores the three settings (an empty
// value deletes the setting) in one audited transaction.
func (s *Server) serverPost(c *Ctx) {
	values := map[string]string{}
	for _, k := range []string{TimezoneSetting, ContestURLSetting, RankingURLSetting} {
		values[k] = c.ArgDefault(strings.ReplaceAll(k, ".", "_"), "")
	}
	if tz := values[TimezoneSetting]; tz != "" {
		if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
			c.NotifyError(N("Invalid field(s)"), c.T("Unknown time zone: ")+tz)
			c.Redirect(c.URL("server"))
			return
		}
	}
	for _, k := range []string{ContestURLSetting, RankingURLSetting} {
		if !validPublicURL(values[k]) {
			c.NotifyError(N("Invalid field(s)"), c.T("Invalid address: ")+values[k])
			c.Redirect(c.URL("server"))
			return
		}
	}
	err := c.Write(func(tx *Tx) error {
		before := map[string]string{}
		rows, err := tx.Q.ListServerSettings(tx.Ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var v string
			if json.Unmarshal(r.Value, &v) == nil {
				before[r.Key] = v
			}
		}
		after := map[string]string{}
		for k, v := range before {
			after[k] = v
		}
		for k, v := range values {
			if v == "" {
				delete(after, k)
				if _, err := tx.Q.DeleteServerSetting(tx.Ctx, k); err != nil {
					return err
				}
				continue
			}
			after[k] = v
			raw, _ := json.Marshal(v)
			if _, err := tx.Q.SetServerSetting(tx.Ctx, q.SetServerSettingParams{Key: k, Value: raw}); err != nil {
				return err
			}
		}
		return tx.Audit(AuditEntry{Action: "server_settings.update", TargetType: "server_settings",
			Before: before, After: after})
	})
	if c.Commit(err) {
		s.tz.refresh(c.Context())
	}
	c.Redirect(c.URL("server"))
}

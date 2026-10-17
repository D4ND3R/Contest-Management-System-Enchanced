// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package rankingweb

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Visibility (R7.4). ranking_web.visibility says who reads the scoreboard
// (its page, data and events; the static files are public):
//
//   - public: everybody, as CMS;
//   - secret: whoever opened <scoreboard>/?secret=<token> with the token
//     of one of its contests (config.RankingLinkToken of the contest name,
//     derived from secret_key, printed by the ranking page of the admin
//     site), and administrators;
//   - admins: administrators only.
//
// Administrators come from the admin site with <scoreboard>/?admin=<token>
// (config.RankingAdminToken, valid for minutes) or authenticate with the
// write credentials (HTTP basic). They see every score, including those
// hidden by a freeze. Links work in port-based installations: they are
// relative to the address the browser used, and the access is kept in a
// signed cookie (HttpOnly, SameSite=Lax, named after the service so it
// does not clash with the other sites on the same host).

type access int

const (
	denied access = iota
	viewer
	admin
)

const (
	cookieName   = "cms_ranking"
	cookieMaxAge = 12 * time.Hour
)

func (s *Server) basicOK(r *http.Request) bool {
	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}
	uok := subtle.ConstantTimeCompare([]byte(u), []byte(s.user))
	pok := subtle.ConstantTimeCompare([]byte(p), []byte(s.pass))
	return uok&pok == 1
}

// access returns what the request may see.
func (s *Server) access(r *http.Request) access {
	if s.basicOK(r) {
		return admin
	}
	vis := s.cfg.RankingWeb.Visibility
	if v, err := s.signer.Cookie(r, cookieName, cookieMaxAge); err == nil {
		switch c := string(v); {
		case c == "a":
			return admin
		case strings.HasPrefix(c, "s:") && vis != config.RankingAdmins:
			if s.hasContest(c[2:]) {
				return viewer
			}
		}
	}
	if vis == config.RankingPublic || vis == "" {
		return viewer
	}
	return denied
}

func (s *Server) hasContest(name string) bool {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	_, ok := s.store.st.items[kContest][EncodeID(name)]
	return ok
}

// secretContest returns the contest whose secret link token is tok.
func (s *Server) secretContest(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	for _, name := range s.store.contestNames() {
		want := s.cfg.RankingLinkToken(name)
		if subtle.ConstantTimeCompare([]byte(want), []byte(tok)) == 1 {
			return name, true
		}
	}
	return "", false
}

// grant handles ?secret= and ?admin= on the root: a valid token sets the
// cookie and redirects to the clean address (so the token does not stay
// in the address bar or the history). It reports whether it answered.
func (s *Server) grant(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	sec, adm := q.Get("secret"), q.Get("admin")
	if sec == "" && adm == "" {
		return false
	}
	var value string
	switch {
	case adm != "" && s.cfg.CheckRankingAdminToken(adm, time.Now()):
		value = "a"
	case sec != "":
		if name, ok := s.secretContest(sec); ok {
			value = "s:" + name
		}
	}
	if value == "" {
		s.rt.Log.Warn("invalid scoreboard link", "client", r.RemoteAddr)
		if s.access(r) == denied {
			forbidden(w)
			return true
		}
	} else {
		s.signer.SetCookie(w, r, cookieName, []byte(value), cookieMaxAge)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "./", http.StatusSeeOther)
	return true
}

func forbidden(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
}

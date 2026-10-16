// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/authentication.py and the password
// functions of cmscommon/crypto.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>

package contestweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// authHeader carries the API's login_data (CMS X-CMS-Authorization).
const authHeader = "X-CMS-Authorization"

// BcryptCost is the cost of the passwords hashed by the site (CMS
// BCRYPT_ROUNDS).
const BcryptCost = 12

// bcryptCost is BcryptCost; the tests lower it (a hash at cost 12 takes
// seconds under the race detector).
var bcryptCost = BcryptCost

// loginCookieName is the login cookie of a contest (CMS "<contest>_login").
func loginCookieName(contest string) string { return contest + "_login" }

// loginData is the content of the login cookie and of the API's
// login_data. CMS stores [username, password on record, timestamp,
// impersonated]; here the timestamp is the signing time of the value
// (web.Signer) and the password on record is replaced by an HMAC of it,
// so that the cookie still stops working when the password changes but
// no longer carries the password (plaintext in many contests) or its
// hash to the browser.
type loginData struct {
	Username string `json:"u"`
	// Password is the fingerprint of the password on record ("" when
	// impersonated).
	Password     string `json:"p"`
	Impersonated bool   `json:"i,omitempty"`
}

// Participation is a participant of the contest with its user and group
// (the current_user of CMS): templates read .Participation.User.Username,
// .Participation.Group.PerUserTime, .Participation.StartingTime...
type Participation struct {
	q.Participation
	User  q.User
	Group q.Group
}

// password returns the password the participation logs in with (CMS
// get_password): its own, else the user's.
func (p *Participation) password() string {
	if p.Participation.Password != nil {
		return *p.Participation.Password
	}
	return p.User.Password
}

// allowedFrom reports whether addr may use the participation when the
// contest restricts addresses: a NULL list allows any address, an empty
// one none (CMS ip_restriction).
func (p *Participation) allowedFrom(addr netip.Addr) bool {
	if p.Ip == nil {
		return true
	}
	for _, n := range p.Ip {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}

// errBadAuthentication marks a password on record CMS cannot read.
var errBadAuthentication = errors.New("invalid authentication string")

// ValidatePassword checks a password against the authentication string on
// record (CMS validate_password): "bcrypt:<hash>" or "plaintext:<pw>".
// Other strings are an error (configuration problem), never a match.
func ValidatePassword(authentication, password string) (bool, error) {
	method, payload, ok := strings.Cut(authentication, ":")
	if !ok {
		return false, errBadAuthentication
	}
	switch method {
	case "bcrypt":
		// Python's bcrypt (as CMS uses it) reads at most 72 bytes.
		err := bcrypt.CompareHashAndPassword([]byte(payload), bcryptInput(password))
		return err == nil, nil
	case "plaintext":
		return subtle.ConstantTimeCompare([]byte(payload), []byte(password)) == 1, nil
	default:
		return false, fmt.Errorf("%w: unknown method %q", errBadAuthentication, method)
	}
}

// HashPassword returns the authentication string of a new password (CMS
// hash_password with bcrypt).
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword(bcryptInput(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return "bcrypt:" + string(h), nil
}

func bcryptInput(password string) []byte {
	b := []byte(password)
	if len(b) > 72 {
		b = b[:72]
	}
	return b
}

// fingerprint is the value of the password on record kept in the login
// cookie.
func (s *Server) fingerprint(stored string) string {
	m := hmac.New(sha256.New, s.pwKey)
	m.Write([]byte(stored))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// checkPassword validates a password; bcrypt runs in a slot of the
// client address addr (errBcryptBusy when its queue is full).
func (s *Server) checkPassword(ctx context.Context, addr netip.Addr, stored, password string) (bool, error) {
	if !strings.HasPrefix(stored, "bcrypt:") {
		return ValidatePassword(stored, password)
	}
	var (
		ok  bool
		err error
	)
	if gerr := s.withBcrypt(ctx, addr, func() { ok, err = ValidatePassword(stored, password) }); gerr != nil {
		return false, gerr
	}
	return ok, err
}

// participationByUsername returns the participation of username in the
// contest, nil if there is none.
func (s *Server) participationByUsername(ctx context.Context, contestID int32, username string) (*Participation, error) {
	row, err := s.st.Q.GetContestantByUsername(ctx, q.GetContestantByUsernameParams{ContestID: contestID, Username: username})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Participation{Participation: row.Participation, User: row.User, Group: row.Group}, nil
}

// LoginResult is the outcome of a login attempt: the participation and
// the signed login data to set as cookie (or return as the API's
// login_data); nil Participation means the login failed.
type LoginResult struct {
	Participation *Participation
	// Data is the signed login data, "" when the login failed.
	Data string
}

// ValidateLogin authenticates a user logging in with a password, or an
// administrator impersonating them with adminToken (CMS validate_login).
// Failures are logged (without the password) and give an empty result;
// the error is only for database failures, the end of ctx and
// errBcryptBusy (too many password checks waiting: answer 429).
func (s *Server) ValidateLogin(ctx context.Context, c *Contest, now time.Time, username, password string, addr netip.Addr, adminToken string) (LoginResult, error) {
	fail := func(msg string) (LoginResult, error) {
		s.log.Info("unsuccessful login attempt: "+msg, "ip", addr.String(), "username", clip(username),
			"contest", c.Name)
		return LoginResult{}, nil
	}
	if !c.AllowPasswordAuthentication && adminToken == "" {
		return fail("password authentication not allowed")
	}
	p, err := s.participationByUsername(ctx, c.ID, username)
	if err != nil {
		return LoginResult{}, err
	}
	if p == nil {
		return fail("user not registered to contest")
	}
	if adminToken != "" {
		cfgToken := s.cfg.ContestWeb.AdminToken
		if cfgToken == "" {
			return fail("admin token not configured")
		}
		if subtle.ConstantTimeCompare([]byte(adminToken), []byte(cfgToken)) != 1 {
			return fail("invalid admin token")
		}
		s.log.Info("successful impersonated login", "ip", addr.String(), "username", username, "contest", c.Name)
		return LoginResult{Participation: p, Data: s.signLogin(c, loginData{Username: username, Impersonated: true}, now)}, nil
	}
	stored := p.password()
	ok, err := s.checkPassword(ctx, addr, stored, password)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, errBcryptBusy) {
			return LoginResult{}, err
		}
		s.log.Warn("invalid password stored in the database", "username", username, "contest", c.Name, "err", err)
		return LoginResult{}, nil
	}
	if !ok {
		return fail("wrong password")
	}
	if c.IpRestriction && !p.allowedFrom(addr) {
		return fail("unauthorized IP address")
	}
	if c.BlockHiddenParticipations && p.Hidden {
		return fail("participation is hidden and unauthorized")
	}
	s.log.Info("successful login", "ip", addr.String(), "username", username, "contest", c.Name)
	return LoginResult{Participation: p, Data: s.signLogin(c, loginData{Username: username, Password: s.fingerprint(stored)}, now)}, nil
}

// signLogin signs login data for a contest's cookie at now.
func (s *Server) signLogin(c *Contest, d loginData, now time.Time) string {
	b, _ := json.Marshal(d)
	return s.signer.Sign(loginCookieName(c.Name), b, now)
}

// AuthResult is the outcome of authenticating a request (CMS
// authenticate_request).
type AuthResult struct {
	// Participation is the participant, nil if none.
	Participation *Participation
	// Data is the refreshed signed login data to set as cookie; "" means
	// the cookie must be cleared (failure, or an IP autologin).
	Data string
	// Impersonated: the login data came from the admin token.
	Impersonated bool
}

// errAmbiguousIP: several participations have the client's address.
var errAmbiguousIP = errors.New("ambiguous IP address")

// Authenticate identifies the participant of a request (CMS
// authenticate_request): by IP address when the contest has IP
// autologin, else by the signed login data of the X-CMS-Authorization
// header or of the cookie (header wins), checked against the cookie
// duration; then the IP restriction and the block of hidden
// participations apply (not to impersonations).
func (s *Server) Authenticate(ctx context.Context, c *Contest, now time.Time, cookie, header string, addr netip.Addr) (AuthResult, error) {
	var res AuthResult
	if c.IpAutologin {
		p, err := s.participationByIP(ctx, c, addr)
		if errors.Is(err, errAmbiguousIP) {
			return AuthResult{}, nil
		}
		if err != nil {
			return AuthResult{}, err
		}
		res.Participation = p
	}
	if res.Participation == nil {
		data := cookie
		if header != "" {
			data = header
		}
		var err error
		res, err = s.authenticateLoginData(ctx, c, now, data)
		if err != nil || res.Participation == nil {
			return AuthResult{}, err
		}
	}
	p := res.Participation
	if c.IpRestriction && !res.Impersonated && !p.allowedFrom(addr) {
		s.log.Info("unsuccessful authentication: unauthorized IP address", "ip", addr.String(),
			"contest", c.Name, "username", p.User.Username)
		return AuthResult{}, nil
	}
	if c.BlockHiddenParticipations && p.Hidden && !res.Impersonated {
		s.log.Info("unsuccessful authentication: participation is hidden and unauthorized",
			"ip", addr.String(), "contest", c.Name, "username", p.User.Username)
		return AuthResult{}, nil
	}
	return res, nil
}

// participationByIP is CMS _authenticate_request_by_ip_address: the only
// participation whose networks are exactly the client's address (hidden
// ones left out when they are blocked); errAmbiguousIP if there are
// several.
func (s *Server) participationByIP(ctx context.Context, c *Contest, addr netip.Addr) (*Participation, error) {
	if !addr.IsValid() {
		return nil, nil
	}
	rows, err := s.st.Q.ListContestantsByIP(ctx, q.ListContestantsByIPParams{ContestID: c.ID, Ip: addr})
	if err != nil {
		return nil, err
	}
	single := netip.PrefixFrom(addr, addr.BitLen())
	var found []*Participation
	for _, r := range rows {
		if c.BlockHiddenParticipations && r.Participation.Hidden {
			continue
		}
		// CMS looks for the address as a /32 (/128) network among the
		// participation's networks: a wider network does not match.
		exact := false
		for _, n := range r.Participation.Ip {
			if n.Masked() == single {
				exact = true
				break
			}
		}
		if exact {
			found = append(found, &Participation{Participation: r.Participation, User: r.User, Group: r.Group})
		}
	}
	switch len(found) {
	case 0:
		s.log.Info("unsuccessful IP authentication: no user matches the IP address", "ip", addr.String(), "contest", c.Name)
		return nil, nil
	case 1:
		return found[0], nil
	}
	s.log.Warn("ambiguous IP address", "ip", addr.String(), "participations", len(found), "contest", c.Name)
	return nil, errAmbiguousIP
}

// authenticateLoginData is CMS
// _authenticate_request_from_cookie_or_authorization_header: the signed
// login data must be valid, not older than the cookie duration, and name
// a participation of the contest whose password on record has the same
// fingerprint; it is refreshed with the current time.
func (s *Server) authenticateLoginData(ctx context.Context, c *Contest, now time.Time, signed string) (AuthResult, error) {
	if signed == "" {
		return AuthResult{}, nil
	}
	raw, err := s.signer.Verify(loginCookieName(c.Name), signed, s.cookieDuration(), now)
	if err != nil {
		// Tampered with, signed for another contest, or expired.
		s.log.Debug("unsuccessful cookie authentication", "contest", c.Name, "err", err)
		return AuthResult{}, nil
	}
	var d loginData
	if err := json.Unmarshal(raw, &d); err != nil {
		s.log.Warn("invalid login cookie", "contest", c.Name, "err", err)
		return AuthResult{}, nil
	}
	if !c.AllowPasswordAuthentication && !d.Impersonated {
		return AuthResult{}, nil
	}
	p, err := s.participationByUsername(ctx, c.ID, d.Username)
	if err != nil {
		return AuthResult{}, err
	}
	if p == nil {
		s.log.Info("unsuccessful cookie authentication: user not registered to contest",
			"username", d.Username, "contest", c.Name)
		return AuthResult{}, nil
	}
	fresh := loginData{Username: d.Username, Impersonated: d.Impersonated}
	if !d.Impersonated {
		fresh.Password = s.fingerprint(p.password())
		if subtle.ConstantTimeCompare([]byte(fresh.Password), []byte(d.Password)) != 1 {
			s.log.Info("unsuccessful cookie authentication: wrong password", "username", d.Username, "contest", c.Name)
			return AuthResult{}, nil
		}
	}
	return AuthResult{Participation: p, Data: s.signLogin(c, fresh, now), Impersonated: d.Impersonated}, nil
}

// cookieDuration is contest_web.cookie_duration (at least a minute).
func (s *Server) cookieDuration() time.Duration {
	return max(s.cfg.ContestWeb.CookieDuration.D(), time.Minute)
}

// setLoginCookie sets (or clears, with data "") the login cookie of a
// contest. Like every cookie of the site it is HttpOnly and SameSite=Lax
// (CMS sets neither).
func (s *Server) setLoginCookie(w http.ResponseWriter, r *http.Request, c *Contest, data string) {
	name := loginCookieName(c.Name)
	if data == "" {
		web.ClearCookie(w, r, name, "/")
		return
	}
	web.SetCookie(w, r, &http.Cookie{Name: name, Value: data, Path: "/",
		MaxAge: int(s.cookieDuration() / time.Second)})
}

// clientAddr is the client's address as seen through num_proxies trusted
// proxies (CMS request.remote_ip); IPv4-mapped IPv6 addresses become
// IPv4. The zero Addr if it cannot be parsed.
func clientAddr(r *http.Request) netip.Addr {
	a, err := netip.ParseAddr(httpx.ClientIP(r))
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

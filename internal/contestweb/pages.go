// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/{base,main}.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015-2018 William Di Luigi <williamdiluigi@gmail.com>

package contestweb

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// contestListItem is a contest of the list (CMS contest_list).
type contestListItem struct {
	Name        string
	Description string
	// Over: the main group has stopped (the link is grey).
	Over bool
}

// contestListPage is GET / without contest_web.contest (CMS
// ContestListHandler).
func (s *Server) contestListPage(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	ch := s.ui.Choose(r, s.ui.All())
	rc := s.site.NewRender(w, r, ch.Lang)
	rc.Now = now
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	rows, err := s.st.Q.CWSContestList(r.Context())
	if err != nil {
		if r.Context().Err() == nil {
			s.log.Error("listing the contests", "err", err)
			s.site.Error(w, r, rc, http.StatusInternalServerError)
		}
		return
	}
	items := make([]contestListItem, len(rows))
	for i, row := range rows {
		items[i] = contestListItem{Name: row.Name, Description: row.Description,
			Over: row.MainStop != nil && row.MainStop.Before(now)}
	}
	s.site.Page(w, r, rc, http.StatusOK, "contest_list.html", struct{ Contests []contestListItem }{items})
}

// overviewData is the data of overview.html.
type overviewData struct {
	*Page
	// Start and Stop are the times of the participant's contest: those
	// of their group moved by their delay and extra time (CMS computes
	// them in the template).
	Start, Stop time.Time
}

// overviewPage is GET <contest> (CMS MainHandler): the overview, or the
// login form when nobody is logged in.
func (s *Server) overviewPage(c *Ctx) {
	p, err := c.Page("overview")
	if err != nil {
		c.Fail(err)
		return
	}
	d := overviewData{Page: p}
	if pa := c.Participation; pa != nil {
		d.Start = pa.Group.Start.Add(pa.DelayTime)
		d.Stop = pa.Group.Stop.Add(pa.DelayTime).Add(pa.ExtraTime)
	}
	c.Render(http.StatusOK, "overview.html", d)
}

// loginPage is POST <contest>/login (CMS LoginHandler).
func (s *Server) loginPage(c *Ctx) {
	errorArgs := []string{"login_error", "true"}
	next, hasNext := c.Arg("next")
	if hasNext {
		errorArgs = append(errorArgs, "next", next)
	}
	nextPage := web.NormalizeNextPage(next, hasNext, c.RC.Root, c.URL.String())
	errorPage := c.URL.JoinQuery(nil, errorArgs...)

	username := c.ArgDefault("username", "")
	password := c.ArgDefault("password", "")
	addr := clientAddr(c.R)
	if !s.allowLogin(c, username, addr.String()) {
		return
	}
	res, err := s.ValidateLogin(c.Context(), c.Contest, c.Now, username, password, addr, "")
	if err != nil {
		c.Fail(err)
		return
	}
	s.setLoginCookie(c.W, c.R, c.Contest, res.Data)
	if res.Participation == nil {
		c.Redirect(errorPage)
		return
	}
	c.Redirect(nextPage)
}

// allowLogin applies the login rate limits (per contest and user name,
// and a generous one per address, since a contest hall may share one);
// when a limit is hit it answers 429 and returns false.
func (s *Server) allowLogin(c *Ctx, username, addr string) bool {
	ok, wait := s.loginByUser.Allow(c.Contest.Name + "\x00" + username)
	if ok {
		ok, wait = s.loginByIP.Allow(addr)
	}
	if !ok {
		s.log.Warn("too many login attempts", "contest", c.Contest.Name, "username", username, "ip", addr)
		web.TooManyRequests(c.W, wait)
	}
	return ok
}

// logoutPage is POST <contest>/logout (CMS LogoutHandler).
func (s *Server) logoutPage(c *Ctx) {
	s.setLoginCookie(c.W, c.R, c.Contest, "")
	c.Redirect(c.URL.String())
}

// startPage is POST <contest>/start (CMS StartHandler): a participant of
// a USACO-like contest starts their time frame. A second request keeps
// the first starting time.
func (s *Server) startPage(c *Ctx) {
	p := c.Participation
	if _, err := s.st.Q.StartParticipation(c.Context(), q.StartParticipationParams{ID: p.ID, StartingTime: c.Now}); err != nil {
		c.Fail(err)
		return
	}
	s.log.Info("starting now for user", "username", p.User.Username, "contest", c.Contest.Name)
	s.pub.Publish(live.ParticipationChange{V: live.Version, Contest: int64(c.Contest.ID),
		Participation: int64(p.ID)}.Event())
	c.Redirect(c.URL.String())
}

// languageDoc is a language documentation link (CMS language_docs).
type languageDoc struct {
	Name string
	Ext  string
}

// documentationData is the data of documentation.html.
type documentationData struct {
	*Page
	LanguageDocs        []languageDoc
	HasJava             bool
	CompilationMessages []steps.HumanMessage
	EvaluationMessages  []steps.HumanMessage
}

// documentationPage is GET <contest>/documentation (CMS
// DocumentationHandler).
func (s *Server) documentationPage(c *Ctx) {
	p, err := c.Page("documentation")
	if err != nil {
		c.Fail(err)
		return
	}
	d := documentationData{Page: p,
		CompilationMessages: steps.CompilationMessages.All(),
		EvaluationMessages:  steps.EvaluationMessages.All(),
	}
	dir := s.cfg.ContestWeb.DocsPath
	for _, l := range p.Languages {
		if slices.Contains(l.SourceExtensions, ".java") {
			d.HasJava = true
		}
		if dir == "" {
			continue
		}
		ext := l.SourceExtension()[1:]
		if _, err := os.Stat(filepath.Join(dir, ext)); err == nil {
			d.LanguageDocs = append(d.LanguageDocs, languageDoc{Name: l.Name, Ext: ext})
		}
	}
	if dir == "" {
		d.LanguageDocs = []languageDoc{{"C++", "en"}}
	}
	c.Render(http.StatusOK, "documentation.html", d)
}

// Registration limits (CMS RegistrationHandler).
const (
	maxInputLength    = 50
	minPasswordLength = 6
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// registerData is the data of register.html.
type registerData struct {
	*Page
	MaxInputLength    int
	MinPasswordLength int
	Teams             []q.Team
	Next              *string
}

// registerPage is GET <contest>/register (CMS RegistrationHandler.get).
func (s *Server) registerPage(c *Ctx) {
	if !c.Contest.AllowRegistration {
		c.Error(http.StatusNotFound)
		return
	}
	p, err := c.Page("register")
	if err != nil {
		c.Fail(err)
		return
	}
	teams, err := s.st.Q.ListTeams(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render(http.StatusOK, "register.html", registerData{Page: p, MaxInputLength: maxInputLength,
		MinPasswordLength: minPasswordLength, Teams: teams, Next: p.Next})
}

// errStatus is an error answered with an HTTP status (CMS HTTPError).
type errStatus int

func (e errStatus) Error() string { return http.StatusText(int(e)) }

// registerPost is POST <contest>/register (CMS RegistrationHandler.post):
// it creates the participation of an existing user (new_user=false, with
// their password) or of a new one, in the main group, and answers with
// the username. Errors are statuses the page's script explains: 400
// (invalid fields, missing team), 403 (wrong password), 404 (no such
// user), 409 (username taken, already registered).
func (s *Server) registerPost(c *Ctx) {
	if !c.Contest.AllowRegistration {
		c.Error(http.StatusNotFound)
		return
	}
	if ok, wait := s.loginByIP.Allow(clientAddr(c.R).String()); !ok {
		web.TooManyRequests(c.W, wait)
		return
	}
	newUser, ok := c.Arg("new_user")
	if !ok {
		c.Error(http.StatusBadRequest)
		return
	}
	var (
		user   *q.User
		create *q.CreateUserParams
		err    error
	)
	if newUser == "true" {
		create, err = s.newUserParams(c)
	} else {
		user, err = s.existingUser(c)
	}
	if err == nil {
		var pid int32
		pid, err = s.register(c, user, create)
		if err == nil {
			s.pub.Publish(live.ParticipationChange{V: live.Version, Contest: int64(c.Contest.ID),
				Participation: int64(pid)}.Event())
		}
	}
	var st errStatus
	switch {
	case errors.As(err, &st):
		c.Error(int(st))
	case err != nil:
		c.Fail(err)
	default:
		username := ""
		if user != nil {
			username = user.Username
		} else {
			username = create.Username
		}
		c.W.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = c.W.Write([]byte(username))
	}
}

// newUserParams validates the fields of a new user (CMS _create_user)
// and hashes the password.
func (s *Server) newUserParams(c *Ctx) (*q.CreateUserParams, error) {
	var vals [5]string
	for i, name := range []string{"first_name", "last_name", "username", "password", "email"} {
		v, ok := c.Arg(name)
		if !ok {
			return nil, errStatus(http.StatusBadRequest)
		}
		vals[i] = v
	}
	first, last, username, password, email := vals[0], vals[1], vals[2], vals[3], vals[4]
	inRange := func(s string, lo int) bool {
		n := utf8.RuneCountInString(s)
		return lo <= n && n <= maxInputLength
	}
	if !inRange(first, 1) || !inRange(last, 1) || !inRange(username, 1) ||
		!usernamePattern.MatchString(username) || !inRange(password, minPasswordLength) {
		return nil, errStatus(http.StatusBadRequest)
	}
	hashed, err := s.hashPassword(c.Context(), password)
	if err != nil {
		return nil, err
	}
	p := &q.CreateUserParams{FirstName: first, LastName: last, Username: username, Password: hashed}
	if email != "" {
		p.Email = &email
	}
	return p, nil
}

// hashPassword is HashPassword sharing the bcrypt slots of the logins.
func (s *Server) hashPassword(ctx context.Context, password string) (string, error) {
	select {
	case s.bcryptSlots <- struct{}{}:
		defer func() { <-s.bcryptSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return HashPassword(password)
}

// existingUser finds the user joining the contest and checks their
// password (CMS _get_user).
func (s *Server) existingUser(c *Ctx) (*q.User, error) {
	username, ok1 := c.Arg("username")
	password, ok2 := c.Arg("password")
	if !ok1 || !ok2 {
		return nil, errStatus(http.StatusBadRequest)
	}
	u, err := s.st.Q.GetUserByUsername(c.Context(), username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errStatus(http.StatusNotFound)
	}
	if err != nil {
		return nil, err
	}
	ok, err := s.checkPassword(c.Context(), u.Password, password)
	if err != nil && c.Context().Err() != nil {
		return nil, err
	}
	if !ok {
		return nil, errStatus(http.StatusForbidden)
	}
	return &u, nil
}

// register creates the user if needed and the participation, in one
// transaction, and returns the participation's id.
func (s *Server) register(c *Ctx, user *q.User, create *q.CreateUserParams) (int32, error) {
	var teamCode *string
	if v, ok := c.Arg("team"); ok {
		teamCode = &v
	}
	var pid int32
	err := s.st.InTx(c.Context(), func(qq *q.Queries) error {
		uid := int32(0)
		if create != nil {
			if _, err := qq.GetUserByUsername(c.Context(), create.Username); err == nil {
				return errStatus(http.StatusConflict)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			u, err := qq.CreateUser(c.Context(), *create)
			if store.IsUniqueViolation(err, "users_username_key") {
				return errStatus(http.StatusConflict)
			}
			if err != nil {
				return err
			}
			uid = u.ID
		} else {
			uid = user.ID
		}
		// If there are teams, the team is mandatory.
		teams, err := qq.ListTeams(c.Context())
		if err != nil {
			return err
		}
		var teamID *int32
		if len(teams) > 0 {
			if teamCode == nil {
				return errStatus(http.StatusBadRequest)
			}
			i := slices.IndexFunc(teams, func(t q.Team) bool { return t.Code == *teamCode })
			if i < 0 {
				return errStatus(http.StatusBadRequest)
			}
			teamID = &teams[i].ID
		}
		pid, err = qq.CWSRegisterParticipation(c.Context(), q.CWSRegisterParticipationParams{
			UserID: uid, TeamID: teamID, ContestID: c.Contest.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			// Already a participant (or the contest has no main group).
			return errStatus(http.StatusConflict)
		}
		return err
	})
	return pid, err
}

// pageNext returns the "next" argument of the login form (CMS
// handler.get_argument("next", None)), nil if absent.
func pageNext(r *http.Request) *string {
	if v, ok := web.Arg(r, "next"); ok {
		return &v
	}
	return nil
}

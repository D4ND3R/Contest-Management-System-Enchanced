// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/italy_yaml.py of CMS (get_contest,
// make_group, get_user, get_team):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Jonathan Baumann <jonathan.baumann@edu.ruhr-uni-bochum.de>

package italy

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// LoadContest implements importer.ContestLoader (upstream get_contest).
func (Loader) LoadContest(dir string, opts *importer.Options) (*importer.Contest, error) {
	opts = options(opts)
	path := filepath.Join(dir, "contest.yaml")
	m, err := readYAML(path)
	if notExist(err) {
		return nil, fmt.Errorf("file missing: %s", path)
	}
	if err != nil {
		return nil, err
	}
	c := conf{m: m, where: path, rep: opts.Report}

	name, ok := c.str("name", "nome_breve")
	if !ok {
		c.rep.Errorf(path, "the contest has no name (name or nome_breve)")
	}
	contest := importer.NewContest(name)
	if s, ok := c.str("description", "nome"); ok {
		contest.Description = s
	}
	if l, ok := c.strings("allowed_localizations"); ok {
		contest.AllowedLocalizations = l
	}
	if l, ok := c.strings("languages"); ok {
		contest.Languages = l
	}
	setBool(c, &contest.SubmissionsDownloadAllowed, "submissions_download_allowed")
	setBool(c, &contest.AllowQuestions, "allow_questions")
	setBool(c, &contest.AllowUserTests, "allow_user_tests")
	if n, ok := c.int32("score_precision"); ok {
		contest.ScorePrecision = n
	}

	// Logging in.
	setBool(c, &contest.BlockHiddenParticipations, "block_hidden_participations")
	setBool(c, &contest.AllowPasswordAuthentication, "allow_password_authentication")
	setBool(c, &contest.AllowRegistration, "allow_registration")
	setBool(c, &contest.IPRestriction, "ip_restriction")
	setBool(c, &contest.IPAutologin, "ip_autologin")

	loadTokens(c, &contest.Tokens, "contest.yaml")

	if s, ok := c.str("timezone"); ok {
		contest.Timezone = &s
	}
	// Limits.
	if n, ok := c.int32("max_submission_number"); ok {
		contest.MaxSubmissionNumber = &n
	}
	if n, ok := c.int32("max_user_test_number"); ok {
		contest.MaxUserTestNumber = &n
	}
	if d, ok := c.seconds("min_submission_interval"); ok {
		contest.MinSubmissionInterval = &d
	}
	if d, ok := c.seconds("min_user_test_interval"); ok {
		contest.MinUserTestInterval = &d
	}

	// Groups: either the times of the contest itself, which make the
	// group "default", or a list of groups.
	mainKeys := [][]string{{"start", "inizio"}, {"stop", "fine"}, {"per_user_time"},
		{"analysis_enabled"}, {"analysis_start"}, {"analysis_stop"}}
	if !c.has("groups") {
		g := makeGroup(c, "default", "start", "inizio", "stop", "fine")
		contest.Groups = []importer.Group{g}
		contest.MainGroup = g.Name
	} else {
		for _, keys := range mainKeys {
			if c.has(keys...) {
				c.rep.Errorf(path, "you should not specify start, stop, per_user_time, analysis_start, "+
					"analysis_stop or analysis_enabled when using groups (found %s)", keys[0])
				break
			}
		}
		groups, _ := c.list("groups")
		for i, v := range groups {
			gc, ok := c.mapping(v, fmt.Sprintf("%s: groups[%d]", path, i))
			if !ok {
				continue
			}
			name, ok := gc.str("name")
			if !ok {
				gc.rep.Errorf(gc.where, "the group has no name")
			}
			contest.Groups = append(contest.Groups, makeGroup(gc, name, "start", "", "stop", ""))
		}
		if s, ok := c.str("main_group"); ok {
			contest.MainGroup = s
		} else if len(contest.Groups) == 1 {
			contest.MainGroup = contest.Groups[0].Name
		} else {
			contest.MainGroup = "main"
		}
	}

	if l, ok := c.strings("tasks", "problemi"); ok {
		contest.Tasks = l
	}
	users, participations := loadUsers(c)
	contest.Users = users
	contest.Participations = participations
	contest.Teams = loadTeams(c)
	if name == "" {
		return nil, nil
	}
	return contest, nil
}

// setBool sets *dst to the boolean at key, if present.
func setBool(c conf, dst *bool, keys ...string) {
	if b, ok := c.boolean(keys...); ok {
		*dst = b
	}
}

// loadTokens reads the token rules of a contest or a task: the current
// keys (token_mode...) or, with a warning, the deprecated ones
// (token_initial, token_total, token_gen_time, token_max).
func loadTokens(c conf, tk *importer.Tokens, file string) {
	if c.has("token_mode") {
		if s, ok := c.str("token_mode"); ok {
			tk.Mode = s
		}
		if n, ok := c.int32("token_max_number"); ok {
			tk.MaxNumber = &n
		}
		if d, ok := c.seconds("token_min_interval"); ok {
			tk.MinInterval = d
		}
		if n, ok := c.int32("token_gen_initial"); ok {
			tk.GenInitial = n
		}
		if n, ok := c.int32("token_gen_number"); ok {
			tk.GenNumber = n
		}
		if d, ok := c.seconds("token_gen_interval"); ok {
			tk.GenInterval = d
		}
		if n, ok := c.int32("token_gen_max"); ok {
			tk.GenMax = &n
		}
		return
	}
	c.rep.Warnf(c.where, "%s uses a deprecated format for token settings which will soon stop being "+
		"supported, you're advised to update it", file)
	// The mode.
	_, initial, hasInitial := c.value("token_initial")
	genNumber, genTime := 0.0, 0.0
	if _, v, ok := c.value("token_gen_number"); ok {
		genNumber, _ = toFloat(v)
	}
	if _, v, ok := c.value("token_gen_time"); ok {
		genTime, _ = toFloat(v)
	}
	switch {
	case !hasInitial || initial == nil:
		tk.Mode = importer.TokenModeDisabled
	case genNumber > 0 && genTime == 0:
		tk.Mode = importer.TokenModeInfinite
	default:
		tk.Mode = importer.TokenModeFinite
	}
	// The old defaults, then the old keys.
	tk.GenInitial, tk.GenNumber, tk.GenInterval = 0, 0, 0
	if n, ok := c.int32("token_total"); ok {
		tk.MaxNumber = &n
	}
	if d, ok := c.seconds("token_min_interval"); ok {
		tk.MinInterval = d
	}
	if n, ok := c.int32("token_initial"); ok {
		tk.GenInitial = n
	}
	if n, ok := c.int32("token_gen_number"); ok {
		tk.GenNumber = n
	}
	if d, ok := c.seconds("token_gen_time"); ok {
		tk.GenInterval = d
	}
	if n, ok := c.int32("token_max"); ok {
		tk.GenMax = &n
	}
	if tk.GenInterval == 0 {
		tk.GenInterval = time.Minute
	}
}

// makeGroup builds a group (upstream make_group) from c, whose start and
// stop may have an alias (the main group of a contest without groups:
// inizio, fine).
func makeGroup(c conf, name, start, startAlias, stop, stopAlias string) importer.Group {
	g := importer.NewGroup(name)
	keys := func(k, alias string) []string {
		if alias == "" {
			return []string{k}
		}
		return []string{k, alias}
	}
	if t, ok := c.datetime(keys(start, startAlias)...); ok {
		g.Start = t
	}
	if t, ok := c.datetime(keys(stop, stopAlias)...); ok {
		g.Stop = t
	}
	// Upstream ignores analysis_start (a bug: the analysis window could
	// never be set) and fails on analysis_end.
	if t, ok := c.datetime("analysis_start"); ok {
		g.AnalysisStart = t
	}
	if t, ok := c.datetime("analysis_stop", "analysis_end"); ok {
		g.AnalysisStop = t
	}
	setBool(c, &g.AnalysisEnabled, "analysis_enabled")
	if d, ok := c.seconds("per_user_time"); ok {
		g.PerUserTime = &d
	}
	return g
}

// loadUsers reads the users of contest.yaml (users or utenti) as users
// (upstream get_user) and as participations (upstream get_contest).
func loadUsers(c conf) ([]importer.User, []importer.Participation) {
	list, _ := c.list("users", "utenti")
	var users []importer.User
	var parts []importer.Participation
	for i, v := range list {
		uc, ok := c.mapping(v, fmt.Sprintf("%s: users[%d]", c.where, i))
		if !ok {
			continue
		}
		u, p, ok := loadUser(uc)
		if !ok {
			continue
		}
		users = append(users, u)
		parts = append(parts, p)
	}
	return users, parts
}

// loadUser reads one entry of the users of contest.yaml.
func loadUser(uc conf) (importer.User, importer.Participation, bool) {
	username, ok := uc.str("username")
	if !ok {
		uc.rep.Errorf(uc.where, "the user has no username")
		return importer.User{}, importer.Participation{}, false
	}
	uc.where = fmt.Sprintf("%s (%s)", uc.where, username)
	u := importer.User{Username: username, LastName: username}
	p := importer.Participation{Username: username}
	if pw, ok := uc.str("password"); ok {
		u.Password = importer.BuildPassword(pw, importer.PasswordPlaintext)
		p.Password = importer.Ptr(u.Password)
	}
	if s, ok := uc.str("first_name", "nome"); ok {
		u.FirstName = s
	}
	if s, ok := uc.str("last_name", "cognome"); ok {
		u.LastName = s
	}
	// The participation: only what is given changes an existing one
	// (upstream _participation_to_db).
	if uc.has("team") {
		code := ""
		if s, ok := uc.str("team"); ok {
			code = s
		}
		p.Team = &code
	}
	if b, ok := uc.boolean("hidden"); ok {
		p.Hidden = &b
	}
	if s, ok := uc.str("ip"); ok {
		nets, err := importer.ParseIPNetworks(s)
		if err != nil {
			uc.rep.Errorf(uc.where, "ip: %v", err)
		} else {
			p.IP = nets
		}
	}
	if d, ok := uc.seconds("delay"); ok {
		p.DelayTime = &d
	}
	if s, ok := uc.str("group"); ok {
		p.Group = s
	}
	return u, p, true
}

// loadTeams reads the teams of contest.yaml (upstream get_team).
func loadTeams(c conf) []importer.Team {
	list, _ := c.list("teams")
	var teams []importer.Team
	for i, v := range list {
		tc, ok := c.mapping(v, fmt.Sprintf("%s: teams[%d]", c.where, i))
		if !ok {
			continue
		}
		code, ok := tc.str("code")
		if !ok {
			tc.rep.Errorf(tc.where, "the team has no code")
			continue
		}
		name, _ := tc.str("name")
		teams = append(teams, importer.Team{Code: code, Name: name})
	}
	return teams
}

// contestOf finds the contest.yaml of the users or teams at dir: in dir,
// or (upstream: the path of one user, <contest>/<username>) in its parent,
// which selects the one named after dir.
func contestOf(dir string) (path, only string, err error) {
	path = filepath.Join(dir, "contest.yaml")
	if isFile(path) {
		return path, "", nil
	}
	parent := filepath.Join(filepath.Dir(dir), "contest.yaml")
	if isFile(parent) {
		return parent, filepath.Base(dir), nil
	}
	return "", "", fmt.Errorf("file missing: %s", path)
}

// LoadUsers implements importer.UserLoader: the users of contest.yaml, or
// the one dir names (upstream get_user).
func (Loader) LoadUsers(dir string, opts *importer.Options) ([]importer.User, error) {
	opts = options(opts)
	path, only, err := contestOf(dir)
	if err != nil {
		return nil, err
	}
	m, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	c := conf{m: m, where: path, rep: opts.Report}
	users, _ := loadUsers(c)
	if only == "" {
		return users, nil
	}
	for _, u := range users {
		if u.Username == only {
			return []importer.User{u}, nil
		}
	}
	return nil, fmt.Errorf("the user %q cannot be found in %s", only, path)
}

// LoadTeams implements importer.TeamLoader: the teams of contest.yaml, or
// the one dir names (upstream get_team).
func (Loader) LoadTeams(dir string, opts *importer.Options) ([]importer.Team, error) {
	opts = options(opts)
	path, only, err := contestOf(dir)
	if err != nil {
		return nil, err
	}
	m, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	teams := loadTeams(conf{m: m, where: path, rep: opts.Report})
	if only == "" {
		return teams, nil
	}
	for _, t := range teams {
		if t.Code == only {
			return []importer.Team{t}, nil
		}
	}
	return nil, fmt.Errorf("the team %q cannot be found in %s", only, path)
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// doctorCommand is `cms ctl doctor` (new): what is wrong with a running
// installation and how to fix it, for organizers who are not system
// administrators. The installer runs it at the end.
func doctorCommand() *cli.Command {
	var asJSON bool
	return &cli.Command{
		Name:  "doctor",
		Short: "diagnose this installation: what is wrong and how to fix it",
		Long: `Checks the running installation and prints PASS, WARN or FAIL lines, each
problem with a "fix:" line: the configuration, the database and its
migrations, the administrators, Valkey, the judges and the queue of
submissions waiting for them, the systemd services and their health
endpoints, the three sites, the scoreboards, the file store (files the
services cannot read), free disk, the clock, the contests (times, tasks
without tests, languages without graders, no participants) and the
firewall.

Run it with sudo for every check (the service journal and the firewall need
root). It changes nothing and finishes in seconds. The exit status is 1
when a check FAILs. "sudo cmsctl verify-host" checks the judging machine in
depth.`,
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&asJSON, "json", false, "print the report as JSON")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			d := &doctor{env: env, root: os.Geteuid() == 0, run: runCommand}
			d.check(ctx)
			return d.report(asJSON)
		},
	}
}

// doctorCheck is one line of the report.
type doctorCheck struct {
	Group  string `json:"group"`
	Level  string `json:"level"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type doctor struct {
	env    *cli.Env
	root   bool
	run    func(ctx context.Context, name string, args ...string) (string, error)
	group  string
	checks []doctorCheck

	cfg     *config.Config
	cfgPath string
	pool    *pgxpool.Pool
	kv      *kv.Client
	units   map[string]bool // cms units installed (systemd)
}

func (d *doctor) add(level, name, detail, fix string) {
	d.checks = append(d.checks, doctorCheck{Group: d.group, Level: level, Name: name, Detail: detail, Fix: fix})
}

// runCommand runs a command and returns its combined output.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// sudo is the prefix of the commands of the fixes.
const sudo = "sudo "

func (d *doctor) check(ctx context.Context) {
	d.checkConfig()
	if d.cfg == nil {
		return
	}
	defer func() {
		if d.pool != nil {
			d.pool.Close()
		}
		if d.kv != nil {
			_ = d.kv.Close()
		}
	}()
	d.checkDatabase(ctx)
	d.checkValkey(ctx)
	d.checkServices(ctx)
	d.checkHTTP(ctx)
	d.checkStorage()
	d.checkHost(ctx)
	d.checkContests(ctx)
	d.checkFirewall(ctx)
}

func (d *doctor) checkConfig() {
	d.group = "configuration"
	cfg, path, err := config.Load(d.env.ConfigPath)
	if err != nil {
		d.add(checkFail, "configuration", err.Error(), "run with sudo, or fix the file it names (sudo cmsctl config check)")
		return
	}
	d.cfg, d.cfgPath = cfg, path
	if path == "" {
		d.add(checkWarn, "configuration", "no configuration file found: built-in settings and CMS_* variables",
			"give the file with --config, e.g. sudo cmsctl --config /etc/cms/cms.yaml doctor")
	} else {
		d.add(checkPass, "configuration", path, "")
	}
	if err := cfg.RequireSecretKey(); err != nil {
		d.add(checkFail, "secret_key", err.Error(), "set secret_key (the same on every machine): sudo cmsctl gen-secret")
	}
}

func (d *doctor) checkDatabase(ctx context.Context) {
	d.group = "database"
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := openDB(cctx, d.cfg, d.cfgPath)
	if err == nil {
		err = pool.Ping(cctx)
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		d.add(checkFail, "PostgreSQL", headLine(err.Error()),
			"sudo systemctl status postgresql; check database.url in the configuration")
		return
	}
	d.pool = pool
	d.add(checkPass, "PostgreSQL", "connected", "")
	version, pending, err := db.Status(cctx, pool)
	switch {
	case err != nil:
		d.add(checkFail, "migrations", headLine(err.Error()),
			"the database was not made by this cms (back it up and reinstall with --clean)")
	case len(pending) > 0:
		d.add(checkFail, "migrations", fmt.Sprintf("schema version %d, %d migration(s) pending", version, len(pending)),
			sudo+"cms --config "+d.configArg()+" migrate")
	default:
		d.add(checkPass, "migrations", fmt.Sprintf("schema version %d, up to date", version), "")
	}
	var admins, enabled int
	if err := pool.QueryRow(cctx, `SELECT count(*), count(*) FILTER (WHERE enabled AND permission_all) FROM admins`).
		Scan(&admins, &enabled); err == nil {
		switch {
		case admins == 0:
			d.add(checkFail, "administrators", "there is no administrator", sudo+"cmsctl add-admin admin")
		case enabled == 0:
			d.add(checkWarn, "administrators", "no enabled administrator has every permission",
				sudo+"cmsctl admin-password USERNAME --enable")
		default:
			d.add(checkPass, "administrators", fmt.Sprintf("%d (%d with every permission)", admins, enabled), "")
		}
	}
}

func (d *doctor) configArg() string {
	if d.cfgPath != "" {
		return d.cfgPath
	}
	return "/etc/cms/cms.yaml"
}

func (d *doctor) checkValkey(ctx context.Context) {
	d.group = "valkey and judges"
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := kv.Open(cctx, d.cfg.Redis)
	if err == nil {
		err = c.Ping(cctx)
	}
	if err != nil {
		if c != nil {
			_ = c.Close()
		}
		d.add(checkFail, "Valkey", headLine(err.Error()),
			"sudo systemctl status valkey-server (or redis-server); check redis.url in the configuration")
		return
	}
	d.kv = c
	d.add(checkPass, "Valkey", "connected", "")
	if info, err := c.Redis().ConfigGet(cctx, "maxmemory-policy").Result(); err == nil {
		if p := info["maxmemory-policy"]; p != "" && p != "noeviction" {
			d.add(checkWarn, "Valkey eviction", "maxmemory-policy is "+p+": queued jobs could be evicted",
				"set maxmemory-policy noeviction in the Valkey configuration")
		}
	}

	workers, err := registry.List(cctx, c)
	if err != nil {
		d.add(checkWarn, "judges", headLine(err.Error()), "")
	} else {
		d.checkWorkers(workers, time.Now())
	}
	jq, err := queue.Open(cctx, c, job.JobQueue, queue.Options{Priorities: job.JobPriorities, Visibility: job.JobVisibility})
	if err != nil {
		return
	}
	st, err := jq.Stats(cctx)
	if err != nil {
		return
	}
	switch {
	case st.Ready > 0 && st.OldestReady > 2*time.Minute:
		d.add(checkFail, "queue", fmt.Sprintf("%d job(s) waiting for a judge, the oldest for %s", st.Ready, st.OldestReady.Round(time.Second)),
			"submissions are not being judged: see the judges above and sudo journalctl -u cms-worker -n 50")
	default:
		d.add(checkPass, "queue", fmt.Sprintf("%d waiting, %d being judged", st.Ready, st.Pending), "")
	}
}

// checkWorkers reports the judges registered in Valkey.
func (d *doctor) checkWorkers(workers []registry.Info, now time.Time) {
	if len(workers) == 0 {
		d.add(checkFail, "judges", "no judge is connected: submissions will not be judged",
			"sudo systemctl restart cms-worker; sudo journalctl -u cms-worker -n 50; sudo cmsctl verify-host")
		return
	}
	for _, w := range workers {
		name := "judge " + w.Name
		switch {
		case w.Error != "":
			d.add(checkFail, name, "takes no jobs: "+w.Error, "sudo cmsctl verify-host shows what the sandbox needs")
		case now.Sub(w.Heartbeat) > time.Minute:
			d.add(checkWarn, name, fmt.Sprintf("last seen %s ago", now.Sub(w.Heartbeat).Round(time.Second)),
				"sudo systemctl status cms-worker (on "+w.Host+")")
		case w.Draining:
			d.add(checkWarn, name, "stopping (draining)", "")
		case w.Disabled:
			d.add(checkWarn, name, "disabled in the admin site (Judges page)", "enable it on the Judges page")
		default:
			d.add(checkPass, name, fmt.Sprintf("%s, %d job(s) in parallel", w.Host, w.Concurrency), "")
		}
	}
}

// cmsServices are the services of an installation, with the ops address
// of their /healthz.
func (d *doctor) cmsServices() []struct{ unit, ops string } {
	return []struct{ unit, ops string }{
		{"cms-contest-web", d.cfg.ContestWeb.OpsListen},
		{"cms-admin-web", d.cfg.AdminWeb.OpsListen},
		{"cms-ranking-web", d.cfg.RankingWeb.OpsListen},
		{"cms-dispatcher", d.cfg.Dispatcher.OpsListen},
		{"cms-worker", d.cfg.Worker.OpsListen},
		{"cms-monitor", ""},
	}
}

var secretLine = regexp.MustCompile(`(?i)password|requirepass|secret|token`)

func (d *doctor) checkServices(ctx context.Context) {
	d.group = "services"
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		d.add(checkWarn, "systemd", "not running under systemd: the services are not checked", "")
		return
	}
	d.units = map[string]bool{}
	for _, s := range d.cmsServices() {
		out, err := d.run(ctx, "systemctl", "show", "-p", "LoadState,ActiveState,UnitFileState", s.unit+".service")
		if err != nil {
			continue
		}
		props := parseProps(out)
		if props["LoadState"] != "loaded" {
			continue // not installed on this machine
		}
		d.units[s.unit] = true
		switch {
		case props["ActiveState"] != "active":
			detail := props["ActiveState"]
			if d.root {
				if j, err := d.run(ctx, "journalctl", "-u", s.unit, "-n", "5", "--no-pager", "-o", "cat"); err == nil && j != "" {
					detail += ": " + strings.Join(filterSecrets(strings.Split(j, "\n")), " | ")
				}
			} else {
				detail += " (run with sudo to see its journal)"
			}
			d.add(checkFail, s.unit, detail, "sudo systemctl restart "+s.unit+"; sudo journalctl -u "+s.unit+" -n 50")
		case props["UnitFileState"] != "enabled":
			d.add(checkWarn, s.unit, "running but not enabled: it will not start after a reboot", "sudo systemctl enable "+s.unit)
		default:
			d.add(checkPass, s.unit, "active and enabled", "")
		}
	}
	if len(d.units) == 0 {
		d.add(checkWarn, "services", "no cms systemd unit is installed on this machine", "")
	}
}

func filterSecrets(lines []string) []string {
	out := lines[:0]
	for _, l := range lines {
		if !secretLine.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

func parseProps(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

// localURL turns a listen address into a URL that reaches it from here.
func localURL(addr, path string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path
}

func (d *doctor) checkHTTP(ctx context.Context) {
	d.group = "health"
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(url string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return res.StatusCode, nil
	}
	installed := func(unit string) bool { return d.units == nil || d.units[unit] }
	for _, s := range d.cmsServices() {
		if s.ops == "" || !installed(s.unit) {
			continue
		}
		u := localURL(s.ops, "/healthz")
		code, err := get(u)
		switch {
		case err != nil:
			d.add(checkFail, s.unit+" health", "does not answer at "+u, "sudo systemctl restart "+s.unit)
		case code != http.StatusOK:
			d.add(checkFail, s.unit+" health", fmt.Sprintf("%s answers %d: it is not ready", u, code),
				"sudo journalctl -u "+s.unit+" -n 50 says why")
		default:
			d.add(checkPass, s.unit+" health", "ok", "")
		}
	}
	for _, site := range []struct{ name, unit, addr string }{
		{"contestant site", "cms-contest-web", d.cfg.ContestWeb.Listen},
		{"administration", "cms-admin-web", d.cfg.AdminWeb.Listen},
		{"scoreboard", "cms-ranking-web", d.cfg.RankingWeb.Listen},
	} {
		if site.addr == "" || !installed(site.unit) {
			continue
		}
		u := localURL(site.addr, "/")
		if code, err := get(u); err != nil || code >= 500 {
			d.add(checkFail, site.name, "does not answer at "+u, "sudo systemctl restart "+site.unit)
		} else {
			d.add(checkPass, site.name, "answers at "+u, "")
		}
	}
	for _, r := range d.cfg.Dispatcher.Rankings {
		user, pass := r.Credentials(d.cfg)
		base := r.URL
		if i := strings.Index(base, "@"); i >= 0 && strings.Contains(base[:i], "://") {
			base = base[:strings.Index(base, "://")+3] + base[i+1:] // credentials stay out of the report
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/contests/", nil)
		if err != nil {
			continue
		}
		req.SetBasicAuth(user, pass)
		res, err := client.Do(req)
		if err != nil {
			d.add(checkFail, "scoreboard push", "cannot reach "+base, "check dispatcher.rankings and sudo systemctl status cms-ranking-web")
			continue
		}
		res.Body.Close()
		if res.StatusCode >= 400 {
			d.add(checkFail, "scoreboard push", fmt.Sprintf("%s refuses the dispatcher's credentials (%d)", base, res.StatusCode),
				"the ranking and the dispatcher must share secret_key (or set the same password in both)")
		} else {
			d.add(checkPass, "scoreboard push", base, "")
		}
	}
}

func (d *doctor) checkStorage() {
	d.group = "storage"
	if d.cfg.Storage.Backend != "local" {
		return
	}
	root := d.cfg.Storage.Local.Path
	st, err := os.Stat(root)
	if err != nil {
		d.add(checkFail, "file store", err.Error(), "run the installer again, or create "+root+" owned by the service user")
		return
	}
	owner := fileOwner(st)
	svc, _ := user.Lookup("cms")
	if svc != nil && owner != "" && owner != svc.Uid {
		d.add(checkFail, "file store", root+" is not owned by the service user cms", "sudo chown -R cms:cms "+root)
	}
	// Files written by commands run as root that the services cannot read.
	foreign, seen := 0, 0
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || seen > 50000 {
			return filepath.SkipAll
		}
		seen++
		if info, err := e.Info(); err == nil && owner != "" && fileOwner(info) != owner {
			foreign++
		}
		return nil
	})
	if foreign > 0 {
		d.add(checkFail, "file store", fmt.Sprintf("%d file(s) in %s have another owner: the services may not read them", foreign, root),
			"sudo chown -R cms:cms "+root)
	} else {
		d.add(checkPass, "file store", root, "")
	}
	d.checkDisk("free disk ("+root+")", root)
}

func (d *doctor) checkDisk(name, path string) {
	free, ok := freeBytes(path)
	if !ok {
		return
	}
	gib := float64(free) / (1 << 30)
	switch {
	case free < 500<<20:
		d.add(checkFail, name, fmt.Sprintf("%.1f GiB free", gib), "free space: old backups in /var/lib/cms/backups, sudo apt-get clean, sudo journalctl --vacuum-size=200M")
	case free < 2<<30:
		d.add(checkWarn, name, fmt.Sprintf("%.1f GiB free", gib), "free some space before the contest")
	default:
		d.add(checkPass, name, fmt.Sprintf("%.1f GiB free", gib), "")
	}
}

func (d *doctor) checkHost(ctx context.Context) {
	d.group = "machine"
	if out, err := d.run(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value"); err == nil {
		if out == "yes" {
			d.add(checkPass, "clock", "synchronised (NTP)", "")
		} else {
			d.add(checkWarn, "clock", "not synchronised: contest times depend on it", "sudo timedatectl set-ntp true")
		}
	}
	if d.units != nil && !d.units["cms-worker"] {
		return // no judge here
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		d.add(checkWarn, "cgroups v2", "not available: the judge cannot limit memory reliably", "sudo cmsctl verify-host explains")
	}
	if _, err := exec.LookPath("isolate"); err != nil {
		d.add(checkFail, "isolate", "not installed: nothing can be judged", "run the installer again (it installs isolate)")
	} else {
		d.add(checkPass, "isolate", "installed (sudo cmsctl verify-host checks it in depth)", "")
	}
}

func (d *doctor) checkContests(ctx context.Context) {
	d.group = "contests"
	if d.pool == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := d.pool.Query(cctx, `SELECT c.id, c.name, c.languages,
	(SELECT min(start) FROM groups g WHERE g.contest_id = c.id),
	(SELECT max(stop) FROM groups g WHERE g.contest_id = c.id),
	(SELECT count(*) FROM participations p WHERE p.contest_id = c.id)
FROM contests c ORDER BY c.id`)
	if err != nil {
		return
	}
	type contestRow struct {
		id             int32
		name           string
		langs          []string
		start, stop    *time.Time
		participations int64
	}
	var contests []contestRow
	for rows.Next() {
		var c contestRow
		if err := rows.Scan(&c.id, &c.name, &c.langs, &c.start, &c.stop, &c.participations); err != nil {
			rows.Close()
			return
		}
		contests = append(contests, c)
	}
	rows.Close()
	if len(contests) == 0 {
		d.add(checkWarn, "contests", "there is no contest yet", "create one in the admin site, or sudo cmsctl import-contest DIR")
		return
	}
	langs := language.Builtin()
	if d.cfg.LanguagesDir != "" {
		if l, err := language.Load(d.cfg.LanguagesDir); err == nil {
			langs = l
		}
	}
	for _, c := range contests {
		name := "contest " + c.name
		var problems []string
		fail := false
		if c.start == nil || c.stop == nil {
			problems = append(problems, "it has no start and stop times")
			fail = true
		} else if time.Now().After(*c.stop) {
			problems = append(problems, "it ended on "+c.stop.Local().Format("2006-01-02 15:04"))
		}
		if c.participations == 0 {
			problems = append(problems, "it has no participants (Users page: Import users, or the contest's Users page)")
		}
		tp, tfail := d.contestTasks(cctx, c.id, c.langs, langs)
		problems = append(problems, tp...)
		fail = fail || tfail
		switch {
		case len(problems) == 0:
			d.add(checkPass, name, "ready", "")
		case fail:
			d.add(checkFail, name, strings.Join(problems, "; "), "fix it in the admin site (contest and task pages)")
		default:
			d.add(checkWarn, name, strings.Join(problems, "; "), "")
		}
	}
}

// contestTasks reports the tasks of a contest that cannot be judged: no
// active dataset, no testcases, or graders missing for allowed languages.
func (d *doctor) contestTasks(ctx context.Context, contest int32, allowed []string, langs *language.Registry) ([]string, bool) {
	rows, err := d.pool.Query(ctx, `SELECT t.name, t.active_dataset_id IS NOT NULL,
	(SELECT count(*) FROM testcases tc WHERE tc.dataset_id = t.active_dataset_id),
	COALESCE((SELECT array_agg(m.filename) FROM managers m WHERE m.dataset_id = t.active_dataset_id), '{}')
FROM tasks t WHERE t.contest_id = $1 ORDER BY t.num`, contest)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	var problems []string
	fail, n := false, 0
	for rows.Next() {
		var task string
		var active bool
		var testcases int64
		var managers []string
		if err := rows.Scan(&task, &active, &testcases, &managers); err != nil {
			return problems, fail
		}
		n++
		switch {
		case !active:
			problems = append(problems, "task "+task+" has no active dataset")
			fail = true
			continue
		case testcases == 0:
			problems = append(problems, "task "+task+" has no testcases")
			fail = true
		}
		if missing := missingGraders(managers, allowed, langs); len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("task %s has no grader for %s (allow only the languages it supports on the contest page)",
				task, strings.Join(missing, ", ")))
		}
	}
	if n == 0 {
		problems = append(problems, "it has no tasks")
		fail = true
	}
	return problems, fail
}

// missingGraders returns the allowed languages without a grader, for a
// dataset that uses graders (it has at least one grader.*).
func missingGraders(managers, allowed []string, langs *language.Registry) []string {
	if !slices.ContainsFunc(managers, func(m string) bool { return strings.HasPrefix(m, "grader.") }) {
		return nil
	}
	var missing []string
	for _, name := range allowed {
		l, ok := langs.Lookup(name)
		if ok && !slices.Contains(managers, "grader"+l.SourceExtension()) {
			missing = append(missing, name)
		}
	}
	return missing
}

func (d *doctor) checkFirewall(ctx context.Context) {
	d.group = "firewall"
	if !d.root {
		return
	}
	out, err := d.run(ctx, "ufw", "status")
	if err != nil || !strings.HasPrefix(out, "Status: active") {
		return
	}
	before := len(d.checks)
	for _, site := range []struct{ name, addr string }{
		{"contestant site", d.cfg.ContestWeb.Listen}, {"administration", d.cfg.AdminWeb.Listen}, {"scoreboard", d.cfg.RankingWeb.Listen},
	} {
		host, port, err := net.SplitHostPort(site.addr)
		if err != nil || host == "127.0.0.1" || host == "localhost" || host == "::1" {
			continue // behind Caddy: 80/443
		}
		if _, err := strconv.Atoi(port); err != nil {
			continue
		}
		if !regexp.MustCompile(`(?m)^` + port + `(/tcp)?\s+ALLOW`).MatchString(out) {
			d.add(checkWarn, site.name+" port", "ufw is active and does not allow port "+port, "sudo ufw allow "+port+"/tcp")
		}
	}
	if len(d.checks) == before {
		d.add(checkPass, "ufw", "active, the sites' ports are allowed", "")
	}
}

// headLine is the first line of an error message.
func headLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func (d *doctor) report(asJSON bool) error {
	failed, warned := 0, 0
	for _, c := range d.checks {
		switch c.Level {
		case checkFail:
			failed++
		case checkWarn:
			warned++
		}
	}
	w := d.env.Stdout
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Checks   []doctorCheck `json:"checks"`
			Failed   int           `json:"failed"`
			Warnings int           `json:"warnings"`
		}{d.checks, failed, warned}); err != nil {
			return err
		}
	} else {
		group := ""
		for _, c := range d.checks {
			if c.Group != group {
				group = c.Group
				fmt.Fprintf(w, "%s\n", strings.ToUpper(group[:1])+group[1:])
			}
			fmt.Fprintf(w, "  %s  %s: %s\n", c.Level, c.Name, c.Detail)
			if c.Fix != "" && c.Level != checkPass {
				fmt.Fprintf(w, "        fix: %s\n", c.Fix)
			}
		}
		if !d.root {
			fmt.Fprintln(w, "(run with sudo for every check: the services' journal and the firewall need root)")
		}
		fmt.Fprintf(w, "\n%d checks: %d failed, %d warnings\n", len(d.checks), failed, warned)
	}
	if failed > 0 {
		return errors.New(countOf(failed, "check") + " failed")
	}
	return nil
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Steps of an upgrade, in order. CMS_UPGRADE_FAIL_AT=<step> makes the
// upgrade fail right after that step, to rehearse the rollback.
const (
	stepFetch   = "fetch"
	stepBackup  = "backup"
	stepStop    = "stop"
	stepInstall = "install"
	stepMigrate = "migrate"
	stepStart   = "start"
	stepHealth  = "health"
)

// UpgradeFailEnv names the variable that simulates a failure.
const UpgradeFailEnv = "CMS_UPGRADE_FAIL_AT"

var upgradeSteps = []string{stepFetch, stepBackup, stepStop, stepInstall, stepMigrate, stepStart, stepHealth}

// databaseServices are the services that use PostgreSQL.
var databaseServices = []string{"contest-web", "admin-web", "dispatcher", "monitor"}

type upgradeOptions struct {
	binary, version, releaseURL, backupDir, units, ignoreHealth string
	healthTimeout                                               time.Duration
	noDatabase, noServices, force                               bool
}

func upgradeCommand() *cli.Command {
	var o upgradeOptions
	return &cli.Command{
		Name:  "upgrade",
		Args:  "[flags]",
		Short: "upgrade the cms binary: backup, stop, migrate, start, health check; automatic rollback on failure",
		Long: `Upgrades the installation to a new cms binary (run as root):

  fetch    the new binary: --binary PATH, or the release --version (default:
           the latest) downloaded from GitHub and checked against the
           checksums.txt of the release; it must run ("cms version")
  backup   cms ctl backup of the database and configuration (blobs are only
           ever added, so they are not needed to roll back), in --backup-dir
  stop     systemctl stop of the enabled cms-*.service units
  install  the current binary is kept as <binary>.prev and replaced
  migrate  the NEW binary applies its migrations
  start    systemctl start of the units
  health   every unit active and its /healthz (ops listener) answering 200
           within --health-timeout

If any step fails, everything is undone automatically: the units are
stopped, the previous binary is put back, the database is restored from the
backup taken in this run (when the migration step was reached), the units
are started again and checked, and the command says so and exits with an
error. Anything written between the start of the new version and the
rollback is lost.

On machines that only judge (no database) use --no-database; it is implied
when the only enabled unit is cms-worker.

Rehearse a rollback with ` + UpgradeFailEnv + `=<step> (one of fetch, backup, stop,
install, migrate, start, health): the upgrade fails right after that step.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&o.binary, "binary", "", "use this cms binary instead of downloading one")
			fs.StringVar(&o.version, "version", "latest", `release to download ("latest" or a tag such as v1.2.0)`)
			fs.StringVar(&o.releaseURL, "release-url", buildinfo.DefaultSourceURL+"/releases", "base URL of the releases")
			fs.StringVar(&o.backupDir, "backup-dir", "", "where the pre-upgrade backup goes (default <data_dir>/backups)")
			fs.StringVar(&o.units, "units", "", "systemd units to stop and start, separated by commas (default: the enabled cms-*.service)")
			fs.StringVar(&o.ignoreHealth, "ignore-health", "", "services whose /healthz is not checked (e.g. worker), separated by commas")
			fs.DurationVar(&o.healthTimeout, "health-timeout", 2*time.Minute, "how long the services have to become healthy")
			fs.BoolVar(&o.noDatabase, "no-database", false, "no backup, migration or restore (judging-only machines)")
			fs.BoolVar(&o.noServices, "no-services", false, "do not stop, start or check services (manual setups)")
			fs.BoolVar(&o.force, "force", false, "upgrade even if the new binary reports the current version")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			cfg, cfgPath, err := config.Load(env.ConfigPath)
			if err != nil {
				return err
			}
			u := &upgrader{o: o, cfg: cfg, cfgPath: cfgPath, out: env.Stdout}
			return u.run(ctx)
		},
	}
}

type upgrader struct {
	o       upgradeOptions
	cfg     *config.Config
	cfgPath string
	out     io.Writer

	current, staged, prev string   // binary paths (current: os.Executable unless set)
	installed             bool     // current was moved to prev
	units                 []string // systemd units
	useDB                 bool
	backup                string // backup file of this run
	reached               int    // index of the last step started
}

func (u *upgrader) logf(format string, a ...any) {
	fmt.Fprintf(u.out, "upgrade: "+format+"\n", a...)
}

func (u *upgrader) run(ctx context.Context) error {
	if os.Geteuid() != 0 && !u.o.noServices {
		return errors.New("cms ctl upgrade must run as root (it replaces the binary and restarts the systemd units)")
	}
	if u.current == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if u.current, err = filepath.EvalSymlinks(exe); err != nil {
			return err
		}
	}
	u.staged = filepath.Join(filepath.Dir(u.current), ".cms.upgrade-new")
	u.prev = u.current + ".prev"
	release, err := tryLockFile(filepath.Join(filepath.Dir(u.current), ".cms.upgrade.lock"))
	if err != nil {
		return fmt.Errorf("another upgrade is running? %w", err)
	}
	defer release()
	if !u.o.noServices {
		if u.units, err = u.findUnits(ctx); err != nil {
			return err
		}
	}
	u.useDB = !u.o.noDatabase && (u.o.noServices || slices.ContainsFunc(u.units, func(unit string) bool {
		return slices.Contains(databaseServices, unitService(unit))
	}))
	u.logf("current binary %s (cms %s); units: %s", u.current, buildinfo.String(), strings.Join(u.units, " "))

	for i, step := range upgradeSteps {
		u.reached = i
		if err := u.step(ctx, step); err != nil {
			if step == stepFetch {
				os.Remove(u.staged)
				return fmt.Errorf("upgrade aborted before changing anything: %w", err)
			}
			return u.rollback(ctx, step, err)
		}
	}
	u.logf("done: cms upgraded; the previous binary is kept as %s", u.prev)
	if u.backup != "" {
		u.logf("pre-upgrade backup: %s", u.backup)
	}
	return nil
}

func (u *upgrader) step(ctx context.Context, step string) error {
	var err error
	switch step {
	case stepFetch:
		err = u.fetch(ctx)
	case stepBackup:
		if u.useDB {
			dir := u.o.backupDir
			if dir == "" {
				dir = filepath.Join(u.cfg.DataDir, "backups")
			}
			u.logf("backing up to %s", dir)
			u.backup, err = Backup(ctx, u.cfg, u.cfgPath, BackupOptions{Output: dir + "/", NoBlobs: true, Label: "pre-upgrade"}, u.out)
		}
	case stepStop:
		err = u.systemctl(ctx, "stop")
	case stepInstall:
		u.logf("installing the new binary in %s", u.current)
		if err = os.Rename(u.current, u.prev); err == nil {
			u.installed = true
			err = os.Rename(u.staged, u.current)
		}
	case stepMigrate:
		if u.useDB {
			u.logf("applying the migrations of the new binary")
			err = u.runBinary(ctx, u.current, "migrate")
		}
	case stepStart:
		err = u.systemctl(ctx, "start")
	case stepHealth:
		err = u.health(ctx)
	}
	if err == nil && os.Getenv(UpgradeFailEnv) == step {
		err = fmt.Errorf("simulated failure (%s=%s)", UpgradeFailEnv, step)
	}
	return err
}

func (u *upgrader) rollback(ctx context.Context, step string, cause error) error {
	u.logf("FAILED at step %q: %v", step, cause)
	u.logf("rolling back")
	reached := func(s string) bool { return u.reached >= slices.Index(upgradeSteps, s) }
	var errs []error
	if reached(stepStop) {
		if err := u.systemctl(ctx, "stop"); err != nil {
			errs = append(errs, err)
		}
	}
	// Only a prev of this run goes back (an older one may be left over).
	if u.installed {
		if _, err := os.Stat(u.prev); err == nil {
			if err := os.Rename(u.prev, u.current); err != nil {
				errs = append(errs, fmt.Errorf("putting the previous binary back: %w", err))
			} else {
				u.logf("previous binary restored")
			}
		}
	}
	os.Remove(u.staged)
	if reached(stepMigrate) && u.useDB && u.backup != "" {
		u.logf("restoring the database from %s", u.backup)
		if err := RestoreForUpgrade(ctx, u.cfg, u.backup, u.out); err != nil {
			errs = append(errs, fmt.Errorf("restoring the database: %w", err))
		}
	}
	if reached(stepStop) && len(errs) == 0 {
		if err := u.systemctl(ctx, "start"); err != nil {
			errs = append(errs, err)
		} else if err := u.health(ctx); err != nil {
			errs = append(errs, fmt.Errorf("after the rollback: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("upgrade failed at %q (%v) and the ROLLBACK FAILED: %w\n"+
			"Fix it by hand: put back the previous binary (%s -> %s), restore the database "+
			"(cmsctl restore --clean %s), then systemctl start cms.target",
			step, cause, errors.Join(errs...), u.prev, u.current, u.backup)
	}
	services := "the services are healthy"
	if u.o.noServices {
		services = "the services were not touched (--no-services)"
	}
	return fmt.Errorf("upgrade failed at %q: %v; everything was rolled back to cms %s and %s",
		step, cause, buildinfo.String(), services)
}

// fetch puts the new binary in u.staged and checks that it runs.
func (u *upgrader) fetch(ctx context.Context) error {
	os.Remove(u.staged)
	if u.o.binary != "" {
		u.logf("using %s", u.o.binary)
		if err := copyFile(u.o.binary, u.staged, 0o755); err != nil {
			return err
		}
	} else if err := u.download(ctx); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, u.staged, "version").Output()
	if err != nil {
		return fmt.Errorf("the new binary does not run: %w", err)
	}
	newVersion := strings.TrimPrefix(strings.SplitN(string(out), "\n", 2)[0], "cms ")
	u.logf("new binary: cms %s", newVersion)
	if newVersion == buildinfo.String() && !u.o.force {
		return fmt.Errorf("the new binary is the running version (%s); use --force to reinstall it", newVersion)
	}
	return nil
}

func (u *upgrader) download(ctx context.Context) error {
	base := strings.TrimSuffix(u.o.releaseURL, "/")
	if u.o.version == "latest" {
		base += "/latest/download/"
	} else {
		base += "/download/" + u.o.version + "/"
	}
	asset := "cms_linux_" + runtime.GOARCH + ".tar.gz"
	u.logf("downloading %s%s", base, asset)
	sums, err := httpGet(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt of the release does not list %s", asset)
	}
	data, err := httpGet(ctx, base+asset, 512<<20)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s does not match checksums.txt: the download is corrupt", asset)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s has no cms binary", asset)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "cms" {
			f, err := os.OpenFile(u.staged, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			return f.Close()
		}
	}
}

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return readAllLimited(resp.Body, limit)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (u *upgrader) runBinary(ctx context.Context, bin string, args ...string) error {
	if u.cfgPath != "" {
		args = append([]string{"--config", u.cfgPath}, args...)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = u.out, u.out
	return cmd.Run()
}

// findUnits returns --units or the enabled cms-*.service units (without
// the backup job).
func (u *upgrader) findUnits(ctx context.Context) ([]string, error) {
	if u.o.units != "" {
		return splitList(u.o.units), nil
	}
	out, err := exec.CommandContext(ctx, "systemctl", "list-unit-files", "--type=service", "--state=enabled",
		"--no-legend", "--plain", "cms-*.service").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the cms units (systemctl): %w; use --units or --no-services", err)
	}
	var units []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] == "cms-backup.service" {
			continue
		}
		units = append(units, f[0])
	}
	if len(units) == 0 {
		return nil, errors.New("no enabled cms-*.service unit: use --units or --no-services")
	}
	return units, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// unitService returns the service of a unit: cms-contest-web.service ->
// contest-web (cms-worker@2.service -> worker).
func unitService(unit string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(unit, "cms-"), ".service")
	s, _, _ = strings.Cut(s, "@")
	return s
}

func (u *upgrader) systemctl(ctx context.Context, verb string) error {
	if u.o.noServices || len(u.units) == 0 {
		return nil
	}
	u.logf("systemctl %s %s", verb, strings.Join(u.units, " "))
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{verb}, u.units...)...)
	cmd.Stdout, cmd.Stderr = u.out, u.out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", verb, err)
	}
	return nil
}

// opsAddress returns the address of the /healthz of a service.
func opsAddress(cfg *config.Config, service string) string {
	var addr string
	switch service {
	case "contest-web":
		addr = cfg.ContestWeb.OpsListen
	case "admin-web":
		addr = cfg.AdminWeb.OpsListen
	case "ranking-web":
		addr = cfg.RankingWeb.OpsListen
	case "dispatcher":
		addr = cfg.Dispatcher.OpsListen
	case "worker":
		addr = cfg.Worker.OpsListen
	case "monitor":
		addr = cfg.Monitor.Listen
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// health waits until every unit is active and every /healthz answers 200.
func (u *upgrader) health(ctx context.Context) error {
	if u.o.noServices {
		return nil
	}
	ignore := splitList(u.o.ignoreHealth)
	deadline := time.Now().Add(u.o.healthTimeout)
	pending := slices.Clone(u.units)
	var last error
	for {
		var still []string
		for _, unit := range pending {
			if err := u.unitHealthy(ctx, unit, ignore); err != nil {
				still = append(still, unit)
				last = err
			} else {
				u.logf("%s healthy", unit)
			}
		}
		pending = still
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy after %s: %s (%v)", u.o.healthTimeout, strings.Join(pending, " "), last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (u *upgrader) unitHealthy(ctx context.Context, unit string, ignore []string) error {
	if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", unit).Run(); err != nil {
		return fmt.Errorf("%s is not active", unit)
	}
	service := unitService(unit)
	addr := opsAddress(u.cfg, service)
	if addr == "" || slices.Contains(ignore, service) || strings.Contains(unit, "@") {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(hctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", unit, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: /healthz answered %s", unit, resp.Status)
	}
	return nil
}

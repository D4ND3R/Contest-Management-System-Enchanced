# Troubleshooting

*Español: [../es/problemas-frecuentes.md](../es/problemas-frecuentes.md).*

| Symptom | Cause and fix |
|---|---|
| A service does not start: `the database schema is outdated` | migrations pending: `sudo -u cms cms --config /etc/cms/cms.yaml migrate` (upgrade does it) |
| `the database is at schema version N but this binary only knows M` | an older binary on a newer database: install the newer one, or restore a backup taken with this version |
| The sites refuse to start: `secret_key` | it must be set and identical everywhere (D5). Never generate one per service |
| Submissions stay "Compiling…" | no judge connected (Judges page), or the judge's sandbox failed: `journalctl -u cms-worker`, `sudo cmsctl verify-host` |
| `verify-host` FAILs on cgroups | the machine has no cgroups v2 (old kernel, container without `--cgroupns=private`): judging cannot limit memory |
| WARN on SMT, governor or swap | times will be noisier: see [Judges](judges.md#checking-the-host) |
| The scoreboard stays empty | `dispatcher.rankings` must point to ranking-web (`http://127.0.0.1:PORT/`); both services need the same `secret_key` (credentials are derived from it); logs of `cms-dispatcher` |
| Live updates do not arrive behind a proxy | the proxy buffers or compresses `*/events`: see [what a proxy needs](contestant-site.md#what-a-proxy-needs) |
| HTTPS certificate not issued | DNS of the three names must point to the machine and ports 80/443 be open: `journalctl -u caddy` |
| Links from the administration to the other sites are wrong | Server page: the public addresses (`:PORT` in installations by ports) |
| `upgrade failed at "health"` | a service was not healthy within 2 minutes; everything was rolled back. Look at `journalctl -u cms-<service>` for the new version, or pass `--ignore-health worker` if only a judge without cgroups v2 fails |
| `restore`: `other sessions are connected` | stop the services first (`systemctl stop cms.target`) |
| `restore`: `migration … differs from the one of the backup` | restore with the version that made the backup (or a newer release of the same line) |
| Restored files not readable by the services | run restore as the `cms` user (`sudo -u cms cmsctl restore …`) |
| Locked out after the installer enabled ufw | it allows the SSH port it finds in `sshd -T` first; from the console: `ufw allow <port>/tcp` |

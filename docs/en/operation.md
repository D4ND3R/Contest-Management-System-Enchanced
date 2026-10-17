# Operation

*Español: [../es/operacion.md](../es/operacion.md).*

## Services

| Unit | Service | Runs as |
|---|---|---|
| `cms-contest-web` | contestant site | cms |
| `cms-admin-web` | administration | cms |
| `cms-ranking-web` | public scoreboard | cms |
| `cms-dispatcher` | queues, scoring, push to the scoreboard, blob server for judges | cms |
| `cms-worker` | judge (isolate) | root |
| `cms-monitor` | health and host resources | cms |

```sh
systemctl status cms.target 'cms-*'
sudo systemctl restart cms-contest-web
sudo systemctl stop cms.target          # everything
journalctl -u cms-dispatcher -f         # JSON logs, one line per event
```

Every service restarts by itself if it dies. Restarting any of them during a
contest loses nothing: queues are durable and results idempotent (N3);
running jobs of a stopped judge go back to the queue.

## Health and metrics

Each service has an operations listener on localhost (`*.ops_listen`, see
[Configuration](configuration.md#default-ports)) with `/healthz` (the state
of each dependency), `/metrics` (Prometheus) and `/debug/pprof/`:

```sh
curl -s http://127.0.0.1:9191/healthz    # dispatcher
```

The administration shows judges and queues (Judges and Overview pages) and
the monitor's resource statistics.

## Judges

- Add a machine: [installation, extra judges](installation.md#extra-judges).
- More parallel jobs: `worker.concurrency` (one per physical core), then
  `systemctl restart cms-worker`.
- Before a contest: `sudo cmsctl verify-host` on every judge (cgroups v2,
  SMT off, `performance` governor, no swap), see [Judges](judges.md).
- After upgrading a compiler: restart the workers.

## Daily commands

| Task | Command |
|---|---|
| New administrator | `sudo -u cms cmsctl add-admin NAME [--role all|messaging|tasks|readonly]` |
| Effective configuration (secrets hidden) | `cmsctl config show` |
| Backup now | `sudo systemctl start cms-backup.service` |
| Upgrade | `sudo cmsctl upgrade` |
| Reevaluate | `sudo -u cms cmsctl reevaluate --task NAME --level evaluation` |
| Judging state of a contest | `sudo -u cms cmsctl judging-status --contest NAME` |

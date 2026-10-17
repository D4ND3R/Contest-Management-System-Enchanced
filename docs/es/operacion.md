# Operación

*English: [../en/operation.md](../en/operation.md).*

## Servicios

| Unidad | Servicio | Corre como |
|---|---|---|
| `cms-contest-web` | sitio del concursante | cms |
| `cms-admin-web` | administración | cms |
| `cms-ranking-web` | scoreboard público | cms |
| `cms-dispatcher` | colas, puntajes, envío al scoreboard, servidor de blobs para los jueces | cms |
| `cms-worker` | juez (isolate) | root |
| `cms-monitor` | salud y recursos del host | cms |

```sh
systemctl status cms.target 'cms-*'
sudo systemctl restart cms-contest-web
sudo systemctl stop cms.target          # todo
journalctl -u cms-dispatcher -f         # logs JSON, una línea por evento
```

Cada servicio se reinicia solo si muere. Reiniciar cualquiera durante un
concurso no pierde nada: las colas son durables y los resultados
idempotentes (N3); los trabajos en curso de un juez detenido vuelven a la
cola.

## Salud y métricas

Cada servicio tiene un listener de operaciones en localhost
(`*.ops_listen`, ver [Configuración](configuracion.md#puertos-por-defecto))
con `/healthz` (el estado de cada dependencia), `/metrics` (Prometheus) y
`/debug/pprof/`:

```sh
curl -s http://127.0.0.1:9191/healthz    # dispatcher
```

La administración muestra jueces y colas (páginas Jueces y Resumen) y las
estadísticas de recursos del monitor.

## Jueces

- Agregar una máquina: [instalación, jueces adicionales](instalacion.md#jueces-adicionales).
- Más trabajos en paralelo: `worker.concurrency` (uno por núcleo físico) y
  `systemctl restart cms-worker`.
- Antes de un concurso: `sudo cmsctl verify-host` en cada juez (cgroups v2,
  SMT apagado, governor `performance`, sin swap), ver [Jueces](jueces.md).
- Después de actualizar un compilador: reiniciar los workers.

## Comandos de todos los días

| Tarea | Comando |
|---|---|
| Nuevo administrador | `sudo -u cms cmsctl add-admin NOMBRE [--role all|messaging|tasks|readonly]` |
| Configuración efectiva (sin secretos) | `cmsctl config show` |
| Respaldo ahora | `sudo systemctl start cms-backup.service` |
| Actualizar | `sudo cmsctl upgrade` |
| Reevaluar | `sudo -u cms cmsctl reevaluate --task NOMBRE --level evaluation` |
| Estado de la evaluación de un concurso | `sudo -u cms cmsctl judging-status --contest NOMBRE` |

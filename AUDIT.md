# Auditoría de requisitos

Cada requisito de SPEC.md con su código, su prueba y su estado:
**hecho**, **parcial**, **falta** o **requiere hardware** (necesita una
máquina que esta VM no ofrece, p. ej. cgroups v2 para isolate).

Actualizada al cerrar F4.

## F0 — Cimientos

| Req. | Código | Prueba | Estado |
|---|---|---|---|
| R0.1 Licencia AGPL, avisos, cabeceras | `LICENSE.txt`, `AUTHORS.txt`, `scripts/check-headers.sh` | `make lint` | hecho |
| R0.2 Documentos del proyecto | `SPEC.md`, `CLAUDE.md`, `PROGRESS.md`, `DECISIONS.md`, `AUDIT.md` | — | hecho |
| R0.3 Configuración YAML + `CMS_*` | `internal/config`, `config/cms.sample.yaml` | `internal/config/config_test.go` | hecho |
| R0.4 Logs, métricas, `/healthz` | `internal/obs`, `internal/httpx`, `internal/app` | `internal/obs`, `internal/httpx`, `cmd/cms` `TestServicesSmoke` | hecho |
| R0.5 Binario único + `cmsctl` | `cmd/cms`, `internal/cli`, `internal/ctl` | `cmd/cms` `TestUsageAndAlias` | hecho |
| R0.6 Migraciones | `internal/db/migrate.go`, `internal/db/migrations/` | `internal/db/migrate_test.go` | hecho |
| R0.7 Infra de pruebas | `scripts/infra.sh`, `internal/testutil` | `TestDatabasesAreIsolated`, `TestKeyPrefixAndIsolation` | hecho |
| R0.8 CI | `.github/workflows/ci.yml` | GitHub Actions (run 37567326965: lint, test y sandbox en verde; sandbox con isolate 2.7 y cgroups v2) | hecho |
| R0.9 `make dev`, `make dev-native` | `deploy/`, `scripts/dev*.sh` | verificado a mano (contenedores y procesos sanos) | hecho |
| R0.10 i18n desde el día 1 | `internal/i18n`, `locale/`, `cmd/i18n-extract` | `internal/i18n/...`, `locale/locale_test.go` | hecho |

## F1–F9

| Req. | Estado |
|---|---|
| R1.1 Modelo de CMS | hecho — `internal/db/migrations/0002_cms_model.sql`, prueba `internal/db/schema_test.go` |
| R1.2 Blobs local/S3/remoto + LRU | hecho — `internal/blob`, pruebas en `internal/blob` |
| R1.3 sqlc + pgx, sin N+1 | hecho — `sqlc.yaml`, `internal/store`, pruebas con `QueryCounter` |
| R1.4 Tablas del fork | parcial — `audit_log`, `permission_tasks`, `server_settings`; la generación de resultados llega en F4 |
| R2.1 Wrapper de isolate | hecho — `internal/sandbox`, pruebas `isolate_test.go`, `sandbox_test.go`; CI con cgroups v2 |
| R2.2 Seccomp → *Security violation* | hecho — `internal/sandbox/{launcher,seccomp}_linux.go`, pruebas `seccomp_linux_test.go`, batería |
| R2.3 Caché de compilaciones | hecho — `internal/worker/cache.go`, pruebas `cache_test.go` (con compiladores reales en CI) |
| R2.4 Batería maliciosa | hecho — `internal/sandbox/battery_test.go` en `make test-sandbox` (CI) |
| R2.5 `verify-host` | hecho — `internal/ctl/verifyhost.go`, pruebas con `/sys` falso y en CI (runner y contenedor) |
| R2.6 Worker sin BD | hecho — `internal/worker`, pruebas `worker_test.go`, `sandbox_test.go`, `TestWorkerHasNoDatabaseAccess` |
| R2.7 Imagen del worker | hecho — `deploy/docker/Dockerfile.worker`, trabajo `worker-image` del CI |
| R3.1 Tipos de problema | hecho — `internal/grading/tasktype`, `cmstestsuite` portado (`TestCMSTestSuite`) |
| R3.2 Checkers | hecho — `internal/grading/checker`, casos dorados contra CMS |
| R3.3 Tipos de puntaje | hecho — `internal/grading/{scoretype,scoring}`, pruebas de CMS portadas |
| R3.4 Lenguajes en YAML | hecho — `config/languages`, `internal/grading/language`, `TestUpstreamCommands` |
| R4.1 Colas con prioridades | hecho — `internal/dispatcher` (D36), prueba de prioridades y orden en `dispatcher_test.go` |
| R4.2 Resultados idempotentes, reintentos, barrido | hecho — `internal/judging`, `internal/dispatcher`; pruebas de repetidos, viejos, reintentos y barrido |
| R4.3 Puntajes y agregados por tarea | hecho — `internal/judging` + caché `task_scores` (D34); pruebas con los tres modos y tokens |
| R4.4 Reevaluación | hecho — `judging.Invalidate`, `cms ctl reevaluate`; pruebas por alcance y nivel |
| R4.5 Datasets en segundo plano y cambio en vivo | hecho — autojudge y `ActivateDataset`; prueba de carrera con resultados |
| R4.6 Caos | hecho — `TestChaos` (binario real, `kill -9`) |
| R5.1–R5.9 Sitio del concursante | falta (esqueleto: `/healthz`, `/source`) |
| R6.1–R6.9 Administración | falta (esqueleto) |
| R7.1–R7.4 Scoreboard público | falta (esqueleto) |
| R8.1–R8.3 Importación desde CMS | falta |
| R9.1–R9.6 Instalación | falta (imagen base y Compose de desarrollo hechos) |

## No funcionales

| Req. | Estado |
|---|---|
| N1 Rendimiento (k6) | parcial: dispatcher (`TestLoad`, 2 CPU) y SSE (1500 conexiones) medidos; las páginas, con F5–F7; k6 en hardware real pendiente |
| N2 Sin N+1 | parcial: consultas probadas con `QueryCounter`; las páginas se prueban al construirlas |
| N3 Durabilidad | hecho: colas durables, ACK tras commit, resultados idempotentes con generación, lease con fencing; `TestChaos` |
| N4 Seguridad del juez | hecho: seccomp, batería maliciosa, entorno del worker fuera de la caja, sin `/etc` en compilaciones (D28–D30) |
| N5 Seguridad web | parcial: CSP con nonce, cabeceras, límites de cuerpo; CSRF, cookies y límites de frecuencia en F5 |
| N6 Observabilidad | hecho para los esqueletos |
| N7 Traducible | parcial: infraestructura y pruebas; textos a medida que llegan las páginas |
| N8 AGPL | parcial: `/source` en las tres webs; enlace en el pie con las plantillas (F5–F7) |
| N9 Misma interfaz | falta (F5–F7) |
| N10 Operación | falta (F9) |

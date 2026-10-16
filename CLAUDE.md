# CLAUDE.md — reglas de trabajo

Fork de CMS con el backend reescrito en Go. La interfaz es la del original.
Requisitos: [SPEC.md](SPEC.md). Avance: [PROGRESS.md](PROGRESS.md).
Decisiones: [DECISIONS.md](DECISIONS.md). Estado por requisito:
[AUDIT.md](AUDIT.md).

## Comandos

| Comando | Qué hace |
|---|---|
| `make build` | `bin/cms` + alias `bin/cmsctl` |
| `make test` | toda la suite con `-race` contra PostgreSQL y Valkey desechables (`scripts/infra.sh`) |
| `make test-short` | sin infraestructura (las pruebas que la necesitan se saltan) |
| `make test-sandbox` | como root: isolate, batería maliciosa, soluciones de ejemplo |
| `make lint` | gofmt + go vet (también con `-tags sandbox`) + cabeceras de licencia |
| `make generate` | código generado (sqlc) y plantillas de traducción `.pot` |
| `make bench` | benchmarks de los caminos calientes |
| `make dev` | stack completo con Docker Compose (`deploy/docker-compose.dev.yml`) |
| `make dev-native` | stack completo con binarios nativos (`scripts/dev.sh`; `make dev-stop`) |
| `scripts/infra.sh up/down/env` | PostgreSQL (55432) y Valkey (56379) desechables |

Para correr un paquete a mano: `source .infra/env` (después de
`scripts/infra.sh up`) y `go test ./internal/db/...`.

## Mapa de paquetes

| Ruta | Contenido |
|---|---|
| `cmd/cms` | binario único: subcomandos, `cmsctl`, prueba de humo de todos los servicios |
| `cmd/i18n-extract` | herramienta de desarrollo: genera los `.pot` |
| `internal/app` | runtime común de servicios (config, logger, métricas, health, reintentos, ciclo de vida). **No importa `internal/db`** |
| `internal/buildinfo` | versión y URL del código (AGPL) |
| `internal/cli` | árbol de subcomandos |
| `internal/config` | `cms.yaml` + `CMS_*` |
| `internal/db` | pool de PostgreSQL, migraciones embebidas (`migrations/`) |
| `internal/kv` | cliente Valkey con prefijo de claves |
| `internal/store` | acceso a datos (sqlc en `store/q`, consultas en `store/queries/*.sql`) |
| `internal/blob` | almacén de archivos por SHA-256 (local, S3, remoto) con caché LRU |
| `internal/job`, `internal/queue` | protocolo dispatcher↔worker y colas en Valkey Streams |
| `internal/judging` | lado de la BD del juez: operaciones pendientes, resultados, puntajes, caché `task_scores`, invalidación |
| `internal/live` | eventos en vivo (Valkey pub/sub → hub por proceso → SSE) |
| `internal/grading` | `language`, `steps`, `checker`, `tasktype`, `scoretype`, `scoring` (semántica de CMS) |
| `internal/importer` | modelo y escritor de importación; loaders `italy`, `polygon`, `tps`, `pkg` (formato propio); `dump` (dumps de CMS) |
| `internal/web` | núcleo compartido de las webs: plantillas, idiomas, CSRF, cookies, estáticos, descargas, Markdown |
| `internal/l10n` | formatos de fecha y número (CLDR de babel 2.12.1, `make l10n`) |
| `internal/httpx` | servidor HTTP, middleware, CSP, `/healthz`, `/metrics`, `/source`, pprof |
| `internal/obs` | logs JSON, registro Prometheus, health checks |
| `internal/i18n` | `.po`, formas plurales, catálogos, `Format` de `%(x)s`; `extract/` para los `.pot` |
| `internal/testutil` | `DB(t)`, `EmptyDB(t)`, `Redis(t)` |
| `internal/contestweb` | sitio del concursante (CMS ContestWebServer) |
| `internal/adminweb` | administración (CMS AdminWebServer) |
| `internal/rankingweb` | scoreboard público (CMS RankingWebServer); sin BD |
| `internal/dispatcher` | colas, puntajes, push al ranking (Evaluation/Scoring/ProxyService) |
| `internal/worker` | juez; **sin acceso a la BD** |
| `internal/monitor` | salud de servicios y host (Checker/ResourceService) |
| `internal/sandbox` | isolate + seccomp |
| `internal/ctl` | `cms ctl` (reemplaza cmscontrib) y `cms migrate` |
| `locale/` | catálogos `.po` embebidos (`cms` = los 25 de CMS, `admin` = nuevo) |
| `config/` | `cms.sample.yaml` (todos los valores por defecto); `languages/` (F3) |
| `deploy/` | Dockerfile, Compose |
| `scripts/` | `infra.sh`, `dev.sh`, `dev-compose.sh`, `check-headers.sh` |
| `docs/es`, `docs/en` | documentación de usuario |

## Reglas

- **Fases cortas y verificables** (F0, F1…; después bloques A, B…). Cada fase
  termina con código, pruebas, documentación (PROGRESS, AUDIT, docs) y un
  commit. Releer SPEC.md al empezar cada fase.
- **`make lint` y `make test` en verde antes de cada commit.**
- **Prioridades:** rendimiento primero; corrección y seguridad del juez en
  segundo lugar. Nada de N+1. Perfilar (pprof en `ops_listen`) antes de
  optimizar; benchmarks en caminos calientes; k6 sobre el hardware objetivo
  (2 vCPU).
- **Todo sobrevive reinicios**: colas durables, ACK explícito, resultados
  idempotentes, reintentos. Probar el caos (matar dispatcher/worker a mitad).
- **Observabilidad**: logs JSON, métricas y `/healthz` en todos los servicios.
- **Revisión de campo después de cada fase grande**: `make dev-native`, juez
  real, Playwright con Chromium (`executablePath: '/opt/pw-browsers/chromium'`
  si hace falta); crear un problema, enviar correctas/parciales/TLE/WA,
  responder una clarificación, mirar el scoreboard; capturas revisadas.
- **Interfaz = la del CMS original.** Portar plantillas Jinja2 a
  `html/template` sin cambiar HTML, clases, CSS ni JS; comparar con capturas
  lado a lado. Lo nuevo usa componentes que ya existen en el original. Nada
  de skills de diseño ni rediseños.
- **Traducible desde el día 1**: todo texto visible pasa por `T`/`Tn`/`Tc`
  (Go) o `{{T "…"}}` (plantillas); mismos msgids que CMS; `make generate`
  y traducir al español en `locale/es/LC_MESSAGES/admin.po`.
- **Licencia**: cabecera SPDX AGPL en cada archivo Go; archivos portados
  conservan el copyright original y dicen de dónde vienen.
- **Idioma**: código, identificadores y commits en inglés; documentación de
  usuario en español e inglés (`docs/es`, `docs/en`); SPEC/PROGRESS/
  DECISIONS/AUDIT en español.
- **Antes de declarar algo terminado, verificarlo** (prueba, captura o
  comando). Si no se pudo verificar, decirlo.
- **Seguridad del flujo**: nunca pedir ni mostrar contraseñas o llaves; los
  diagnósticos ocultan secretos (`cms ctl config show` ya lo hace;
  `grep -v requirepass`); etiquetas y releases los publica el dueño.
- **Git**: rama asignada; PRs solo si el dueño lo pide; merge a `main` con
  commit de merge cuando el CI esté verde y el dueño lo pida.

## Lecciones aprendidas (no repetir)

- Un archivo `algo_windows.go`, `_linux.go` o `_test.go` solo se compila en
  esa plataforma o en pruebas: no nombrar así archivos normales (una
  traducción `es_windows.go` "desapareció").
- isolate rechaza `--dir` relativos: los directorios del worker se convierten
  a absolutos al arrancar (`internal/worker`).
- `secret_key` igual en todos los servicios; nunca generarla por proceso (D5).
- SSE detrás de proxies: ping inmediato y cada ≤ 15 s, `X-Accel-Buffering:
  no`, excluir `*/events` de la compresión en Caddy/nginx, el cliente pasa a
  sondeo si no recibe pings. Los handlers de eventos terminan cuando
  `httpx.Draining(ctx)` se cierra.
- Instalación por puertos: la administración no conoce la URL pública de los
  otros sitios; soportar `":PUERTO"` y configurarlas en la página Servidor.
- Restaurar respaldos: desactivar triggers de usuario durante la carga y
  regenerar las tablas sembradas por migraciones.
- Matar procesos de desarrollo por PID (`scripts/dev.sh stop`); `pkill -f`
  con un patrón que aparece en tu propio comando mata tu shell (usar
  patrones anclados como `^bash scripts/dev.sh`).
- `gofmt -l` sin argumentos lee stdin y se cuelga: siempre pasarle rutas.
- PostgreSQL no corre como root: `scripts/infra.sh` usa `runuser -u nobody`.
- Colación: las instalaciones reales y el CI usan `en_US.UTF-8`, que ignora
  la puntuación al ordenar. Los identificadores usan `COLLATE "C"` (D26) y
  `scripts/infra.sh` crea la base de pruebas en `en_US.UTF-8` si el locale
  existe (`locale-gen en_US.UTF-8`), para que los `ORDER BY` fallen aquí y
  no solo en CI.
- `make generate` (no `bin/tools/sqlc generate` a secas): agrega las
  cabeceras de licencia al código generado.

## Entorno de desarrollo de esta VM

- cgroups v1: isolate funciona sin `--cg` (`CMS_SANDBOX_CGROUPS=0`); el
  trabajo `sandbox` del CI corre con cgroups v2.
- El puerto del proxy HTTPS cambia al reiniciarse la sesión: actualizar
  `/etc/apt/apt.conf.d/99proxy` con `$HTTPS_PROXY` antes de usar apt.
- CMS original de referencia (para comparar páginas y formatos) en el
  scratchpad: `refcms-start.sh` / `refcms-stop.sh` (puertos 28888–28890,
  base `refcms`, concurso `demo` importado de `testdata/contests/demo`).

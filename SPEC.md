# SPEC — CMS en Go

Fuente de verdad de los requisitos. Se relee al empezar cada fase. Los pedidos
nuevos del dueño van a un `SPEC_<NOMBRE>.md` numerado por puntos (§1, §2…), y
cada punto se cierra con código y pruebas.

- Estado por requisito: [AUDIT.md](AUDIT.md). Avance por fase:
  [PROGRESS.md](PROGRESS.md). Decisiones: [DECISIONS.md](DECISIONS.md).
- Identificadores: `R<fase>.<n>` para el MVP (F0–F9), `N<n>` para los requisitos
  no funcionales y `<bloque><n>` (A1, B3…) para lo posterior al MVP.

---

## 0. Qué es

Un **fork de CMS** (<https://github.com/cms-dev/cms>, el sistema de la IOI) con
todo el backend reescrito en Go. Conserva la arquitectura, el modelo de datos,
el comportamiento y **la interfaz** del original (HTML, CSS, JavaScript, menús,
textos y traducciones), quita lo que lo hace difícil de instalar y operar, y
agrega funciones nuevas con el mismo estilo visual.

Debe poder correr un concurso real tipo IOI/OMI (cientos de concursantes,
subtareas, jueces en varias máquinas) e **instalarse con un solo comando**.

Licencia: **AGPL-3.0-or-later**, como el original (ver D2 en DECISIONS.md).

---

## 1. Inventario real del upstream

Leído del commit `1914a5a` de `cms-dev/cms` (5 de octubre de 2026), versión
`1.6.dev0`, **esquema de base de datos versión 49**. Python ≥ 3.11, Tornado
4.5.3, gevent, SQLAlchemy 1.3, PostgreSQL, isolate ≥ 2.0 (cgroups v2), RPC
propio entre servicios. Configuración en `cms.toml` (TOML) y
`cms_ranking.toml`.

### 1.1 Servicios

| Servicio | Qué hace | Reemplazo en Go |
|---|---|---|
| LogService | recibe los logs de todos los servicios | logs JSON a stdout (`log/slog`) |
| ResourceService | arranca/vigila servicios de una máquina; estadísticas de CPU, memoria y disco para la página *Resources* del admin | systemd/Docker supervisan; `monitor` mide el host |
| Checker | verifica que los servicios respondan | `monitor` |
| EvaluationService | cola de compilación/evaluación con prioridades, reparte a los Workers (`esoperations`, `workerpool`, `priorityqueue`) | `dispatcher` |
| ScoringService | calcula puntajes al terminar la evaluación | `dispatcher` |
| ProxyService | empuja contest/tasks/users/teams/submissions/subchanges a uno o más RankingWebServer por HTTP PUT | `dispatcher` |
| Worker | compila y ejecuta en isolate; hasta 64 por máquina | `worker` (concurrencia configurable) |
| ContestWebServer | sitio del concursante + API JSON `/api` | `contest-web` |
| AdminWebServer | administración | `admin-web` |
| RankingWebServer (`cmsranking`) | scoreboard público; app JS estática + almacén REST en disco + SSE `/events` | `ranking-web` |
| PrometheusExporter (`cmscontrib`) | métricas: envíos, colas, workers | `/metrics` en cada servicio |
| TelegramBot (`cmscontrib`) | sincroniza preguntas con un chat de Telegram | posterior al MVP (bloque C) |

El PrintingService ya **no existe** en el upstream: la impresión es una
función nueva (bloque C).

### 1.2 Sitios web y rutas

**ContestWebServer.** Sin contest fijo, `/` lista los concursos y cada uno
vive bajo `/<concurso>/…`. Rutas: `login`, `logout`, `register`, `start`
(ventana por usuario), `notifications` (sondeo), `documentation`,
`tasks/<t>/description`, `tasks/<t>/statements/<lang>`,
`tasks/<t>/attachments/<f>`, `tasks/<t>/submit`, `tasks/<t>/submissions`,
`…/submissions/<id>`, `…/details`, `…/files/<f>`, `…/token`, `testing`,
`tasks/<t>/test`, `tasks/<t>/tests/<id>` (+ `details`, `input|output`,
`files/<f>`), `communication`, `question`, `/docs` (documentación de
lenguajes), `/static`.
API JSON (docs/API.rst): `POST /api/login` → `{"login_data"}` que se manda en
`X-CMS-Authorization`; `GET /api/task_list`; `POST /api/<t>/submit` →
`{"id"}`; `GET /api/<t>/submission_list`; `GET
/api/<t>/submissions/<id>/details` (filtrado por feedback level) y
`…/full_details` (solo con token de impersonación); impersonación con
`admin_token` = `contest_admin_token` y parámetros `override_phase_check`,
`override_official`, `override_max_number`, `override_min_interval`.
Plantillas: `base`, `contest_list`, `overview`, `task_description`,
`task_submissions`, `submission_details`, `test_interface`,
`user_test_details`, `user_test_row`, `communication`, `documentation`,
`register`, `error`, `macro/submission`. Estáticos: Bootstrap 2
(`bootstrap.css/js`), `cws_style.css`, `cws_utils.js`, iconos de tipos MIME,
jQuery compartido.

**AdminWebServer.** `/` (overview), login/logout, `resources` (estado de
servicios), `notifications`, `file/<digest>/<nombre>`, `render_markdown`;
concursos (`contests`, `contest/<id>`, usuarios/participaciones, **grupos**,
tareas del concurso, envíos, pruebas de usuario, anuncios, preguntas con
*reply/ignore/claim*, mensajes, ranking en `online`/`txt`/`csv`); tareas
(`tasks`, `task/<id>`, enunciados, adjuntos, datasets); datasets (`clone`,
`rename`, `delete`, `activate`, `autojudge`, managers, casos uno por uno y
desde zip, descarga de casos); usuarios, equipos, admins; envío
(`submission/<id>[/<dataset>]`, comentario, estado oficial, archivo, diff
entre envíos); pruebas de usuario. Roles del modelo `Admin`:
`permission_all` y `permission_messaging` (los demás solo lectura).
Estáticos: `aws_style.css`, `aws_utils.js`, `web_rpc.js`, prism (resaltado),
`reset.css`.

**RankingWebServer.** REST con autenticación básica para escribir:
`/contests`, `/tasks`, `/teams`, `/users`, `/submissions`, `/subchanges`
(`GET/PUT/DELETE /` y `/<key>`); lectura pública: `/`, `/history`, `/scores`,
`/events` (SSE), `/logo`, `/faces/<user>`, `/flags/<team>`, `/sublist/<user>`,
`/config`. Interfaz: `Ranking.html` + `Ranking.js`, `Scoreboard.js`,
`DataStore.js`, `HistoryStore.js`, `Overview.js`, `TimeView.js`,
`UserDetail.js`, `TeamSearch.js`, `Chart.js`, `Ranking.css`, imágenes.

### 1.3 Modelo de datos (esquema 49)

- **Contest**: `name`, `description`, `allowed_localizations`, `languages`,
  `submissions_download_allowed`, `allow_questions`, `allow_user_tests`,
  `allow_unofficial_submission_before_analysis_mode`,
  `block_hidden_participations`, `allow_password_authentication`,
  `allow_registration`, `ip_restriction`, `ip_autologin`, tokens de concurso
  (`token_mode` disabled/finite/infinite, `token_max_number`,
  `token_min_interval`, `token_gen_initial/number/interval/max`), `timezone`,
  `max_submission_number`, `max_user_test_number`, `min_submission_interval`,
  `min_submission_interval_grace_period`, `min_user_test_interval`,
  `score_precision`, `main_group_id`.
- **Group** (nuevo respecto del inventario del prompt): los tiempos ya no están
  en Contest sino en grupos de participantes del concurso: `name`, `start`,
  `stop`, `analysis_enabled`, `analysis_start`, `analysis_stop`,
  `per_user_time`. `Contest.main_group` es el grupo por defecto. Fases: -1
  antes, 0 activo, 1 terminado antes de análisis, 2 análisis, 3 terminado.
- **Announcement** (contest, admin, subject, text, timestamp).
- **User** (`first_name`, `last_name`, `username`, `password` con método
  `plaintext:`/`bcrypt:`, `email`, `timezone`, `preferred_languages`),
  **Team** (`code`, `name`), **Participation** (`ip`, `starting_time`,
  `delay_time`, `extra_time`, `password`, `hidden`, `unrestricted`,
  contest, user, group, team), **Message**, **Question** (pregunta,
  respuesta, `ignored`, admin que responde).
- **Admin** (`name`, `username`, `authentication`, `enabled`,
  `permission_all`, `permission_messaging`).
- **Task** (`num`, contest, `name`, `title`, `submission_format`,
  `primary_statements`, `allowed_languages`, tokens de tarea, límites de
  envío, `feedback_level` full/restricted/oi_restricted, `score_precision`,
  `score_mode` max_tokened_last/max/max_subtask, `active_dataset`),
  **Statement** (idioma, digest), **Attachment**.
- **Dataset** (`description`, `autojudge`, `time_limit`, `memory_limit`
  múltiplo de MiB, `task_type` + parámetros JSON, `score_type` + parámetros
  JSON), **Manager**, **Testcase** (`codename`, `public`, `input`, `output`
  como digests).
- **Submission** (`opaque_id`, participation, task, `timestamp`,
  `language`, `comment`, `official`), **File**, **Token**,
  **SubmissionResult** (por dataset: compilación — outcome, texto, tries,
  stdout/stderr, tiempo, memoria, shard, rutas/digests del sandbox —,
  evaluación, `score`, `score_details`, `scored_at`, `public_score`,
  `public_score_details`, `ranking_score_details`), **Executable**,
  **Evaluation** (por caso: outcome, `text`, `admin_text`, tiempo, tiempo de
  pared, memoria, shard, sandbox).
- **UserTest**, **UserTestFile**, **UserTestManager**, **UserTestResult**,
  **UserTestExecutable**.
- **FSObject** (large objects de PostgreSQL). File cacher con backends
  `DBBackend` (contenido en la BD), `FSBackend` (disco) y `NullBackend`;
  caché local por servicio.

### 1.4 Juez

- **Tipos de problema** (`cms/grading/tasktypes`): **Batch** (`alone` o
  `grader`; E/S por stdin/stdout o archivos; `diff` o `comparator`),
  **BatchAndOutput** (Batch + casos solo-salida listados en un parámetro;
  el comparador recibe `batch`/`outputonly` en argv[4]), **OutputOnly**
  (`output_<codename>.txt`, envío parcial), **Communication** (manager +
  N procesos por FIFOs o stdin/stdout, con o sin grader), **Interactive**
  (controlador que lanza instancias de la solución; límites propios del
  controlador; marcado como reciente/poco probado), **TwoSteps**
  (desaconsejado).
- **Puntuación** (`scoretypes`): **Sum**, **GroupMin**, **GroupMul**,
  **GroupThreshold**; los de grupo aceptan `[[max, n_casos | regex], …]`.
- **Protocolo de checker/manager** (`steps/trusted.py`): una línea en stdout
  con el resultado (real 0–1) y una en stderr con el texto;
  `translate:success|partial|wrong` se traduce. El comparador recibe
  `input correct_output user_output`. testlib (parche `testlib-cms`) se
  incluye para Polygon.
- **White diff** (`steps/whitediff.py`): compara ignorando espacios en blanco.
- **Lenguajes** (13): `C11 / gcc`, `C++11 / g++`, `C++14 / g++`,
  `C++17 / g++`, `C++20 / g++`, `C# / Mono`, `Haskell / ghc`, `Java / JDK`,
  `Pascal / fpc`, `PHP`, `Python 3 / CPython`, `Python 3 / PyPy`, `Rust`.
  No hay Go ni Kotlin.
- **Sandbox**: isolate ≥ 2.0 con `--cg`, `--box-id`, `--dir`, `--fsize`,
  `--stack`, `--cg-mem`, `--processes`, `--time`, `--wall-time`,
  `--extra-time`, `--meta`; límites de compilación y de ejecuciones
  confiables en la configuración.

### 1.5 Herramientas (`cmscontrib`)

`cmsAddAdmin`, `cmsAddParticipation`, `cmsAddStatement`, `cmsAddSubmission`,
`cmsAddTeam`, `cmsAddTestcases`, `cmsAddUser`, `cmsCleanFiles`,
`cmsDumpExporter`, `cmsDumpImporter`, `cmsDumpUpdater` (+ `updaters/` desde
1.5), `cmsExportSubmissions`, `cmsImportContest`, `cmsImportDataset`,
`cmsImportTask`, `cmsImportTeam`, `cmsImportUser`, `cmsRWSHelper`,
`cmsRemoveContest`, `cmsRemoveParticipation`, `cmsRemoveSubmissions`,
`cmsRemoveTask`, `cmsRemoveUser`, `cmsSolutionChecker`, `cmsSpoolExporter`,
`cmsPrometheusExporter`, `cmsTelegramBot`. Loaders: **italy_yaml**
(`task.yaml`, `contest.yaml`, `gen/GEN`), **Polygon** (`problem.xml`) y
**TPS** (`problem.json`). Dump: `contest.json` con `_version` y `_objects`,
`files/` y `descriptions/`.

### 1.6 Instalación y operación del original

Instalación manual de compiladores, isolate, PostgreSQL y un virtualenv;
escribir `cms.toml`; `cmsInitDB`; arrancar `cmsResourceService` en cada
máquina. Docker orientado a desarrollo y pruebas. Sin instalador, sin
actualización con respaldo ni vuelta atrás, sin verificación del host.

### 1.7 Traducciones y pruebas

25 traducciones en `cms/locale` (ar, bg, bs, cs, de, es, es_CL, et, fr, he,
hu, it, ja, ko, lt, lv, nl, ro, ru, sl, th, uk, vi, zh_CN, zh_TW) del dominio
`cms` (sitio del concursante). El admin no está traducido. Pruebas: unitarias
(`cmstestsuite/unit_tests`) y funcionales (`cmstestsuite/code`,
`cmstestsuite/tasks`: soluciones correctas, incorrectas, TLE, MLE, errores
de compilación, comunicación… en varios lenguajes).

### 1.8 Diferencias con el inventario del prompt

1. Los tiempos del concurso viven en **grupos** (`groups`), no en `contests`.
2. Hay **C++11/14/17/20**, **PHP** y **C11**; no hay Go ni Kotlin (se
   agregan).
3. La configuración es `cms.toml` (antes JSON).
4. Ya existen `opaque_id` en envíos, `admin_text` en evaluaciones,
   `scored_at`, *claim* de preguntas y export del ranking en CSV.
5. No hay PrintingService.

---

## 2. Arquitectura objetivo

Resumen; el porqué de cada punto está en DECISIONS.md.

- **Un binario** `cms` con subcomandos `contest-web`, `admin-web`,
  `ranking-web`, `dispatcher`, `worker`, `monitor`, `ctl`, `migrate`
  (y `version`, `healthcheck`); instalado también como `cmsctl` = `cms ctl`
  (D1).
- **PostgreSQL** con SQL explícito: sqlc para consultas estáticas, pgx a mano
  para filtros dinámicos; sin ORM; migraciones embebidas solo hacia
  adelante; cada índice con un comentario que lo justifica (D3).
- **Valkey/Redis**: Streams para las colas (prioridades, ACK explícito,
  reintentos con backoff, barrido de atascados) y pub/sub para eventos en
  vivo (D1).
- **Blobs** por SHA-256 en disco o S3 con caché LRU; en la BD solo digests.
- **Workers sin acceso a la BD** (D12).
- **Sandbox**: isolate (cgroups v2) + lanzador con **seccomp** →
  veredicto *Security Violation*; rutas siempre absolutas.
- **Webs** renderizadas en el servidor portando las plantillas Jinja2 del
  original a `html/template` sin cambiar HTML, clases, CSS ni JS; htmx o JS
  pequeño solo para lo nuevo; CSP estricta (D10), CSRF, cookies HttpOnly +
  SameSite, límites de tamaño y frecuencia; plantillas y estáticos embebidos.
- **Compatibilidad con CMS**: formatos italy_yaml, Polygon y TPS; dumps de
  CMS; semántica de tipos de problema, puntuación, tokens, ventanas y modos;
  protocolos de checker/manager + testlib; API JSON de ContestWebServer;
  API REST de RankingWebServer.
- **Lenguajes en archivos** `config/languages/*.yaml`.
- **Observabilidad**: logs JSON, `/metrics` (Prometheus) y `/healthz` en
  todos los servicios; pprof en el listener de operaciones (D7).

---

## 3. Requisitos no funcionales

- **N1 Rendimiento (objetivo de carga del MVP).** Con todos los servicios
  salvo los jueces en **una máquina de 2 vCPU y 4 GB** (PostgreSQL y Valkey
  incluidos), probado con k6:
  - 500 concursantes activos navegando (una página cada 10–20 s): p95 de las
    páginas < 300 ms y p99 < 800 ms;
  - envíos: 2/s sostenidos y ráfaga de 20/s durante 60 s (cierre del
    concurso): p95 de aceptación del envío < 500 ms, cero envíos perdidos;
  - 1500 conexiones de eventos en vivo abiertas (concursantes, admins y
    scoreboard público); un veredicto llega al cliente en < 2 s (p95)
    después de guardarse;
  - 2000 espectadores del scoreboard público;
  - 0 respuestas 5xx; errores totales < 0,1 %.
  - Sobrecarga del dispatcher < 50 ms por trabajo (sin contar la ejecución).
- **N2 Sin consultas N+1**: cada página hace un número de consultas acotado
  que no crece con el número de filas (verificado en pruebas con un contador
  de consultas).
- **N3 Durabilidad**: reiniciar o matar (`kill -9`) cualquier servicio a
  mitad de una evaluación no pierde ni duplica trabajos ni resultados; un
  trabajo atascado vuelve a la cola en < 60 s.
- **N4 Seguridad del juez**: la batería maliciosa (fork bomb, memoria, red,
  archivos, señales, syscalls prohibidas) termina con el veredicto correcto
  y sin afectar al host ni a otros envíos.
- **N5 Seguridad web**: CSP estricta, CSRF en cada POST, cookies HttpOnly +
  SameSite, límites de tamaño y de frecuencia, contraseñas con bcrypt, sin
  secretos en logs.
- **N6 Observabilidad**: logs JSON con el nombre del servicio, `/metrics` y
  `/healthz` en todos los servicios.
- **N7 Traducible**: todo texto visible pasa por la función de traducción;
  el admin está completo en español (prueba); agregar un idioma al sitio
  del concursante es agregar un archivo `.po`.
- **N8 AGPL**: cada sitio muestra en el pie un enlace al código de la versión
  que corre (`/source`).
- **N9 Misma interfaz**: cada página existente se ve igual que en CMS
  (capturas lado a lado); lo nuevo usa los componentes visuales del
  original.
- **N10 Operación**: un solo comando instala; `cmsctl upgrade` respalda,
  migra y vuelve atrás si algo falla.

---

## 4. MVP

Lo mínimo para correr **un concurso IOI real de punta a punta**, instalado
con un comando. Cada fase termina con código, pruebas, documentación, un
commit y una revisión de campo.

### F0 — Cimientos

- **R0.1** Repositorio Go con licencia AGPL, `LICENSE.txt`, `AUTHORS.txt`
  del original + el aviso del fork, cabecera SPDX en cada archivo Go.
  *Aceptación:* `scripts/check-headers.sh` pasa en `make lint`.
- **R0.2** Documentos `SPEC.md`, `CLAUDE.md`, `PROGRESS.md`,
  `DECISIONS.md`, `AUDIT.md`. *Aceptación:* existen y se enlazan.
- **R0.3** Configuración YAML (`cms.yaml`) con variables `CMS_*` (y
  `CMS_*_FILE`) que la sobrescriben; claves desconocidas y valores inválidos
  son errores; `secret_key` obligatoria para las webs y nunca generada por
  proceso. *Aceptación:* pruebas de `internal/config`; `config/cms.sample.yaml`
  documenta todos los valores por defecto (prueba).
- **R0.4** Logs JSON por stdout con el nombre del servicio; métricas
  Prometheus y `/healthz` (público mínimo y detallado en el listener de
  operaciones) en todos los servicios. *Aceptación:* prueba de humo de
  `cmd/cms` levanta los seis servicios contra PostgreSQL y Valkey reales y
  consulta `/healthz` y `/metrics`.
- **R0.5** Binario único con subcomandos y alias `cmsctl`. *Aceptación:*
  pruebas de `cmd/cms`.
- **R0.6** Migraciones embebidas, solo hacia adelante, con checksum, lock
  entre procesos y rechazo de esquemas desfasados o más nuevos; índices con
  comentario. *Aceptación:* pruebas de `internal/db`.
- **R0.7** Infraestructura de pruebas: `make test` levanta PostgreSQL y
  Valkey desechables (nativos o Docker) con `scripts/infra.sh`;
  `testutil.DB(t)` da una base migrada y aislada; `testutil.Redis(t)` un
  espacio de claves aislado. *Aceptación:* pruebas de aislamiento en
  `internal/db` e `internal/kv`.
- **R0.8** CI con tres trabajos: lint, test (race detector, PostgreSQL y
  Valkey reales) y sandbox (isolate). *Aceptación:* `.github/workflows/ci.yml`
  en verde en GitHub.
- **R0.9** `make dev` (Docker Compose) y `make dev-native` levantan el stack
  completo. *Aceptación:* todos los contenedores/procesos quedan sanos.
- **R0.10** Interfaz traducible desde el día 1: catálogos `.po` (las 25
  traducciones de CMS portadas), extractor de cadenas (`make generate`),
  prueba que falla si un texto del admin no tiene traducción al español.
  *Aceptación:* pruebas de `internal/i18n` y `locale`.

### F1 — Datos

- **R1.1** El modelo completo de CMS (esquema 49, §1.3) en PostgreSQL con sus
  restricciones (CHECK, UNIQUE, FK con CASCADE), mismos nombres de tablas y
  columnas salvo donde DECISIONS.md diga lo contrario. *Aceptación:* prueba
  que compara columnas y restricciones con una lista derivada del original;
  pruebas de restricciones.
- **R1.2** Almacén de blobs direccionado por SHA-256 con backends **local** y
  **S3**, escritura atómica, verificación del digest al leer y caché LRU con
  tope de tamaño. *Aceptación:* pruebas de ambos backends (S3 contra MinIO o
  un servidor compatible), de concurrencia y de expulsión LRU.
- **R1.3** Consultas sqlc para lo estático y pgx para filtros; ninguna
  consulta N+1 en las listas. *Aceptación:* `make generate` reproduce el
  código; pruebas con contador de consultas.
- **R1.4** Tablas propias del fork: auditoría, ajustes del servidor
  (`server_settings`), roles extendidos. *Aceptación:* pruebas.

### F2 — Sandbox y worker

- **R2.1** Wrapper de isolate: init/run/cleanup, lectura de `--meta`, límites
  de tiempo (CPU y pared), memoria (cgroup), procesos, tamaño de archivo,
  directorios montados **siempre absolutos**; absorción de los límites de
  CMS (`[sandbox]` de la configuración).
- **R2.2** Lanzador con filtro **seccomp** que da el veredicto *Security
  Violation* ante syscalls prohibidas.
- **R2.3** Compilación y ejecución con **caché de compilaciones** por digest
  de fuentes + lenguaje + managers.
- **R2.4** Batería maliciosa en `make test-sandbox`: fork bomb, memoria,
  red, escritura fuera del sandbox, archivos enormes, señales, syscalls
  prohibidas, sleep (TLE de pared), salida gigante.
- **R2.5** `cms-verify-host` (`cmsctl verify-host`): cgroups v2, isolate,
  núcleos sin SMT reservados para juzgar, frecuencia fija, swap.
- **R2.6** Worker sin BD: recibe trabajos de Valkey, pide blobs por digest,
  devuelve resultados; ACK explícito.
- **R2.7** Imagen de worker (isolate + compiladores) para Compose.
  *Aceptación de F2:* `make test-sandbox` en verde en CI; soluciones de
  ejemplo dan AC/WA/TLE/MLE/RE/CE/SV esperados.

### F3 — Tipos de problema, checkers y lenguajes

- **R3.1** Batch (con y sin grader, stdin/stdout o archivos), BatchAndOutput,
  OutputOnly, Communication (N procesos, FIFOs o stdio), Interactive
  (controlador con testlib) y TwoSteps, con la semántica de CMS.
- **R3.2** Checkers integrados: white diff (el de CMS), exacto, reales con
  tolerancia absoluta/relativa; protocolo de comparador de CMS; testlib.
- **R3.3** Sum, GroupMin, GroupMul, GroupThreshold, con los mismos detalles
  públicos/privados y redondeo (`score_precision`).
- **R3.4** Lenguajes en YAML (`config/languages/*.yaml`): los 13 de CMS con
  sus nombres exactos + Go y Kotlin.
  *Aceptación de F3:* las tareas de `cmstestsuite/tasks` portadas dan los
  puntajes esperados.

### F4 — Dispatcher

- **R4.1** Colas en Valkey Streams con prioridades justas: envíos de
  concursantes antes que pruebas de usuario y reevaluaciones; dentro de cada
  prioridad, por antigüedad.
- **R4.2** Resultados idempotentes (un resultado repetido no cambia nada),
  reintentos con backoff, barrido de trabajos atascados.
- **R4.3** Puntajes y agregados por tarea con `score_mode` (max,
  max_subtask, max_tokened_last) y tokens.
- **R4.4** Reevaluación por concurso, tarea, dataset, usuario o envío
  (recompilar, reevaluar o solo repuntuar).
- **R4.5** Datasets en segundo plano (autojudge) y cambio del dataset activo
  en vivo.
- **R4.6** Prueba de caos: matar el dispatcher o un worker a mitad de
  evaluación no pierde ni duplica nada (N3).

### F5 — Sitio del concursante

- **R5.1** Todas las páginas de §1.2 con el HTML/CSS/JS del original,
  verificadas con capturas lado a lado.
- **R5.2** Login (contraseña, IP, autologin, registro), tiempos (grupos,
  ventanas por usuario, `extra_time`, `delay_time`, análisis), enunciados
  (PDF, Markdown, LaTeX) y adjuntos.
- **R5.3** Envíos y pruebas de usuario con todos los límites; tokens.
- **R5.4** Detalle por subtarea y por caso público; veredicto **PA**
  (parcial); espera configurable entre envíos.
- **R5.5** Comunicación: preguntas, respuestas, anuncios y mensajes
  aparecen sin recargar.
- **R5.6** Scoreboard del concursante según la configuración del concurso.
- **R5.7** **API JSON compatible** (§1.2) probada contra la documentación
  del original.
- **R5.8** **Tiempo real con indicador** verde/amarillo/rojo junto al
  usuario: `live.js` compartido, regiones `data-live`, eventos agrupados
  (≤ 1/s por concurso), jitter, resincronización al reconectar, ping
  inmediato y cada ≤ 15 s, `X-Accel-Buffering: no`, sondeo si faltan pings.
- **R5.9** Horas en **tiempo de concurso** (con la hora absoluta como
  tooltip) y una sola zona horaria de servidor.

### F6 — Administración

- **R6.1** Páginas del original: concursos, grupos, tareas, datasets,
  usuarios, equipos (importar CSV), participaciones, admins.
- **R6.2** Preparación de problemas desde el navegador (§4 del prompt):
  importar zip con vista previa; opciones como el formulario del original y
  como `problem.yaml` descargable/editable; archivos uno por uno reconocidos
  por nombre; casos uno por uno (salida escrita, subida, vacía o generada por
  la solución de referencia), generador en el sandbox, importación desde zip,
  una página por caso; envíos de prueba con veredicto por caso; lista de lo
  que le falta al problema.
- **R6.3** Envíos con filtros, reevaluación, comunicación **por concurso**
  (respuestas rápidas, tomar/ignorar, contador de pendientes).
- **R6.4** Ranking con export CSV y el enlace secreto del scoreboard.
- **R6.5** Roles: todo, mensajería, preparador de problemas, solo lectura.
- **R6.6** Registro de auditoría de cada cambio.
- **R6.7** Página de jueces: workers, colas, trabajos en curso, reencolar.
- **R6.8** Página **Servidor**: zona horaria y direcciones públicas de los
  sitios (`":PUERTO"` = mismo host de la página).
- **R6.9** Todo en tiempo real (colas, preguntas, puntajes).

### F7 — Scoreboard público

- **R7.1** API REST compatible con RankingWebServer y la interfaz
  `cmsranking` sin cambios visuales.
- **R7.2** El dispatcher empuja deltas en vivo; SSE `/events` como el
  original.
- **R7.3** Congelamiento al final, historial de puntajes.
- **R7.4** Visibilidad: las opciones del original más **solo
  administradores** y **enlace secreto** (derivado de `secret_key`), que
  funciona también en instalaciones por puertos.

### F8 — Importación desde CMS

- **R8.1** Loaders italy_yaml, Polygon y TPS con las mismas reglas que
  `cmscontrib/loaders`.
- **R8.2** Formato propio de paquete: `problem.yaml`, `statement/`,
  `tests/`, `graders/`, `attachments/`, `solutions/` (veredicto esperado en
  el nombre) con reporte de validación; exportable.
- **R8.3** Importador de **dumps de CMS** (`contest.json`, esquema 49 y
  anteriores vía las reglas de `updaters/`).

### F9 — Instalación simple

- **R9.1** `curl -fsSL …/install.sh | sudo bash` con modos `--domain`
  (Caddy, HTTPS automático) y por puertos (`--http-ports 8000,8001,8002`);
  instala PostgreSQL, Valkey, isolate, compiladores, systemd y firewall;
  genera los secretos **una vez**; escribe `cms.yaml` solo si no existe;
  muestra una contraseña de admin generada (sin admin/admin).
- **R9.2** `cmsctl upgrade`: respaldo, migraciones y vuelta atrás automática.
- **R9.3** `cmsctl backup` / `restore` (triggers desactivados durante la
  carga, tablas sembradas regeneradas) y respaldos programados.
- **R9.4** Docker Compose de producción e imágenes en GHCR (amd64 y arm64).
- **R9.5** Releases con GoReleaser (binarios, checksums, changelog).
- **R9.6** Documentación de requisitos, dimensionamiento, instalación,
  actualización y desinstalación (español e inglés).

### Criterio de aceptación del MVP

En una VM limpia de Ubuntu, el instalador deja todo funcionando. Desde el
navegador se crea un concurso con un problema importado de un paquete de CMS
y otro armado a mano; se inscriben usuarios por CSV. Un concursante obtiene
AC, PA y TLE con actualización en vivo y recibe la respuesta a su
clarificación sin recargar. El scoreboard público se actualiza al instante.
Matar un worker a mitad de una evaluación no pierde nada. `cmsctl upgrade`
vuelve atrás ante un fallo simulado. La prueba de carga con k6 cumple N1.

---

## 5. Después del MVP

En este orden aproximado; cada bloque se detalla en su propio
`SPEC_<NOMBRE>.md` cuando empiece.

- **A — Modo ICPC**: veredicto binario, penalización por minuto,
  congelamiento, globos (cola para el staff), desempate por tiempo.
- **B — Operación del concurso**: pausar envíos (concurso o problema),
  extensión de tiempo global en caliente y extra por usuario, modo práctica,
  concursos por equipos con envíos compartidos, sedes con su propio inicio,
  registro abierto, política de contraseñas, duración de sesión, bloqueo de
  sesiones simultáneas, restricción por IP, impersonar a un concursante en
  solo lectura.
- **C — Herramientas del admin**: estadísticas por problema, ajuste manual
  de puntaje con justificación, exports (CSV, JSON, PDF), descarga de envíos
  en zip, plagio con vista lado a lado, certificados PDF, credenciales en
  PDF, impresión con cola, apelaciones, 2FA (TOTP), líderes de delegación,
  bot de Telegram.
- **D — Preparación de problemas**: editor de enunciados con vista previa y
  PDF, ejemplos explicados, validación con soluciones esperadas, exportar
  problema como paquete, configuración del concurso en Git.
- **E — Evaluación**: cortocircuito por subtarea, posición en la cola y
  tiempo estimado, calibración de tiempos por lenguaje, envíos sospechosos
  marcados.
- **F — Confiabilidad**: réplica de lectura para scoreboards, archivado WAL,
  Valkey con Sentinel, varios servidores web (sesiones en Valkey), alertas,
  ensayo y simulacro de fallos automatizados, Ansible, worker externo con
  token.
- **G — Accesibilidad e idiomas** (sin cambiar el diseño): teclado completo,
  lectores de pantalla, RTL, tamaño de letra, alto contraste, modo oscuro,
  editor de código con borrador local, 16+ idiomas para el concursante,
  fotos, institución y medallas en el scoreboard.
- **H — Archivo de concursos**: exportar/importar un concurso completo con
  envíos y resultados.

### Pendientes conocidos del proyecto anterior (hechos bien desde el MVP)

| Pendiente | Requisito |
|---|---|
| Todo en tiempo real con indicador de conexión | R5.8, R6.9, R7.2 |
| Comunicación por concurso | R6.3 |
| Horas en tiempo de concurso | R5.9 |
| Enlace secreto del scoreboard en instalaciones por puertos | R7.4, R6.8 |

---

## 6. Fuera de alcance

- Rediseñar la interfaz o usar skills de diseño (la interfaz es la de CMS).
- RPC propio entre servicios (se usa Valkey).
- Soporte de sistemas sin cgroups v2 para juzgar.

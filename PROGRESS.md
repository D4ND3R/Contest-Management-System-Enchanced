# Avance

Una sección por fase: lo hecho, las pruebas que lo cubren y lo pendiente.

---

## F0 — Cimientos (hecha)

### Hecho

- Repositorio Go (`github.com/d4nd3r/contest-management-system-enchanced`,
  Go 1.26+, toolchain 1.27.1) con `LICENSE.txt` (AGPL-3.0) y `AUTHORS.txt`
  del original más el aviso del fork; cabecera SPDX en cada archivo Go.
- Documentos: SPEC.md (inventario real del upstream en el commit `1914a5a`,
  esquema 49, y requisitos del MVP con criterios de aceptación),
  CLAUDE.md, DECISIONS.md (D1–D17), AUDIT.md, este archivo.
- Binario único `cms` con `contest-web`, `admin-web`, `ranking-web`,
  `dispatcher`, `worker`, `monitor`, `ctl`, `migrate`, `version` y
  `healthcheck`; alias `cmsctl`. Los servicios son esqueletos: conectan a sus
  dependencias (con reintentos y verificación del esquema), exponen
  `/healthz`, `/metrics`, pprof y, las webs, `/source` (AGPL) con CSP y
  cabeceras de seguridad, y se apagan limpio con SIGTERM.
- Configuración `cms.yaml` + `CMS_*` + `CMS_*_FILE`; `cms ctl config
  check|show|env`, `cms ctl gen-secret`; `config/cms.sample.yaml`.
- Logs JSON (`log/slog`) con el nombre del servicio; registro Prometheus con
  `cms_build_info` y métricas HTTP por ruta (patrón, sin cardinalidad alta).
- Migraciones embebidas con checksum, lock entre procesos y rechazo de
  esquemas desfasados; primera migración: `server_settings`.
- `scripts/infra.sh` (PostgreSQL 16 y Valkey/Redis nativos o Docker),
  `testutil.DB/EmptyDB/Redis`, `make test`, `make lint`, `make generate`,
  `make bench`, `make test-sandbox`.
- `make dev` (Compose: PostgreSQL, Valkey, migrate y los seis servicios con
  healthchecks) y `make dev-native` (`scripts/dev.sh` con PIDs).
- i18n: parser `.po`, formas plurales de gettext, catálogos con fallback
  (`es_CL` → `es` → inglés), negociación de `Accept-Language`,
  `Format("%(x)s")`; las **25 traducciones de CMS** portadas; extractor de
  cadenas (`make generate`); dominio `admin` con español obligatorio.
- CI (`.github/workflows/ci.yml`): lint + `make generate` sin diferencias +
  compilación cruzada amd64/arm64; test con PostgreSQL y Valkey de servicio;
  sandbox con isolate del repositorio apt oficial.
- Documentación: `docs/es` y `docs/en` (índice, configuración, desarrollo).

### Pruebas

| Área | Pruebas |
|---|---|
| Configuración | `internal/config`: archivo + entorno, `_FILE`, ruta por `CMS_CONFIG`, claves desconocidas, validaciones, nombres de variables únicos, redacción de secretos, `cms.sample.yaml` = valores por defecto |
| HTTP | `internal/httpx`: métricas por patrón de ruta, IP real tras N proxies, nonce CSP, límite de cuerpo, recover, `/source`, endpoints de operaciones, apagado con un stream SSE abierto |
| Observabilidad | `internal/obs`: logs JSON, `cms_build_info`, health checks con timeout y sin detalles en público |
| Migraciones | `internal/db`: desde cero e idempotente, migradores concurrentes aplican una vez, migración editada rechazada, base más nueva rechazada, índices con comentario, aislamiento entre bases |
| Valkey | `internal/kv`: prefijos aislados, Streams disponibles, borrado por prefijo |
| i18n | `internal/i18n`: formas plurales (ru, ar, sl…), `.po` (multilínea, contexto, plurales, fuzzy, obsoletos), `Format`, negociación; `extract`: Go y plantillas; `locale`: los 26 catálogos cargan, español del admin completo, `.pot` al día |
| Binario | `cmd/cms`: uso y alias, webs sin `secret_key` no arrancan, **prueba de humo** (migrate + los seis servicios contra PostgreSQL y Valkey reales: `/healthz`, `/metrics`, `/source`, CSP, `healthcheck`, apagado limpio), **el worker no depende de la BD** |
| Sandbox | `internal/sandbox` (tag `sandbox`): isolate ≥ 2.0 usable y rechaza `--dir` relativo |

Verificado en esta VM: `make lint`, `make test` (con `-race`), `make
dev-native` (seis servicios sanos, apagado limpio) y `make dev` (ocho
contenedores *healthy*). Verificado en GitHub Actions (run 37567326965):
lint, test y sandbox en verde; `TestIsolateIsUsable` corrió contra isolate
2.7 con cgroups v2.

### Pendiente / no verificado

- Esta VM tiene cgroups v1, así que las pruebas de sandbox solo corren en
  CI (o en una máquina con cgroups v2); en F2 eso incluye la batería
  maliciosa.
- La revisión de campo con navegador no aplica en F0 (aún no hay páginas).
- sqlc se incorpora en F1 junto con las primeras consultas.
- `cms.pot` (dominio del concursante) se regenerará desde nuestras
  plantillas en F5; hoy es el del original.

---

## F1 — Datos (hecha)

### Hecho

- `internal/db/migrations/0002_cms_model.sql`: las 26 tablas de CMS (esquema
  49) con los mismos nombres de tablas, columnas y restricciones, más
  `audit_log` y `admins.permission_tasks` (rol de preparador). Dominios y
  enums como CHECK (D18), digests SHA-256 (D19), FK del dataset activo que
  no borra datos (D20), índices justificados.
- `internal/blob`: backends local, S3 (minio-go) y remoto (servidor HTTP
  para jueces de otras máquinas con token derivado, D25), caché LRU en disco
  con singleflight y lock por servicio; todo se verifica contra su digest.
- sqlc v1.31.1 (`make tools`, `make generate`): 189 consultas en
  `internal/store/queries`, código en `internal/store/q`; `internal/store`
  con transacciones con reintento, `ListSubmissions` con filtros y keyset,
  escrituras de resultados idempotentes (D21, D23), páginas acotadas (D24).
- `db.QueryCounter` y `testutil.CountingDB` para probar que no hay N+1.
- `cms ctl blob-token`; configuración `storage.remote.{url,token}`.

### Pruebas

| Área | Pruebas |
|---|---|
| Esquema | `internal/db/schema_test.go`: columnas, claves, UNIQUE, FK con sus acciones, 69 CHECK, índices de CMS, valores por defecto, 110 filas inválidas por SQLSTATE y restricción, 15 cascadas, grafo completo |
| Consultas | `internal/store`: cada consulta corre (la suite falla si una nunca se ejecuta), 16 listas con exactamente 1 consulta para N = 1 y N = 20, idempotencia ante reentregas, reintento de transacciones serializables, planes de las páginas del admin, benchmark con 45 000 envíos |
| Blobs | `internal/blob`: suite común para todos los backends, corrupción detectada, digests inválidos y path traversal, concurrencia con `-race`, caché (LRU, reinicio, singleflight, lock, un solo hash), servidor remoto (401/403/422/413, cupos de subida), S3 con bucket inexistente, benchmarks |

`make lint` y `make test` en verde.

### Pendiente

- Contador de generación de `submission_results` para descartar resultados
  tardíos en SQL (F4, D23).
- Recolector de blobs sin referencias (con período de gracia): F9 junto con
  los respaldos.

---

## F2 + F3 — Juez: sandbox, colas, worker, tipos de problema (hecha)

Se hicieron juntas: el worker necesita los tipos de problema para ejecutar
trabajos reales.

### Hecho

- `internal/sandbox`: envoltorio de isolate 2.x (cajas con lock por id
  compartido entre workers, directorios siempre absolutos, lectura de
  archivos por `os.Root`, límites de CMS con cgroups v2 o rlimits en
  desarrollo, estados donde un error interno nunca es veredicto) y lanzador
  seccomp: el propio binario `cms` con un filtro BPF escrito a mano que da
  *Security violation* (D28, D29).
- `cms ctl verify-host`: cgroups v2, isolate y su cgroup, rangos de cajas,
  SMT, governor, swap, hard links, seccomp y una prueba real de caja.
- `internal/queue` (cola durable con prioridades sobre Streams, D27) e
  `internal/job` (protocolo dispatcher ↔ worker, port de `Job.py`, con
  intento y generación).
- `config/languages`: los 13 lenguajes de CMS + Go y Kotlin como plantillas
  YAML (D30); `internal/grading/{language,steps,checker}`: pasos de
  compilación/evaluación y checkers (white diff, exacto, reales,
  comparadores y testlib, D31).
- `internal/grading/tasktype`: Batch, BatchAndOutput, OutputOnly,
  Communication, Interactive y TwoSteps (D32);
  `internal/grading/{scoretype,scoring}`: Sum, GroupMin, GroupMul,
  GroupThreshold y los modos max, max_subtask y max_tokened_last.
- `internal/worker`: ranuras que consumen la cola, reserva de cajas,
  vigilante, caché de compilación en Valkey, registro de workers vivos para
  la página de jueces y el monitor (D33).
- Imagen del worker (`deploy/docker/Dockerfile.worker`) con isolate y los
  compiladores; su entrypoint mantiene el cgroup de isolate dentro del
  contenedor. Compose de desarrollo la usa.

### Pruebas

| Área | Pruebas |
|---|---|
| Sandbox | batería maliciosa (fork bomb, memoria, pila, red, escritura fuera, archivos enormes, salida gigante, señales, syscalls prohibidas, bucle de CPU, sleep), hola mundo en 12 lenguajes bajo seccomp, filtro BPF, pool concurrente, FIFOs, pipes |
| Lenguajes y checkers | comandos idénticos a las clases de CMS en casos generados, white diff contra `_white_diff` de CMS en 457 casos, parser de managers en 23, cada lenguaje compila y corre en caja real |
| Tipos de problema | `cmstestsuite/Tests.py` portado con sus tareas y programas (AC, WA, TLE, MLE, RE, CE, comparadores, graders, Communication, Interactive, TwoSteps, OutputOnly) |
| Puntajes | pruebas unitarias de CMS portadas y casos dorados generados con el CMS original |
| Worker | consumo y ACK, `kill -9` y reinicio sin perder trabajos, apagado, reservas de cajas, caché de compilación, prueba de punta a punta con caja real |
| CI | trabajo `sandbox` con isolate 2.7 y cgroups v2 (run 37722160673: todo pasa, 0 pruebas saltadas, "memory limit exceeded" real); trabajo `worker-image`: `verify-host` sin fallas dentro del contenedor |

### Pendiente

- La revisión de campo con el juez real llega con el dispatcher (F4) y el
  sitio del concursante (F5).
- Interactores de testlib y códigos de "parcialmente correcto" de testlib
  (16 en adelante): no soportados, como en CMS.

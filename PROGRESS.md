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

---

## F4 — Dispatcher (hecha)

### Hecho

- Migración 0004: generación en los resultados (D35), caché `task_scores`
  por participación y tarea (D34) e índices parciales para el barrido.
- `internal/judging` (port de `esoperations.py` y del cálculo de
  ScoringService): operaciones pendientes con las prioridades e intentos
  de CMS, barrido por ventanas, armado de trabajos, aplicación de
  resultados por lotes con puntaje y caché en la misma transacción,
  invalidación por alcance (concurso, tarea, dataset, participación,
  envío, caso) y nivel (compilación, evaluación, puntaje), cambio de
  dataset activo y un stream durable de notificaciones.
- `internal/dispatcher`: un activo con lease y reservas (D36), consumo de
  notificaciones y resultados, encolado sin duplicados, reintentos,
  servidor de blobs para jueces remotos (`dispatcher.blob_listen`) y un
  gancho para el scoreboard (F7).
- `internal/live`: eventos en vivo por Valkey con secuencia global, hub por
  proceso y SSE (D37).
- `cms ctl reevaluate` y `cms ctl judging-status`.

### Pruebas

| Área | Pruebas |
|---|---|
| Judging | estados pendientes, intentos máximos, consultas constantes (N = 1 y 50), resultados repetidos y viejos, puntajes Sum/GroupMin con los detalles JSON de CMS, caché con los tres modos y tokens, invalidaciones, carrera con el cambio de dataset, planes de consulta con 30 000 envíos |
| Dispatcher | compilar → evaluar → puntuar con eventos, reintentos, intentos máximos, invalidación durante la evaluación, notificación perdida, toma del lease sin trabajo doble, reinicio a mitad de lote |
| Punta a punta | `TestEndToEnd` (isolate real): 11 envíos C++/Python y 2 pruebas de usuario |
| Caos | `TestChaos`: binario real, 4–5 `kill -9` del dispatcher a mitad de lote, 5 de workers y un `CLIENT KILL`: cada trabajo corrió una sola vez y los puntajes coinciden con una corrida limpia |
| Carga | `TestLoad` con 2 CPU: 20 envíos/s durante 60 s con 20 casos; veredicto p95 < 30 ms tras la última evaluación; 0,3 ms de CPU del dispatcher por trabajo |
| Eventos | 1500 SSE: p95 ≈ 20 ms por difusión, ~34 KiB por conexión |

### Pendiente

- El scoreboard público recibe los cambios en F7 (hoy el gancho no hace
  nada).
- Un evento en vivo puede perderse si el dispatcher muere entre el commit
  y la publicación: el sitio del concursante conserva el sondeo de CMS de
  los envíos pendientes (F5).
- k6 sobre hardware real de 2 vCPU (las cifras son con `taskset`).

---

## F8 — Importación desde CMS (hecha)

### Hecho

- `internal/importer`: modelo de importación (concursos con grupos y
  participaciones, tareas con enunciados, adjuntos, datasets, managers y
  casos, usuarios, equipos) con los valores por defecto de CMS; registro
  de loaders con autodetección (`choose_loader`); reporte de validación
  que rechaza lo que el dispatcher nunca podría juzgar; escritor en una
  transacción con semántica de actualización de `ImportContest`/
  `ImportTask` y `--dry-run` (D38).
- Loaders: **italy_yaml** (`contest.yaml`, `task.yaml`, `gen/GEN` con
  `#ST`, todos los tipos de problema), **Polygon** (`problem.xml`,
  `contest.xml`, `cms_conf.py` leído como literales, checkers testlib
  compilados con el `testlib.h` parcheado de CMS) y **TPS**
  (`problem.json`, subtareas, graders, checker, managers) (D39).
- **Formato propio** (`problem.yaml`, directorio o zip leído sin extraer)
  con reporte de validación y exportación; ida y vuelta exacta (D40).
- **Dumps de CMS**: versiones 14–49 (updaters portados), directorio, zip o
  tar; SHA-1 → SHA-256; ids nuevos de las secuencias; `COPY` en una
  transacción; `-d`, `--reuse-users`, `-G/-S/-U/-X`; `--dry-run -d` sin
  bloquear (D41).
- `cms ctl import-contest`, `import-task`, `import-users`, `import-team`,
  `import-dataset`, `validate-task`, `validate-contest`, `export-task` e
  `import-dump`, con las opciones de los comandos de CMS.
- Guías: [importar](docs/es/importar.md) y
  [formato de paquete](docs/es/formato-de-paquete.md) (y en inglés).

### Pruebas

| Área | Pruebas |
|---|---|
| Escritor | ida y vuelta, transacción (una tarea con error no deja nada), actualización de datasets en su lugar y nuevos inactivos, envíos conservados, sentencias constantes con 4 y 200 casos y con 3 y 150 participantes |
| italy_yaml | el concurso `demo` y tareas sintéticas dan los mismos archivos, parámetros y casos que `cmsImportContest`/`cmsImportTask` del original |
| Polygon y TPS | seis importaciones de referencia hechas con los loaders del original (esquema 49): mismas filas y mismos binarios de checkers compilados |
| Formato propio | todos los tipos de problema y de puntaje: paquete → base → exportar → importar da filas idénticas; zip slip, enlaces, CRC, duplicados; reporte de validación |
| Dumps | `testdata/dumps/demo` y una instalación completa exportada por el original: 0 diferencias tabla por tabla con `cmsDumpImporter` (salvo admins, a propósito); v14 y v44 contra `cmsDumpUpdater`; digest corrupto; dumps `-c` con otro concurso; `-F`; `--dry-run -d` con lectores abiertos; sentencias constantes con 4 y 4000 evaluaciones |

### Pendiente

- Vista previa de un zip en la administración (F6): `pkg.Load`,
  `importer.ValidateTask` y `Writer.DryRun` ya la cubren.
- Juzgar las soluciones de `solutions/` contra su veredicto esperado (F6).

---

## F5 — Sitio del concursante (hecha)

### Hecho

- `internal/web` (núcleo compartido de las webs, D43): plantillas con
  herencia al estilo Jinja, funciones de las plantillas de CMS, elección de
  idioma de CMS, CSRF, cookies firmadas, estáticos, descargas, Markdown
  como markdown-it, límites de frecuencia.
- `internal/l10n`: formatos de fecha, hora, duración, tamaño y número de
  los 26 idiomas con los datos CLDR de babel 2.12.1 (`make l10n`).
- `internal/contestweb`: todas las páginas de CMS ContestWebServer
  (lista de concursos, entrada, inscripción, inicio, resumen, enunciados y
  adjuntos, envíos con sus límites, detalle con el HTML de los tipos de
  puntaje, tokens, pruebas de usuario, comunicación, documentación,
  errores), la API JSON de `docs/API.rst` con impersonación, las
  notificaciones por sondeo de CMS y eventos en vivo por SSE con `live.js`
  (indicador verde/amarillo/rojo, resincronización, vuelta al sondeo).
- Plantillas, CSS y JS de CMS portados; los manejadores en línea pasaron a
  `cws_handlers.js` (D10); enlace AGPL al código en el pie.
- Seguridad: límites por dirección y por usuario, cola de bcrypt justa,
  cuerpos de 1 MiB salvo envíos (D42).
- Guías: [sitio del concursante](docs/es/sitio-concursante.md) y
  [API](docs/es/api.md) (y en inglés).

### Pruebas

| Área | Pruebas |
|---|---|
| Núcleo web | CSRF, cookies (firma, vencimiento, flags), elección de idioma con 34 casos calculados por CMS, Markdown contra 860 entradas de markdown-it-py, estáticos, rangos, redirecciones seguras; `base.html` byte a byte contra la página de CMS |
| l10n | salidas doradas de babel para todos los idiomas |
| Sitio | autenticación y fases (casos de upstream), tokens (casos de upstream), cada handler con permisos, límites y CSRF, detalle de puntajes contra las plantillas de CMS, consultas acotadas con `CountingDB`, planes de consulta, SSE |
| API | contrato contra respuestas grabadas del CMS original |
| Comparación | 61 páginas/estados en en y es contra el CMS original con el mismo dump: mismo HTML normalizado, mismos códigos y redirecciones, sin errores de CSP ni de JS; capturas lado a lado; envíos juzgados por el dispatcher y un worker real que se actualizan sin recargar |
| Carga (2 CPU, 500 concursantes, 1500 SSE) | páginas p95 21–31 ms durante una ola de entradas; anuncios repartidos en el tiempo (antes p95 516 ms al llegar uno) |

### Pendiente

- Volver a medir con carga después de las correcciones de la revisión
  (cola de bcrypt con todos los núcleos, anuncios repartidos) y con k6 en
  hardware real de 2 vCPU.
- La cola de bcrypt no protege a quien comparte la dirección con el
  atacante (un NAT para toda la sala).
- `testdata/contests/demo/sum/sol/partial.cpp` saca 100: su caso de
  desborde todavía cabe en `int`.

---

## F9 — Instalación simple (hecha salvo pruebas en VM)

Por pedido del dueño esta fase se implementó sin pruebas: compila
(`go build ./...`, `go vet`), los scripts pasan `bash -n` y los Compose
`docker compose config`. Las pruebas van en la fase final.

### Hecho

- `scripts/install.sh`: modos `--domain` (Caddy, subdominios) y
  `--http-ports`, `--worker-only` para jueces adicionales, `--uninstall
  [--purge]`; PostgreSQL, Valkey/Redis ≥ 7, isolate (compilado en arm64),
  compiladores, unidades systemd con aislamiento, ufw, respaldos
  programados; secretos una vez, `cms.yaml` solo si no existe, primer admin
  con contraseña generada, `verify-host` y resumen (D45).
- `cms ctl add-admin`, `public-urls`, `backup`, `restore` (D44) y `upgrade`
  (D46); `db.MigrateTx`.
- `deploy/docker-compose.yml` de producción con secretos de Docker,
  `deploy/compose-init.sh`, `deploy/compose/{cms.yaml,Caddyfile}`,
  `deploy/env.example`; Dockerfiles multi-arquitectura
  (`BINARY_SOURCE=release`); workflows `images.yml` (GHCR, amd64 y arm64) y
  `release.yml` (GoReleaser, `.goreleaser.yaml`).
- El scoreboard local configurado en `dispatcher.rankings` en el instalador,
  los Compose y `scripts/dev.sh` (los `.dev/cms.yaml` existentes no se
  tocan: agregar el ranking a mano o borrarlo).
- Guías en español e inglés: instalación y dimensionamiento, actualización y
  respaldos, Docker, operación, problemas frecuentes, primeros pasos.

### Pendiente (fase de pruebas)

- Instalar en VMs limpias (Ubuntu 22.04, 24.04, Debian 12; amd64 y arm64),
  ambos modos y un juez adicional; volver a correr el instalador;
  desinstalar.
- Pruebas de `backup`/`restore` (ida y vuelta tabla por tabla, `--clean`,
  respaldo de un esquema anterior, archivo dañado) y de `upgrade` con
  `CMS_UPGRADE_FAIL_AT` en cada paso.
- Correr GoReleaser (`--snapshot`) y el workflow de imágenes; comprobar la
  imagen arm64 del worker.

---

## F6 + F7 — Administración y scoreboard público (hecha)

Por pedido del dueño, F6, F7 y F9 se construyeron sin pruebas por fase; las
pruebas se hacen al final.

### Hecho

- `internal/adminweb`: núcleo (login con límites, roles todo / mensajería /
  preparador / solo lectura declarados en cada ruta, auditoría obligatoria
  en la misma transacción, página Servidor, eventos en vivo) y las páginas
  de CMS: concursos, grupos, usuarios, equipos (importar CSV),
  participaciones, admins, comunicación por concurso, ranking (txt, CSV y
  enlace secreto), tareas, datasets, casos, envíos con filtros, pruebas de
  usuario, diff, reevaluación y la página de jueces.
- Preparación de problemas (R6.2): importar zip con vista previa,
  `problem.yaml` editable, archivos reconocidos por nombre, una página por
  caso con salida generada por la solución de referencia, generador,
  envíos de prueba con veredicto esperado (participación oculta
  `_preparer`) y la lista de lo que falta.
- `internal/rankingweb`: RankingWebServer portado (REST, SSE, historial,
  congelamiento, visibilidad pública / enlace secreto / solo admins) y el
  ProxyService portado como sink del dispatcher.

### Verificado

- `make lint` y la suite completa con `-race` en verde (las pruebas
  existentes; las páginas nuevas aún no tienen pruebas propias).
- Revisión de campo con `make dev-native` y el juez real: las 27 páginas
  de la administración cargan sin errores de JS ni de CSP; un concursante
  envía correcta, incorrecta y lenta por la API; las tres se juzgan, se ven
  en la lista de envíos del admin y el scoreboard público muestra 100.
- `cms ctl backup` → `restore --clean`: mismas filas y secuencias sanas.
- `cms ctl upgrade` con `CMS_UPGRADE_FAIL_AT=migrate`: respalda, falla,
  restaura y vuelve al binario anterior.

### Pendiente (fase final de pruebas)

- Pruebas de las páginas nuevas (roles, auditoría, consultas acotadas,
  comparación con las capturas de CMS), del scoreboard y del sink.
- Instalador en VMs limpias (Ubuntu 22.04/24.04, Debian 12), imágenes y
  GoReleaser.
- `scripts/dev.sh start` usa un `bin/cms` viejo si no se corrió `make build`.

---

## Fase final de pruebas (hecha)

- Pruebas nuevas de la administración (cada página por rol, acciones con
  auditoría, rankings, filtros, preparación de problemas), del scoreboard
  y su sink, y de los comandos de F9 (backup/restore, upgrade con falla
  simulada en cada paso, add-admin, public-urls). Encontraron y
  corrigieron 12 errores.
- `make lint` y la suite completa con `-race` en verde.
- Pendiente: instalador en VMs limpias, imágenes multi-arquitectura y
  GoReleaser (requieren infraestructura que esta VM no tiene); k6 sobre
  hardware real de 2 vCPU.

## Listo para usar en un servidor

- Importación de usuarios desde CSV en la administración (criterio del
  MVP: usuarios por CSV), opcionalmente agregándolos a un concurso.
- Instalador: Ubuntu 26.04 (isolate compilado desde el código fuente;
  C# no existe en 26.04 y se omite), y tres errores encontrados al
  probarlo en un contenedor Ubuntu 24.04 con systemd (sustitución de
  comandos en un comentario del heredoc, almacenamiento del juez local,
  `VERSION` de os-release pisando variables).
- Guía corta de instalación y uso (`docs/es/primeros-pasos.md`,
  `docs/en/getting-started.md`): compilar en el servidor mientras no haya
  una versión publicada.
- Verificado: instalador completo en Ubuntu 24.04 (contenedor); paquetes,
  compilación de isolate y la suite completa con PostgreSQL 18 y Valkey 9
  de Ubuntu 26.04. No verificado aquí: la instalación completa con systemd
  sobre 26.04 (esta VM usa cgroups v1).

## Errores del primer servidor real (v0.1.0) y v0.2.0

Instalando v0.1.0 en un servidor Ubuntu 26.04 con otros programas
aparecieron: Valkey no arrancaba por un redis-server en el puerto 6379; una
base `cms` de otro programa rompía las migraciones (y se le cambiaba la
contraseña al rol `cms`); una corrida se cortó en silencio dejando los
servicios sin habilitar; la contraseña del administrador no se podía
recuperar; `cms ctl` sin `sudo` usaba la configuración de fábrica; la
importación no aceptaba zip y avisaba de graders con ruido; un concurso
borrado quedaba en el scoreboard; el instalador activaba ufw.

Arreglos:

- Instalador (D47): revisión previa (`--check`) con la solución de cada
  falla, informe de falla con diagnóstico, log en
  `/var/log/cms-install.log` sin secretos, `--clean` (respalda y borra todo
  rastro anterior), ufw sólo si ya estaba activo (`--firewall` para
  forzarlo), errores de systemd visibles, repositorio de isolate por https.
- `cms ctl doctor` (D48): diagnóstico de la instalación en marcha con la
  solución de cada problema; el instalador lo corre al final.
- `cms ctl admin-password`; configuración ilegible → error con «usa sudo»;
  los archivos que crea `sudo cmsctl` quedan del usuario de los servicios
  (`internal/fsowner`); importaciones desde `.zip`/`.tar.gz`/`.tar`;
  avisos de graders breves y con la solución.
- Scoreboard: el dispatcher borra lo que ya no envía (opción `prune`, D49).
- CSV de usuarios y equipos: se acepta la marca BOM de «CSV UTF-8» de Excel.
- Manual paso a paso en `docs/es/manual` y `docs/en/manual`.
- Verificado: `make lint`, pruebas de los paquetes tocados, instalación y
  `--clean` en un contenedor Ubuntu 24.04 con systemd.


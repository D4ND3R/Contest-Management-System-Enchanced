# Decisiones

Cada decisión no obvia, numerada, con el porqué. Una decisión nueva se agrega
al final; si se revierte una, se marca como *reemplazada por Dn* sin
borrarla.

---

## D1 — Un solo binario y Valkey Streams en lugar del RPC de CMS

**Decisión.** Todos los servicios están en un binario `cms` con un subcomando
por servicio. Los servicios no se llaman entre sí: se comunican por
PostgreSQL (estado), Valkey **Streams** (colas de trabajos) y Valkey
**pub/sub** (eventos en vivo). El ranking recibe los datos por HTTP como en
CMS.

**Por qué.**
- CMS necesita una lista de servicios con host y puerto en `cms.toml`, un
  ResourceService por máquina y su propio RPC sobre gevent. Si un servicio
  cae, los demás pierden mensajes en vuelo, y agregar un Worker exige editar
  la configuración de todos.
- Un binario se instala y actualiza copiando un archivo; las versiones de los
  servicios nunca se mezclan.
- Streams da colas durables con *consumer groups*: ACK explícito,
  `XAUTOCLAIM` para recuperar trabajos de un worker muerto y entrega
  repetida que, con resultados idempotentes, no duplica nada. Los workers
  se agregan o quitan sin configuración central.
- Pub/sub cubre los eventos en vivo, que pueden perderse sin daño porque el
  cliente se resincroniza al reconectar.
- Valkey/Redis es un componente conocido, con Sentinel para failover
  (bloque F).

**Costo.** Una dependencia más (Valkey) que el instalador gestiona.

## D2 — Licencia AGPL y qué se porta del original

**Decisión.** El repositorio es AGPL-3.0-or-later. Se conservan
`LICENSE.txt` y `AUTHORS.txt` del original (con el aviso del fork arriba).
Cada archivo Go lleva `SPDX-License-Identifier: AGPL-3.0-or-later` y el
copyright del fork; los archivos portados casi literalmente conservan además
las líneas de copyright del archivo original y un comentario con su origen
(`Ported from cms/…`). Se reutilizan del original: plantillas, CSS,
JavaScript, imágenes, textos, traducciones `.po`, formatos de importación y
casos de la test suite. Cada sitio enlaza en el pie a `/source`, que redirige
al código de la versión que corre.

**Por qué.** Un fork es una obra derivada; la AGPL obliga a ofrecer el código
a los usuarios por red. Reutilizar la interfaz es el requisito central
(mismo diseño) y lo permite la licencia.

## D3 — PostgreSQL con SQL explícito, migraciones hacia adelante

**Decisión.** Sin ORM: sqlc para consultas estáticas, pgx a mano para
filtros dinámicos. Migraciones `NNNN_nombre.sql` embebidas en el binario,
numeradas sin huecos, aplicadas cada una en su transacción bajo un
`pg_advisory_lock`; se guarda su SHA-256 y editar una migración aplicada es
un error. Los servicios no arrancan si el esquema no coincide con su versión
(ni más viejo ni más nuevo). Cada índice lleva un comentario que lo justifica
(prueba en `internal/db`); borrar una tabla exige una nota `-- destructive:`.

**Por qué.** El rendimiento es la prioridad: SQL a la vista hace evidentes
las consultas N+1 y los índices. Solo hacia adelante porque una vuelta atrás
de esquema con datos reales es peligrosa: la vuelta atrás de `cmsctl
upgrade` restaura el respaldo previo.

## D4 — Configuración YAML con variables de entorno

**Decisión.** `cms.yaml` (búsqueda: `--config`, `$CMS_CONFIG`, `./cms.yaml`,
`/etc/cms/cms.yaml`; sin archivo valen los valores por defecto + entorno).
Cada hoja se sobrescribe con `CMS_<RUTA>` y `CMS_<RUTA>_FILE` lee el valor de
un archivo (secretos de Docker). Una clave desconocida es un error.

**Por qué.** Un archivo para instalaciones con systemd, variables para
contenedores; un typo silencioso en una clave era una fuente de errores en
el proyecto anterior.

## D5 — `secret_key` compartida, nunca generada por proceso

**Decisión.** `secret_key` (hex, ≥ 16 bytes) es obligatoria para las tres
webs y debe ser la misma en todos los servicios. Si falta, el servicio no
arranca con un error que explica cómo generarla (`cms ctl gen-secret`). El
instalador y los scripts de desarrollo la generan una sola vez.

**Por qué.** Lección del proyecto anterior: cada proceso generaba la suya,
las cookies no valían entre réplicas y los enlaces secretos del scoreboard
(derivados de esta clave) no funcionaban.

## D6 — Una zona horaria para todo el servidor

**Decisión.** La BD guarda `timestamptz` y el código trabaja en UTC (la
conexión fija `timezone=UTC`). La presentación usa **una** zona horaria de
servidor: la de `cms.yaml` como valor inicial y la de `server_settings`
cuando la administración la cambia en la página Servidor. Las zonas por
usuario y por concurso de CMS se importan pero no cambian la presentación.
Los envíos muestran además el **tiempo de concurso**.

**Por qué.** Varias zonas confundían a concursantes y staff en el proyecto
anterior; en una competencia presencial todos comparten reloj.

## D7 — Listener de operaciones separado

**Decisión.** Cada servicio expone `/metrics`, el `/healthz` detallado y
`/debug/pprof` en `*.ops_listen` (por defecto en 127.0.0.1). Los sitios
públicos solo tienen un `/healthz` mínimo (`{"status":"ok"}`, sin detalles).

**Por qué.** Las métricas y los errores de dependencias filtran información;
pprof es necesario para perfilar en producción (regla "perfila antes de
optimizar").

## D8 — Ruta del módulo en minúsculas

**Decisión.** `github.com/d4nd3r/contest-management-system-enchanced`.

**Por qué.** GitHub no distingue mayúsculas, pero el proxy de Go sí; la ruta
en minúsculas evita el escape `!d4!n!d3!r`.

## D9 — Traducciones gettext reutilizando las de CMS

**Decisión.** Catálogos `.po` en `locale/<idioma>/LC_MESSAGES/<dominio>.po`,
embebidos. Dominio `cms` (concursante y ranking) con las 25 traducciones del
original y **los mismos msgids**, incluidos los marcadores de Python
`%(nombre)s`, que `i18n.Format` interpreta. Dominio `admin` nuevo, con el
español obligatorio (prueba). `make generate` extrae las cadenas de Go
(`T`, `Tn`, `Tc`, `N`) y de las plantillas (`{{T "…"}}`) a `.pot`; una prueba
falla si el `.pot` quedó viejo. Parser `.po` y formas plurales propios
(~400 líneas) en lugar de una dependencia.

**Por qué.** Mantener los msgids permite reutilizar y seguir importando las
traducciones del original (Weblate de CMS). Agregar un idioma es un archivo.

## D10 — CSP estricta con nonce

**Decisión.** `script-src 'self' 'nonce-…'` por petición; los `<script>` en
línea de las plantillas portadas llevan el nonce y los manejadores en línea
(`onclick=…`) se mueven a archivos JS sin cambiar el comportamiento ni el
aspecto. `style-src 'self' 'unsafe-inline'` porque el markup original usa
atributos `style` (riesgo bajo comparado con scripts).

**Por qué.** Requisito de seguridad sin tocar el diseño.

## D11 — Infraestructura de pruebas real y aislada

**Decisión.** Las pruebas usan PostgreSQL y Valkey reales. `scripts/infra.sh`
los levanta como binarios nativos (preferido: rápido, sin Docker) o con
Docker. Cada prueba recibe una base propia clonada de una plantilla ya
migrada (`CREATE DATABASE … TEMPLATE`, ~10 ms) y un prefijo de claves propio
en Valkey. Sin las variables, las pruebas que lo necesitan se saltan; con
`CMS_TEST_REQUIRE_INFRA=1` (lo pone `make test` y CI) fallan.

**Por qué.** Los mocks de SQL esconden justamente los errores que importan
(restricciones, concurrencia, planes de consulta).

## D12 — Workers sin acceso a la base

**Decisión.** El worker no importa `internal/db` ni pgx; una prueba
(`TestWorkerHasNoDatabaseAccess`) lo verifica con `go list -deps`. Por eso
`internal/app` (runtime común) no conoce la BD y `db.OpenService` abre el
pool de los demás servicios.

**Por qué.** Un worker comprometido por un envío malicioso no debe poder leer
ni escribir la base; además los workers pueden vivir en otra red.

## D13 — Versión de Go

**Decisión.** `go 1.26` mínimo (lo exigen pgx, go-redis y client_golang
actuales) y `toolchain go1.27.1`.

## D14 — El ranking guarda su propio estado

**Decisión.** `ranking-web`, como RankingWebServer, no accede a PostgreSQL ni
a Valkey: recibe los datos del dispatcher por la API REST de CMS y los guarda
en `ranking_web.data_dir`.

**Por qué.** Permite ponerlo en un host público aislado, mantiene la
compatibilidad con herramientas que hablan con RWS y aguanta muchos
espectadores sin cargar la base.

## D15 — Imágenes Docker desde el binario compilado

**Decisión.** Las imágenes copian `bin/cms` (compilado por `make build` o
GoReleaser) sobre `distroless/static`; no compilan Go dentro de Docker. El
worker tendrá una imagen aparte con isolate y compiladores (F2).

**Por qué.** Builds reproducibles, sin red dentro del build, imágenes chicas
y sin shell en los servicios expuestos.

## D16 — Sin LogService ni ResourceService

**Decisión.** Los logs van a stdout en JSON (journald o Docker los guardan y
rotan). systemd o Docker supervisan los procesos. `monitor` mide la salud de
los servicios y del host y alerta.

**Por qué.** Son funciones que el sistema operativo ya resuelve mejor; en CMS
eran la causa de que hubiera que arrancar un ResourceService por máquina.

## D17 — URLs del sitio del concursante como en CMS

**Decisión.** Sin `contest_web.contest`, `/` lista los concursos y cada uno
vive bajo `/<nombre>/…`; con él, el concurso se sirve en `/`.

**Por qué.** Compatibilidad con enlaces, documentación y herramientas de CMS.

## D18 — Tipos de CMS como CHECK sobre `text`

**Decisión.** Los dominios de CMS (CODENAME, FILENAME, FILENAME_SCHEMA,
FILENAME_SCHEMA_ARRAY, DIGEST) y sus enums (`token_mode`, `feedback_level`,
`score_mode`…) son columnas `text` con un CHECK en línea, no `CREATE DOMAIN`
ni tipos enum de PostgreSQL. Se conservan los nombres de restricciones de
CMS (`contests_check`, `groups_check1`, `datasets_memory_limit_check1`,
`participation_opaque_unique`, `fk_active_dataset_id`…). Los ids son
`integer GENERATED BY DEFAULT AS IDENTITY` (los dumps conservan sus ids; el
importador ajusta las secuencias) y los `DateTime` de CMS son `timestamptz`.

**Por qué.** sqlc no entiende `CREATE DOMAIN`; un CHECK se cambia en una
migración sin reescribir la tabla; los mismos nombres hacen comparables los
errores y los dumps con CMS.

## D19 — Digests SHA-256 con la lápida `x`

**Decisión.** Las columnas de digest aceptan `^([0-9a-f]{64}|x)$`: SHA-256
en lugar del SHA-1 de CMS, y la lápida `x` de CMS (`cmsCleanFiles
--tombstone`, ejecutables borrados que se regeneran) porque puede venir en
los dumps. El importador de dumps vuelve a calcular los digests. El almacén
de blobs valida el formato antes de tocar disco o red (evita path
traversal).

## D20 — La FK del dataset activo no borra datos

**Decisión.** `fk_active_dataset_id` es `FOREIGN KEY (id,
active_dataset_id) REFERENCES datasets (task_id, id) ON UPDATE CASCADE`, sin
acción ON DELETE: borrar el dataset activo falla (23503); primero hay que
activar otro. Es el comportamiento efectivo de CMS (su `SET NULL` sobre la
FK compuesta anulaba `tasks.id` y siempre fallaba).

## D21 — Escrituras de resultados idempotentes

**Decisión.** Los resultados de los jueces se guardan con "gana el primero":
compilación con `WHERE compilation_outcome IS NULL`, evaluación con
`evaluation_outcome IS NULL`, puntaje con `score IS NULL`, evaluaciones y
ejecutables con `ON CONFLICT DO NOTHING`; las consultas devuelven las filas
afectadas (0 = duplicado). La reevaluación invalida explícitamente. Un
resultado repetido (reentrega de Valkey) no cambia nada.

**Por qué.** Las colas entregan al menos una vez (D1); el puntaje ya se
calculó con el primer resultado.

## D22 — Almacén de blobs

**Decisión.** Paquete `internal/blob` con backends `local` (árbol
`raíz/ab/cd/<digest>`, escritura atómica con fsync y rename), `s3`
(minio-go) y `remote` (servidor HTTP `GET/HEAD/PUT /blobs/{digest}` montado
por el dispatcher, sin DELETE ni listado, para jueces en otras máquinas),
más una caché LRU en disco con singleflight delante de `s3` y `remote`. Todo
lo que se lee se verifica contra su digest. Las pruebas de S3 usan gofakes3
en memoria (sin servicios externos).

## D23 — Intentos y resultados tardíos

**Decisión.** Las escrituras de resultados fallidos llevan el número de
intento del trabajo (`tries` = el mayor intento visto); si afectan 0 filas,
el resultado era repetido o viejo y el dispatcher no reencola. Un resultado
de un trabajo que estaba en vuelo cuando se invalidó el envío se descarta
(como `WorkerPool.ignore_operation` de CMS); en F4 se agrega un contador de
generación a `submission_results` para hacerlo verificable en SQL.

## D24 — Páginas de listas acotadas

**Decisión.** Las listas grandes del admin (envíos, pruebas de usuario)
eligen primero la página por tarea usando los índices y unen
participaciones, usuarios, resultados y tokens solo a esas filas: se leen a
lo sumo `tareas × tamaño de página` envíos sin importar el tamaño del
concurso (de 74,6 ms a 1,7 ms con 45 000 envíos). Una prueba revisa los
planes de consulta.

## D25 — Token de los jueces remotos

**Decisión.** Los jueces en otras máquinas no reciben `secret_key`: usan
`storage.remote.token` = HMAC de `secret_key` con un propósito fijo
(`config.DeriveKey`, con longitudes prefijadas para que propósitos y
contextos distintos nunca coincidan), que imprime `cms ctl blob-token`.
Cada servicio usa su propia caché (`cache_dir/<servicio>`) con un lock.

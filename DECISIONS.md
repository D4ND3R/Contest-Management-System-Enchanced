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
`integer GENERATED BY DEFAULT AS IDENTITY` (los dumps de CMS no traen ids:
el importador los toma de las secuencias, D41) y los `DateTime` de CMS son
`timestamptz`.

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

## D26 — Identificadores ordenados por código de carácter

**Decisión.** Las columnas de identificadores (nombres de concursos, tareas
y grupos, usuarios, códigos de equipo, codenames de casos, nombres de
archivo, idiomas de enunciados, claves de ajustes) usan `COLLATE "C"`
(migración 0003). `scripts/infra.sh` crea la base de pruebas con
`en_US.UTF-8` cuando existe, como el CI y las instalaciones reales.

**Por qué.** CMS ordena en Python por código de carácter; una colación
lingüística (en_US ignora la puntuación) ordenaba `aaa.h` antes que
`aaa.%l` y hacía que los listados dependieran del idioma del servidor. Lo
detectó el CI, cuyo PostgreSQL usa en_US.

## D27 — Cola durable: un stream por prioridad

**Decisión.** `internal/queue` usa un stream de Valkey por prioridad con
un *consumer group*; los consumidores leen en orden estricto de prioridad.
Un mensaje cuyo consumidor murió se recupera con `XAUTOCLAIM` pasado el
tiempo de visibilidad (los trabajos largos lo extienden con `Extend`). Los
reintentos esperan en un conjunto ordenado por hora (backoff exponencial)
y tras el máximo de intentos van a una cola de mensajes muertos. Los
trabajos (`internal/job`, port de `Job.py`) llevan número de intento y de
generación para descartar resultados tardíos (D23).

## D28 — Seccomp con un lanzador mínimo

**Decisión.** Los programas de los concursantes corren con
`/cms-launcher/seccomp-exec programa…`: un ELF estático de ~1 KiB que el
worker escribe al arrancar (código ensamblado a mano para amd64 y arm64
más el filtro BPF; una prueba lo compara con su fuente en ensamblador).
Hace `prctl(NO_NEW_PRIVS)`, instala el filtro (`SECCOMP_RET_KILL_PROCESS`)
y hace `execve`. Una llamada prohibida (ptrace, mount, espacios de nombres,
bpf, keyctl, io_uring, kexec, módulos…) mata al programa con SIGSYS, que se
informa como *Security violation*. Los sockets que no son unix fallan con
`EAFNOSUPPORT` en lugar de matar (la JVM abre un socket IPv6 al arrancar);
la red sigue inaccesible. Compiladores, checkers y managers nunca corren
con el filtro.

**Por qué.** Sin runtime en la caja, el programa recibe sus límites
directamente de isolate y paga ~0,3 ms de CPU (un `execve` más) y nada de
memoria; la primera versión usaba el binario `cms` como lanzador y costaba
8 ms y 14 MiB por ejecución, cargados al concursante. No depende de
libseccomp (cgo) ni de un segundo binario que instalar. Sin canal de
estado: SIGSYS basta para distinguir el veredicto.

## D29 — Errores del sandbox nunca son veredictos

**Decisión.** `Box.Run`/`Wait` y los pasos de `internal/grading/steps`
devuelven error solo ante fallas de infraestructura (isolate, disco,
meta ilegible); el trabajo se reintenta y nunca se convierte en un
veredicto del concursante. Los directorios pasados a isolate son
absolutos (isolate rechaza los relativos). Las cajas se reservan con un
archivo de lock por id compartido por todos los workers de la máquina;
`cms ctl verify-host` revisa rangos disjuntos, cgroups v2, SMT,
governor, swap y hace una prueba real de caja.

## D30 — Lenguajes como plantillas YAML

**Decisión.** `config/languages/*.yaml` (embebidos) describen cada
lenguaje con plantillas de comandos (`{sources}`, `{executable}`,
`{main}`, `{args}`…) validadas de forma estricta; `languages_dir` agrega
o reemplaza lenguajes sin recompilar. Los 13 de CMS conservan nombre,
extensiones, comandos y flags (comparados con las clases de Python en
casos generados); se agregan Go y Kotlin.

**Diferencias con CMS.** Los compiladores no ven `/etc`: cada lenguaje
lista lo que necesita (`/etc/java-*-openjdk`, `/var/lib/ghc`;
`/etc/fpc.cfg` se copia a la caja). Go necesita la biblioteca estándar
precompilada en cada juez (`go install -pkgdir=/var/lib/cms/go-std std`;
con la caché vacía compilar cuesta ~13 s de CPU, más que el límite de
10 s): el instalador y la imagen del worker lo hacen. Kotlin compila con
el colector serial y C1 (la mitad de CPU). C# recibe `{args}`.

## D31 — Checkers y testlib

**Decisión.** `internal/grading/checker` porta el white diff de CMS
(mismo resultado en 457 casos dorados, leyendo en streaming), la
comparación exacta, la de números reales, los comparadores con el
protocolo de managers de CMS y los checkers testlib. El `testlib.h`
parcheado de CMS se embebe (para el importador de Polygon): con `-DCMS`
es un comparador normal; sin él (`KindTestlib`) corre como
`checker entrada salida respuesta` y los códigos 0 → correcto, 1/2/4/8 →
incorrecto, 7 → puntos en stderr, otro → error del sistema.

## D32 — Tipos de problema: diferencias con CMS

**Decisión.** Los seis tipos (`internal/grading/tasktype`) conservan los
parámetros y la semántica de CMS; la suite `cmstestsuite/Tests.py` está
portada con sus tareas y programas. Diferencias:

- `output_eval` acepta además `exact`, `real` (tolerancia 1e-6) y `testlib`
  (datasets que no se pueden exportar a CMS).
- Communication: decide quién falló primero. Si un proceso del usuario
  falla antes que el manager, el resultado es 0 con ese error y se detiene
  el manager (CMS espera 21 s y falla el trabajo, que queda reintentándose
  sin juzgarse); un manager muerto por SIGPIPE cuenta como falla del
  usuario. Si el manager falla primero, el trabajo falla (error de
  configuración), nunca se culpa al concursante. Como el worker puede
  notar en cualquier orden el fin de procesos que mueren juntos, una
  falla del usuario que el manager no puede causar (señal distinta de
  SIGPIPE, límite de CPU o memoria, syscall prohibida) a lo sumo 250 ms
  después del fin del manager también es culpa del usuario.
- Las expresiones regulares de los grupos (GroupMin/Mul/Threshold) usan la
  sintaxis de Python traducida a RE2; lo que RE2 no puede ejecutar
  (lookahead, referencias hacia atrás…) se rechaza al guardar el dataset.
  Los números de los managers se leen como `float()` de Python.
- Interactive: el controlador es un *auto manager* (las pruebas de usuario
  funcionan), las soluciones reciben el límite de procesos del concurso y
  `%` en el mensaje del controlador se escapa.
- BatchAndOutput ignora elementos vacíos de la lista de salida; los nombres
  de archivos de E/S de Batch no pueden contener `/`.
- Las cajas de trabajos fallidos no se conservan (usar `keep_sandbox`).

`Boxes()` es el máximo de cajas que un trabajo usa a la vez (Interactive:
`process_limit + 1`); el worker las reserva antes de empezar para que dos
trabajos en paralelo no se bloqueen entre sí.

## D33 — Worker: nombres, reservas y caché de compilación

**Decisión.**
- Un proceso por nombre de worker: una entrada en Valkey con TTL de 15 s
  (renovada cada 5 s) lo reserva; si el dueño es un PID muerto del mismo
  host se toma enseguida. Los consumidores de la cola se llaman
  `<nombre>/<ranura>` y liberan al arrancar y al cerrar los trabajos que
  tenían, así un `kill -9` seguido de reinicio no espera el tiempo de
  visibilidad.
- El resultado se publica y el trabajo se confirma en un solo paso
  (`AckAndEnqueue`). Fallas de infraestructura, pánicos del tipo de
  problema y el vigilante (`job_timeout`) publican `success=false`: decide
  el dispatcher. Al apagar, lo que no termina en `shutdown_timeout` vuelve
  a la cola.
- Las cajas se reservan antes de empezar (semáforo de `box_count`);
  `box_count` debe ser al menos 2 × `concurrency` y un trabajo que necesita
  más cajas que `box_count` falla enseguida en lugar de bloquear al worker.
- Un sandbox roto no tira el worker: queda sin tomar trabajos, lo informa
  en `/healthz` y reintenta la prueba de caja.
- Caché de compilación en Valkey: la clave cubre tipo de problema y sus
  parámetros, lenguaje, SHA-256 de los binarios de los compiladores,
  límites, cgroups, arquitectura y los digests de fuentes y managers
  compilables. Solo se guardan compilaciones exitosas y cada ejecutable se
  comprueba en el almacén de blobs antes de usarlo (90 ms → 0,2 ms en C++).
  La identidad del compilador sigue scripts envoltorio, intérpretes y las
  instalaciones con `bin/` y `lib/` (JDK, kotlinc, mono). Tras actualizar
  un compilador hay que reiniciar los workers; la JVM que kotlinc toma del
  PATH no está cubierta (vaciar `compcache:*` tras actualizarla).

## D34 — Caché de puntajes por participación y tarea

**Decisión.** La tabla `task_scores` (migración 0004) guarda por
participación y tarea el puntaje, el puntaje público, el de envíos con
token y una marca de parcial (las tres vistas de CMS comparten esa marca),
más los puntajes por subtarea que necesita `max_subtask`. Se recalcula
desde cero en la misma transacción que cada cambio que la afecta
(resultado puntuado, token, cambio de dataset activo, invalidación,
cambio de modo de puntaje), con las tareas bloqueadas en modo compartido
para que el dataset activo no cambie a mitad. Sin fila = 0, no parcial.

**Por qué.** El sitio del concursante, el ranking del admin y el
scoreboard leen el puntaje sin recalcularlo por pedido (500 concursantes
en 2 vCPU). Recalcular una tarea de 13 000 envíos cuesta 60 ms.

## D35 — Generaciones y reintentos de resultados

**Decisión.** Amplía D23: recompilar o reevaluar incrementa `generation`
del resultado (repuntuar no); los trabajos la llevan y las escrituras la
comparan, así un resultado de un trabajo creado antes de una invalidación
se descarta en SQL. La clave de deduplicación de la cola es
`operación@generación`. `tries` es el mayor intento fallido, de modo que
un fallo entregado dos veces cuenta una vez, y un éxito no lo toca (solo
cuenta fallos, como muestra el admin; CMS lo vuelve a 0). Repuntuar cubre
todos los datasets al llegar la notificación, como CMS; el barrido solo
recorre los datasets que se juzgan y crea las filas faltantes de
`task_scores` (bases migradas, importaciones, restauraciones). A diferencia de CMS, un caso
de prueba faltante cuenta como intento y un ejecutable faltante provoca
recompilar. Los trabajos ya encolados de un resultado invalidado no se
quitan de la cola: corren y su resultado se descarta por viejo.

## D36 — Un dispatcher activo con lease

**Decisión.** El dispatcher activo tiene un lease en Valkey (`SET NX PX`
de 10 s, renovado cada tercio, con un número de fencing que crece en cada
toma); deja de trabajar cuando vence su plazo local, antes de que la clave
expire en el servidor, y lo libera al apagarse. Las instancias de reserva
esperan y lo toman. La corrección no depende del lease: toda escritura es
idempotente y compara la generación. Un solo bucle guarda resultados y
encola trabajos; antes de encolar lo leído de la base descarta lo que ya
está en cola, corriendo o esperando reintento.

Los trabajos que siguen a un lote ya confirmado se encolan aunque el lease
se haya perdido (los duplicados caen por la clave y los resultados viejos
por la generación). Si no alcanza PostgreSQL durante 3 TTL, el activo
suelta el lease para que una reserva que sí lo alcanza siga juzgando.
Los sitios notifican después del commit; si el dispatcher todavía no ve
un envío notificado, lo revisa de nuevo (100 ms, duplicando hasta 6,4 s) y
después lo deja al barrido. Los resultados se consumen y confirman por
lotes (12,8 viajes a Valkey por envío a 20 envíos/s).

**Prioridades** (de mayor a menor): primera compilación de un envío de
concursante; sus evaluaciones y compilaciones que fallaron una vez;
pruebas de usuario, reevaluaciones y lo encontrado por barridos;
evaluaciones que ya fallaron; datasets no activos. Dentro de cada
prioridad, el más antiguo primero.

## D37 — Eventos en vivo con una secuencia global

**Decisión.** `internal/live` numera cada mensaje con un contador de Valkey
(un script Lua incrementa y publica) y cada proceso web mantiene una sola
suscripción a `live:*` con historial de 4096 eventos: todas las réplicas
ven los mismos ids, detectan pérdidas exactas (salto en la secuencia) y
mandan *resync*. Los pings son eventos `ping` reales (EventSource no ve
comentarios) que dicen si la suscripción está viva. Un cliente lento
recibe una sola marca de resync en lugar de acumular memoria; las marcas
llevan un `spread` para que 1500 páginas no recarguen a la vez. Los
eventos llevan ids, nunca puntajes: la página consulta los detalles con
sus permisos.

## D38 — Importación en una transacción, sin escribir en el paquete

**Decisión.** Toda importación (concurso, tareas, usuarios, equipos)
valida primero, sube los archivos al almacén de blobs y escribe todas las
filas en una sola transacción, con una cantidad de sentencias que no crece
con los casos ni los participantes. Lo que el dispatcher nunca podría
juzgar (tipo de problema o de puntaje desconocido, parámetros inválidos,
lenguaje desconocido, managers faltantes) se rechaza antes de escribir.
Las actualizaciones comparan el paquete con la base (CMS usa archivos
`.itime` dentro del paquete): nada se escribe en el directorio importado.
`--dry-run` deshace la transacción y muestra qué cambiaría. Un dataset con
la misma descripción se actualiza en su lugar conservando casos y
resultados (como CMS, con un aviso que sugiere reevaluar); una descripción
nueva crea un dataset inactivo; los envíos se conservan.

## D39 — Checkers y managers compilados al importar

**Decisión.** Polygon, TPS y el formato propio traen checkers y managers
como fuente C++; se compilan con g++ al importar con las opciones de CMS
(Polygon: `testlib.h` parcheado de CMS y `-DCMS`). La máquina que importa
necesita g++ y la misma arquitectura que los jueces. `cms_conf.py` de
Polygon se lee como literales de Python y nunca se ejecuta.

## D40 — Formato de paquete propio

**Decisión.** `problem.yaml` (claves desconocidas son error) más
`statement/`, `tests/`, `graders/`, `attachments/` y `solutions/` con el
veredicto esperado en el nombre (`correcta.ac.cpp`). Se acepta como
directorio o como zip leído sin extraer, con controles de zip slip,
enlaces, cifrado, duplicados, tamaño declarado y CRC-32. La exportación
escribe solo lo que difiere de los valores por defecto y conserva entero
o real como lo escribe Python, de modo que importar → exportar → importar
da filas idénticas. Las soluciones no se guardan (CMS no tiene tabla para
ellas).

## D41 — Importación de dumps de CMS

**Decisión.** `cms ctl import-dump` lee la salida de `cmsDumpExporter`
(directorio, zip o tar) de las versiones 14 a 49 del modelo; las anteriores
a 49 pasan por los `updaters/` de CMS portados y comparados con la salida
de `cmsDumpUpdater`. Cada archivo se verifica contra su SHA-1, se guarda
por SHA-256 y se reescriben todas las columnas de digest; un dump sin
archivos (`-F`) se rechaza porque el SHA-256 necesita el contenido. Los
dumps no tienen ids de la base: las filas toman ids nuevos de las
secuencias, en el orden de la instalación original. Todo se escribe en una
transacción con `COPY` (pgx directo, no sqlc: unas 25 tablas a granel),
con el caché de puntajes calculado en la misma transacción. `-d` vacía las
tablas de concursos, tareas, usuarios y equipos en la misma transacción
pero conserva admins, ajustes y auditoría (CMS borra la base entera); con
`--dry-run` trabaja sobre copias temporales vacías y no bloquea a nadie.
Un dump exportado con `-c` trae otros concursos sin sus archivos: se
importan con lápidas y un aviso. Los admins se buscan por nombre de usuario
y los que faltan se crean deshabilitados.

**Por qué.** Migrar una instalación de CMS sin pasos manuales y sin dejar
nada a medias; cambiar de SHA-1 a SHA-256 (D19) exige leer cada archivo.

## D42 — Límites de entrada y cola de bcrypt del sitio del concursante

**Decisión.** Las comprobaciones de contraseña pasan por tres capas, en
este orden: límite por dirección (IPv6 por `/64`; amplio, un laboratorio
puede compartir una dirección), límite por usuario y concurso (clave
hasheada; 10 fallos seguidos y luego uno cada 6 s; un acierto devuelve su
ficha) y una cola de bcrypt con una comprobación por núcleo
(`GOMAXPROCS`) que atiende a las direcciones por turnos, con un tope de
espera por dirección y total que responde 429 al instante. La inscripción
de un usuario existente comparte las fichas por usuario. Los cuerpos de
formularios se limitan a 1 MiB; solo envíos y pruebas aceptan 64 MiB. Las
páginas reaccionan a un anuncio tras una espera aleatoria (el `spread` del
ping, D37). El tiempo de entrada de un usuario inexistente sigue sin
bcrypt, como en CMS: los nombres de usuario son públicos en el ranking, y
un bcrypt ficticio delataría a los usuarios con contraseña `plaintext:`.

**Por qué.** Antes una sola dirección podía llenar la memoria con nombres
de usuario largos, bloquear cuentas ajenas con intentos que el límite por
dirección rechazaba, usar la inscripción como oráculo de contraseñas sin
límite por usuario y encolar sin tope intentos de bcrypt delante de los
demás; con la mitad de los núcleos, una ola de 500 entradas con costo 12 dejaba
26 % sin entrar en 60 s.

## D43 — Núcleo web compartido y diferencias deliberadas con CMS

**Decisión.** `internal/web` sirve a las tres webs: plantillas
`html/template` con herencia y bloques al estilo Jinja (un conjunto de
copias por página ligado a cada petición, porque `html/template` no deja
cambiar funciones después de ejecutar), funciones de las plantillas de CMS
y textos de los catálogos tratados como HTML de confianza (como `{% trans
%}`; los valores insertados se escapan). CSRF compatible con Tornado
(`_xsrf`, `X-XSRFToken`) más el rechazo de `Sec-Fetch-Site` ajeno; cookies
firmadas con HMAC-SHA256 ligadas a su nombre, `HttpOnly` y
`SameSite=Lax` (CMS no usa ninguna de las dos); estáticos con hash en la
URL. Los formatos de fechas y números salen de las tablas CLDR de babel
2.12.1 (la versión que fija CMS), comparadas con salidas de babel.

Diferencias visibles deliberadas con CMS, además de las de D10 y R5.8–R5.9
(nonce, manejadores en JS, indicador en vivo, enlace al código, tiempos de
concurso): el tooltip de fecha de los envíos usa el idioma de la página (CMS
lo deja en inglés por un error de su plantilla) y el detalle de una
subtarea con casos públicos y privados muestra los públicos (CMS muestra
"Score details temporarily unavailable").

**Por qué.** Una sola implementación de seguridad y formato para las tres
webs; las páginas portadas salen idénticas byte a byte a las de CMS salvo
lo listado.

## D44 — Respaldos con COPY desde Go, un archivo con manifiesto

**Decisión.** `cms ctl backup` escribe un `.tar.gz` con `manifest.json`
(versión de cms, versión del esquema y checksums de sus migraciones, tablas
y columnas), `db/<tabla>.copy` (COPY de texto, todas las tablas en una
instantánea REPEATABLE READ), `blobs/<sha256>` (Walk del almacén, después
de la instantánea), `config/cms.yaml` y `SHA256SUMS` al final. Se escribe
con nombre temporal y se renombra; `--keep N` borra los más viejos.
`task_scores` no se guarda (derivada, D34). `cms ctl restore` lee el
archivo dos veces (verificar todo, después cargar) y restaura la base en
**una transacción**: `--clean` borra tablas, funciones y tipos del esquema
public (no el esquema: en PostgreSQL 14 no es del usuario cms, y en Docker
cms es superusuario dueño de los catálogos, así que `DROP OWNED` no sirve);
`db.MigrateTx` reconstruye el esquema del respaldo; las tablas sembradas
por migraciones se vacían; triggers de usuario desactivados y claves
foráneas quitadas durante la carga (hay ciclos, p. ej. tasks↔datasets) y
agregadas de nuevo al final (verifica cada fila); secuencias reajustadas;
migraciones más nuevas aplicadas; `task_scores` recalculada.

**Por qué.** `pg_dump`/`pg_restore` exigen clientes de la versión del
servidor y no existen en la imagen distroless; COPY es igual de rápido.
Una transacción hace que una restauración fallida (y la vuelta atrás de
`upgrade`) no deje la base a medias. Verificar antes de tocar nada evita
restaurar un archivo truncado.

## D45 — Instalador: subdominios, secretos una vez, juez local por el servidor de blobs

**Decisión.** `scripts/install.sh` con `--domain D` publica el concurso en
`D`, la administración en `admin.D` y el scoreboard en `ranking.D` (Caddy).
Los secretos se generan una vez en `/etc/cms/secrets` y `cms.yaml` se
escribe solo si no existe; lo que depende del modo (direcciones, proxies,
servidor de blobs) va a `/etc/cms/cms.env`, reescrito en cada ejecución.
Los servicios web, dispatcher y monitor corren como `cms` con el
aislamiento de systemd; el worker corre como root (isolate) y usa el
servidor de blobs del dispatcher en localhost (`worker.env`), como un juez
remoto. Las credenciales del scoreboard se derivan de `secret_key`
(F7): no hay otro secreto. Valkey con contraseña, `appendonly` y
`noeviction`; en Ubuntu 22.04 se usa el repositorio de Redis (trae 6.0).
isolate se compila de la misma versión en arm64 (el repositorio de sus
autores solo tiene amd64), igual que en la imagen del worker.

**Por qué.** Las páginas de CMS usan rutas absolutas: cada sitio necesita
la raíz de su host, y hosts separados aíslan las cookies del admin. El
almacén local crea directorios 0700: si el worker (root) escribiera en él,
los servicios del usuario `cms` no podrían leer lo que crea. Cambiar de
modo al volver a correr el instalador no debe tocar los secretos.

## D46 — `cmsctl upgrade` con vuelta atrás automática

**Decisión.** Pasos fetch (release de GitHub verificada con
`checksums.txt`, o `--binary`), backup (sin blobs: solo se agregan), stop,
install (el binario anterior queda como `cms.prev`), migrate (con el binario
**nuevo**), start y health (`systemctl is-active` y `/healthz` de cada
listener de operaciones). Ante cualquier falla: detener, reponer el
binario, restaurar el respaldo con el binario anterior (el proceso que
corre) si se llegó a migrar, arrancar y verificar. `CMS_UPGRADE_FAIL_AT=<paso>`
simula una falla después de ese paso. Los archivos de la release no llevan
la versión en el nombre (`cms_linux_amd64.tar.gz`) para que
`releases/latest/download/…` funcione.

**Por qué.** Las migraciones son solo hacia adelante (D3): volver atrás es
restaurar. Lo escrito entre el arranque de la versión nueva y la vuelta
atrás se pierde; es preferible a dejar una instalación rota en un concurso.

## D47 — El instalador explica cada falla y puede empezar de cero

**Contexto.** En el primer servidor real el instalador falló por causas del
entorno (un Redis en el puerto 6379, una base `cms` de otro programa) con
mensajes genéricos, y una corrida se cortó sin decir nada.

**Decisión.** Antes de cambiar nada, una revisión previa con PASS/WARN/FAIL
y la solución de cada falla (`--check` la corre sola). Después, cualquier
falla (trap ERR, `die`, señales) imprime un informe con el paso, el
comando, la salida y un diagnóstico de causas conocidas; todo va a
`/var/log/cms-install.log`, nunca la contraseña generada. `--clean`
respalda (`pg_dump` y `/etc/cms` en `/var/backups/cms`) y borra todo rastro
de cms, sin desinstalar paquetes ni tocar otros programas; `--purge`
comparte ese código. ufw sólo se toca si ya estaba activo.

**Consecuencias.** Volver a correr el comando sigue siendo seguro; los
errores del entorno dicen qué hacer en vez de dejar el sistema a medias.

## D48 — `cms ctl doctor`

**Contexto.** Los organizadores no son administradores de sistemas: «los
envíos no se evalúan» puede ser el juez, Valkey, cgroups, permisos…

**Decisión.** Un comando de sólo lectura, acotado a 25 s, que revisa la
configuración, la base y sus migraciones, los administradores, Valkey, los
jueces y la cola, los servicios de systemd y sus `/healthz`, los sitios, el
scoreboard, el almacén (dueños), el disco, la hora, isolate, los concursos
(horario, problemas sin casos, lenguajes sin grader, sin participantes) y
ufw; cada problema con su solución. El instalador lo corre al final.

## D49 — El scoreboard refleja la base (prune)

**Contexto.** Como CMS, el dispatcher nunca borraba nada de ranking-web: un
concurso de prueba borrado seguía en el scoreboard público.

**Decisión.** En cada actualización (`ranking_refresh`) el dispatcher lista
lo que tiene cada ranking y borra los concursos, problemas, equipos y
usuarios que ya no enviaría. Una opción por ranking, `prune` (por defecto
`true`), lo desactiva para rankings que alimentan varias instalaciones.

**Consecuencias.** Diferencia deliberada con CMS; un ranking compartido
debe poner `prune: false`.


# Desarrollo

## Requisitos

- Go: la versión de `go.mod` se descarga sola (`GOTOOLCHAIN=auto`).
- Para las pruebas y el stack local: binarios de **PostgreSQL 16** y de
  **Valkey** o **Redis ≥ 7**, o bien **Docker**. `scripts/infra.sh` elige
  solo (`CMS_INFRA=native|docker` para forzar).
- Para `make test-sandbox` y `make chaos`: root, isolate ≥ 2.0, cgroups v2
  y los compiladores (ver [Jueces](jueces.md)), además de PostgreSQL y
  Valkey como para `make test` (los arranca `scripts/infra.sh` si
  `CMS_TEST_*` no está definida). En una máquina sin cgroups v2:
  `CMS_SANDBOX_CGROUPS=0 make test-sandbox` (se saltan los veredictos de
  memoria; el CI los corre con cgroups v2).

## Comandos

```sh
make build         # bin/cms y el alias bin/cmsctl
make test          # suite completa con -race contra PostgreSQL y Valkey desechables
make test-short    # sin infraestructura (se saltan las pruebas que la usan)
make lint          # gofmt, go vet, cabeceras de licencia
make generate      # código generado y plantillas de traducción (.pot)
make dev-native    # stack completo con binarios nativos; make dev-stop lo detiene
make dev           # stack completo con Docker Compose; make dev-down lo borra
make test-sandbox  # como root: isolate, batería maliciosa, tipos de problema, worker y dispatcher de punta a punta, caos
make chaos         # como root: solo la prueba de caos del juez
make load          # prueba de carga del dispatcher con workers simulados
```

`make dev-native` escribe `.dev/cms.yaml` una sola vez (con una
`secret_key` generada), aplica las migraciones y arranca los seis servicios;
`scripts/dev.sh logs [servicio]` muestra sus logs. Los sitios quedan en
<http://127.0.0.1:8888> (concursante), <http://127.0.0.1:8889>
(administración) y <http://127.0.0.1:8890> (scoreboard).

## Infraestructura de pruebas

`scripts/infra.sh up` arranca PostgreSQL en el puerto 55432 y Valkey en el
56379 (datos en `.infra/`, sin durabilidad) y escribe `.infra/env`:

```sh
scripts/infra.sh up
source .infra/env
go test ./internal/db/...
scripts/infra.sh down   # detiene y borra los datos
```

En las pruebas, `testutil.DB(t)` da una base nueva clonada de una plantilla
ya migrada (se borra al terminar) y `testutil.Redis(t)` un cliente con un
prefijo de claves propio. Sin las variables `CMS_TEST_*` esas pruebas se
saltan; `make test` y el CI las vuelven obligatorias
(`CMS_TEST_REQUIRE_INFRA=1`).

## Traducciones

Todo texto visible pasa por `T("…")`, `Tn("uno", "varios", n)` o
`Tc("contexto", "…")` en Go y por `{{T "…"}}` en las plantillas. Los msgids
del sitio del concursante son los de CMS, con sus marcadores `%(nombre)s`.
Después de agregar textos:

```sh
make generate                      # actualiza locale/*.pot
$EDITOR locale/es/LC_MESSAGES/admin.po   # el admin debe estar completo en español
```

## Migraciones

Archivos `internal/db/migrations/NNNN_nombre.sql`, numerados sin huecos,
**nunca se editan** una vez aplicados (hay un checksum). Cada `CREATE INDEX`
lleva encima un comentario que justifica el índice. `cms migrate` aplica;
`cms migrate status` lista las pendientes.

## El circuito del juez

```
contest-web, admin-web, cmsctl      dispatcher (uno activo, el resto en espera)    worker (sin base de datos)
  envío + puntaje de la tarea (SQL)
  aviso ──▶ judging:events ──▶ Pending, BuildJobs ──▶ trabajos (5 prioridades) ──▶ isolate
            (stream de Valkey)  Apply (una transacción  ◀── resultados ◀──── resultado y ACK
                                por lote de resultados)                       en un solo paso
                                └─▶ siguientes trabajos, caché de puntajes, eventos en vivo, scoreboard
```

Lo que le permite sobrevivir a un `kill -9` de cualquier proceso (SPEC N3):

- **Los avisos** son un stream de Valkey leído por un grupo de
  consumidores y confirmado una vez atendido: los de un dispatcher que
  murió los vuelve a leer el siguiente. Un aviso perdido solo retrasa el
  trabajo: cada dispatcher barre la base buscando resultados sin terminar
  al volverse activo y cada `dispatcher.sweep_interval`.
- **Los trabajos** (`internal/queue`, D27) los confirma el worker en el
  mismo paso atómico en que publica el resultado. Los trabajos de un worker
  que murió vuelven a la cola cuando reinicia con el mismo nombre, o pasan
  a otro worker tras el tiempo de visibilidad (30 s).
- **Los resultados** los confirma el dispatcher después del commit que los
  guarda. Un resultado entregado dos veces no cambia nada (gana el primero,
  D21) y el de un trabajo que corría cuando se invalidó su envío se
  descarta por la generación (D23).
- **Un solo dispatcher activo**: tiene una concesión en Valkey (10 s,
  renovada cada tercio); uno en espera la toma cuando muere (enseguida si
  se detiene limpiamente) y empieza tomando los resultados y avisos que el
  anterior dejó sin confirmar.
- **Los eventos en vivo** no son durables: los de un dispatcher que muere
  entre un commit y su publicación se pierden (la prueba de caos los
  cuenta). Los resultados guardados están completos; una página los
  muestra la próxima vez que recarga sus regiones en vivo.

## Pruebas de punta a punta, de caos y de carga

- `TestEndToEnd` (`make test-sandbox`) corre el dispatcher y un worker en
  el proceso de la prueba sobre cajas reales de isolate: una tarea Batch
  con puntaje GroupMin, envíos en C++ y Python 3 para cada veredicto
  (correcto, parcial, incorrecto, tiempo límite, señal, código de salida,
  error de compilación) y pruebas de usuario. Revisa resultados, puntajes,
  la caché de puntajes por tarea, las colas vacías y los eventos en vivo
  que recibe un proceso web a través de Valkey.
- `TestChaos` (`make chaos`, también parte de `make test-sandbox`) compila
  el binario `cms` y corre un dispatcher y dos workers como procesos. Juzga
  44 envíos sin interrupciones y luego otra vez mientras mata con SIGKILL
  al dispatcher (activo, y mientras tiene un lote de resultados sin
  confirmar) y a los workers a mitad de un trabajo, deja un worker caído
  40 s y corta las conexiones a Valkey de todos los procesos
  (`CLIENT KILL`). Cada envío debe terminar juzgado exactamente una vez por
  dataset, con los mismos resultados y puntajes que sin interrupciones, la
  misma caché de puntajes, las colas vacías, ningún proceso terminado por
  su cuenta y los trabajos del worker caído retomados en menos de 60 s.
  Las muertes son aleatorias: `CMS_TEST_CHAOS_SEED` (la prueba la imprime)
  repite una corrida y `CMS_TEST_CHAOS_SUBMISSIONS` cambia su tamaño. Si
  falla, la prueba muestra el final del log de cada proceso.
- `TestLoad` (`make load`) manda 2 envíos/s durante
  `CMS_TEST_LOAD_SUSTAINED` segundos (30) y luego 20/s durante
  `CMS_TEST_LOAD_BURST` segundos (60) a un dispatcher con workers simulados
  que responden al instante. Informa la latencia del aviso al veredicto, la
  CPU por trabajo y las idas y vueltas del dispatcher a la base y a Valkey
  por envío; `CMS_TEST_LOAD_CPUPROFILE=cpu.out` escribe un perfil para
  `go tool pprof`. Para el objetivo de 2 vCPU correrla con `GOMAXPROCS=2
  taskset -c 0,1 make load` y fijar PostgreSQL y Valkey a las mismas CPU
  (`taskset -a -p -c 0,1 PID`).

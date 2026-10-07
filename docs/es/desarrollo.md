# Desarrollo

## Requisitos

- Go: la versión de `go.mod` se descarga sola (`GOTOOLCHAIN=auto`).
- Para las pruebas y el stack local: binarios de **PostgreSQL 16** y de
  **Valkey** o **Redis ≥ 7**, o bien **Docker**. `scripts/infra.sh` elige
  solo (`CMS_INFRA=native|docker` para forzar).
- Para `make test-sandbox`: root, isolate ≥ 2.0 y cgroups v2.

## Comandos

```sh
make build         # bin/cms y el alias bin/cmsctl
make test          # suite completa con -race contra PostgreSQL y Valkey desechables
make test-short    # sin infraestructura (se saltan las pruebas que la usan)
make lint          # gofmt, go vet, cabeceras de licencia
make generate      # código generado y plantillas de traducción (.pot)
make dev-native    # stack completo con binarios nativos; make dev-stop lo detiene
make dev           # stack completo con Docker Compose; make dev-down lo borra
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

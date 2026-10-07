# Auditoría de requisitos

Cada requisito de SPEC.md con su código, su prueba y su estado:
**hecho**, **parcial**, **falta** o **requiere hardware** (necesita una
máquina que esta VM no ofrece, p. ej. cgroups v2 para isolate).

Actualizada al cerrar F0.

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
| R1.1–R1.4 Datos | falta |
| R2.1–R2.7 Sandbox y worker | falta (pruebas reales **requieren hardware**: cgroups v2) |
| R3.1–R3.4 Tipos de problema, checkers, lenguajes | falta |
| R4.1–R4.6 Dispatcher | falta |
| R5.1–R5.9 Sitio del concursante | falta (esqueleto: `/healthz`, `/source`) |
| R6.1–R6.9 Administración | falta (esqueleto) |
| R7.1–R7.4 Scoreboard público | falta (esqueleto) |
| R8.1–R8.3 Importación desde CMS | falta |
| R9.1–R9.6 Instalación | falta (imagen base y Compose de desarrollo hechos) |

## No funcionales

| Req. | Estado |
|---|---|
| N1 Rendimiento (k6) | falta |
| N2 Sin N+1 | falta (se prueba desde F1) |
| N3 Durabilidad | falta (F4) |
| N4 Seguridad del juez | falta (F2) |
| N5 Seguridad web | parcial: CSP con nonce, cabeceras, límites de cuerpo; CSRF, cookies y límites de frecuencia en F5 |
| N6 Observabilidad | hecho para los esqueletos |
| N7 Traducible | parcial: infraestructura y pruebas; textos a medida que llegan las páginas |
| N8 AGPL | parcial: `/source` en las tres webs; enlace en el pie con las plantillas (F5–F7) |
| N9 Misma interfaz | falta (F5–F7) |
| N10 Operación | falta (F9) |

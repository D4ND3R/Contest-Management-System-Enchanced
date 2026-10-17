# Docker

*English: [../en/docker.md](../en/docker.md).*

`deploy/docker-compose.yml` corre toda la instalación en contenedores:
PostgreSQL 16, Valkey 8, un trabajo de migración, los seis servicios y, con
el perfil `caddy`, Caddy con HTTPS automático. Las imágenes se publican en
GHCR para amd64 y arm64 en cada release: `ghcr.io/d4nd3r/cms` (todos los
servicios, distroless, sin root) y `ghcr.io/d4nd3r/cms-worker` (isolate y
los compiladores).

## Primer arranque

```sh
git clone https://github.com/D4ND3R/Contest-Management-System-Enchanced cms && cd cms
sudo deploy/compose-init.sh          # una vez: deploy/.env y deploy/secrets/
$EDITOR deploy/.env                  # versión, puertos, dominios, trabajos del juez
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml run --rm migrate ctl add-admin admin   # muestra la contraseña una vez
```

`compose-init.sh` genera los secretos **una sola vez** (secret_key,
contraseñas de PostgreSQL y Valkey, el token de blobs del juez) en
`deploy/secrets/` y nunca los sobrescribe; no muestra nada secreto. Llegan a
los contenedores como secretos de Docker (`CMS_*_FILE`). El único archivo de
configuración es `deploy/compose/cms.yaml` (el scoreboard en
`dispatcher.rankings`, que las variables no pueden expresar); el resto son
variables `CMS_*`.

Con HTTPS: poner `CMS_DOMAIN`, `CMS_ADMIN_DOMAIN`, `CMS_RANKING_DOMAIN`,
`CMS_BIND=127.0.0.1` y `CMS_NUM_PROXIES=1` en `.env`, y después
`docker compose -f deploy/docker-compose.yml --profile caddy up -d`.

Las direcciones públicas de la página Servidor (o desde la administración):

```sh
docker compose -f deploy/docker-compose.yml run --rm migrate ctl public-urls \
  --contest https://concurso.ejemplo.org/ --ranking https://ranking.ejemplo.org/
```

## El juez

El contenedor `worker` es privilegiado con su propio espacio de nombres de
cgroups (isolate necesita ambas cosas) y un host con cgroups v2. No recibe
`secret_key` ni la base: los archivos vienen del servidor de blobs del
dispatcher con un token derivado. Verificarlo con
`docker compose -f deploy/docker-compose.yml exec worker cms ctl verify-host`.
Los jueces de otras máquinas pueden usar la misma imagen (`docker run
--privileged --cgroupns=private …`) o el modo `--worker-only` del instalador.

## Respaldos, actualizaciones

```sh
# respaldo en CMS_BACKUP_DIR (./backups por defecto)
docker compose -f deploy/docker-compose.yml run --rm migrate ctl backup --output /backups/ --keep 14
# actualizar: respaldar, poner CMS_VERSION en .env y después
docker compose -f deploy/docker-compose.yml pull
docker compose -f deploy/docker-compose.yml up -d     # el trabajo migrate corre primero
# volver atrás: detener los servicios, restaurar, volver al CMS_VERSION anterior
docker compose -f deploy/docker-compose.yml stop contest-web admin-web ranking-web dispatcher worker monitor
docker compose -f deploy/docker-compose.yml run --rm migrate ctl restore --clean /backups/cms-backup-….tar.gz
```

`cmsctl upgrade` (con vuelta atrás automática) es para instalaciones con
systemd; con Compose los pasos de arriba son manuales. Programar los
respaldos con un timer o cron del host que corra el comando de respaldo.

## Construir las imágenes

```sh
make build
docker build -f deploy/docker/Dockerfile -t cms .
docker build -f deploy/docker/Dockerfile.worker -t cms-worker .
```

El workflow de release (`.github/workflows/images.yml`) compila un binario
por arquitectura en `dist/linux_<arq>/` y pasa `--build-arg
BINARY_SOURCE=release`.

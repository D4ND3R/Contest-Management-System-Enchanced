# Actualización y respaldos

*English: [../en/upgrades-and-backups.md](../en/upgrades-and-backups.md).*

## Actualizar

```sh
sudo cmsctl upgrade                     # última release
sudo cmsctl upgrade --version v1.3.0    # una release dada
sudo cmsctl upgrade --binary ./bin/cms  # un binario compilado localmente
```

Pasos, en orden:

| Paso | Qué pasa |
|---|---|
| fetch | descarga `cms_linux_<arq>.tar.gz` de la release, lo verifica contra `checksums.txt` y comprueba que el binario nuevo corre |
| backup | `cms ctl backup` de la base y la configuración en `<data_dir>/backups/…-pre-upgrade.tar.gz` (los blobs solo se agregan, no hacen falta para volver atrás) |
| stop | `systemctl stop` de las unidades `cms-*.service` habilitadas |
| install | el binario actual queda como `cms.prev` y se reemplaza |
| migrate | el binario **nuevo** aplica sus migraciones |
| start | `systemctl start` de las unidades |
| health | cada unidad activa y su `/healthz` (listener de operaciones) responde 200 antes de `--health-timeout` (2 min) |

**Vuelta atrás automática.** Si un paso falla, el comando detiene las
unidades, repone el binario anterior, restaura la base desde el respaldo de
esta ejecución (si se llegó a migrar), arranca las unidades, las verifica y
termina con un error que dice que volvió atrás. Las migraciones son solo
hacia adelante (D3): volver atrás siempre es restaurar el respaldo. Lo
escrito entre el arranque de la versión nueva y la vuelta atrás (segundos) se
pierde.

Si falla la propia vuelta atrás, el mensaje lista los pasos manuales: reponer
`cms.prev`, `cmsctl restore --clean <respaldo>`, `systemctl start cms.target`.

**Ensayar una falla** (antes del concurso, en una máquina de prueba):

```sh
sudo CMS_UPGRADE_FAIL_AT=migrate cmsctl upgrade --binary ./cms-nuevo --force
```

hace fallar la actualización justo después del paso nombrado (`fetch`,
`backup`, `stop`, `install`, `migrate`, `start` o `health`) y ejercita la
vuelta atrás.

Opciones: `--backup-dir`, `--units` (por defecto, las `cms-*.service`
habilitadas), `--ignore-health worker` (un juez cuyo sandbox se sabe que no
está sano), `--no-database` (máquinas que solo juzgan; implícito cuando la
única unidad es `cms-worker`), `--no-services` (instalaciones manuales),
`--release-url` (un espejo). Primero la máquina principal, después los
jueces.

## Respaldos

```sh
cmsctl backup                       # <data_dir>/backups/cms-backup-<hora UTC>.tar.gz
cmsctl backup --output /mnt/usb/    # otro directorio (o un nombre de archivo)
cmsctl backup --keep 14             # después borra todos menos los 14 más nuevos
cmsctl backup --no-blobs            # sin los archivos (p. ej. un bucket S3 versionado)
```

Un `.tar.gz` con:

| Entrada | Contenido |
|---|---|
| `manifest.json` | formato, versión de cms, versión del esquema y los checksums de sus migraciones, tablas y columnas |
| `db/<tabla>.copy` | cada tabla en formato `COPY` de texto de PostgreSQL, todas leídas en **una misma instantánea** |
| `blobs/<sha256>` | cada archivo del almacén de blobs (local o S3) |
| `config/cms.yaml` | el archivo de configuración |
| `SHA256SUMS` | el SHA-256 de cada otra entrada |

`COPY` desde Go en lugar de `pg_dump`: sin herramientas cliente que deban
coincidir con la versión del servidor, y funciona desde la imagen distroless
del stack de Docker. `task_scores` no se guarda (se calcula del resto y se
regenera al restaurar). Los servicios pueden seguir corriendo durante el
respaldo. El archivo contiene todos los secretos de `cms.yaml`: se crea con
permisos 0600; guardarlo como al servidor mismo.

El instalador agrega `cms-backup.timer` (diario a las 03:17, se guardan 14):

```sh
systemctl list-timers cms-backup.timer
sudo systemctl start cms-backup.service      # uno ahora
journalctl -u cms-backup.service
```

Cada respaldo es una copia completa: sacarlos de la máquina (rsync,
almacenamiento de objetos). Durante un concurso conviene uno por hora: volver
a correr el instalador con `--backup-calendar hourly --backup-keep 48`.

## Restaurar

```sh
sudo systemctl stop cms.target
sudo -u cms cmsctl restore --verify-only /var/lib/cms/backups/cms-backup-….tar.gz
sudo -u cms cmsctl restore --clean /var/lib/cms/backups/cms-backup-….tar.gz
sudo systemctl start cms.target
```

El archivo se lee dos veces: primero se verifica cada checksum (un archivo
dañado no cambia nada) y después se carga en **una sola transacción**: con
`--clean` se borran las tablas, funciones y tipos actuales (sin él la base
debe estar vacía, p. ej. una nueva); las migraciones del binario reconstruyen
el esquema del respaldo; las tablas sembradas por migraciones se vacían, se
desactivan los triggers de usuario y se quitan las claves foráneas mientras
se carga cada tabla; después vuelven los triggers, se agregan de nuevo las
claves foráneas (lo que verifica cada fila), las secuencias de identidad
pasan los ids restaurados, se aplican las migraciones más nuevas (un respaldo
de una versión anterior queda actualizado) y se recalcula la caché de
puntajes. Los archivos van al almacén de blobs configurado. Cualquier falla
deja la base como estaba.

Correrlo como el usuario `cms` (dueño del directorio de blobs). Se niega si
hay otras sesiones usando la base (detener los servicios; `--force` lo
fuerza). `--config-to /etc/cms/cms.yaml.restaurado` extrae la configuración
guardada (en una máquina nueva, tomar de ahí `secret_key`: las cookies, los
enlaces secretos del scoreboard y los tokens de los jueces se derivan de
ella). Un respaldo se restaura con un binario cuyas migraciones incluyan las
del respaldo: la misma versión o una más nueva.

**Mudarse a otra máquina:** instalar ahí, detener sus servicios, restaurar
con `--clean --config-to`, poner la `secret_key` anterior (y lo que se haya
cambiado) en su `cms.yaml`, arrancar.

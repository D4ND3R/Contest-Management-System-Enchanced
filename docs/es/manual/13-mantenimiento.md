# 13. Mantenimiento y referencia de `cmsctl`

*English: [Maintenance and cmsctl reference](../../en/manual/13-maintenance.md)*

Cómo cuidar el servidor: los servicios, los registros, los respaldos, las
actualizaciones, y todos los comandos de `cmsctl`. Referencias técnicas:
[../operacion.md](../operacion.md) y
[../actualizacion-y-respaldos.md](../actualizacion-y-respaldos.md).

## Contenido

1. [Reglas de oro](#1-reglas-de-oro)
2. [Los servicios](#2-los-servicios)
3. [Ver los registros](#3-ver-los-registros)
4. [Revisar la salud: `doctor` y `verify-host`](#4-revisar-la-salud-doctor-y-verify-host)
5. [Respaldos](#5-respaldos)
6. [Restaurar un respaldo](#restaurar-un-respaldo)
7. [Actualizar a una versión nueva](#7-actualizar-a-una-versión-nueva)
8. [Archivos y carpetas](#8-archivos-y-carpetas)
9. [Referencia de `cmsctl`](#9-referencia-de-cmsctl)
10. [Antes de un concurso importante](#10-antes-de-un-concurso-importante)

---

## 1. Reglas de oro

- Los comandos se corren **con `sudo`**: `sudo cmsctl …`.
- **Nunca compartas** contraseñas ni el contenido de `/etc/cms/cms.yaml`
  (tiene las claves). Para mostrar la configuración sin secretos:
  `sudo cmsctl config show`.
- **Respalda antes** de cualquier cambio grande y **copia los respaldos
  fuera** del servidor.
- No actualices el sistema ni CMS el día del concurso.

## 2. Los servicios

CMS son seis servicios de systemd, agrupados en `cms.target`:

| Servicio | Qué hace |
|---|---|
| `cms-contest-web` | sitio de los concursantes |
| `cms-admin-web` | administración |
| `cms-ranking-web` | scoreboard público |
| `cms-dispatcher` | reparte las evaluaciones, calcula puntajes, alimenta el scoreboard |
| `cms-worker` | el juez: compila y ejecuta en el sandbox |
| `cms-monitor` | vigila la salud de los servicios y la máquina |

```sh
systemctl status cms.target 'cms-*'          # estado de todos
sudo systemctl restart cms-worker            # reiniciar uno
sudo systemctl restart cms.target            # reiniciar todos
sudo systemctl stop cms.target               # detener todo
```

Arrancan solos al encender el servidor. **Reiniciar es seguro** en
cualquier momento: no se pierden envíos ni resultados.

## 3. Ver los registros

```sh
sudo journalctl -u cms-worker -n 100 --no-pager      # últimas 100 líneas
sudo journalctl -u cms-dispatcher -f                 # en vivo (Ctrl+C para salir)
sudo journalctl -u 'cms-*' --since "10 min ago"      # todos, últimos 10 minutos
```

Los registros están en formato JSON: busca `"level":"ERROR"` o
`"level":"WARN"`. Antes de compartirlos, filtra las líneas con claves:
`… | grep -v -i -e password -e secret`.

## 4. Revisar la salud: `doctor` y `verify-host`

```sh
sudo cmsctl doctor
```

Revisa toda la instalación y dice qué está mal y cómo arreglarlo
([cap. 14](14-solucion-de-problemas.md)). Úsalo siempre primero.

```sh
sudo cmsctl verify-host
```

Revisa a fondo la máquina del juez: cgroups v2, isolate, swap,
hiperthreading, frecuencia de la CPU. Los `WARN` afectan la precisión de
los tiempos; los `FAIL` impiden evaluar con seguridad.

## 5. Respaldos

**Automáticos:** todos los días a las 03:17 se guarda uno en
`/var/lib/cms/backups/` y se conservan los últimos 14 (cámbialo con
`--backup-keep` y `--backup-calendar` al instalar).

**Manual, ahora:**

```sh
sudo systemctl start cms-backup.service              # igual que el automático
sudo cmsctl backup --label antes-del-concurso        # con un nombre
ls -lh /var/lib/cms/backups/
```

Un respaldo es un solo archivo `.tar.gz` con la base de datos, los archivos
de problemas y envíos, y la configuración. Se puede hacer con los servicios
funcionando.

**Cópialo fuera del servidor** (desde tu computadora):

```sh
scp usuario@servidor:/var/lib/cms/backups/cms-backup-*.tar.gz .
```

(Si `scp` no puede leerlo, primero en el servidor:
`sudo cp /var/lib/cms/backups/cms-backup-XXX.tar.gz ~ && sudo chown $USER ~/cms-backup-XXX.tar.gz`.)

## Restaurar un respaldo

Restaurar **reemplaza todos los datos actuales** por los del respaldo.

```sh
sudo systemctl stop cms.target
sudo -u cms cmsctl --config /etc/cms/cms.yaml restore --clean /var/lib/cms/backups/cms-backup-XXX.tar.gz
sudo systemctl start cms.target
sudo cmsctl doctor
```

- Primero verifica todo el archivo; si está dañado no toca nada.
- `--clean` borra la base actual antes de cargar el respaldo; sin él, la
  base debe estar vacía.
- Detalles y casos especiales: [../actualizacion-y-respaldos.md](../actualizacion-y-respaldos.md#restaurar).

Los respaldos que hace `--clean` del instalador (`/var/backups/cms/pre-clean-…sql.gz`)
son volcados de PostgreSQL: se restauran con
`gunzip -c ARCHIVO.sql.gz | sudo -u postgres psql cms` sobre una base `cms`
vacía.

## 7. Actualizar a una versión nueva

```sh
sudo cmsctl upgrade
```

1. Descarga la última versión publicada y verifica su suma de control.
2. Hace un respaldo.
3. Detiene los servicios, reemplaza el programa, actualiza la base de datos
   y vuelve a arrancar.
4. Comprueba que todos los servicios estén sanos.
5. **Si cualquier paso falla, vuelve todo atrás solo** (programa anterior,
   base restaurada) y lo dice.

Opciones útiles: `--version v0.2.0` (una versión concreta), `--binary RUTA`
(un programa ya descargado).

Después de actualizar, conviene correr el instalador otra vez con las
mismas opciones (aplica mejoras de configuración nuevas; es seguro):

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --http-ports 8000,8001,8002
```

## 8. Archivos y carpetas

| Ruta | Contenido |
|---|---|
| `/etc/cms/cms.yaml` | configuración (con claves; sólo root y el grupo `cms`) |
| `/etc/cms/cms.env` | variables de los servicios |
| `/var/lib/cms/blobs/` | archivos de problemas y envíos |
| `/var/lib/cms/backups/` | respaldos automáticos y manuales |
| `/var/lib/cms/ranking/` | datos del scoreboard, logo, banderas, fotos |
| `/var/cache/cms/` | cachés (se pueden borrar) |
| `/var/log/cms-install.log` | registro del instalador |
| `/var/backups/cms/` | respaldos previos a `--clean` y `--purge` |
| `/usr/local/bin/cms`, `cmsctl` | el programa |

## 9. Referencia de `cmsctl`

Todos se usan como `sudo cmsctl COMANDO [opciones]`. `sudo cmsctl help
COMANDO` explica cada uno.

**Diagnóstico**

| Comando | Para qué |
|---|---|
| `doctor` | revisar la instalación completa ([cap. 14](14-solucion-de-problemas.md)) |
| `verify-host` | revisar la máquina del juez a fondo |
| `judging-status` | cuántos envíos hay en cada estado, por problema |
| `config check` | validar `/etc/cms/cms.yaml` |
| `config show` | mostrar la configuración efectiva (sin secretos) |

**Administradores**

| Comando | Para qué |
|---|---|
| `add-admin USUARIO` | crear un administrador (`--role all\|messaging\|tasks\|readonly`, `--name`) |
| `admin-password USUARIO` | nueva contraseña para un administrador (`--enable` lo rehabilita) |
| `public-urls` | ver o fijar las direcciones públicas (página *Servidor*) |

**Importar y exportar** ([cap. 7](07-importar.md))

| Comando | Para qué |
|---|---|
| `import-contest RUTA` | importar un concurso (carpeta o zip); `-i` problemas, `--users` usuarios, `-u`/`-U` actualizar, `--dry-run` |
| `import-task RUTA` | importar un problema; `-c CONCURSO` para agregarlo; `-u` actualizar |
| `import-dataset RUTA` | agregar un dataset nuevo a un problema |
| `import-users`, `import-team` | usuarios y equipos de un paquete |
| `validate-contest`, `validate-task` | revisar un paquete sin importar |
| `export-task NOMBRE ARCHIVO.zip` | exportar un problema |
| `import-dump ARCHIVO` | importar un dump del CMS original |

**Evaluación**

| Comando | Para qué |
|---|---|
| `reevaluate --contest X --level compilation\|evaluation\|score` | recompilar, reevaluar o recalcular puntajes; también `--task`, `--user`, `--dataset`, `--testcase` |

**Respaldos y versiones**

| Comando | Para qué |
|---|---|
| `backup` | respaldo completo (`--label`, `--output`, `--keep N`) |
| `restore ARCHIVO` | restaurar (`--clean`) |
| `upgrade` | actualizar con vuelta atrás automática |

**Otros**

| Comando | Para qué |
|---|---|
| `gen-secret` | generar una `secret_key` nueva |
| `blob-token` | token para jueces en otras máquinas |

## 10. Antes de un concurso importante

- Una semana antes: actualiza (`sudo cmsctl upgrade`) y haz un ensayo
  completo con usuarios de prueba.
- El día anterior: respaldo, `sudo cmsctl doctor`, `sudo cmsctl
  verify-host`.
- Para tiempos más estables en el juez: `sudo swapoff -a` y el modo
  `performance` de la CPU (`verify-host` muestra los comandos).
- Ten a mano: este manual, el acceso SSH al servidor y la contraseña de un
  administrador.

---

**Siguiente:** [14. Solución de problemas](14-solucion-de-problemas.md) ·
**Anterior:** [12. Después del concurso](12-despues-del-concurso.md) ·
[Índice](README.md)

# 14. Solución de problemas

*English: [Troubleshooting](../../en/manual/14-troubleshooting.md)*

Primero, siempre:

```sh
sudo cmsctl doctor
```

Este capítulo explica cómo leer su resultado y cómo resolver los problemas
reales más frecuentes (muchos los encontramos instalando en servidores
reales). Para fallas de la instalación, el instalador muestra su propio
diagnóstico ([cap. 2](02-instalacion.md#7-cuando-la-instalación-falla)).

## Contenido

1. [Leer el resultado de `doctor`](#1-leer-el-resultado-de-doctor)
2. [Instalación](#2-instalación)
3. [`cmsctl` y permisos](#3-cmsctl-y-permisos)
4. [Importar](#4-importar)
5. [El juez no funciona](#el-juez-no-funciona)
6. [Los sitios no abren](#6-los-sitios-no-abren)
7. [El scoreboard](#7-el-scoreboard)
8. [Acceso a la administración](#8-acceso-a-la-administración)
9. [Pedir ayuda](#9-pedir-ayuda)

---

## 1. Leer el resultado de `doctor`

```
Configuration
  PASS  configuration: /etc/cms/cms.yaml
Database
  PASS  PostgreSQL: connected
  PASS  migrations: schema version 4, up to date
  PASS  administrators: 2 (1 with every permission)
Valkey and judges
  PASS  Valkey: connected
  FAIL  judge d4server: takes no jobs: sandbox: ... Cannot open /run/isolate/cgroup
        fix: sudo cmsctl verify-host shows what the sandbox needs
  FAIL  queue: 12 job(s) waiting for a judge, the oldest for 6m10s
        fix: submissions are not being judged: see the judges above and sudo journalctl -u cms-worker -n 50
Services
  PASS  cms-contest-web: active and enabled
  ...
Contests
  WARN  contest D4OI2026: task baches has no grader for C11 / gcc, Pascal / fpc (allow only the languages it supports on the contest page)

24 checks: 2 failed, 1 warnings
```

- Está agrupado: configuración, base de datos, Valkey y jueces, servicios,
  salud, almacenamiento, máquina, concursos, cortafuegos.
- Cada `FAIL` o `WARN` trae su `fix:` con el comando o la acción.
- Corrígelos **de arriba hacia abajo**: un problema de base de datos o de
  Valkey causa otros más abajo.
- Corre `doctor` con `sudo`: sin él, no puede ver los registros ni el
  cortafuegos.
- Termina con código 1 si hay algún `FAIL` (útil en scripts) y `--json`
  da el resultado para otras herramientas.

## 2. Instalación

**«Job for valkey-server.service failed» / el puerto 6379 está ocupado.**
Otro Redis o Valkey usa el puerto. Ahora la revisión previa lo detecta y
dice cuál:
```sh
sudo ss -ltnp 'sport = :6379'                # quién lo usa
sudo systemctl disable --now redis-server    # si es un redis-server que no usas
```
Si otra aplicación tuya necesita ese Redis, no lo detengas: escríbenos (por
ahora CMS necesita Valkey en el puerto 6379 de la máquina).

**«a PostgreSQL database named "cms" already exists and was not made by this
installer» o «column "checksum" does not exist».** Hay una base `cms` de otro
programa (por ejemplo, el CMS original). Si no la necesitas:
```sh
curl -fsSL …/install.sh | sudo bash -s -- --clean --http-ports 8000,8001,8002
```
`--clean` la respalda en `/var/backups/cms/` antes de borrarla. Si otra
aplicación la usa, no la borres: el instalador no la toca ni cambia la
contraseña del usuario `cms` de PostgreSQL.

**La instalación terminó pero los servicios no están activos** (`systemctl
list-units 'cms*'` sólo muestra `cms.target`). Vuelve a correr el
instalador con las mismas opciones; ahora, si un paso falla, lo dice con el
informe de falla en vez de cortarse en silencio.

**No mostró la contraseña del administrador.** El administrador ya existía:
`sudo cmsctl admin-password admin`.

**Apt ocupado («Could not get lock»).** Otra instalación de paquetes está
en curso (por ejemplo, actualizaciones automáticas). Espera unos minutos y
vuelve a correr el instalador.

**Sin red / proxy.** El diagnóstico lo dice («The package mirrors cannot be
reached»). Prueba `sudo apt-get update` y `curl -I https://github.com`.

**Quiero empezar de cero.** `--clean` ([cap. 2](02-instalacion.md#8-instalación-limpia---clean)).

## 3. `cmsctl` y permisos

**«cannot read /etc/cms/cms.yaml: permission denied: run the command with
sudo».** Usa `sudo cmsctl …`. La configuración tiene claves y sólo la leen
root y el grupo `cms`.

**«password authentication failed for user "cms"».** El comando no encontró
la configuración y usó la de fábrica. Usa `sudo cmsctl …` (o
`--config /etc/cms/cms.yaml`).

**`doctor` dice que el almacén tiene archivos de otro dueño.** Algún comando
se corrió como root con una versión anterior y los servicios no pueden
leerlos:
```sh
sudo chown -R cms:cms /var/lib/cms/blobs
```
Desde esta versión `sudo cmsctl` deja los archivos a nombre de `cms`.

## 4. Importar

**«couldn't autodetect the loader … loaders of contests: italy_yaml wants
contest.yaml; polygon wants contest.xml and problems/».** El archivo no
tiene la estructura de un concurso en la raíz ni dentro de una única
carpeta. Revisa con `unzip -l archivo.zip`. Si es un problema suelto, usa
`import-task`.

**«users not found in the database».** Agrega `--users` o crea los usuarios
antes.

**Avisos de grader («grader only for C++…», «no grader.%l for C11 / gcc,
Pascal / fpc»).** El concurso permite lenguajes para los que el problema no
tiene grader. En la página del concurso deja sólo los lenguajes con grader
([cap. 7](07-importar.md#10-los-avisos-al-importar)).

**El CSV de usuarios da «invalid username».** Sin espacios, tildes ni `ñ`
en los nombres de usuario ([cap. 8](08-usuarios-y-equipos.md#11-errores-comunes)).

## El juez no funciona

Síntomas: los envíos se quedan en *Compilando…* o *Evaluando…*; `doctor`
dice «no judge is connected», «takes no jobs» o «job(s) waiting for a
judge».

1. Mira el juez:
   ```sh
   sudo systemctl status cms-worker
   sudo journalctl -u cms-worker -n 50 --no-pager
   ```
2. Revisa la máquina: `sudo cmsctl verify-host`. Los `FAIL` más comunes:
   - **cgroups v2 ausente** («Cannot open /run/isolate/cgroup»): el sistema
     arranca con cgroups v1 (frecuente en algunas VPS o contenedores). En
     Ubuntu 22.04 o posterior viene v2 por defecto; si no, agrega
     `systemd.unified_cgroup_hierarchy=1` a la línea del kernel en
     `/etc/default/grub`, `sudo update-grub` y reinicia. Algunos
     proveedores de VPS no lo permiten: elige otro tipo de servidor.
   - **isolate no instalado**: vuelve a correr el instalador.
3. Reinicia el juez: `sudo systemctl restart cms-worker`.
4. Los envíos pendientes se evalúan solos cuando vuelve; no se pierde nada.

Los `WARN` de `verify-host` (swap, hiperthreading, frecuencia de la CPU,
hugepages) no impiden evaluar: sólo hacen los tiempos menos precisos. Para
un concurso importante, aplícalos ([cap. 13](13-mantenimiento.md#10-antes-de-un-concurso-importante)).

## 6. Los sitios no abren

1. `sudo cmsctl doctor`: sección «Health» y «Services».
2. ¿El servicio está activo? `sudo systemctl restart cms-contest-web` (o
   `cms-admin-web`, `cms-ranking-web`).
3. ¿El cortafuegos? Si ufw está activo, `doctor` avisa si falta abrir un
   puerto: `sudo ufw allow 8000/tcp`.
4. ¿Otro cortafuegos (del proveedor de la nube)? Abre los puertos también
   en su panel.
5. Con dominio: ¿el DNS apunta al servidor? ¿Los puertos 80 y 443 están
   abiertos? Los certificados se piden en la primera visita.

## 7. El scoreboard

**Sigue mostrando un concurso borrado.** Desde esta versión desaparece solo
en menos de un minuto. Si no: `sudo cmsctl doctor` (línea «scoreboard
push») y, como último recurso, vaciarlo
([cap. 11](11-scoreboard.md#7-empezar-el-scoreboard-de-cero)).

**Está vacío.** El concurso necesita horario y participaciones no ocultas.

**Aparece gente de prueba.** Marca sus participaciones como ocultas.

## 8. Acceso a la administración

**Perdí la contraseña.** `sudo cmsctl admin-password admin`.

**No queda ningún administrador.** `sudo cmsctl add-admin admin`.

**«Failed to log in.» muchas veces.** Tras varios intentos fallidos se
frenan los intentos unos segundos: espera y prueba de nuevo con la
contraseña correcta.

## 9. Pedir ayuda

Junta esta información (no contiene contraseñas):

```sh
sudo cmsctl doctor > doctor.txt 2>&1
sudo tail -n 80 /var/log/cms-install.log > instalacion.txt     # si falló la instalación
sudo journalctl -u 'cms-*' --since "30 min ago" --no-pager | grep -v -i -e password -e secret > servicios.txt
cms version
```

Nunca compartas `/etc/cms/cms.yaml`, los respaldos ni contraseñas.

---

**Anterior:** [13. Mantenimiento](13-mantenimiento.md) ·
[Índice](README.md)

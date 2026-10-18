# 2. Instalación

*English: [Installation](../../en/manual/02-installation.md)*

Este capítulo explica cómo instalar CMS en un servidor, qué hace el
instalador, cómo leer sus mensajes cuando algo falla, cómo hacer una
**instalación limpia** que borre todo rastro anterior y cómo desinstalar.

## Contenido

1. [Antes de empezar](#1-antes-de-empezar)
2. [Elegir el modo: dominio o puertos](#2-elegir-el-modo-dominio-o-puertos)
3. [Revisar el servidor sin instalar nada](#3-revisar-el-servidor-sin-instalar-nada)
4. [Instalar](#4-instalar)
5. [Qué hace el instalador, paso a paso](#5-qué-hace-el-instalador-paso-a-paso)
6. [El resumen final](#6-el-resumen-final)
7. [Cuando la instalación falla](#7-cuando-la-instalación-falla)
8. [Instalación limpia (`--clean`)](#8-instalación-limpia---clean)
9. [El cortafuegos](#9-el-cortafuegos)
10. [Todas las opciones](#10-todas-las-opciones)
11. [Jueces en otras máquinas](#11-jueces-en-otras-máquinas)
12. [Desinstalar](#12-desinstalar)
13. [Qué hacer si…](#13-qué-hacer-si)

---

## 1. Antes de empezar

**Sistema operativo:** Ubuntu 22.04, 24.04 o 26.04 LTS, o Debian 12, con
systemd. En 26.04 el compilador de C# no existe y se omite; los demás
lenguajes funcionan.

**Hardware mínimo:** 2 vCPU y 4 GB de RAM para un concurso de unas 100
personas con un juez. Cada juez que evalúe en paralelo necesita un núcleo
propio. Detalles de dimensionamiento en [../instalacion.md](../instalacion.md).

**Acceso:** un usuario con `sudo`. El instalador corre como root.

**Lo que se instala:** PostgreSQL (base de datos), Valkey (colas),
isolate (el sandbox donde corren los programas de los concursantes), los
compiladores, Caddy (sólo con dominio), los servicios de systemd de CMS y
un respaldo diario. Si ya tienes PostgreSQL o Valkey/Redis instalados, se
reutilizan.

> **Servidores con otras aplicaciones.** El instalador nunca desinstala
> nada y, desde esta versión, no activa el cortafuegos si estaba apagado
> (podría bloquear tus otros servicios). Sí necesita que los puertos que
> usa estén libres: lo revisa antes de tocar nada (sección 3).

## 2. Elegir el modo: dominio o puertos

| | Con dominio (`--domain`) | Con puertos (`--http-ports`) |
|---|---|---|
| Direcciones | `https://concurso.ejemplo.org`, `https://admin.concurso.ejemplo.org`, `https://ranking.concurso.ejemplo.org` | `http://IP:8000`, `http://IP:8001`, `http://IP:8002` |
| Cifrado | HTTPS con certificados automáticos (Caddy) | HTTP sin cifrar |
| Requisitos | tres registros DNS apuntando al servidor; puertos 80 y 443 libres | los tres puertos libres |
| Recomendado para | concursos por internet | redes locales, pruebas |

Con dominio, crea primero los tres registros DNS (tipo A, o AAAA para IPv6)
apuntando a la IP del servidor. El instalador avisa si no apuntan bien.

## 3. Revisar el servidor sin instalar nada

Antes de instalar, puedes pedirle al instalador que sólo revise el
servidor:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --check --http-ports 8000,8001,8002
```

Muestra una tabla con una línea por chequeo, por ejemplo:

```
==> preflight checks
  PASS  operating system         ubuntu 26.04
  PASS  disk (/var)              180 GiB free
  FAIL  port 6379 (Valkey)       redis-server (pid 1450) already listens; cms installs valkey-server, which needs the port
                                 fix: if nothing else uses it: sudo systemctl disable --now redis-server (--clean does not remove other software)
  PASS  PostgreSQL               running
  ...
  1 check(s) FAILED, 0 warning(s). Nothing was changed: fix what the
  'fix:' lines say and run the same command again.
```

- **PASS**: bien.
- **WARN**: se puede instalar, pero conviene corregirlo (por ejemplo,
  cgroups v2 ausente: el juez no podría limitar la memoria con precisión).
- **FAIL**: no se instala nada hasta corregirlo. La línea `fix:` dice
  exactamente qué hacer.

Esta misma revisión se hace siempre al comienzo de una instalación normal:
si hay algún FAIL, el instalador se detiene **antes de cambiar nada**.

Qué se revisa: que corra como root, el sistema operativo, que systemd sea
el proceso 1, la arquitectura, el espacio libre en `/var` (al menos unos 3
GiB), la memoria, el acceso a internet (cuando hay que descargar algo), que
apt no esté ocupado por otro proceso, que los puertos estén libres o sean
de CMS, que el puerto 6379 no lo use otro Redis/Valkey, que exista
PostgreSQL o se pueda instalar, que no haya una base de datos `cms` de otro
programa, cgroups v2 y el DNS del dominio.

## 4. Instalar

Con dominio:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --domain concurso.ejemplo.org --email tu@correo.org
```

Sólo con la IP:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --http-ports 8000,8001,8002
```

Tarda entre 5 y 20 minutos según la conexión (los compiladores son lo más
pesado; `--compilers basic` instala sólo C, C++, Python 3 y Java y es mucho
más rápido).

Todo lo que se ve en pantalla queda también en `/var/log/cms-install.log`
(sin contraseñas).

**Volver a correr el mismo comando es seguro**: sólo hace lo que falte, no
borra datos ni cambia la contraseña del administrador. Es la forma normal
de terminar una instalación que falló después de corregir la causa.

## 5. Qué hace el instalador, paso a paso

Cada paso se anuncia con una línea `==>`. Saber qué hace cada uno ayuda a
entender un error:

| Paso (`==>`) | Qué hace |
|---|---|
| `preflight checks` | la revisión de la sección 3 |
| `installing base packages` | paquetes básicos con apt (curl, gnupg…) |
| `installing …/cms` | descarga el programa `cms` de la última versión publicada y verifica su suma de control |
| `creating the cms user and directories` | crea el usuario de sistema `cms` y `/etc/cms`, `/var/lib/cms`, `/var/cache/cms` |
| `installing isolate…` | instala el sandbox |
| `installing compilers` | instala los compiladores de los lenguajes |
| `installing PostgreSQL` | instala/configura PostgreSQL, crea el usuario y la base `cms` |
| `installing valkey-server` | instala/configura Valkey con contraseña y persistencia |
| `writing /etc/cms/cms.yaml` | escribe la configuración (las claves se generan una sola vez) |
| `applying the database migrations` | crea las tablas |
| `writing the systemd units` / `starting the services` | crea y arranca los seis servicios |
| `configuring the firewall` | ver sección 9 |
| verificación del juez | `cms ctl verify-host`: comprueba que el sandbox funciona |
| doctor | `cms ctl doctor`: revisa la instalación completa ([cap. 14](14-solucion-de-problemas.md)) |

## 6. El resumen final

Al terminar se muestra un recuadro como éste:

```
======================================================================
 cms v0.2.0 installed
======================================================================
 Contestant site:  http://203.0.113.10:8000/
 Administration:   http://203.0.113.10:8001/
 Scoreboard:       http://203.0.113.10:8002/
 ...
 First administrator: admin
 Password:            Xxxxxxxxxxxxxxxxxxxx
 It is shown only now and stored nowhere in clear (not in the log): write it down.
```

- **Las tres direcciones**: sitio de los concursantes, administración y
  scoreboard público.
- **El usuario `admin` y su contraseña**: se muestran **una sola vez** y no
  se guardan en ningún lado (tampoco en el log). Anótala en un lugar
  seguro. Si la pierdes: `sudo cmsctl admin-password admin` genera una
  nueva ([cap. 3](03-primer-acceso.md)).
- Si dice **`installed with problems`**, algo no quedó bien: las líneas de
  arriba (`WARNING`, el resultado de `doctor`) dicen qué y cómo arreglarlo.

En una máquina virtual o un servidor sin cgroups v2 verás `Judge check:
FAILED`: los sitios funcionan, pero el juez no puede evaluar con seguridad
([cap. 14](14-solucion-de-problemas.md#el-juez-no-funciona)).

## 7. Cuando la instalación falla

Si cualquier paso falla, el instalador se detiene y muestra un **informe de
falla**:

```
######################################################################
  INSTALLATION FAILED
######################################################################
  Step:       installing base packages
  Command:    apt-get -o DPkg::Lock::Timeout=600 "$@"
  Exit code:  100 (line 452, in apt_get)
  Last lines of output:
    | E: Failed to fetch https://security.ubuntu.com/ubuntu/pool/main/g/gnupg2/gpg_2.4.4-2ubuntu17.6_amd64.deb ...
    | E: Unable to fetch some archives, maybe run apt-get update or try with --fix-missing?

  DIAGNOSIS
    The package mirrors cannot be reached.
    Fix: check the network and the apt sources (sudo apt-get update shows which
         one fails) and run the installer again.

  Full log:   /var/log/cms-install.log
  Running the same command again is safe once the cause is fixed.
  --clean backs up and removes every trace of cms, then installs from zero.
######################################################################
```

Cómo leerlo:

1. **Step**: en qué paso estaba (la tabla de la sección 5).
2. **Command / Exit code**: el comando exacto que falló.
3. **Last lines of output**: lo último que mostró ese comando.
4. **DIAGNOSIS**: la causa probable y **el arreglo**. El instalador reconoce
   los problemas más comunes: apt ocupado por otra instalación, sin red o
   DNS, proxy que bloquea, disco lleno, un puerto ocupado (y por qué
   programa), una base de datos `cms` de otro programa, errores de
   contraseña de PostgreSQL, Valkey/Redis que no arranca (con su
   registro), un servicio que no arranca (con su registro), una descarga o
   suma de control fallida, sistema no soportado, migración fallida.
5. Corrige y **vuelve a correr el mismo comando**.

Si el diagnóstico no alcanza, el log completo está en
`/var/log/cms-install.log`. Para pedir ayuda, comparte el recuadro y las
últimas 50 líneas del log (`sudo tail -n 50 /var/log/cms-install.log`): no
contienen contraseñas.

Los problemas reales más frecuentes y su solución están en el
[capítulo 14](14-solucion-de-problemas.md).

## 8. Instalación limpia (`--clean`)

Una **instalación limpia** borra por completo cualquier instalación
anterior de CMS (configuración, datos, servicios, base de datos, archivos
basura) y vuelve a instalar desde cero, para partir de una base sana. Úsala
cuando:

- una instalación quedó a medias y no sabes en qué estado está;
- quieres empezar de nuevo sin los concursos y usuarios de prueba;
- hay una base de datos `cms` vieja (por ejemplo del CMS original) que
  impide instalar.

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --clean --http-ports 8000,8001,8002
```

1. Muestra **exactamente qué va a borrar** y pide confirmación (escribe
   `yes`). Con `--yes` no pregunta.
2. **Respalda primero** la base de datos `cms` y `/etc/cms` en
   `/var/backups/cms/pre-clean-FECHA…` (con `--no-backup` no respalda).
3. Borra: los servicios y temporizadores de CMS, `/etc/cms`,
   `/var/lib/cms` (archivos de problemas y envíos, respaldos
   programados), `/var/cache/cms`, el programa `cms`, la base de datos y el
   usuario `cms` de PostgreSQL, la configuración y las claves de CMS en
   Valkey, cajas de isolate sobrantes, el sitio de CMS en Caddy, las reglas
   de ufw de CMS, su archivo de sysctl y el usuario de sistema `cms`.
4. **Nunca** desinstala paquetes ni toca otros programas: PostgreSQL,
   Valkey, Caddy, isolate y los compiladores se reutilizan.
5. Instala desde cero y muestra una contraseña de administrador nueva.

> **Atención:** `--clean` borra todos los concursos, usuarios y envíos.
> El respaldo de `/var/backups/cms` permite recuperarlos
> ([cap. 13](13-mantenimiento.md#restaurar-un-respaldo)).

## 9. El cortafuegos

- Si **ufw ya estaba activo**, el instalador agrega las reglas de CMS (SSH
  primero, luego los puertos de los sitios o 80/443 con dominio).
- Si **ufw estaba apagado**, lo deja apagado y al final muestra los
  comandos para abrir los puertos si algún día lo activas. Así no bloquea
  otros servicios del servidor.
- `--firewall` lo activa de todos modos (después de permitir SSH);
  `--no-firewall` no lo toca en absoluto.
- `--admin-allow 10.0.0.0/8` hace que sólo esa red pueda abrir la
  administración (recomendado si los organizadores están en una red
  conocida).

## 10. Todas las opciones

| Opción | Qué hace |
|---|---|
| `--domain NOMBRE` | HTTPS con Caddy: concursantes en NOMBRE, administración en admin.NOMBRE, scoreboard en ranking.NOMBRE |
| `--admin-domain`, `--ranking-domain` | otros nombres para la administración y el scoreboard |
| `--email CORREO` | correo para los certificados |
| `--http-ports C,A,R` | HTTP en esos puertos (por defecto 8000,8001,8002) |
| `--check` | sólo revisar, sin cambiar nada |
| `--clean` | instalación limpia (sección 8) |
| `--no-backup` | con `--clean` o `--purge`: no respaldar antes de borrar |
| `--yes` | no preguntar (necesario con `--clean`/`--purge` sin terminal) |
| `--version vX.Y.Z` | instalar esa versión (por defecto, la última) |
| `--binary RUTA` | instalar ese programa `cms` en vez de descargarlo |
| `--compilers all\|basic\|none` | lenguajes del juez (por defecto todos) |
| `--judge-concurrency N` | evaluaciones en paralelo del juez local (por defecto 1; como máximo uno por núcleo físico) |
| `--admin-user NOMBRE` | nombre del primer administrador (por defecto `admin`) |
| `--admin-allow CIDR` | sólo esa red abre la administración |
| `--firewall` / `--no-firewall` | sección 9 |
| `--allow-judges CIDR` | jueces en otras máquinas de esa red |
| `--backup-keep N` | respaldos diarios que se conservan (14; 0 desactiva) |
| `--backup-calendar ESPEC` | hora de los respaldos (formato OnCalendar de systemd) |
| `--worker-only` y sus opciones | instalar sólo un juez (sección 11) |
| `--uninstall`, `--purge` | sección 12 |
| `--force-os` | intentar en una distribución no soportada |

## 11. Jueces en otras máquinas

Para concursos grandes se pueden agregar máquinas que sólo evalúan. Ver
[../instalacion.md](../instalacion.md#jueces-adicionales) y
[../jueces.md](../jueces.md).

## 12. Desinstalar

- `--uninstall`: quita los servicios y el programa; **conserva** la
  configuración, los datos y la base de datos (puedes reinstalar después).
- `--uninstall --purge`: además borra la base de datos, los datos y la
  configuración (respalda antes en `/var/backups/cms`, salvo con
  `--no-backup`).

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --uninstall --purge
```

## 13. Qué hacer si…

**…el instalador dice que el puerto 6379 está ocupado por `redis-server`.**
Otro Redis ya usa ese puerto. Si no lo necesitas:
`sudo systemctl disable --now redis-server` y vuelve a instalar. Si otra
aplicación lo usa, consulta el [capítulo 14](14-solucion-de-problemas.md).

**…dice que existe una base de datos `cms` que no hizo este instalador.**
Es de otro programa (por ejemplo, el CMS original). Si no la necesitas, usa
`--clean` (la respalda antes de borrarla).

**…no muestra la contraseña del administrador.** El administrador ya
existía de una instalación anterior: `sudo cmsctl admin-password admin`.

**…termina con `installed with problems`.** Lee las líneas `WARNING` y el
resultado de `doctor` justo arriba del resumen, o corre `sudo cmsctl
doctor` ([cap. 14](14-solucion-de-problemas.md)).

**…se cortó la conexión SSH a mitad de la instalación.** Vuelve a correr el
mismo comando.

---

**Siguiente:** [3. Primer acceso](03-primer-acceso.md) ·
**Anterior:** [1. Conceptos](01-conceptos.md) ·
[Índice](README.md)

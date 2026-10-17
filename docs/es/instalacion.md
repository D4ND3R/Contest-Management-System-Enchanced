# Instalación

*English: [../en/installation.md](../en/installation.md).*

Un comando instala todo en una máquina limpia con **Ubuntu 22.04 / 24.04** o
**Debian 12** (amd64 o arm64): PostgreSQL, Valkey (o Redis ≥ 7), isolate,
los compiladores, el binario `cms`, una unidad systemd por servicio, el
firewall, los respaldos programados y el primer administrador. Para
contenedores, ver [Docker](docker.md).

## Requisitos y dimensionamiento

| Parte | Mínimo para un concurso como el de SPEC N1 (500 concursantes) |
|---|---|
| Lado web: los tres sitios, dispatcher, monitor, PostgreSQL y Valkey | **2 vCPU, 4 GB de RAM**, 20 GB de SSD + datos de prueba y envíos |
| Cada juez | **un núcleo físico por trabajo en paralelo** (`worker.concurrency`), 1–2 GB de RAM por trabajo, cgroups v2, SMT apagado para tiempos estables |
| Red | puertos 80/443 (modo dominio) o los tres puertos de los sitios; los jueces de otras máquinas llegan a Valkey (6379) y al servidor de blobs (8891) |

- Juzgar en la máquina web sirve para concursos chicos y ensayos
  (`--judge-concurrency 1`); para un concurso real, los jueces van en sus
  propias máquinas (o en núcleos reservados), así un sitio cargado nunca
  cambia los tiempos medidos.
- Disco: los blobs (enunciados, casos, envíos, ejecutables) crecen con el
  concurso; los respaldos son copias completas y se guardan 14 por defecto.
- El juez necesita **cgroups v2** (Ubuntu 22.04+ y Debian 12+ lo traen).
  `cms ctl verify-host` dice qué corregir.

## Modo 1: un dominio con HTTPS

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --domain concurso.ejemplo.org --email vos@ejemplo.org
```

Cada sitio tiene su nombre (los registros DNS A/AAAA deben apuntar a la
máquina antes de la primera visita, cuando Caddy obtiene los certificados):

| Sitio | Dirección |
|---|---|
| Sitio del concursante | `https://concurso.ejemplo.org/` |
| Administración | `https://admin.concurso.ejemplo.org/` (`--admin-domain` para cambiarla) |
| Scoreboard | `https://ranking.concurso.ejemplo.org/` (`--ranking-domain`) |

Subdominios y no rutas: como en CMS, todas las páginas usan rutas absolutas
(`/tasks/…`, `/static/…`), así que cada sitio necesita la raíz de su host, y
hosts separados mantienen las cookies de la administración lejos de los otros
sitios. Los servicios escuchan en localhost; Caddy termina TLS, comprime todo
salvo los eventos en vivo (`*/events`) y les reenvía (`num_proxies: 1`).
`--admin-allow 10.0.0.0/8` deja abrir la administración solo desde esa red.

## Modo 2: puertos, sin dominio

```sh
curl -fsSL …/scripts/install.sh | sudo bash -s -- --http-ports 8000,8001,8002
```

Sitio del concursante, administración y scoreboard en esos puertos (HTTP
plano: para una red cerrada, como una sede). La administración conoce los
otros sitios como `:8000` y `:8002` en su página Servidor, que significa "el
host desde el que se abrió esta página, en ese puerto": los enlaces funcionan
se entre por nombre o por IP. `--admin-allow CIDR` restringe el puerto de la
administración en el firewall.

## Jueces adicionales

En la máquina principal, habilitar la red de los jueces (abre Valkey y el
servidor de blobs solo a ella):

```sh
… | sudo bash -s -- --domain concurso.ejemplo.org --allow-judges 10.0.0.0/24
```

El resumen nombra dos archivos: `/etc/cms/judges/blob-token` y
`/etc/cms/judges/valkey-url`. Son secretos: copiarlos a cada máquina de
jueces (por ejemplo con `scp`) y ahí correr:

```sh
… | sudo bash -s -- --worker-only --dispatcher http://10.0.0.1:8891 \
      --blob-token-file blob-token --valkey-url-file valkey-url --judge-concurrency 4
```

Una máquina de jueces recibe solo isolate, los compiladores y
`cms-worker.service`. Nunca recibe `secret_key` ni acceso a la base (D12,
D25). Cada máquina aparece en la página Jueces con su nombre de host. Usar
una red privada o una VPN entre las máquinas: el tráfico de Valkey no va
cifrado.

## Qué hace el instalador

1. Paquetes: PostgreSQL (con `conf.d/cms.conf` dimensionado para 4 GB),
   Valkey (o Redis ≥ 7; en Ubuntu 22.04, que trae 6.0, el repositorio de
   Redis) con contraseña, `appendonly` y `noeviction` (las colas nunca se
   desalojan), isolate del repositorio de sus autores (compilado de la misma
   versión en arm64), compiladores (`--compilers all|basic|none`), Caddy en
   modo dominio, ufw.
2. El binario `cms` de la última release de GitHub, verificado contra
   `checksums.txt` (`--version vX.Y.Z`, o `--binary RUTA` para uno
   compilado localmente), en `/usr/local/bin`, más el enlace `cmsctl`.
3. Secretos, generados **una sola vez**: `secret_key` (en `cms.yaml`), las
   contraseñas de la base y de Valkey (`/etc/cms/secrets/`) y el token del
   juez local. Las credenciales del scoreboard se derivan de `secret_key`:
   dispatcher y ranking-web coinciden sin otro secreto.
4. `/etc/cms/cms.yaml`, **solo si no existe**. Lo que depende del modo
   (direcciones de escucha, proxies, servidor de blobs) va a
   `/etc/cms/cms.env`, que se reescribe en cada ejecución.
5. `cms migrate`, el primer administrador (`--admin-user`, por defecto
   `admin`) con una **contraseña generada que se muestra una vez** al final, y
   las direcciones públicas de la página Servidor.
6. Unidades systemd `cms-contest-web`, `cms-admin-web`, `cms-ranking-web`,
   `cms-dispatcher`, `cms-monitor` (usuario `cms`, aisladas: sistema de solo
   lectura, sin dispositivos, sin privilegios nuevos, llamadas al sistema
   restringidas) y `cms-worker` (root, como exige isolate), agrupadas en
   `cms.target`; `cms-backup.timer` (diario, se guardan 14: `--backup-keep`,
   `--backup-calendar`).
7. ufw: primero SSH (el puerto que usa sshd), después 80/443 o los tres
   puertos.
8. `cms ctl verify-host` y un resumen con las direcciones.

El juez local lee los archivos por el servidor de blobs del dispatcher en
localhost, como un juez remoto: nunca escribe en el directorio de blobs del
usuario `cms`.

## Volver a correrlo

Es seguro y es la forma de cambiar el modo o las opciones: se conservan los
secretos, `cms.yaml`, el administrador y la base; se reescriben las unidades,
el sitio de Caddy, las reglas del firewall y `cms.env`. Sin `--domain`,
`--http-ports` ni `--worker-only` se reutiliza el modo anterior
(`/etc/cms/install.env`). El binario se reemplaza solo con `--version` o
`--binary`: para actualizar, [`cmsctl upgrade`](actualizacion-y-respaldos.md).

Si cambia el puerto del scoreboard, actualizar `dispatcher.rankings` en
`cms.yaml` (el instalador avisa).

## Archivos

| Ruta | Contenido |
|---|---|
| `/usr/local/bin/cms`, `cmsctl` | el binario (y `cms.prev`, el anterior, después de una actualización) |
| `/etc/cms/cms.yaml` | configuración (root:cms 0640; tiene secretos) |
| `/etc/cms/cms.env`, `worker.env` | valores que el instalador escribe en cada ejecución |
| `/etc/cms/secrets/` | contraseñas y tokens generados |
| `/var/lib/cms/blobs`, `ranking`, `backups` | archivos, estado del scoreboard, respaldos |
| `/var/lib/cms/worker`, `/var/cache/cms` | directorio de trabajo del juez, cachés |

## Desinstalar

```sh
… | sudo bash -s -- --uninstall
```

detiene y quita las unidades, el binario y el sitio de Caddy, y **conserva**
`/etc/cms`, `/var/lib/cms` (con los respaldos) y la base, para que una
instalación posterior los retome. Para borrarlos también:

```sh
… | sudo bash -s -- --uninstall --purge --yes
```

PostgreSQL, Valkey, Caddy, isolate y los compiladores quedan instalados
(quitarlos con apt si nada más los usa); las reglas del firewall también.

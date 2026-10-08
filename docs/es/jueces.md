# Jueces (workers)

*English: [../en/judges.md](../en/judges.md).*

Un **juez** es un proceso `cms worker`: toma trabajos de compilación y
evaluación de la cola en Valkey, los ejecuta dentro de
[isolate](https://github.com/ioi/isolate) y devuelve el resultado. Nunca se
conecta a PostgreSQL. Puede haber varios en una máquina (con rangos de cajas
distintos) y en varias máquinas.

## Requisitos del host

- Linux con **cgroups v2** (Ubuntu 22.04+, Debian 12+). Sin cgroups v2 no se
  puede limitar la memoria de un programa con todos sus hilos ni dar el
  veredicto "Memory limit exceeded".
- **isolate** 2.0 o posterior (probado con 2.7) y su servicio
  `isolate.service` activo.
- Los compiladores de los lenguajes que se usen.
- El worker corre como root (isolate cambia a sus propios usuarios de caja).

### Preparar una máquina (Ubuntu 24.04)

```sh
# isolate, desde el repositorio de sus autores
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://www.ucw.cz/isolate/debian/signing-key.asc | sudo tee /etc/apt/keyrings/isolate.asc >/dev/null
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/isolate.asc] http://www.ucw.cz/isolate/debian/ noble-isolate main" \
  | sudo tee /etc/apt/sources.list.d/isolate.list
sudo apt-get update
sudo apt-get install -y isolate
sudo systemctl enable --now isolate.service

# compiladores de todos los lenguajes incluidos
sudo apt-get install -y --no-install-recommends g++ gcc python3 pypy3 default-jdk-headless \
  fp-compiler ghc mono-mcs mono-runtime rustc golang-go php-cli kotlin

# Go: biblioteca estándar precompilada (sin ella una compilación supera el
# límite de 10 s de CPU). Repetir después de cada actualización de Go.
sudo env CGO_ENABLED=0 GODEBUG=installgoroot=all /usr/bin/go install -pkgdir=/var/lib/cms/go-std std
```

Después de actualizar un compilador, reiniciar los workers: la caché de
compilación identifica cada compilador por el hash de su binario, calculado
al arrancar.

### Verificar el host

```sh
sudo cms ctl verify-host
```

Revisa cgroups v2 y el cgroup de isolate, la versión y la configuración de
isolate, que el rango de cajas del worker exista y no lo use otro proceso,
seccomp, la protección de hard links, swap, SMT y la frecuencia de la CPU, y
ejecuta un programa real en una caja (también bajo el filtro seccomp).

- **FAIL**: el juez no es confiable o no funciona; corregirlo antes del
  concurso. El comando termina con error.
- **WARN**: funciona, pero los tiempos medidos tendrán más ruido. Para un
  concurso: desactivar SMT (`echo off > /sys/devices/system/cpu/smt/control`),
  fijar la frecuencia (governor `performance`) y desactivar swap
  (`swapoff -a`). `isolate-check-environment --execute` aplica varios de
  estos ajustes.

## Configuración

Sección `worker` de `cms.yaml` (o variables `CMS_WORKER_*`):

| Ajuste | Qué hace |
|---|---|
| `name` | nombre en la página de jueces; vacío = nombre del host. Un solo proceso por nombre |
| `concurrency` | trabajos en paralelo: como mucho uno por núcleo **físico** reservado para juzgar |
| `box_id_base`, `box_count` | cajas de isolate `[base, base + count)`; los workers de una misma máquina necesitan rangos disjuntos. Al menos 2 por trabajo en paralelo; Communication usa una por proceso más una, Interactive el límite de procesos más una |
| `work_dir` | directorio de las cajas (se convierte a ruta absoluta) |
| `seccomp` | filtro seccomp para los programas de los concursantes (recomendado) |
| `shutdown_timeout` | al apagarse, cuánto espera a que terminen los trabajos en curso; los demás vuelven a la cola |
| `job_timeout` | tope de un trabajo trabado por una falla (el sandbox aplica los límites reales) |
| `compilation_cache_ttl` | cuánto se recuerda una compilación exitosa; 0 la desactiva |

`sandbox.cgroups: false` es solo para máquinas de desarrollo sin cgroups v2:
la memoria se limita por proceso como espacio de direcciones y exceder el
límite se ve como un error de ejecución.

Un juez en otra máquina no necesita `secret_key` ni acceso a la base:
configurar `storage.backend: remote` con la URL del servidor de blobs del
dispatcher y el token que imprime `cms ctl blob-token` (ver
[Configuración](configuracion.md)).

## Con Docker

La imagen del worker (`deploy/docker/Dockerfile.worker`) trae isolate, los
compiladores y la biblioteca estándar de Go:

```sh
make build
docker build -f deploy/docker/Dockerfile.worker -t cms-worker .
docker run --privileged --cgroupns=private --env-file cms.env cms-worker worker
```

Necesita un contenedor privilegiado con su propio espacio de cgroups; su
entrypoint hace dentro del contenedor lo que `isolate.service` hace en el
host. `docker run … cms-worker ctl verify-host` verifica el contenedor.

## Seguridad

- Los programas de los concursantes corren con un filtro seccomp: `ptrace`,
  `mount`, espacios de nombres, `bpf`, `keyctl`, `io_uring`, carga de
  módulos y otras llamadas terminan el programa con **Security violation**;
  los sockets de red fallan (no hay red en la caja).
- Las cajas no reciben el entorno del worker (puede tener secretos) y los
  compiladores no ven `/etc`: cada lenguaje declara qué directorios del host
  necesita.
- Compiladores, checkers y managers corren en cajas, sin el filtro seccomp.

## Lenguajes

Incluidos (con los nombres exactos de CMS): C11 / gcc, C++11/14/17/20 / g++,
C# / Mono, Haskell / ghc, Java / JDK, Pascal / fpc, PHP, Python 3 / CPython,
Python 3 / PyPy, Rust, y además Go y Kotlin / JDK.

Cada lenguaje es un archivo YAML (`config/languages/*.yaml`, incluidos en el
binario). Para agregar uno o cambiar los flags de uno existente, poner
archivos en el directorio de `languages_dir`: un archivo con el mismo `name`
reemplaza al incluido. El formato está descrito en
`internal/grading/language/doc.go`; un ejemplo:

```yaml
name: C++17 / g++
source_extensions: [.cpp, .cc, .cxx, .c++, .C]
header_extensions: [.h]
object_extensions: [.o]
compilation:
  commands:
    - [/usr/bin/g++, "{eval:-DEVAL}", -std=gnu++17, -O2, -pipe, -static, -s, -o, "{executable}", "{sources}"]
evaluation:
  commands:
    - ["./{executable}", "{args}"]
```

El nombre identifica al lenguaje en envíos y concursos: no cambiarlo una vez
que haya envíos.

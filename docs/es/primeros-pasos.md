# Primeros pasos: instalar y usar CMS

*English: [../en/getting-started.md](../en/getting-started.md).*

Guía corta para tener un concurso funcionando en un servidor con
**Ubuntu 26.04 LTS** (también sirve para 22.04, 24.04 y Debian 12). Los
detalles de cada paso están en las guías enlazadas.

## 1. Qué necesitas

- Un servidor con Ubuntu limpio, acceso `sudo` y al menos 2 vCPU y 4 GB de
  RAM ([dimensionamiento](instalacion.md)).
- Una de dos:
  - **un dominio** (recomendado): tres registros DNS que apunten al
    servidor, por ejemplo `concurso.ejemplo.org`, `admin.concurso.ejemplo.org`
    y `ranking.concurso.ejemplo.org`; los certificados HTTPS se sacan solos;
  - **solo la IP**: los tres sitios quedan en HTTP en los puertos 8000
    (concursantes), 8001 (administración) y 8002 (scoreboard).

## 2. Compilar el programa

Mientras no haya una versión publicada en GitHub, el binario se compila en
el mismo servidor (tarda un par de minutos):

```sh
sudo apt-get update && sudo apt-get install -y git make curl
curl -fsSL https://go.dev/dl/go1.27.1.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH=/usr/local/go/bin:$PATH

git clone https://github.com/D4ND3R/Contest-Management-System-Enchanced.git cms
cd cms
make build            # deja el programa en bin/cms
```

(En un servidor ARM usa `go1.27.1.linux-arm64.tar.gz`.)

Si el repositorio es privado, `git clone` pide tu usuario de GitHub y, como
contraseña, un *personal access token* (o usa una clave SSH y la dirección
`git@github.com:D4ND3R/Contest-Management-System-Enchanced.git`).

## 3. Instalar

Desde la carpeta `cms`, con dominio:

```sh
sudo bash scripts/install.sh --binary bin/cms --domain concurso.ejemplo.org --email tu@correo.org
```

o solo con la IP:

```sh
sudo bash scripts/install.sh --binary bin/cms --http-ports 8000,8001,8002
```

El instalador pone PostgreSQL, Valkey, el sandbox `isolate`, los
compiladores, los servicios de `systemd`, el cortafuegos y los respaldos
diarios. Al final muestra:

- las direcciones de los **tres sitios**;
- el usuario `admin` y su **contraseña**, que se muestra **una sola vez**:
  anótala en un lugar seguro.

Si algo falla, se puede volver a correr el mismo comando sin perder nada.
Opciones útiles: `--admin-allow 10.0.0.0/8` (solo esa red abre la
administración) y `--compilers basic` (solo C, C++, Python 3 y Java;
instala más rápido). Todas: `sudo bash scripts/install.sh --help`.

## 4. Comprobar que todo funciona

```sh
sudo cmsctl verify-host                    # el juez puede correr programas
systemctl status cms.target 'cms-*'        # todos los servicios "active"
```

Si `verify-host` reporta un problema, mira
[problemas frecuentes](problemas-frecuentes.md).

## 5. Preparar un concurso

Entra a la **administración** con `admin` y la contraseña del paso 3.

1. **Crear el concurso**: menú *Contests* → *(create new contest...)*. En la
   página del concurso pon el nombre, la descripción, los **horarios de
   inicio y fin** y los lenguajes permitidos; guarda con *Update*.
2. **Agregar problemas**, de una de dos formas:
   - **importar un paquete** (zip en formato propio, italy_yaml, Polygon o
     TPS): *Tasks* → *(create new task...)* → *Import a task package
     (zip)*; elige el concurso en *Add it to the contest*, revisa la vista
     previa y confirma ([importar](importar.md));
   - **crearlo desde el navegador**: enunciado, casos de prueba y
     soluciones ([crear un problema](crear-problema.md)).
   En la página del problema, *Test submissions* permite enviar las
   soluciones de ejemplo para comprobar que el juez da el puntaje esperado.
3. **Registrar a los concursantes**: *Users* → al final de la página,
   *Import users*. Sube un CSV como este:

   ```csv
   username,password,first_name,last_name,email
   ana01,clave-ana,Ana,Pérez,ana@ejemplo.org
   beto02,clave-beto,Alberto,Soto,
   ```

   y elige el concurso en *Also add them to contest*. Si una fila está mal no
   se importa nada y la página dice qué línea corregir. El correo es
   opcional. También se pueden crear uno por uno con
   *(create new user...)*.

También se puede importar un concurso completo (problemas y usuarios) por
consola: `sudo -u cms cmsctl import-contest CARPETA` ([importar](importar.md)).

## 6. Durante el concurso

- Los concursantes abren el **sitio del concursante**, entran con su
  usuario y contraseña, leen los enunciados, envían soluciones y ven los
  resultados en vivo ([sitio del concursante](sitio-concursante.md)).
- En la administración, dentro del concurso:
  - *Submissions*: todos los envíos y su evaluación;
  - *Questions*: responder las preguntas de los concursantes;
  - *Announcements*: avisos para todos;
  - *Ranking*: la tabla de posiciones completa;
  - *Judges* y *Resource usage*: estado del juez y del servidor.
- El **scoreboard** público se ve en la tercera dirección del paso 3; se
  puede hacer secreto o congelar al final ([scoreboard](scoreboard.md)).

## 7. Respaldos y actualizaciones

- Hay un **respaldo diario** automático en `/var/lib/cms/backups`. Para uno
  ahora (por ejemplo antes del concurso):
  `sudo systemctl start cms-backup.service`.
- **Actualizar** a una versión nueva del código:

  ```sh
  cd cms && git pull && make build
  sudo cmsctl upgrade --binary bin/cms
  ```

  Respalda, migra la base y, si algo falla, vuelve atrás solo
  ([actualización y respaldos](actualizacion-y-respaldos.md)).

## Más información

[Instalación](instalacion.md) (jueces en otras máquinas, desinstalar) ·
[Operación](operacion.md) · [Administración](administracion.md) ·
[Configuración](configuracion.md) · [Problemas frecuentes](problemas-frecuentes.md)

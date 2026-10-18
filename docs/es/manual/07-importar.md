# 7. Importar concursos, problemas y usuarios

*English: [Importing](../../en/manual/07-importing.md)*

Si tus problemas ya están preparados en un formato conocido, importarlos es
mucho más rápido y seguro que crearlos a mano. Este capítulo explica los
formatos, cómo importar desde la administración y desde la consola, cómo
actualizar un problema ya importado y los errores más frecuentes. La
referencia completa está en [../importar.md](../importar.md) y el formato
propio en [../formato-de-paquete.md](../formato-de-paquete.md).

## Contenido

1. [Qué se puede importar y desde dónde](#1-qué-se-puede-importar-y-desde-dónde)
2. [Los formatos](#2-los-formatos)
3. [El formato italiano (italy_yaml) por dentro](#3-el-formato-italiano-italy_yaml-por-dentro)
4. [Importar un problema desde la administración](#4-importar-un-problema-desde-la-administración)
5. [Importar un concurso completo desde la consola](#5-importar-un-concurso-completo-desde-la-consola)
6. [Revisar antes de importar](#6-revisar-antes-de-importar)
7. [Actualizar un problema o concurso ya importado](#7-actualizar-un-problema-o-concurso-ya-importado)
8. [Usuarios y equipos](#8-usuarios-y-equipos)
9. [Migrar desde el CMS original](#9-migrar-desde-el-cms-original)
10. [Los avisos al importar](#10-los-avisos-al-importar)
11. [Qué hacer si…](#11-qué-hacer-si)

---

## 1. Qué se puede importar y desde dónde

| Qué | Administración | Consola (`sudo cmsctl …`) |
|---|---|---|
| un problema (zip) | sí | `import-task` |
| un concurso con sus problemas y usuarios | no | `import-contest` |
| usuarios (CSV) | sí ([cap. 8](08-usuarios-y-equipos.md)) | — |
| usuarios de un paquete de concurso | no | `import-users` |
| equipos | sí, CSV | `import-team` |
| un dump del CMS original | no | `import-dump` |

La consola acepta **una carpeta o un archivo comprimido** (`.zip`,
`.tar.gz`, `.tgz` o `.tar`). Si el comprimido contiene una sola carpeta con
el concurso adentro, se usa esa carpeta.

Todas las importaciones **revisan primero todo** y no escriben nada si
encuentran un error: o se importa todo, o nada. Y avisan al juez para que
evalúe lo nuevo.

## 2. Los formatos

| Formato | Lo reconoce por | Trae |
|---|---|---|
| **italy_yaml** (el del CMS original) | `contest.yaml` (concurso) o `task.yaml` (problema) | concurso, problemas, usuarios, equipos |
| **Polygon** (Codeforces) | `contest.xml` y `problems/` (concurso) o `problem.xml` (problema) | concurso, problemas, usuarios |
| **TPS** | `problem.json` | problema |
| **Formato propio** (`problem_yaml`) | `problem.yaml` | problema; es el que produce *Descargar como paquete* |

El formato se detecta solo. Si no se reconoce, el mensaje dice qué archivo
busca cada formato:

```
cms: error: couldn't autodetect the loader, please specify it with -L: no loader of contests
accepted /var/tmp/x (loaders of contests: italy_yaml wants contest.yaml;
polygon wants contest.xml and problems/)
```

Para forzar un formato: `-L italy_yaml`, `-L polygon`, `-L tps` o
`-L problem_yaml`.

## 3. El formato italiano (italy_yaml) por dentro

Es el formato más común en olimpiadas que usan CMS. Un concurso es una
carpeta:

```
D4OI2026/
├── contest.yaml          ← el concurso
├── baches/               ← un problema por carpeta
│   ├── task.yaml         ← datos del problema
│   ├── statement/
│   │   └── statement.pdf ← enunciado
│   ├── input/            ← input0.txt, input1.txt…
│   ├── output/           ← output0.txt, output1.txt…
│   ├── sol/              ← grader.cpp, cabeceras, soluciones
│   ├── check/            ← checker (opcional)
│   ├── gen/GEN           ← subtareas (opcional)
│   └── att/              ← adjuntos (opcional)
├── estalagmitas/
└── xibalba/
```

`contest.yaml` (ejemplo):

```yaml
name: D4OI2026              # nombre corto
description: Olimpiada D4 2026
start: 1767225600           # inicio (segundos Unix, UTC)
stop: 1767243600            # fin
languages:                  # lenguajes permitidos
  - "C++17 / g++"
tasks:                      # carpetas de los problemas, en orden
  - baches
  - estalagmitas
users:                      # opcional
  - username: ana01
    first_name: Ana
    last_name: Pérez
    password: clave-ana
```

`task.yaml` (ejemplo):

```yaml
name: baches
title: Baches
time_limit: 1.0
memory_limit: 256
n_input: 25            # cantidad de casos
public_testcases: "0,1"
infile: ""             # vacío = entrada estándar
outfile: ""
score_precision: 0
```

> **Ojo con `languages`**: si `contest.yaml` no la trae, el concurso
> permite los lenguajes por defecto (C, C++ y Pascal). Si tus problemas
> sólo tienen `grader.cpp`, agrega `languages: ["C++17 / g++"]` o
> corrígelo después en la página del concurso ([sección 10](#10-los-avisos-al-importar)).

## 4. Importar un problema desde la administración

1. *Problemas* → *(crear un problema...)* → *Importar un paquete de
   problema (zip)* (*Import a task package (zip)*).
2. Elige el zip y, si quieres, el formato (*detect* lo reconoce solo).
3. Marca *Update the task if it exists* si es una versión nueva de un
   problema que ya está.
4. En *Agregarlo al concurso:* (*Add it to the contest:*) elige el concurso
   (opcional).
5. Pulsa *Vista previa* (*Preview*): muestra qué se va a crear, los avisos
   y los errores. **Nada se guarda todavía.**
6. Si está bien, confirma.

## 5. Importar un concurso completo desde la consola

Para concursos enteros (con problemas y usuarios) se usa la consola del
servidor. Sube el zip al servidor (por ejemplo con `scp D4OI2026.zip
usuario@servidor:`) y:

```sh
sudo cmsctl import-contest -i --users D4OI2026.zip
```

- `-i`: importa también los problemas que no existen todavía.
- `--users`: crea los usuarios y equipos que trae `contest.yaml`. Sin esta
  opción, los usuarios deben existir de antes.
- Funciona igual con una carpeta: `sudo cmsctl import-contest -i --users
  ~/D4OI2026`.

Salida típica:

```
created contest D4OI2026 (id 1): 1 group
created task baches (id 1): 1 statement; 7 attachments; 1 dataset; 25 testcases
created task estalagmitas (id 2): 1 statement; 3 attachments; 1 dataset; 36 testcases
the dispatcher was notified
```

Después, en la administración: revisa el concurso (horario, lenguajes) y
prueba cada problema con *Envíos de prueba* ([cap. 5](05-problemas.md#13-probar-el-problema-con-soluciones)).

> **Siempre con `sudo`.** `cmsctl` necesita leer `/etc/cms/cms.yaml`, que
> sólo pueden leer root y el grupo `cms`. Sin `sudo` verás un error
> «cannot read /etc/cms/cms.yaml: permission denied: run the command with
> sudo». Con `sudo`, los archivos guardados quedan a nombre del usuario
> `cms`, como debe ser.

## 6. Revisar antes de importar

```sh
sudo cmsctl validate-contest D4OI2026.zip     # sin tocar la base de datos
sudo cmsctl import-contest -i --users --dry-run D4OI2026.zip   # con la base, sin escribir
```

`--dry-run` muestra exactamente qué se crearía o cambiaría y termina con
«dry run: nothing was written».

## 7. Actualizar un problema o concurso ya importado

| Quiero | Comando |
|---|---|
| actualizar los problemas de un concurso existente sin perder envíos | `sudo cmsctl import-contest -U D4OI2026.zip` |
| actualizar la configuración y grupos del concurso | `sudo cmsctl import-contest -u D4OI2026.zip` |
| actualizar un problema | `sudo cmsctl import-task -u baches.zip` |
| importar un problema y agregarlo al final de un concurso | `sudo cmsctl import-task -c D4OI2026 nuevo.zip` |
| agregar casos corregidos como dataset nuevo, inactivo | `sudo cmsctl import-dataset -d "casos corregidos" baches.zip` |

Los envíos y el dataset activo se conservan. Si cambiaron los casos del
dataset activo, reevalúa ([cap. 9](09-durante-el-concurso.md)).

## 8. Usuarios y equipos

Lo más simple es el CSV desde la administración
([cap. 8](08-usuarios-y-equipos.md)). Desde un paquete de concurso:

```sh
sudo cmsctl import-users -c D4OI2026 D4OI2026.zip
sudo cmsctl import-team D4OI2026.zip
```

## 9. Migrar desde el CMS original

Si tienes concursos en una instalación del CMS original, expórtalos allí
con `cmsDumpExporter` e impórtalos aquí con `sudo cmsctl import-dump`
(conserva envíos, resultados, preguntas…). Detalles en
[../importar.md](../importar.md#migrar-desde-cms-dumps).

## 10. Los avisos al importar

Los avisos (`warning:`) no impiden importar, pero conviene leerlos:

```
warning: /var/tmp/x/baches/sol: grader only for C++11 / g++, C++14 / g++, C++17 / g++, C++20 / g++:
submissions in other languages cannot be judged
warning: task baches: dataset "Default": no grader.%l for C11 / gcc, Pascal / fpc: submissions in these
languages cannot be judged; allow only the languages the task supports (the contest's languages in the
admin site, or "languages:" in contest.yaml) or add the missing grader files
```

Significa: el problema sólo trae grader para C++, pero el concurso permite
C y Pascal. Un envío en C o Pascal fallaría al compilar. Solución: en la
página del concurso, deja permitidos sólo los lenguajes de C++
([cap. 4](04-concursos.md)), o agrega `languages:` a `contest.yaml` antes
de importar.

## 11. Qué hacer si…

**…«couldn't autodetect the loader».** El zip no tiene `contest.yaml`
(ni `contest.xml`) en la raíz ni dentro de una única carpeta. Revisa su
contenido con `unzip -l archivo.zip`. Si es un solo problema, usa
`import-task` en vez de `import-contest`.

**…«password authentication failed for user "cms"».** Corriste el comando
sin `sudo`. Usa `sudo cmsctl …`.

**…«already exists in the database».** Ya se importó: usa `-u`/`-U` para
actualizarlo, o bórralo antes en la administración.

**…«users not found in the database».** El concurso trae participantes que
no existen: agrega `--users` o impórtalos antes.

**…el concurso aparece pero sin horario o con lenguajes de más.** Corrígelo
en la página del concurso ([cap. 4](04-concursos.md)).

---

**Siguiente:** [8. Usuarios y equipos](08-usuarios-y-equipos.md) ·
**Anterior:** [6. Tipos de problema y de puntaje](06-tipos-de-problema-y-puntaje.md) ·
[Índice](README.md)

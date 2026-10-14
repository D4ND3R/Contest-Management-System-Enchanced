# Importar concursos, problemas y dumps de CMS

*English: [../en/importing.md](../en/importing.md).*

Los comandos `cms ctl import-*` reemplazan a `cmsImportContest`,
`cmsImportTask`, `cmsImportUser`, `cmsImportTeam`, `cmsImportDataset` y
`cmsDumpImporter` de CMS, con las mismas opciones. Necesitan la
configuración de la base y del almacén de blobs (el mismo `cms.yaml` que
los servicios).

Todas las importaciones:

- **validan primero** y no escriben nada si hay errores (tipo de problema o
  de puntaje desconocido, parámetros inválidos, lenguaje desconocido,
  managers que faltan, archivos que no existen…);
- guardan los archivos en el almacén de blobs y escriben todas las filas en
  **una transacción**: o se importa todo o nada;
- nunca escriben en el directorio del paquete;
- aceptan `--dry-run`: revisan todo y muestran qué cambiaría, sin escribir;
- avisan al dispatcher para que juzgue lo nuevo.

## Formatos

| Formato | `-L` | Qué trae |
|---|---|---|
| italy_yaml (CMS) | `italy_yaml` | concurso (`contest.yaml`), tarea (`task.yaml`, `gen/GEN`), usuarios, equipos |
| Polygon | `polygon` (`polygon_task`, `polygon_contest`, `polygon_user`) | tarea (`problem.xml`, `cms_conf.py`), concurso (`contest.xml`), usuarios (`contestants.txt`) |
| TPS | `tps` (`tps_task`) | tarea (`problem.json`) |
| Formato propio | `problem_yaml` (`package`) | tarea (`problem.yaml`), directorio o zip; ver [Formato de paquete](formato-de-paquete.md) |

Sin `-L` el formato se detecta solo; si más de uno reconoce el directorio,
hay que elegirlo con `-L`.

Los checkers y managers que vienen como fuente C++ (Polygon, TPS, formato
propio) **se compilan al importar** con las opciones de CMS (Polygon usa el
`testlib.h` parcheado de CMS). La máquina que importa necesita `g++` y la
misma arquitectura que los jueces. `cms_conf.py` de Polygon se lee como
literales de Python y nunca se ejecuta.

## Comandos

```sh
# revisar un problema sin base de datos
cms ctl validate-task ruta/al/problema
cms ctl validate-contest ruta/al/concurso

# importar un concurso con sus tareas (-i) y sus usuarios
cms ctl import-contest -i --users ruta/al/concurso

# actualizar las tareas de un concurso existente sin perder envíos
cms ctl import-contest -U ruta/al/concurso

# importar una tarea y agregarla al final de un concurso
cms ctl import-task -c miconcurso ruta/al/problema

# actualizar una tarea (los envíos se conservan)
cms ctl import-task -u ruta/al/problema

# agregar los datos de juzgamiento como un dataset nuevo, inactivo
cms ctl import-dataset -d "casos corregidos" ruta/al/problema

# usuarios y equipos
cms ctl import-users -c miconcurso ruta/al/concurso
cms ctl import-team ruta/al/concurso

# exportar una tarea al formato propio
cms ctl export-task suma suma.zip
```

`cms ctl help <comando>` explica cada opción.

### Actualizar una tarea

Como en CMS, un dataset con la descripción de uno existente se actualiza en
su lugar (sus casos por nombre: un caso que cambió conserva sus
evaluaciones) y un dataset con una descripción nueva se agrega **inactivo**.
El dataset activo y los envíos se conservan. Si cambiaron los datos de
juzgamiento del dataset activo, reevaluarlo:

```sh
cms ctl reevaluate --dataset ID_DEL_DATASET --level evaluation
```

CMS usa archivos `.itime` dentro del paquete para saber qué cambió; aquí se
compara el paquete con la base.

## Migrar desde CMS (dumps)

En la instalación de CMS:

```sh
cmsDumpExporter -c ID_DEL_CONCURSO concurso.tar.gz
```

En esta:

```sh
cms ctl import-dump --dry-run concurso.tar.gz   # revisar
cms ctl import-dump concurso.tar.gz
```

- Se aceptan directorios, zip y tar (`.tar`, `.tar.gz`, `.tar.bz2`) de las
  versiones 14 (CMS 1.2) a 49 del modelo; los más viejos se actualizan
  primero con `cmsDumpUpdater` de CMS.
- Se importan concursos, grupos, tareas, datasets, usuarios, equipos,
  participaciones, anuncios, preguntas, mensajes, envíos con tokens,
  resultados y evaluaciones, y pruebas de usuario. Los puntajes se calculan
  con los resultados del dump y el dispatcher juzga lo que falte.
- Cada archivo se verifica contra su nombre (SHA-1) y se guarda por
  SHA-256. Un dump exportado **sin archivos** (`cmsDumpExporter -F`) no se
  puede importar: exportarlo de nuevo sin `-F`.
- Un dump exportado con `-c` incluye también los otros concursos en los que
  participaron sus concursantes, pero sin sus archivos: esas tareas y
  envíos se importan con los archivos marcados como borrados y un aviso.
  Para conservarlos, exportar todos los concursos necesarios con `-c`.
- Si la base ya tiene concursos, tareas, usuarios o equipos con los mismos
  nombres, el dump se rechaza. Opciones:
  - `-d`: borra antes los concursos, tareas, usuarios y equipos de la base
    (no los admins ni los ajustes del servidor), como `cmsDumpImporter -d`;
  - `--reuse-users`: usa los usuarios y equipos existentes (para importar,
    uno tras otro, dumps de concursos con los mismos concursantes).
- `--dry-run -d` no borra nada ni bloquea a nadie: revisa el dump contra
  copias vacías de las tablas.
- `-G` (sin resultados: se juzga todo de nuevo), `-S` (sin envíos), `-U`
  (sin pruebas de usuario), `-X` (sin usuarios ni lo que depende de ellos),
  como en `cmsDumpImporter`.
- Los admins aparecen en un dump solo como autores de anuncios y
  respuestas: se buscan por nombre de usuario y los que faltan se crean
  deshabilitados.

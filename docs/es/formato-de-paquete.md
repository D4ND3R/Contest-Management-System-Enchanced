# Formato de paquete de problemas

*English: [../en/package-format.md](../en/package-format.md).*

Esta instalación tiene un formato propio para **un problema**: un archivo
`problem.yaml` que dice en palabras simples cómo se juzga, y los archivos
que lo acompañan. Un paquete es un **directorio** o un **archivo zip**, y
se puede:

- revisar sin base de datos: `cms ctl validate-task PAQUETE`;
- importar: `cms ctl import-task PAQUETE` (el formato se reconoce por su
  `problem.yaml`; `-L problem_yaml` o `-L package` lo nombran);
- generar a partir de un problema que ya está en la base:
  `cms ctl export-task PROBLEMA DESTINO` (ver [Exportar](#exportar)).

Los formatos de CMS (italy_yaml, Polygon, TPS) se siguen leyendo tal como
son; ver [Paquetes de CMS](#paquetes-de-cms).

## Estructura

```
sum/
├── problem.yaml             el problema (obligatorio)
├── statement/
│   ├── en.pdf               un enunciado por idioma, PDF o Markdown
│   └── es.md
├── tests/
│   ├── 01.in                la entrada del caso 01
│   ├── 01.out               su salida esperada
│   └── …
├── graders/                 archivos que necesitan los jueces (opcional)
│   ├── grader.cpp           grader para C++, uno por lenguaje
│   ├── grader.py
│   ├── sum.h                cabeceras que incluye el código de los concursantes
│   └── checker.cpp          el checker (se compila al importar), o su binario "checker"
├── attachments/             archivos para los concursantes (opcional)
│   └── ejemplos.zip
└── solutions/               soluciones con su veredicto esperado (opcional, recomendado)
    ├── correct.ac.cpp
    ├── slow.tle.py
    ├── half.pa.cpp
    └── wrong.wa.c
```

Los archivos y directorios cuyo nombre empieza con punto (`.gitkeep`,
`.DS_Store`) se ignoran en todas partes. Los demás archivos inesperados se
informan como advertencias.

## problem.yaml

Solo `name` y `title` son obligatorios; todo lo demás tiene el valor por
defecto de CMS. Una clave que el formato no conoce es un **error** (una
errata no debe pasar inadvertida), y una clave que no corresponde al tipo
de problema es una advertencia.

```yaml
name: sum                  # letras, dígitos, _ y -
title: A más B
time_limit: 1.5            # segundos
memory_limit: 256          # MiB
input: ""                  # "" = entrada estándar (por defecto)
output: ""                 # "" = salida estándar (por defecto)
checker: whitediff         # whitediff | exact | float | comparator | testlib
scoring:
  type: GroupMin
  subtasks:
    - points: 30
      tests: 01-02
    - points: 70
      tests: 03-10
public_tests: 01, 02       # resultado visible sin usar un token
languages: [C11 / gcc, C++20 / g++, Python 3 / CPython]
feedback_level: full
```

| Clave | Significado | Por defecto |
|---|---|---|
| `name` | nombre corto del problema (también el del archivo a enviar: `sum.%l`) | obligatorio |
| `title` | título completo | obligatorio |
| `type` | tipo de problema: `batch`, `batch_and_output`, `output_only`, `communication`, `interactive`, `two_steps` (también sirven los nombres de CMS, como `OutputOnly`) | `batch` |
| `time_limit` | segundos (`output_only` no lo usa) | ninguno |
| `memory_limit` | MiB | ninguno |
| `input`, `output` | archivos que el programa lee y escribe (tipos batch); vacío es la entrada o salida estándar | `""` |
| `checker` | cómo se comparan las salidas (tipos que comparan salidas); ver [Checkers](#checkers) | `whitediff` |
| `grader` | compilar el código del concursante con `graders/grader.<ext>` | `true` si `graders/` tiene un grader |
| `scoring` | tipo de puntaje; ver [Puntaje](#puntaje) | 100 puntos repartidos por igual entre los casos |
| `public_tests` | casos cuyo resultado todos ven; ver [Elegir casos](#elegir-casos) | ninguno |
| `submission_format` | archivos de un envío | `[<name>.%l]`, o las salidas de `output_only` |
| `languages` | lenguajes permitidos en este problema, con los nombres de CMS | todos los del concurso |
| `primary_statements` | idiomas de enunciado destacados para todos | el único enunciado, si hay uno solo |
| `feedback_level` | `full`, `restricted` u `oi_restricted` | `restricted` |
| `score_mode` | `max_tokened_last`, `max` o `max_subtask` | `max_tokened_last` |
| `score_precision` | decimales de los puntajes | `0` |
| `max_submissions`, `max_user_tests` | como máximo tantos por concursante | sin límite |
| `min_submission_interval`, `min_user_test_interval` | segundos entre dos de ellos | ninguno |
| `tokens` | `mode` (`disabled`, `finite`, `infinite`), `max_number`, `min_interval` (s), `gen_initial`, `gen_number`, `gen_interval` (s), `gen_max` | `disabled`; 2, 2, 1800 |
| `dataset` | descripción del dataset que se crea | `Default` |
| `autojudge` | juzgar este dataset en segundo plano cuando no es el activo | `false` |

Claves de algunos tipos:

| Clave | Tipo | Significado | Por defecto |
|---|---|---|---|
| `output_only_tests` | `batch_and_output` | casos que se responden solo con un archivo de salida | ninguno |
| `output_tests` | `batch_and_output` | casos que se pueden responder con un archivo de salida o con el programa | ninguno |
| `attach_inputs` | `output_only`, `batch_and_output` | dar las entradas de esos casos como adjuntos `input_<caso>.txt` | `true` |
| `processes` | `communication` | cantidad de procesos del concursante | `1` |
| `user_io` | `communication` | `std_io` o `fifo_io` | `fifo_io` con grader, `std_io` sin él |
| `process_limit` | `interactive` | instancias de la solución que el controlador puede iniciar | `1` |
| `concurrent` | `interactive` | las instancias corren a la vez | `true` |
| `controller_memory_limit` | `interactive` | MiB | obligatorio |
| `controller_time_limit`, `controller_wall_time_limit` | `interactive` | segundos | obligatorio |

`batch` con `output_only_tests` u `output_tests` es `batch_and_output`.
`two_steps` necesita `submission_format` (por ejemplo
`[encoder.%l, decoder.%l]`).

### Tipos de problema y los archivos que necesitan

| Tipo | Qué ocurre | En `graders/` |
|---|---|---|
| `batch` | el programa lee la entrada y escribe la salida | `grader.<ext>` con `grader: true`; un checker para `comparator`/`testlib` |
| `batch_and_output` | como `batch`; algunos casos se responden con `output_<caso>.txt` | como `batch` |
| `output_only` | los concursantes envían archivos `output_<caso>.txt` | un checker para `comparator`/`testlib` |
| `communication` | los procesos del concursante conversan con `manager` | `manager` (o `manager.cpp`) |
| `interactive` | `controller` inicia instancias de la solución y conversa con ellas | `controller` (o `controller.cpp`) |
| `two_steps` | dos programas unidos por un tubo mediante `manager.<ext>` | `manager.<ext>` (con sus cabeceras) |

## Casos de prueba

Cada caso es `tests/<nombre>.in` con `tests/<nombre>.out` (CMS guarda una
salida esperada para cada caso; `communication` e `interactive` le dan a
su manager o controlador solo la entrada, así que ahí el `.out` puede
estar vacío). Los nombres usan letras, dígitos, `_` y `-`; los casos se
ordenan por nombre (orden de bytes: `10` va antes que `2`, así que
conviene numerarlos con ceros a la izquierda: `01`, `02`, … `10`). Una
entrada sin su salida, o una salida sin su entrada, es un error.

### Elegir casos

`public_tests`, `output_only_tests`, `output_tests` y los `tests` de una
subtarea nombran casos de la misma manera:

- `all` (todos);
- nombres: `sample, big-1`;
- números: `7` es el caso llamado `7`, o si no el único cuyo nombre es `7`
  con ceros a la izquierda (`07`, `007`); si coinciden dos, hay que usar
  el nombre;
- rangos de números: `3-7`, `01-10`;
- cualquier combinación, separada por comas o como lista YAML:
  `[1, 3-5, sample]`.

Un número, rango o nombre que no corresponde a ningún caso es un error.

## Checkers

| `checker` | Comparación |
|---|---|
| `whitediff` (también `white_diff`, `diff`) | como CMS: iguales salvo diferencias de espacios |
| `exact` | byte a byte |
| `float` (también `real`) | palabra por palabra; números iguales con tolerancia 10⁻⁶, absoluta o relativa. Se acepta `{type: float, tolerance: 1e-6}`; otra tolerancia necesita un comparador |
| `comparator` | el programa `graders/checker` con el protocolo de CMS: recibe como argumentos la entrada, la salida esperada y la del concursante, escribe el puntaje (0 a 1) en su salida estándar y el mensaje en su salida de error |
| `testlib` | el programa `graders/checker` es un checker testlib común: argumentos entrada, salida del concursante, salida esperada; el código de salida 0 es correcto, 1, 2, 4 y 8 son incorrectos, 7 da los puntos escritos en stderr (`quitp`); 3 o cualquier otro código significa que el checker falló (un error del juez, no un veredicto) |

`checker`, `manager` y `controller` **se compilan al importar** cuando
`graders/` tiene solo su fuente C++ (`checker.cpp`, `.cc` o `.cxx`), con
`g++ -x c++ -std=gnu++20 -O2 -static` junto a las cabeceras de `graders/`:

- un `comparator` se compila con `-DCMS` y el `testlib.h` de CMS (el que
  usan los paquetes de Polygon), así un checker escrito con testlib habla
  el protocolo de CMS; reemplaza al `testlib.h` del paquete;
- un checker `testlib`, un manager y un controlador usan el `testlib.h`
  del paquete si lo tiene, o el de CMS.

La máquina que importa necesita `g++` y la arquitectura de los jueces (lo
que ellos ejecutan es el binario). Un ejecutable que ya está en `graders/`
(un binario, o un script que empieza con `#!`) se usa tal cual; un archivo
con uno de esos nombres que no es ninguna de las dos cosas se informa como
advertencia.

## Puntaje

Sin `scoring`, el tipo de puntaje es **Sum**: 100 puntos repartidos por
igual entre los casos.

```yaml
scoring:
  points: 50        # Sum: el total, repartido por igual
---
scoring:
  per_test: 10      # Sum: los puntos de cada caso
```

Los tipos por grupos usan subtareas. Una subtarea da sus puntos
multiplicados por el resultado mínimo de sus casos (`GroupMin`), por el
producto de sus resultados (`GroupMul`), o completos cuando todos los
resultados son mayores que 0 y no superan su umbral y si no nada
(`GroupThreshold`, para checkers que devuelven un costo):

```yaml
scoring:
  type: GroupMin           # GroupMin, GroupMul o GroupThreshold
  subtasks:
    - points: 20
      tests: 01-03         # o: regex: "0[1-3]"   o: count: 3
    - points: 80
      tests: 04-10
      always_show_tests: true
```

- Cada subtarea nombra sus casos con exactamente uno de `tests` (ver
  [Elegir casos](#elegir-casos)), `regex` (una expresión regular de Python
  que debe coincidir al comienzo del nombre; no se admiten *lookahead* ni
  referencias hacia atrás) o `count` (los N casos siguientes en orden de
  nombre, como en CMS); todas las subtareas usan el mismo.
- `GroupThreshold` necesita `threshold` en cada subtarea.
- Con retroalimentación restringida, los concursantes ven los casos de una
  subtarea fallida solo hasta el primero con el resultado más bajo;
  `always_show_tests: true` muestra todos.
- Los puntos deben sumar 100, o `scoring.points` si se indica (si no, es
  un error); sin `scoring.points`, otro total es una advertencia.
- Un caso que no está en ninguna subtarea no cuenta: una advertencia.
- Los puntos se guardan tal como se escriben: `40` es un entero y `40.0`
  un real, como en CMS.

## Graders, cabeceras y otros managers

Cada archivo de `graders/` se guarda como **manager** con su nombre,
salvo:

- `testlib.h`, que solo se usa para compilar;
- las fuentes de los programas (`checker.<ext>`, `controller.<ext>`, y
  `manager.<ext>` salvo en `two_steps`), que se compilan como se explicó
  arriba o quedan afuera.

Los graders se llaman `grader` más la primera extensión de fuente de cada
lenguaje: `grader.cpp` (todos los C++), `grader.c`, `grader.py` (ambos
Python), `grader.java`, `grader.pas`, `grader.cs`, `grader.hs`,
`grader.php`, `grader.rs`, `grader.go`, `grader.kt`. Un grader con otra
extensión (`grader.cc`) nunca se usa: una advertencia. `stub.<ext>` se
guarda como `grader.<ext>`, como en los paquetes italy_yaml (tener ambos
es un error). Las cabeceras son archivos como `sum.h` o `graderlib.pas`.
Con grader, un envío en un lenguaje sin su grader no se puede juzgar: un
problema sin grader para ninguno de sus lenguajes es un error, e
importarlo a un concurso advierte sobre los lenguajes del concurso que no
tienen uno.

## Enunciados y adjuntos

`statement/<idioma>.pdf` o `statement/<idioma>.md`, donde el idioma es un
código (`en`, `es`, `es_MX`, `zh_TW`) o su nombre en inglés
(`english.pdf`, `spanish.md`, como en italy_yaml). Dos enunciados en un
mismo idioma son un error; un problema sin enunciados es una advertencia.

Cada archivo de `attachments/` se entrega a los concursantes con su nombre
(los subdirectorios son un error: hay que comprimirlos). En `output_only`
y en los casos de salida de `batch_and_output`, la entrada de cada uno de
esos casos también se adjunta como `input_<caso>.txt`, salvo con
`attach_inputs: false` o si `attachments/` ya tiene un archivo con ese
nombre.

## Soluciones y veredictos esperados

Las soluciones se llaman `<nombre>.<veredicto>.<extensión>`; el veredicto
dice qué debería obtener la solución:

| Veredicto | Significado |
|---|---|
| `ac` | aceptada: puntaje completo |
| `pa` | parcial: algunos puntos, no todos |
| `wa` | respuesta incorrecta en algún caso |
| `tle` | tiempo límite excedido en algún caso |
| `mle` | memoria límite excedida en algún caso |
| `re` | error de ejecución en algún caso |
| `ce` | no compila |

Por ejemplo `correct.ac.cpp`, `brute.tle.py`, `greedy.wa.cpp`,
`half.pa.c`. El veredicto no distingue mayúsculas; el lenguaje sale de la
extensión. En los problemas `output_only` una solución es un directorio
`<nombre>.<veredicto>/` o un zip `<nombre>.<veredicto>.zip` con los
archivos `output_<caso>.txt`.

Las soluciones **no se importan** (CMS no tiene dónde guardarlas):
documentan el paquete y la validación las revisa. Advierte cuando no hay
soluciones, cuando ninguna debería ser aceptada, sobre un nombre sin
veredicto o con uno desconocido, un archivo de lenguaje desconocido y un
lenguaje que el problema no permite.

## Paquetes zip

Un paquete zip tiene `problem.yaml` en su raíz o dentro de su único
directorio de primer nivel (sirve `sum.zip` con `sum/problem.yaml`). Se
lee donde está: no se extrae nada, salvo las fuentes que se compilan al
importar, que van a un directorio temporal. Los miembros pueden estar
guardados sin comprimir o con deflate.

Antes de leer nada se revisa todo el archivo; la importación falla si un
miembro:

- podría salir del directorio donde se lo extraería («zip slip»): una
  ruta absoluta, un componente `..`, `.` o vacío, una barra invertida, una
  letra de unidad;
- es un enlace simbólico, o está cifrado;
- aparece dos veces, o es a la vez archivo y directorio.

Un archivo tiene como máximo 200 000 miembros y 16 GiB de contenido (según
lo que declara); cada miembro se compara con su tamaño declarado y su
CRC-32 mientras se lee, así un archivo que miente sobre sus tamaños falla
en lugar de llenar el disco. Los metadatos de macOS (`__MACOSX/`,
`.DS_Store`) se ignoran.

## Validación

`cms ctl validate-task PAQUETE` lee el paquete y lo revisa como lo haría
una importación, sin base de datos, y falla si hay errores (`--json`
imprime el reporte en JSON). La importación hace las mismas revisiones y
no escribe nada si hay un error.

Errores (el problema no se puede importar):

- `problem.yaml` ausente, inválido o con claves desconocidas; sin `name`
  o `title`; un nombre inválido;
- sin casos; un caso sin su salida o sin su entrada; un nombre de caso
  inválido;
- un checker, manager o controlador que el tipo necesita y falta o no
  compila; un grader necesario y ausente;
- un tipo, checker, tipo de puntaje o lenguaje desconocido; límites
  inválidos;
- subtareas cuyos puntos no suman `scoring.points`; un rango o nombre que
  no corresponde a ningún caso; un `count` que pasa del último caso;
  subtareas que mezclan `tests`, `regex` y `count`; una expresión regular
  que no se puede ejecutar;
- una clave que el tipo necesita y falta (`submission_format` de
  `two_steps`, los límites del controlador de `interactive`);
- un zip inseguro.

Advertencias (el problema se puede importar, pero conviene revisarlas):

- sin enunciado; sin límite de tiempo o de memoria; puntos de las
  subtareas que no suman 100; casos fuera de toda subtarea;
- sin soluciones, o ninguna que debería ser aceptada; soluciones sin
  veredicto, con veredicto o lenguaje desconocido, o en un lenguaje que
  el problema no permite;
- claves que no corresponden al tipo; archivos que se ignoran o se guardan
  sin usarse (`graders/checker` con `whitediff`, graders con
  `grader: false`, `grader.cc`); un programa que no parece ejecutable;
  lenguajes del concurso sin grader.

## Comandos

```sh
cms ctl validate-task sum/                 # revisar un paquete
cms ctl import-task sum.zip                # crear el problema
cms ctl import-task -c demo sum/           # … al final del concurso demo
cms ctl import-task -u sum/                # actualizarlo, conservando sus envíos
cms ctl import-task --dry-run -u sum/      # decir qué cambiaría, sin escribir nada
cms ctl import-dataset -d "v2" sum/        # agregar el paquete como un dataset nuevo
cms ctl export-task sum sum.zip            # exportar el dataset activo
cms ctl export-task -d "v2" sum sum-v2/    # exportar otro dataset a un directorio
```

Actualizar un problema (`-u`) sigue a `cmsImportTask` de CMS: su
configuración, enunciados y adjuntos pasan a ser los del paquete; un
dataset con la misma descripción se actualiza en su lugar (los casos por
nombre: un caso que cambió conserva sus evaluaciones, que
`cms ctl reevaluate --dataset` vuelve a juzgar), y una descripción nueva
agrega un dataset que no queda activo.

## Exportar

`cms ctl export-task PROBLEMA DESTINO` escribe el problema (nombre o id)
con su dataset activo, u otro indicado con `-d`, como paquete: un zip si
`DESTINO` termina en `.zip` (no debe existir), si no un directorio (no
debe existir o debe estar vacío; se escribe al lado y se renombra al
final). `problem.yaml` incluye solo lo que difiere de los valores por
defecto. Importar el paquete devuelve las mismas filas (sobre el mismo
problema, `import-task -u` informa que no cambió nada), sin importar de
qué formato se importó el problema: los problemas importados de
italy_yaml, Polygon o TPS se pueden trasladar así.

No se exportan: los envíos, los demás datasets (el comando los lista) ni
las soluciones. Un enunciado se escribe como `.pdf` si su contenido es un
PDF, si no como `.md`. Los managers que este formato no volvería a
importar (`testlib.h`, `checker.cpp`, `stub.cpp`…) se escriben y se
informan como advertencias. Los programas (`checker`, `manager`,
`controller`) se escriben como ejecutables.

## Paquetes de CMS

`cms ctl import-contest`, `import-task`, `import-users` e `import-team`
leen los formatos de CMS con las reglas de sus loaders (se reconocen solos,
o con `-L italy_yaml`, `-L polygon_task`, `-L tps_task`…; `cms ctl help
import-task` los lista).

El formato italy_yaml (`contest.yaml`, `task.yaml`, `gen/GEN`, `input/`,
`output/`, `sol/`, `check/` o `cor/`, `statement/` o `testo/`, `att/`) se
importa como lo hace `cmsImportContest -L italy_yaml`: las mismas claves
con sus nombres en italiano, los mismos valores por defecto y las mismas
deducciones (el tipo de problema según los archivos presentes, las
subtareas según las líneas `#ST:` de `gen/GEN`, el formato viejo de
tokens). Los problemas del concurso de demostración y de las pruebas se
importaron con el CMS original y dan las mismas filas. Diferencias:

- no se escribe nada en el paquete (CMS deja archivos `.itime` e
  `.import_error`): una actualización compara el paquete con la base;
- todos los problemas del paquete se informan juntos, como error o
  advertencia, donde CMS se detiene en el primero o se cae;
- se lee `analysis_start` (CMS lo ignora), y `analysis_end` se acepta
  como `analysis_stop`;
- las horas YAML sin zona horaria son UTC (CMS: la zona de la máquina que
  importa); también se pueden escribir como `"2026-01-01 10:00:00"`;
- un usuario sin contraseña no recibe ninguna en la participación, y una
  al azar cuando se crea (CMS se cae);
- los casos solo-salida de `BatchAndOutput` se guardan ordenados;
- los valores desconocidos de `feedback_level` y `score_mode` son
  advertencias (CMS los ignora en silencio);
- un problema que CMS podía guardar pero nunca juzgar (un lenguaje
  desconocido, parámetros inválidos del tipo de problema o de puntaje) se
  rechaza al importar.

Para pasar un problema de CMS a este formato, se lo importa y se lo
exporta:
`cms ctl import-task -L italy_yaml viejo/sum && cms ctl export-task sum sum/`.

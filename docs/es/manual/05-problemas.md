# 5. Problemas

*English: [Tasks](../../en/manual/05-tasks.md)*

Un **problema** (*task*) es lo que los concursantes resuelven: un
enunciado, archivos adjuntos y, por dentro, los casos de prueba y la forma
de evaluarlos. Este capítulo explica cada parte de la página de un problema,
los *datasets*, los casos de prueba y cómo comprobar que el problema evalúa
bien antes del concurso. Los tipos de problema y de puntaje tienen su propio
[capítulo 6](06-tipos-de-problema-y-puntaje.md); importar paquetes, el
[capítulo 7](07-importar.md).

## Contenido

1. [Dos caminos: importar o crear](#1-dos-caminos-importar-o-crear)
2. [Crear un problema en el navegador](#2-crear-un-problema-en-el-navegador)
3. [La página del problema](#3-la-página-del-problema)
4. [Datos del problema](#4-datos-del-problema)
5. [Enunciados y adjuntos](#5-enunciados-y-adjuntos)
6. [Formato de envío](#6-formato-de-envío)
7. [Envíos, pruebas y tokens del problema](#7-envíos-pruebas-y-tokens-del-problema)
8. [Puntaje y retroalimentación](#8-puntaje-y-retroalimentación)
9. [Datasets](#9-datasets)
10. [Límites, tipo de problema y tipo de puntaje](#10-límites-tipo-de-problema-y-tipo-de-puntaje)
11. [Managers: graders, checkers y otros archivos](#11-managers-graders-checkers-y-otros-archivos)
12. [Casos de prueba](#12-casos-de-prueba)
13. [Probar el problema con soluciones](#13-probar-el-problema-con-soluciones)
14. [Cambiar un problema durante el concurso](#14-cambiar-un-problema-durante-el-concurso)
15. [Eliminar un problema](#15-eliminar-un-problema)
16. [Ejemplo completo](#16-ejemplo-completo)
17. [Qué hacer si…](#17-qué-hacer-si)

---

## 1. Dos caminos: importar o crear

| | Importar un paquete | Crear en el navegador |
|---|---|---|
| Cuándo | ya tienes el problema preparado (Polygon, TPS, formato italiano de CMS, o el formato propio) | lo preparas desde cero |
| Cómo | [cap. 7](07-importar.md) | secciones 2 a 13 de este capítulo |
| Ventaja | un solo paso, repetible | no necesitas herramientas externas |

En ambos casos el resultado es el mismo: un problema con su dataset, que
puedes seguir editando en esta página.

## 2. Crear un problema en el navegador

1. Menú *Problemas* (*Tasks*) → *(crear un problema...)* (*(create new
   task...)*).
2. Escribe el **nombre corto** (sin espacios, por ejemplo `suma`) y pulsa el
   botón para crearlo. Se crea con un dataset vacío.
3. Se abre la página del problema. Desde ahí, el enlace *Preparación del
   problema* (*Problem preparation*) lleva a una página guiada que muestra
   *Qué falta* (*What is missing*) para que el problema pueda evaluarse
   (enunciado, casos, checker…) y permite escribir o generar casos. Está
   descrita paso a paso en [../crear-problema.md](../crear-problema.md).
4. Para que los concursantes lo vean, agrégalo a un concurso: en el
   concurso, página *Problemas* → elegir el problema → agregar
   ([cap. 4](04-concursos.md#10-los-problemas-del-concurso)).

## 3. La página del problema

Se abre desde el menú *Problemas* o desde la lista de problemas de un
concurso. Tiene dos grandes partes:

- **Arriba, la configuración del problema** (lo que ven los concursantes y
  las reglas de envío): datos, enunciados, adjuntos, formato de envío,
  tokens, puntaje. Se guarda con *Actualizar* (*Update*) al pie del
  formulario.
- **Abajo, los datasets**: cómo se evalúa (tipo de problema, límites,
  managers, casos de prueba, tipo de puntaje).

> **Importante:** *Actualizar* guarda sólo el formulario de arriba. Los
> archivos (enunciados, adjuntos, managers, casos) se suben con sus
> propios botones y se guardan al instante.

## 4. Datos del problema

| Campo | Qué es | Ejemplo |
|---|---|---|
| Nombre (*Name*) | identificador corto, único; aparece en las URL, en el scoreboard y en los nombres de archivo | `suma` |
| Título (*Title*) | el título que ven los concursantes | `Suma de dos números` |

Cambiar el nombre de un problema durante un concurso confunde a los
concursantes y al scoreboard: decide el nombre antes.

## 5. Enunciados y adjuntos

**Enunciados** (*Statements*): uno por idioma, en PDF (o el formato que
uses). Cada uno se identifica con un código de idioma (`es`, `en`, `es_CL`…).

1. En *Enunciados*, elige el código de idioma y el archivo, y súbelo.
2. Marca *¿Principal?* (*Primary?*) en los idiomas que deben destacarse para
   todos los concursantes (normalmente el idioma oficial del concurso).
3. Si subes otro archivo con el mismo idioma, reemplaza al anterior.

Cada concursante ve destacados los enunciados principales y los de sus
idiomas preferidos (campo del usuario, [cap. 8](08-usuarios-y-equipos.md)).

**Adjuntos** (*Attachments*): archivos que el concursante puede descargar
desde la página del problema: ejemplos de entrada y salida, un grader de
ejemplo, plantillas de código, una imagen. Usa *Agregar archivos* (*Add
files*). El nombre del archivo es el que verá el concursante.

## 6. Formato de envío

*Formato de envío* (*Submission format*) es la lista de archivos que el
concursante debe enviar. Para un problema normal es un único archivo:

```
suma.%l
```

`%l` se reemplaza por la extensión del lenguaje elegido (`suma.cpp`,
`suma.py`…). Algunos tipos de problema usan otros formatos (por ejemplo,
OutputOnly pide un archivo de salida por caso): ver el
[capítulo 6](06-tipos-de-problema-y-puntaje.md).

## 7. Envíos, pruebas y tokens del problema

Estos campos son iguales a los del concurso ([cap. 4](04-concursos.md)),
pero aplican sólo a este problema. Vacío significa «sin límite propio» (se
aplica el del concurso, si hay).

| Campo | Qué hace |
|---|---|
| *Lenguajes de programación permitidos* (*Allowed programming languages*) | restringe los lenguajes del concurso para este problema; vacío = todos los del concurso |
| *Cantidad máxima de envíos* (*Maximum number of submissions*) | cuántos envíos puede hacer cada concursante en este problema |
| *Cantidad máxima de pruebas de usuario* (*Maximum number of user tests*) | idem para las pruebas |
| *Intervalo mínimo entre envíos* (*Minimum interval between submissions*) | tiempo de espera obligatorio entre dos envíos (evita que se use el juez como depurador) |
| *Intervalo mínimo entre pruebas de usuario* | idem para pruebas |
| *Parámetros de los tokens* (*Tokens parameters*) | igual que en el concurso; el concursante debe cumplir las reglas del concurso **y** las del problema |

## 8. Puntaje y retroalimentación

**Nivel de retroalimentación** (*Feedback level*): cuánto ve el concursante
de cada envío.

| Opción | Qué ve |
|---|---|
| *Completo* (*Full*) | cada caso público con su resultado, tiempo y memoria |
| *Restringido* (*Restricted*) | en una subtarea no resuelta del todo, los casos públicos hasta el primero con peor resultado |
| *Restringido OI* (*OI Restricted*) | sólo ese caso |

Con *Completo* un concursante podría deducir datos de los casos (por
ejemplo, por el tiempo); en olimpiadas se suele usar *Restringido*.

**Opciones de puntaje** (*Score options*):

| Campo | Qué hace |
|---|---|
| *Decimales del puntaje* (*Score decimal places*) | con cuántos decimales se muestra el puntaje (0 = enteros) |
| *Modo de puntaje* (*Score mode*) | cómo se combinan varios envíos (abajo) |

Modos de puntaje:

| Opción | Puntaje del concursante en el problema | Uso típico |
|---|---|---|
| *Usar la suma, por subtarea, del mejor resultado de esa subtarea entre todos los envíos (IOI 2017-)* | para cada subtarea, el mejor resultado de cualquier envío; luego se suman | IOI actual y la mayoría de las olimpiadas |
| *Usar el mejor de todos los envíos (IOI 2013-2016)* | el mejor envío completo | concursos escolares |
| *Usar el mejor entre los envíos con ficha y el último (IOI 2010-2012)* | el mejor entre los envíos con token y el último | concursos con tokens |

## 9. Datasets

Un **dataset** es una versión completa de «cómo se evalúa» un problema: tipo
de problema, límites, managers, casos de prueba y tipo de puntaje. Un
problema puede tener varios; **uno es el activo** (*live*): el que da el
puntaje oficial.

¿Para qué tener varios? Para corregir un problema **durante el concurso**
sin perder nada: si descubres un caso de prueba incorrecto, clonas el
dataset, lo corriges, compruebas cómo cambian los resultados y recién
entonces lo activas. Todos los envíos se reevalúan con el nuevo y el
scoreboard se actualiza.

Acciones de cada dataset (enlaces junto a su nombre):

| Acción | Qué hace |
|---|---|
| *Clonar* (*Clone*) | crea una copia para modificarla; puedes copiar también los managers y los resultados de evaluación ya calculados |
| *Renombrar* (*Rename*) | cambia la descripción |
| *Activar ...* (*Make Live ...*) | lo vuelve el dataset activo; antes muestra qué puntajes cambiarían (con los resultados ya calculados) y permite avisar a los concursantes afectados con un mensaje |
| *Activar/Desactivar el juzgamiento en segundo plano* | si un dataset no activo evalúa los envíos en segundo plano (para comparar antes de activarlo) |
| *Ver resultados* (*View results*) | los resultados de los envíos con ese dataset |
| *Borrar* (*Delete*) | borra un dataset no activo |
| *Crear un dataset nuevo* (*Create a new dataset*) | uno vacío |

## 10. Límites, tipo de problema y tipo de puntaje

En cada dataset:

| Campo | Qué es | Ejemplo |
|---|---|---|
| *Límite de tiempo* (*Time limit*) | tiempo máximo de CPU por caso, en segundos | `1` |
| *Límite de memoria* (*Memory limit*) | memoria máxima por caso, en MiB | `256` |
| *Tipo de problema* (*Task type*) | cómo se compila y ejecuta (Batch, OutputOnly…) | `Batch` |
| *Configuración del tipo de problema* | sus parámetros | compilación sola, entrada estándar, comparación exacta |
| *Tipo de puntaje* (*Score Type*) | cómo se calcula el puntaje (Sum, GroupMin…) | `GroupMin` |
| *Parámetros del puntaje* (*Score Parameters*) | las subtareas y sus puntos | `[[30, 5], [70, 10]]` |

Todo esto se explica en detalle, con ejemplos, en el
[capítulo 6](06-tipos-de-problema-y-puntaje.md).

## 11. Managers: graders, checkers y otros archivos

Los **managers** (*Managers*) son archivos de los organizadores que
participan en la evaluación y el concursante no ve:

| Archivo | Para qué |
|---|---|
| `checker` | programa que decide si una salida es correcta (cuando no basta comparar exactamente) |
| `grader.cpp`, `grader.c`, `grader.py`… | programa principal con el que se compila la solución cuando el concursante sólo implementa funciones; **uno por lenguaje permitido** |
| `suma.h` y similares | cabeceras para compilar con el grader |
| `manager` | el programa de los organizadores en problemas Communication |

Se suben en la sección *Managers* del dataset. Los nombres importan:
consulta el [capítulo 6](06-tipos-de-problema-y-puntaje.md) para saber qué
nombre espera cada tipo de problema.

> **Error frecuente:** el concurso permite C, C++ y Pascal, pero el problema
> sólo tiene `grader.cpp`. Los envíos en C y Pascal no se pueden evaluar.
> Restringe los lenguajes del problema o del concurso a los que tienen
> grader, o agrega los graders que falten. `sudo cmsctl doctor` avisa de
> este problema.

## 12. Casos de prueba

Cada caso (*testcase*) tiene un **nombre** (*Codename*, por ejemplo `01` o
`sub1_03`), una **entrada**, una **salida correcta** y la marca **Público**
(*Public*): los casos públicos muestran su resultado al concursante (según
el nivel de retroalimentación).

**Agregar uno:** *Agregar un caso* (*Add a testcase*): nombre, archivo de
entrada y de salida.

**Agregar muchos de una vez** (lo habitual): *Agregar varios casos* (*Add
multiple testcases*).

1. Prepara un **zip que contenga sólo archivos** (sin carpetas), por
   ejemplo `input.01`, `output.01`, `input.02`, `output.02`…
2. Indica la *Plantilla de nombres de entrada* (por defecto `input.*`) y la
   de salida (`output.*`). El `*` es el nombre del caso. Si tus archivos se
   llaman `01.in` y `01.out`, usa `*.in` y `*.out`.
3. Marca *Público* si quieres que todos sean públicos, y *Reemplazar los
   casos existentes* (*Overwrite existing testcases*) si estás corrigiendo
   casos que ya existían.
4. Pulsa *Subir casos* (*Upload testcases*).

**Descargar:** *Descargar todos los casos* (*Download all testcases*) baja
un zip con todos.

Los nombres de los casos importan para las subtareas por expresión regular
([cap. 6](06-tipos-de-problema-y-puntaje.md)): elige nombres que agrupen,
como `s1_01`, `s1_02`, `s2_01`.

## 13. Probar el problema con soluciones

**Nunca uses un problema en un concurso sin probarlo.** La página *Envíos de
prueba* (*Test submissions*) del problema permite enviar soluciones como si
fueras un concursante, sin que cuenten para nadie:

1. Abre el problema → *Envíos de prueba*.
2. Sube una o varias soluciones (*Soluciones*). El veredicto esperado va
   como **segunda extensión** del nombre: `correcta.ac.cpp`,
   `lenta.tle.cpp`, `mala.wa.py`. Los códigos son `ac` (puntaje máximo),
   `pa` (parcial), `wa` (respuesta incorrecta en algún caso), `tle`
   (tiempo excedido), `mle` (memoria excedida), `re` (error en ejecución) y
   `ce` (no compila). Para archivos sin código se usa el *Veredicto
   esperado (para nombres sin uno)*.
3. Pulsa *Enviar* (*Submit*) y espera los resultados en la tabla: puntaje,
   resultado de cada caso y si coincide con lo esperado. La página de
   *Preparación del problema* marca «Every test submission gets its
   expected verdict» cuando todo coincide.

Qué probar como mínimo:

- una solución correcta → puntaje máximo;
- una solución lenta → límite de tiempo excedido en los casos grandes;
- una solución incorrecta → puntaje parcial o cero;
- si hay subtareas, una solución que resuelva sólo la primera → exactamente
  los puntos de esa subtarea.

Si un envío de prueba se queda en «Evaluando…» más de un minuto, el juez no
está funcionando: `sudo cmsctl doctor` ([cap. 14](14-solucion-de-problemas.md)).

## 14. Cambiar un problema durante el concurso

- **Corregir un caso de prueba:** *Clonar* el dataset → reemplazar el caso
  en la copia → comprobar con *Ver resultados* → *Activar ...*. Todos los
  envíos se reevalúan y el scoreboard se actualiza.
- **Cambiar el enunciado:** sube el archivo nuevo y publica un anuncio
  ([cap. 9](09-durante-el-concurso.md)) para que los concursantes lo
  sepan.
- **Cambiar límites de tiempo o memoria:** igual que corregir un caso
  (dataset nuevo), para poder comparar antes de activar.

## 15. Eliminar un problema

Desde la lista *Problemas*, eligiendo el problema y la opción de eliminar.
Se pide confirmación mostrando cuántos envíos se borrarán. **Borra el
problema con todos sus datasets y todos sus envíos**, sin vuelta atrás
(salvo restaurando un respaldo). Para sacarlo sólo de un concurso, quítalo
desde la página *Problemas* del concurso.

## 16. Ejemplo completo

Problema «Suma» (leer dos números, imprimir la suma), con dos subtareas:
números pequeños (30 puntos) y grandes (70 puntos).

1. *Problemas* → *(crear un problema...)* → nombre `suma`.
2. Título: `Suma de dos números`; *Formato de envío*: `suma.%l`;
   *Decimales del puntaje* 0; modo IOI 2017-. *Actualizar*.
3. Enunciado: sube `suma-es.pdf` con idioma `es` y márcalo principal.
4. Dataset: *Límite de tiempo* `1`, *Límite de memoria* `256`, tipo
   `Batch` con compilación sola, entrada y salida estándar y comparación
   exacta.
5. Casos: un zip con `s1_01.in`, `s1_01.out`, … `s1_05.out`, `s2_01.in` …
   `s2_10.out`; plantillas `*.in` y `*.out`; marca públicos los dos
   primeros a mano después.
6. Tipo de puntaje `GroupMin` con parámetros
   `[[30, "s1_.*"], [70, "s2_.*"]]` ([cap. 6](06-tipos-de-problema-y-puntaje.md)).
7. *Envíos de prueba*: `correcta.ac.cpp` (debe dar 100), `chica.pa.cpp`
   (sólo números pequeños: 30), `mala.wa.cpp` (0).
8. Agrégalo al concurso.

## 17. Qué hacer si…

**…los envíos dicen «Compilación fallida» y la solución compila en mi
computadora.** Mira *Salida de la compilación* en el detalle del envío.
Causas frecuentes: falta el grader o la cabecera del lenguaje, o el nombre
del archivo no coincide con el formato de envío.

**…todo da cero.** Revisa que las salidas correctas correspondan a las
entradas (nombres de caso iguales) y el tipo de comparación (espacios al
final, saltos de línea de Windows: usa un checker o la comparación que los
ignora, [cap. 6](06-tipos-de-problema-y-puntaje.md)).

**…el puntaje máximo no es el esperado.** Revisa los parámetros del tipo de
puntaje: las expresiones regulares deben abarcar todos los casos.

**…«no grader for C11 / gcc…» al importar.** Ver la sección 11.

---

**Siguiente:** [6. Tipos de problema y de puntaje](06-tipos-de-problema-y-puntaje.md) ·
**Anterior:** [4. Concursos](04-concursos.md) ·
[Índice](README.md)

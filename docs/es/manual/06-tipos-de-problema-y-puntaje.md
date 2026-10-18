# 6. Tipos de problema y de puntaje

*English: [Task and score types](../../en/manual/06-task-and-score-types.md)*

El **tipo de problema** dice cómo se compila y ejecuta una solución y cómo se
decide si cada caso está bien. El **tipo de puntaje** dice cómo se
convierten los resultados de los casos en puntos. Ambos se eligen en cada
dataset ([cap. 5](05-problemas.md#10-límites-tipo-de-problema-y-tipo-de-puntaje)).
Son los mismos del CMS original, con los mismos parámetros. La referencia
completa de archivos y formatos está en
[../formato-de-paquete.md](../formato-de-paquete.md).

## Contenido

1. [¿Qué tipo de problema necesito?](#1-qué-tipo-de-problema-necesito)
2. [Batch](#2-batch)
3. [Comparar salidas: diff o checker](#3-comparar-salidas-diff-o-checker)
4. [Problemas con grader (implementar funciones)](#4-problemas-con-grader-implementar-funciones)
5. [OutputOnly](#5-outputonly)
6. [BatchAndOutput](#6-batchandoutput)
7. [Communication](#7-communication)
8. [Interactive](#8-interactive)
9. [TwoSteps](#9-twosteps)
10. [Tipos de puntaje](#10-tipos-de-puntaje)
11. [Subtareas paso a paso](#11-subtareas-paso-a-paso)
12. [Lenguajes](#12-lenguajes)
13. [Errores comunes](#13-errores-comunes)

---

## 1. ¿Qué tipo de problema necesito?

| El concursante… | Tipo |
|---|---|
| escribe un programa completo que lee la entrada y escribe la salida | **Batch** |
| implementa funciones que llama un programa nuestro (grader) | **Batch** con grader |
| sólo entrega archivos de salida, sin programa | **OutputOnly** |
| puede entregar el programa o, en algunos casos, la salida directamente | **BatchAndOutput** |
| dialoga con un programa nuestro mientras corre (problema interactivo clásico de CMS) | **Communication** |
| dialoga con un controlador que lanza varias instancias de su programa | **Interactive** |
| escribe dos funciones que se ejecutan por separado (codificar/decodificar) | **TwoSteps** (obsoleto en CMS) |

El 90 % de los problemas de olimpiada son **Batch**, con o sin grader.

## 2. Batch

Un programa del concursante, compilado solo o con un grader, que lee la
entrada (de la entrada estándar o de un archivo) y escribe la salida (a la
salida estándar o a un archivo). Su salida se compara con la correcta.

Parámetros (en *Configuración del tipo de problema*):

| Parámetro | Opciones | Cuándo |
|---|---|---|
| *Compilación* (*Compilation*) | sola (`alone`) / con grader (`grader`) | con grader si el concursante sólo implementa funciones (sección 4) |
| *E/S (en blanco para stdin/stdout)* (*I/O*) | *Archivo de entrada* y *Archivo de salida* | vacíos = entrada y salida estándar (lo habitual); con nombres (`input.txt`, `output.txt`) el programa debe leer/escribir esos archivos |
| *Evaluación de la salida* (*Output evaluation*) | `diff` / `comparator` | sección 3 |

Archivos necesarios: los casos de prueba; `checker` si la evaluación es
con comparador; `grader.<ext>` por lenguaje si la compilación es con
grader.

Formato de envío: `nombre.%l` (por ejemplo `suma.%l`).

## 3. Comparar salidas: diff o checker

**`diff` — *Salidas comparadas ignorando espacios (white diff)*.** Compara
la salida del concursante con la correcta palabra por palabra (secuencias
de caracteres que no son espacios), ignorando cuántos espacios o
tabuladores hay entre ellas y las líneas vacías del final. Sirve cuando hay
una única respuesta correcta.

**`comparator` — *Las salidas las compara un comparador*.** Hace falta
cuando hay varias respuestas válidas (cualquier camino mínimo, números con
decimales, puntaje parcial…). Subes un programa llamado exactamente
`checker` (un ejecutable para Linux, compilado de forma estática; por
ejemplo con `g++ -O2 -static -o checker checker.cpp`) a los *Managers* del
dataset. CMS lo ejecuta así:

```
./checker entrada salida_correcta salida_del_concursante
```

y el checker debe:

- escribir en la **salida estándar** un número entre `0.0` y `1.0`: la
  fracción del puntaje del caso (1 = correcto);
- escribir en la **salida de error** un mensaje corto para el
  concursante. Los mensajes `translate:success`, `translate:wrong` y
  `translate:partial` se muestran traducidos («Respuesta correcta», etc.).

Ejemplo mínimo (acepta cualquier respuesta cuyo valor sea igual al
correcto con tolerancia 1e-6):

```cpp
#include <cstdio>
#include <cmath>
int main(int argc, char **argv) {
    FILE *ok = fopen(argv[2], "r"), *out = fopen(argv[3], "r");
    double a, b;
    if (fscanf(ok, "%lf", &a) != 1 || fscanf(out, "%lf", &b) != 1) {
        printf("0.0\n"); fprintf(stderr, "translate:wrong\n"); return 0;
    }
    if (fabs(a - b) <= 1e-6) { printf("1.0\n"); fprintf(stderr, "translate:success\n"); }
    else { printf("0.0\n"); fprintf(stderr, "translate:wrong\n"); }
    return 0;
}
```

> El checker corre en el sandbox del juez: no puede usar la red ni leer
> otros archivos. Debe terminar rápido aunque la salida del concursante sea
> enorme o basura: léela con cuidado.

## 4. Problemas con grader (implementar funciones)

El concursante implementa una o más funciones (por ejemplo
`long long suma(int a, int b)`) y un programa nuestro, el **grader**, lee
la entrada, llama a sus funciones e imprime el resultado.

1. En el dataset, *Compilación* = con grader.
2. Sube a *Managers* un grader por lenguaje permitido, con nombre
   `grader.<extensión>`: `grader.cpp`, `grader.c`, `grader.py`,
   `grader.java`, `grader.pas`… y las cabeceras que use
   (`suma.h`, etc.).
3. Para que los concursantes puedan probar en su computadora, sube como
   **adjuntos** (*Attachments*) un grader de ejemplo y la cabecera.
4. Restringe los lenguajes del problema a los que tienen grader
   ([cap. 5](05-problemas.md#7-envíos-pruebas-y-tokens-del-problema)).

Si falta el grader de un lenguaje permitido, los envíos en ese lenguaje no
compilan. Al importar se avisa («no grader.%l for …») y `sudo cmsctl
doctor` también lo detecta.

## 5. OutputOnly

No hay programa: el concursante resuelve en su computadora y entrega un
archivo de salida por caso, llamado `output_<caso>.txt` (por ejemplo
`output_01.txt`). Puede entregar sólo algunos. El formato de envío lista
esos archivos. Las salidas se evalúan con `diff` o con `checker` como en
Batch. Los casos de entrada se suelen dar como adjuntos.

## 6. BatchAndOutput

Como Batch, pero en los casos indicados en *Comma-separated list of output
only testcases* el concursante entrega la salida (`output_<caso>.txt`) en
vez de que corra su programa; en los demás, si entrega la salida se usa, y
si no, se ejecuta el programa. Un checker recibe un cuarto argumento:
`outputonly` o `batch`.

## 7. Communication

Un programa de los organizadores, el **`manager`**, corre junto a una o más
instancias del programa del concursante y se comunica con cada una por un
par de FIFOs. El manager lee la entrada del caso de su entrada estándar,
recibe las rutas de las FIFOs como argumentos y escribe el puntaje (0 a 1)
en su salida estándar y el mensaje en la de error, como un checker.

| Parámetro | Qué es |
|---|---|
| *Número de procesos* (*Number of Processes*) | cuántas instancias del programa del concursante |
| *Compilación* | sola o con grader (lo habitual: con un grader que hace la comunicación) |
| *E/S del usuario* (*User I/O*) | `std_io`: el programa del concursante usa su entrada/salida estándar; `fifo_io`: recibe las FIFOs como argumentos |

Archivos: `manager` (ejecutable) y, con grader, `grader.<ext>` por lenguaje.

## 8. Interactive

Un **controlador** (`controller`) de los organizadores lanza instancias del
programa del concursante cuando las necesita y se comunica con cada una por
tuberías. Parámetros: límite de procesos (*Process limit*), compilación,
si las instancias corren a la vez (*Concurrent solutions*) y los límites de
memoria, tiempo y tiempo real del controlador. Es una extensión de esta
versión; el protocolo está en
[../formato-de-paquete.md](../formato-de-paquete.md).

## 9. TwoSteps

El concursante envía dos archivos con una función cada uno; la primera
procesa la entrada y la segunda debe recuperar algo a partir de lo que
produjo la primera. El `manager.<ext>` de los organizadores se compila con
ambos. Obsoleto en el CMS original; se mantiene por compatibilidad.

## 10. Tipos de puntaje

Todos reciben los resultados de los casos (un número de 0 a 1 por caso) y
producen el puntaje del envío.

### Sum

Cada caso vale lo mismo. Parámetro: los puntos de cada caso.

```
5
```

Con 20 casos, el máximo es 100. Simple, pero no permite subtareas.

### GroupMin (subtareas; el más usado)

Los casos se agrupan en **subtareas**; cada subtarea vale el **mínimo** de
sus casos multiplicado por sus puntos. Una subtarea con un solo caso mal
vale 0. Parámetro: una lista de `[puntos, casos]`, donde *casos* es:

- un **número**: los siguientes N casos en orden alfabético de nombre;
- o una **expresión regular** sobre los nombres de los casos.

```
[[30, 5], [70, 10]]
```

Primera subtarea: los 5 primeros casos, 30 puntos; segunda: los 10
siguientes, 70 puntos.

```
[[30, "s1_.*"], [70, "s2_.*"]]
```

Lo mismo, eligiendo por nombre (más seguro: no depende del orden).

### GroupMul

Como GroupMin, pero la subtarea vale el **producto** de los resultados de
sus casos (útil con checkers que dan puntaje parcial por caso).

### GroupThreshold

Como GroupMin, pero cada subtarea tiene un umbral: `[puntos, casos,
umbral]`. El resultado de cada caso (que da el checker) se interpreta como
un valor que debe ser mayor que 0 y no superar el umbral; la subtarea da sus
puntos si todos sus casos cumplen.

### Cómo lo ve el concursante

Con subtareas, el detalle de un envío muestra cada subtarea con sus puntos
y, según el nivel de retroalimentación, el resultado de los casos públicos
([cap. 5](05-problemas.md#8-puntaje-y-retroalimentación)).

## 11. Subtareas paso a paso

Problema con tres subtareas: N ≤ 100 (20 puntos), N ≤ 10⁵ (30 puntos), sin
límite (50 puntos).

1. Nombra los casos con un prefijo por subtarea: `1_01`…`1_05`,
   `2_01`…`2_08`, `3_01`…`3_12`.
2. Tipo de puntaje **GroupMin**, parámetros:
   ```
   [[20, "1_.*"], [30, "2_.*"], [50, "3_.*"]]
   ```
3. Si los casos de una subtarea pequeña también deben pasar en las
   grandes, inclúyelos con una expresión regular que abarque ambos:
   `"(1|2)_.*"` para la segunda subtarea.
4. *Modo de puntaje* del problema: el de IOI 2017- suma la mejor
   puntuación de cada subtarea entre todos los envíos.
5. Comprueba con *Envíos de prueba*: una solución que sólo resuelve N ≤ 100
   debe dar exactamente 20.

## 12. Lenguajes

Lenguajes disponibles (con los nombres de CMS): C11 / gcc, C++11, C++14,
C++17 y C++20 / g++, C# / Mono, Haskell / ghc, Java / JDK, Pascal / fpc,
PHP, Python 3 / CPython, Python 3 / PyPy, Rust, Go y Kotlin / JDK.

- Un lenguaje sólo funciona si su compilador está instalado en el juez. Con
  `--compilers basic` el instalador pone sólo C, C++, Python 3 y Java. En
  Ubuntu 26.04 no hay C#.
- Los lenguajes que puede usar cada concursante se eligen en el concurso
  ([cap. 4](04-concursos.md)) y se pueden restringir por problema.
- Agregar o cambiar lenguajes: [../jueces.md](../jueces.md#lenguajes).

## 13. Errores comunes

| Síntoma | Causa | Arreglo |
|---|---|---|
| Todo da «Compilación fallida» en un lenguaje | falta `grader.<ext>` o la cabecera | súbelos o quita el lenguaje |
| Todo da 0 con salidas visualmente iguales | `diff` y la salida correcta tiene otro formato (decimales) | usa un checker |
| El checker siempre da 0 | no es ejecutable para Linux, no está compilado estático o escribe el puntaje en la salida de error | compílalo con `-static` y revisa a dónde escribe |
| Una subtarea nunca suma | la expresión regular no coincide con los nombres de los casos | revisa los nombres en *Casos de prueba* |
| El máximo no es 100 | la suma de los puntos de las subtareas no es 100 | corrige los parámetros |

---

**Siguiente:** [7. Importar](07-importar.md) ·
**Anterior:** [5. Problemas](05-problemas.md) ·
[Índice](README.md)

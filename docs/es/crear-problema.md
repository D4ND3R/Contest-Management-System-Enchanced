# Crear un problema

*English: [../en/creating-a-problem.md](../en/creating-a-problem.md).*

Un problema se puede preparar entero desde la administración, sin la
línea de comandos. Hace falta el permiso **todo** o el de **preparador de
problemas** (`permission_tasks`); eliminar un problema y agregarlo a un
concurso requieren el permiso **todo**. Cada cambio queda en el
[registro de auditoría](administracion.md).

## 1. Crear o importar

- **Problemas → Agregar** crea un problema vacío con un dataset `Default`
  (Batch, 100 puntos repartidos entre los casos), como CMS.
- **Importar un paquete de problema (zip)** sube un zip del
  [formato propio](formato-de-paquete.md) (`problem.yaml`) o de los
  formatos de CMS (italy_yaml, Polygon, TPS). Antes de escribir nada la
  página muestra la **vista previa**: errores y advertencias de la
  validación y la lista de cambios (lo que crearía o actualizaría, como
  `cms ctl import-task --dry-run`). **Confirmar** lo escribe en una
  transacción. Los zip se leen con controles de *zip slip*, enlaces y
  tamaño.
- En la página del problema, **Actualizar desde un paquete** hace lo mismo
  sobre un problema existente (el nombre del paquete no importa: se
  actualiza ese problema; los envíos se conservan).
- Si el paquete trae `solutions/` (`correcta.ac.cpp`, `lenta.tle.py`…), la
  confirmación puede enviarlas como **envíos de prueba**.

## 2. Opciones y problem.yaml

La página del problema es la del CMS original (nombre, título, enunciados,
adjuntos, formato de envío, lenguajes, fichas, límites, retroalimentación,
modo de puntaje y, por dataset, límites, tipo de problema y de puntaje con
sus parámetros, managers y casos).

Las mismas opciones se ven como **problem.yaml**: se puede descargar,
editar en la página o subir un archivo. **Vista previa** muestra los
cambios; **Confirmar** los aplica (se exporta el problema como paquete con
ese `problem.yaml` y se vuelve a importar sobre él). **Descargar como
paquete** baja el zip completo.

## 3. Archivos uno por uno

**Agregar archivos** acepta varios archivos a la vez y los reconoce por el
nombre; la página lista para qué se tomó cada uno:

| Nombre | Se guarda como |
|---|---|
| `statement.pdf`, `statement.md`, `statement-es.pdf`, `statement/en.pdf` | enunciado (idioma del nombre o el indicado) |
| `attachments/<archivo>` | adjunto |
| `01.in`, `input.01`, `input_01.txt`, `tests/01.in` | entrada del caso `01` |
| `01.out`, `01.ans`, `output.01`, `output_01.txt` | salida del caso `01` |
| `checker`, `manager`, `controller` | manager tal cual (ejecutable) |
| `checker.cpp`, `controller.cpp`, `manager.cpp` (Communication) | compilado con g++ como al importar |
| `grader.cpp`, `grader.py`, `stub.c`, `*.h` | manager |
| `<nombre>.<veredicto>.<ext>`, `solutions/…` | envío de prueba |

Un archivo con el nombre de uno existente lo reemplaza. Los casos y los
managers van al dataset elegido.

## 4. Casos uno por uno

Cada caso tiene su página (el nombre del caso en la tabla del dataset):
muestra el comienzo de la entrada y la salida y permite cambiarlas. La
entrada se **sube** o se **escribe**; la salida se **escribe**, se
**sube**, se deja **vacía** o se **genera con la solución de
referencia**. **Escribir o generar un caso** crea uno nuevo.

**Generar entradas** corre un **generador** en el sandbox: se sube su
fuente y se escriben líneas `nombre argumentos`; el generador recibe los
argumentos en su entrada y lo que escribe es la entrada del caso. Con una
solución de referencia elegida, después se generan las salidas.

Las generaciones son pruebas de usuario de un participante oculto (ver
abajo) que juzgan el dispatcher y los workers; la página muestra las que
siguen en curso y guarda los resultados al recargarla.

## 5. Envíos de prueba

**Envíos de prueba** recibe soluciones con el veredicto esperado en el
nombre (`ac`, `pa`, `wa`, `tle`, `mle`, `re`, `ce`: `correcta.ac.cpp`) o
elegido en la página. Se juzgan como cualquier envío y la tabla muestra el
resultado de cada caso y si coincide con lo esperado:

- `ac`: puntaje completo; `pa`: algunos puntos;
- `wa`, `tle`, `mle`, `re`: algún caso con ese resultado y no el puntaje
  completo; `ce`: error de compilación.

Los envíos de prueba y las generaciones usan una participación **oculta y
sin restricciones** del usuario reservado `_preparer` en el concurso del
problema (se crea sola) y **no son oficiales**: nunca cuentan para los
puntajes ni aparecen en el scoreboard. Por eso el problema tiene que estar
en un concurso, y se juzgan en el dataset activo y en los que tienen
**juzgamiento en segundo plano**.

## 6. Qué falta

**Qué falta** revisa el dataset elegido: enunciado, casos, salidas
esperadas (las vacías se marcan), parámetros de puntaje válidos (subtareas
y puntos), managers y checker que pide el tipo de problema (la misma
validación que `cms ctl validate-task`), si el dataset se juzga, si hay un
envío de prueba que debe dar `ac` y lo da, y si todos los envíos de
prueba dan lo esperado.

## 7. Activar un dataset

Para cambiar los casos de un problema en un concurso ya empezado, conviene
**clonar** el dataset, prepararlo (con juzgamiento en segundo plano) y
usar **Activar**, que muestra qué puntajes cambian y permite avisar a los
concursantes, como CMS.

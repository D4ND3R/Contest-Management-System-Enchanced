# 4. Concursos

*English: [Contests](../../en/manual/04-contests.md)*

Un **concurso** reúne todo lo que viven los concursantes durante una
competencia: el horario, los problemas, quién puede entrar y cómo, cuántas
veces se puede enviar y qué información reciben sobre sus envíos. Este
capítulo recorre, campo por campo, la página de configuración de un
concurso, los grupos (horarios distintos para distintos concursantes), la
lista de problemas del concurso y cómo eliminarlo. Al final hay dos
configuraciones listas para copiar.

## Contenido

1. [Antes de empezar](#1-antes-de-empezar)
2. [Crear un concurso](#2-crear-un-concurso)
3. [Cómo funciona la página del concurso](#3-cómo-funciona-la-página-del-concurso)
4. [Información del concurso](#4-información-del-concurso)
5. [Ingreso](#5-ingreso)
6. [Tokens](#6-tokens)
7. [Horario del concurso y modo análisis](#7-horario-del-concurso-y-modo-análisis)
8. [Límites](#8-límites)
9. [Grupos](#9-grupos)
10. [Los problemas del concurso](#10-los-problemas-del-concurso)
11. [Eliminar un concurso](#11-eliminar-un-concurso)
12. [Configuraciones listas para usar](#12-configuraciones-listas-para-usar)
13. [Errores comunes](#13-errores-comunes)
14. [Qué hacer si…](#14-qué-hacer-si)

---

## 1. Antes de empezar

Necesita:

- La administración instalada y funcionando ([capítulo 2](02-instalacion.md)).
- Un administrador con **Todos los permisos** (*All permissions*). Los
  administradores sin ese permiso ven las páginas de este capítulo, pero
  los botones para guardar, eliminar o reordenar aparecen desactivados
  ([capítulo 3](03-primer-acceso.md)).
- **La zona horaria del servidor bien configurada**, en la página
  *Servidor* (*Server*) de la administración. Todas las horas que se
  escriben en la página del concurso se interpretan en esa zona, y las
  etiquetas la nombran: por ejemplo *Hora de inicio (en America/Santiago)*.
  Si la zona está mal, el concurso empezará a otra hora. Configúrela
  **antes** de escribir los horarios ([capítulo 3](03-primer-acceso.md) y
  [Administración](../administracion.md#página-servidor)).

Los conceptos de concurso, problema, usuario, participación y grupo se
explican en el [capítulo 1](01-conceptos.md).

> **Sobre los nombres de las pantallas.** La administración se muestra en
> español cuando el navegador prefiere el español. Este manual nombra cada
> menú, botón y campo con la etiqueta exacta en español y, la primera vez,
> la etiqueta en inglés entre paréntesis. Algunas etiquetas en español son
> engañosas; se avisa en cada caso.

---

## 2. Crear un concurso

Ejemplo de este capítulo: la **OI 2026**, una olimpiada de 5 horas con 3
problemas y 30 concursantes.

1. En la barra lateral izquierda, bajo **Concursos** (*Contests*), haga
   clic en **(crear un concurso...)** (*(create new contest...)*).
2. Se abre la página **Concurso nuevo** (*New contest*). En
   **Nombre:** (*Name:*) escriba un nombre corto, por ejemplo `oi2026`.
3. Haga clic en el botón de envío del formulario (no tiene texto propio: el
   navegador lo muestra como *Enviar* o *Submit*).
4. Si todo salió bien aparece la notificación **Operación exitosa.**
   (*Operation successful.*) y se abre la página **Configuración del
   concurso** (*Contest configuration*) del concurso nuevo.

El nombre solo puede tener **letras sin acentos, números, guion bajo (`_`)
y guion (`-`)**, sin espacios, y no puede repetirse. `OI 2026` o
`olimpíada` no sirven; `oi2026`, `oi-2026` u `OI_2026` sí.

### Qué crea el sistema por usted

Al crear el concurso el sistema crea además su **grupo principal**,
llamado `default`, con estos valores:

| Qué | Valor inicial |
|---|---|
| Descripción | igual al nombre (`oi2026`) |
| Inicio | 1 de enero de 2000, 00:00:00 UTC |
| Fin | 1 de enero de 2100, 00:00:00 UTC |
| Lenguajes permitidos | C11 / gcc, C++20 / g++ y Pascal / fpc |
| Tokens | modo *Infinitos* (ver la [sección 6](#6-tokens)) |

> **Importante: un concurso recién creado ya está abierto.** Con inicio en
> el año 2000 y fin en el 2100, cualquier concursante que tenga
> participación puede entrar y ver los problemas apenas se agreguen.
> **Ponga el horario real antes de agregar problemas y concursantes.**

En la página aparecen convertidos a la zona del servidor; por ejemplo, en
`America/Santiago` se ven como `1999-12-31 21:00:00` y
`2099-12-31 21:00:00`.

Al entrar a un concurso, la barra lateral cambia: arriba aparece el nombre
del concurso y un enlace **Administración** (*Administration*) para
volver, y debajo el menú del concurso: **General** (*General*, la página
de configuración), **Ranking**, **Envíos** (*Submissions*), **Pruebas de
usuario** (*User tests*, solo si están permitidas), **Usuarios** (*Users*),
**Grupos** (*Groups*), **Problemas** (*Tasks*), **Anuncios**
(*Announcements*), **Preguntas** (*Questions*) y **Registro de auditoría**
(*Audit log*).

---

## 3. Cómo funciona la página del concurso

La página **Configuración del concurso** tiene un solo formulario con
cinco bloques: *Información del concurso*, *Ingreso*, *Parámetros de los
tokens*, *Configuración del grupo principal* (con el horario y el modo
análisis) y *Límites*. Al pie hay tres botones:

| Botón | Qué hace |
|---|---|
| **Actualizar** (*Update*) | guarda **todo** el formulario de una vez |
| **Borrar** (*Reset*) | **no borra nada**: devuelve el formulario a los valores guardados, descartando lo que escribió y no guardó |
| **Eliminar** (*Remove*) | abre la confirmación para eliminar el concurso ([sección 11](#11-eliminar-un-concurso)) |

Cada ícono de información junto a un campo muestra una ayuda al pasar el
mouse.

Qué pasa al hacer clic en **Actualizar**:

- Si algún valor no se puede leer, no se guarda **nada** y aparece
  **Campo(s) inválido(s).** (*Invalid field(s).*) con el detalle en inglés
  (por ejemplo `Can't cast 14/11/2026 to datetime.`).
- Si los valores se leen pero no son válidos para la base de datos (un
  cero donde se pide un número positivo, un fin anterior al inicio…), no
  se guarda nada y aparece **La operación falló.** (*Operation failed.*)
  con un mensaje del tipo `new row for relation "contests" violates check
  constraint "…"`. La [sección 14](#14-qué-hacer-si) traduce cada uno.
- Si todo está bien: **Operación exitosa.** Los cambios se aplican de
  inmediato en el sitio del concursante y quedan en el **Registro de
  auditoría** con el valor anterior y el nuevo.

### Formatos de los campos

| Tipo de campo | Formato | Ejemplo |
|---|---|---|
| Fecha y hora | `AAAA-MM-DD HH:MM:SS`, 24 horas, **con segundos**, en la zona del servidor | `2026-11-14 09:00:00` |
| Duración en segundos | número (se aceptan decimales) | `10800` = 3 horas |
| Duración en minutos | número (solo el *Período de generación de tokens*) | `60` |
| Número entero | sin puntos ni comas | `50` |
| Casilla | marcada = sí | |

`2026-11-14 09:00` (sin segundos) o `14/11/2026 09:00:00` dan **Campo(s)
inválido(s)**.

> **Vacío no es lo mismo que cero.** En los campos opcionales (los
> límites, *Cantidad máxima de tokens*, *Máximo de tokens acumulados*,
> *Duración del concurso*), **vacío significa "sin límite"**. Un `0` en
> la mayoría de ellos es un error. En cambio, los campos obligatorios
> (*Decimales del puntaje*, *Intervalo mínimo entre tokens*, *Cantidad
> inicial de tokens*, *Tokens por generación*, *Período de generación de
> tokens*, inicio y fin) no se pueden dejar vacíos.

---

## 4. Información del concurso

Primer bloque, **Información del concurso** (*Contest information*).

| Campo | Qué hace | Por defecto | Ejemplo |
|---|---|---|---|
| **Título** (*Name*) | nombre corto e identificador | el que escribió al crear | `oi2026` |
| **Descripción** (*Description*) | nombre que ven los concursantes y el público | igual al nombre | `Olimpiada de Informática 2026` |
| **Idiomas permitidos** (*Allowed localizations*) | idiomas de la interfaz del sitio del concursante | vacío (todos) | `es` |
| **Lenguajes de programación permitidos** (*Allowed programming languages*) | lenguajes en que se puede enviar | C11 / gcc, C++20 / g++, Pascal / fpc | C11 / gcc, C++20 / g++, Python 3 / CPython |
| **Permitir descargar los envíos** (*Submissions download allowed*) | el concursante puede bajar los archivos que envió | marcada | marcada |
| **Permitir preguntas** (*Allow questions*) | el concursante puede hacer preguntas | marcada | marcada |
| **Permitir pruebas de usuario** (*Allow user tests*) | el concursante puede probar su programa con sus propias entradas | marcada | marcada |
| **Decimales del puntaje** (*Score decimal places*) | redondeo del puntaje total | `0` | `0` o `2` |
| **Permitir envíos no oficiales antes del modo análisis** (*Allow unofficial submission before analysis mode*) | enviar fuera de concurso cuando terminó el tiempo propio | desmarcada | desmarcada |
| **Zona horaria** (*Timezone*) | se guarda, pero **no cambia** cómo se muestran las horas | vacío | vacío |

### Título (Name)

> **Ojo con la etiqueta.** En español este campo aparece como **Título**
> (también en la columna de la lista de concursos), pero es el **nombre
> corto**, no el título que ven los concursantes. Ese es la
> *Descripción*.

Las reglas son las del momento de crearlo: letras sin acentos, números,
`_` y `-`, único. Para qué se usa:

- Es la dirección del concurso en el sitio del concursante:
  `https://concurso.ejemplo.org/oi2026` (cuando el sitio sirve varios
  concursos; ver [Sitio del concursante](../sitio-concursante.md#urls)).
- Es el nombre de la cookie de sesión de los concursantes en ese concurso.
- Interviene en el enlace secreto al scoreboard ([capítulo 11](11-scoreboard.md)).

**No lo cambie después de publicar el concurso**: cambia la dirección,
todos los concursantes conectados quedan fuera (deben volver a entrar) y
el enlace secreto al scoreboard deja de funcionar. Si en `cms.yaml` se
configuró `contest_web.contest` con este nombre (para servir un solo
concurso), también hay que cambiarlo ahí.

### Descripción (Description)

Es el nombre "de verdad": aparece como título del sitio del concursante, en
la barra superior, en la lista de concursos que ven los concursantes y como
nombre del concurso en el scoreboard. Puede tener espacios, acentos y
cualquier carácter. Al crear el concurso vale lo mismo que el nombre:
**cámbiela siempre**.

### Idiomas permitidos (Allowed localizations)

Limita los idiomas de la **interfaz** del sitio del concursante (menús,
botones, mensajes). No tiene nada que ver con los enunciados ni con los
lenguajes de programación. Se escribe una lista de códigos separados por
comas.

- **Vacío**: el concursante ve el sitio en el idioma de su navegador (si
  existe la traducción) y puede cambiarlo con el selector.
- **`es`**: siempre en español, aunque el navegador esté en inglés. `es`
  incluye las variantes, como `es-CL`.
- **`es, en`**: español o inglés según el navegador. Si el navegador no
  pide ninguno de los dos, se usa el inglés.
- **`es-CL`**: solo la variante chilena. Escríbala con **guion**: `es_CL`
  (con guion bajo) no se reconoce.

> **Error común:** un código mal escrito o de un idioma sin traducción
> (por ejemplo `sp` o `es_CL`) no da error al guardar, pero el sitio queda
> en **inglés**. Después de guardar, abra el sitio del concursante y
> compruebe el idioma.

### Lenguajes de programación permitidos (Allowed programming languages)

Una casilla por lenguaje disponible en la instalación, en orden
alfabético: C# / Mono, C++11 / g++, C++14 / g++, C++17 / g++, C++20 / g++,
C11 / gcc, Go, Haskell / ghc, Java / JDK, Kotlin / JDK, PHP, Pascal / fpc,
Python 3 / CPython, Python 3 / PyPy y Rust (más los que se agreguen en
`languages_dir`; ver [Jueces](../jueces.md#lenguajes)).

- Los concursantes solo pueden enviar en los lenguajes marcados (cada
  problema puede restringir aún más su propia lista; ver
  [capítulo 5](05-problemas.md)).
- **Si no marca ninguno**, no se puede enviar ninguna solución que
  necesite un lenguaje.
- **La casilla no instala nada.** El juez necesita el compilador. Con la
  instalación `--compilers basic` solo se instalan C, C++, Python 3 y
  Java: **Pascal / fpc viene marcado por defecto y no estará instalado**.
  Desmarque todo lenguaje cuyo compilador no esté en los jueces
  ([capítulo 2](02-instalacion.md)).
- La ayuda del campo avisa: *Todos los lenguajes permitidos deben estar
  soportados por las tareas, por ejemplo en las tareas con manager*. Si un
  problema usa un *grader* o *manager* escrito para ciertos lenguajes, no
  permita otros ([capítulo 6](06-tipos-de-problema-y-puntaje.md)).
- **C# / Mono, Go, Java / JDK y Kotlin / JDK necesitan varios hilos.** Si
  marca cualquiera de ellos, **todos** los programas del concurso, en
  cualquier lenguaje, se ejecutan con permiso para crear muchos procesos e
  hilos (hasta 1000). Si no los necesita, no los marque.

### Permitir descargar los envíos (Submissions download allowed)

Marcada, cada concursante puede bajar desde la lista de sus envíos los
archivos que envió (útil si pierde su copia local). Desmarcada, los enlaces
de descarga no aparecen y la descarga no está disponible. Solo afecta a
los propios archivos del concursante.

### Permitir preguntas (Allow questions)

Marcada, la página **Comunicación** del sitio del concursante muestra el
formulario para hacer preguntas, que el staff responde desde
**Preguntas** ([capítulo 9](09-durante-el-concurso.md)). Desmarcada, el
formulario desaparece; los anuncios y los mensajes del staff se siguen
viendo.

### Permitir pruebas de usuario (Allow user tests)

Marcada, aparece en el sitio del concursante el menú **Testing** (no está
traducido), donde el concursante ejecuta su programa con una entrada
propia, sin puntaje. Solo funciona mientras compite y en problemas cuyo
tipo lo admite ([capítulo 6](06-tipos-de-problema-y-puntaje.md)).
Desmarcada, desaparece ese menú y también **Pruebas de usuario** del menú
del concurso en la administración. Cada prueba ocupa al juez como un envío:
en un servidor chico, considere limitarlas ([sección 8](#8-límites)).

### Decimales del puntaje (Score decimal places)

Cantidad de decimales con que se muestra el **puntaje total** de cada
concursante (la suma de sus problemas) en el **Ranking** de la
administración y en el scoreboard. `0` muestra `287`; `2` muestra
`287.35`. Cada problema tiene su propio campo de decimales para su
puntaje ([capítulo 5](05-problemas.md)). El puntaje se guarda sin
redondear: puede cambiar este valor en cualquier momento sin reevaluar
nada. No puede quedar vacío ni ser negativo.

### Permitir envíos no oficiales antes del modo análisis

(*Allow unofficial submission before analysis mode*.) Cuando el tiempo de
un concursante terminó, normalmente ya no puede enviar. Con esta casilla
marcada puede seguir enviando, pero como **envíos no oficiales**: se
evalúan y el concursante ve el resultado, pero **no cuentan** para su
puntaje ni para el ranking. Vale en dos situaciones:

- terminó su **ventana personal** ([sección 7](#ventana-por-concursante-concursos-tipo-usaco))
  pero el concurso sigue abierto para otros;
- terminó el concurso y el **modo análisis** está habilitado pero todavía
  no empezó.

Sin modo análisis, una vez terminado el concurso nadie puede enviar,
aunque la casilla esté marcada.

### Zona horaria (Timezone)

La ayuda del campo dice que es la zona para mostrar las horas a los
concursantes, pero **en esta versión el campo se guarda y no cambia
nada**: todas las fechas de los tres sitios usan una sola zona, la del
servidor (página **Servidor**). Existe para conservar el dato al importar
concursos de CMS. Déjelo vacío. Si escribe algo, debe ser un nombre de
zona válido (`America/Santiago`, `America/Mexico_City`, `Europe/Madrid`);
si no, aparece `Unknown time zone …` y no se guarda nada.

---

## 5. Ingreso

Segundo bloque, **Ingreso** (*Logging in*): cómo entran los concursantes al
sitio. Algunas opciones usan datos de cada **participación** (dirección IP,
oculta), que se editan en la página de la participación
([capítulo 8](08-usuarios-y-equipos.md)).

| Campo | Qué hace | Por defecto |
|---|---|---|
| **Bloquear participaciones ocultas** (*Block hidden participations*) | las participaciones marcadas como ocultas no pueden entrar | desmarcada |
| **Permitir el ingreso con contraseña** (*Allow password authentication*) | entrar con usuario y contraseña | marcada |
| **Permitir la inscripción** (*Allow registration*) | los concursantes crean su propia cuenta | desmarcada |
| **Restricción de ingreso por IP** (*IP based login restriction*) | quien tiene IP asignada solo entra desde ella | **marcada** |
| **Ingreso automático por IP** (*IP based autologin*) | quien llega desde su IP asignada entra sin contraseña | desmarcada |

### Bloquear participaciones ocultas

Una participación **oculta** (casilla *Participación oculta* en la página
de la participación) no aparece en el scoreboard público. Se usa para
cuentas de prueba del staff o para concursantes fuera de concurso. Con
esta casilla marcada, esas cuentas **no pueden entrar** al sitio del
concursante. Márquela el día del concurso si no quiere que las cuentas de
prueba se usen; déjela desmarcada si hay concursantes "fuera de
competencia" que deben poder participar sin aparecer en el scoreboard.

### Permitir el ingreso con contraseña

Es la forma normal de entrar. La contraseña es la del usuario o, si la
participación tiene una propia, esa ([capítulo 8](08-usuarios-y-equipos.md)).
Si la desmarca, el formulario de entrada sigue apareciendo pero **nadie
puede entrar con contraseña** (el sitio responde *Acceso denegado.* /
*Failed to log in.*): solo tiene sentido junto con el ingreso automático
por IP.

### Permitir la inscripción

Marcada, debajo del formulario de entrada aparece **¿No tienes una
cuenta? Registrarse** (*Don't have an account? Register*). El concursante
puede:

- crear un usuario nuevo (nombre, apellido, nombre de usuario de hasta 50
  caracteres con letras sin acentos, números, `_` y `-`, contraseña de al
  menos 6 caracteres y correo opcional), o
- unir al concurso un usuario que ya existe, con su contraseña.

En ambos casos queda en el **grupo principal**. Si hay **equipos**
creados, elegir uno es obligatorio. Úsela en concursos abiertos; en una
olimpiada con lista cerrada de concursantes déjela desmarcada y cargue los
usuarios usted ([capítulo 8](08-usuarios-y-equipos.md)). Los inscritos
entran con contraseña: no desmarque *Permitir el ingreso con contraseña*.

### Restricción de ingreso por IP

Viene **marcada**. Afecta solo a las participaciones que tienen algo en
*Dirección IP o subred* (*IP address or subnet*): esas solo pueden entrar
(y seguir conectadas) desde esas direcciones. Se aceptan direcciones
(`10.0.5.21`) y redes (`10.0.5.0/24`) separadas por comas. Una
participación sin direcciones puede entrar desde cualquier lado.

Ejemplo: en la OI 2026 cada concursante tiene una computadora fija en el
laboratorio `10.0.5.0/24`. Con la dirección de cada uno cargada en su
participación, nadie puede entrar con la cuenta de otro desde otra
máquina.

### Ingreso automático por IP

Marcada, quien llega desde la dirección **exacta** de una participación
entra sin escribir nada. Condiciones:

- La participación debe tener una **sola dirección**, no una red
  (`10.0.5.21`, no `10.0.5.0/24`).
- Si dos participaciones tienen la misma dirección, **ninguna** de las dos
  entra automáticamente (queda registrado en el log del sitio).
- Si no hay coincidencia, el concursante puede entrar igual con contraseña
  (si está permitido).

Para un laboratorio donde cada puesto tiene IP fija: cargue la IP de cada
puesto, marque *Ingreso automático por IP* y, si no quiere contraseñas,
desmarque *Permitir el ingreso con contraseña*.

> **Detrás de un proxy.** Las opciones por IP necesitan que el sitio vea
> la dirección real del concursante. La instalación con dominio ya lo
> configura (`num_proxies: 1`); si agrega otro proxy delante, ajuste
> `num_proxies` ([Sitio del concursante](../sitio-concursante.md#configuración)).
> Si todos los concursantes salen a internet por la misma dirección (un
> NAT), las opciones por IP no sirven.

---

## 6. Tokens

### Qué son y para qué sirven

Normalmente el concursante ve un resultado **parcial** de cada envío: el
de los casos de prueba públicos, según el nivel de retroalimentación del
problema ([capítulo 5](05-problemas.md)). Un **token** es una "ficha" que
el concursante juega sobre un envío para ver su **resultado completo**.
Además, con el modo de puntaje *Usar el mejor entre los envíos con ficha y
el último (IOI 2010-2012)* (*Use best among tokened and last submissions
(IOI 2010-2012)*), solo cuentan los envíos con token y el último.

Hay dos juegos de tokens que se combinan: los **del concurso** (comunes a
todos los problemas, se configuran aquí) y los **de cada problema** (en la
página del problema, [capítulo 5](05-problemas.md)). Para jugar un token
sobre un envío el concursante necesita uno de cada juego.

### Modo de tokens (Token mode)

| Opción | Significado |
|---|---|
| **Desactivados** (*Disabled*) | no hay tokens en todo el concurso |
| **Finitos** (*Finite*) | cantidad limitada, según los parámetros de abajo |
| **Infinitos** (*Infinite*) | el concurso no limita; manda lo que diga cada problema |

Por defecto el concurso viene en **Infinitos** y cada problema nuevo en
**Desactivados**: el resultado es que **no hay tokens**. Para usarlos, el
concurso y los problemas tienen que estar en *Finitos* o *Infinitos*. Si
alguno de los dos lados está en *Desactivados*, el concursante no ve
tokens.

Combinaciones habituales:

| Concurso | Problemas | Resultado para el concursante |
|---|---|---|
| Desactivados | cualquiera | sin tokens |
| Infinitos | Desactivados | sin tokens (valor por defecto) |
| Finitos | Infinitos | un juego de tokens **compartido entre todos los problemas** |
| Infinitos | Finitos | un juego **distinto en cada problema** |
| Infinitos | Infinitos | tokens ilimitados: ve todos los resultados completos |
| Finitos | Finitos | dos juegos a la vez; confunde, evítelo |

> **Olimpiadas modernas (estilo IOI desde 2017):** no usan tokens. Ponga
> el concurso en **Desactivados** y, en cada problema, el **Modo de
> puntaje** (*Score mode*) *Usar la suma, por subtarea, del mejor
> resultado de esa subtarea entre todos los envíos (IOI 2017-)*. Si deja
> el modo por defecto (*…con ficha y el último*) sin tokens, **el puntaje
> de cada problema será el del último envío**, aunque uno anterior haya
> sido mejor ([capítulo 6](06-tipos-de-problema-y-puntaje.md)).

### Parámetros (solo con *Finitos*)

| Campo | Qué hace | Unidad | Por defecto | Puede quedar vacío |
|---|---|---|---|---|
| **Cantidad máxima de tokens** (*Maximum number of tokens*) | cuántos tokens puede usar en total en todo el concurso | tokens | vacío (sin tope) | sí |
| **Intervalo mínimo entre tokens** (*Minimum interval between tokens*) | espera obligatoria después de usar uno | **segundos** | `0` | no (`0` = sin espera) |
| **Cantidad inicial de tokens** (*Initial number of tokens*) | cuántos tiene al empezar su tiempo de concurso | tokens | `2` | no |
| **Tokens por generación** (*Token generation number*) | cuántos recibe en cada generación | tokens | `2` | no (`0` = nunca recibe más) |
| **Período de generación de tokens** (*Token generation period*) | cada cuánto se generan | **minutos** | `30` | no (mayor que 0) |
| **Máximo de tokens acumulados** (*Maximum accumulated tokens*) | tope de tokens guardados a la vez; lo que se genere por encima se pierde | tokens | vacío (sin tope) | sí |

Reglas que conviene conocer:

- Las generaciones se cuentan desde el **inicio del tiempo** del
  concursante: el inicio del concurso (de su grupo) o, en concursos con
  ventana personal, el momento en que apretó **¡Comenzar!**.
- El *Máximo de tokens acumulados*, si lo pone, no puede ser menor que la
  *Cantidad inicial* (si no, **La operación falló.** con
  `contests_check`), aunque el modo no sea *Finitos*.
- *Cantidad máxima de tokens* y *Máximo de tokens acumulados* no admiten
  `0`: para "ninguno", use el modo *Desactivados*.
- Los tokens solo se juegan **mientras el concursante compite**: no en el
  modo análisis ni después.
- Un concursante con **tiempo extra** sigue recibiendo tokens durante ese
  tiempo. Si no quiere que tenga más que los demás, ponga una *Cantidad
  máxima de tokens*.
- **No cambie las reglas durante el concurso**: los tokens disponibles se
  recalculan con las reglas nuevas sobre todo lo ya jugado, y el resultado
  puede sorprender a los concursantes.

### Ejemplo completo

La OI 2026 (de 09:00 a 14:00) usa un juego de tokens **compartido entre
todos los problemas**:

| Lugar | Campo | Valor |
|---|---|---|
| Concurso | Modo de tokens | Finitos |
| Concurso | Cantidad máxima de tokens | `6` |
| Concurso | Intervalo mínimo entre tokens | `600` (10 minutos) |
| Concurso | Cantidad inicial de tokens | `2` |
| Concurso | Tokens por generación | `1` |
| Concurso | Período de generación de tokens | `60` (una hora) |
| Concurso | Máximo de tokens acumulados | `4` |
| Cada problema | Modo de tokens | Infinitos |

En la página **Carátula** (*Overview*) del sitio del concursante, el
concursante lee exactamente:

> Posee un conjunto de tokens compartido entre todos los problemas.
> Comienza con 2 tokens. Cada 60 minutos recibe otro token, hasta un
> máximo de 4 tokens. Puede utilizar un token cada 600 segundos y no más
> de 6 tokens en total.

Cómo evoluciona para Ana:

| Hora | Qué pasa | Tokens disponibles |
|---|---|---|
| 09:00 | empieza el concurso | 2 |
| 09:40 | Ana juega un token | 1 |
| 09:45 | quiere jugar otro: debe esperar hasta las 09:50 (intervalo de 600 s) | 1, en espera |
| 10:00 | se genera 1 | 2 |
| 11:00 | se genera 1 | 3 |
| 12:00 | se genera 1 | 4 (llegó al máximo acumulado) |
| 13:00 | se generaría otro, pero ya tiene 4: se pierde | 4 |
| 13:00–13:30 | juega 4 tokens, uno cada 10 minutos | 0 (usó 5 de los 6 del total) |

En la página de envíos de cada problema el concursante ve cuántos tokens
tiene, desde cuándo puede jugar el siguiente y cuándo recibirá uno nuevo.

---

## 7. Horario del concurso y modo análisis

El bloque **Configuración del grupo principal** (*Main group settings*)
muestra el enlace al **Grupo principal** (*Main group*, al crear el
concurso se llama `default`) y sus horarios. Los horarios pertenecen al
**grupo**, no al concurso: cada participación sigue los horarios de su
grupo ([sección 9](#9-grupos)). Lo que se escribe aquí es el horario del
grupo principal, que además es el que usan el ranking de la
administración, el scoreboard y los visitantes que todavía no entraron.

### Horario del concurso (Contest time)

| Campo | Qué hace | Por defecto | Ejemplo |
|---|---|---|---|
| **Hora de inicio (en *zona*)** (*Start time (in zone)*) | desde cuándo se puede competir | 2000-01-01 00:00:00 UTC | `2026-11-14 09:00:00` |
| **Hora de fin (en *zona*)** (*End time (in zone)*) | hasta cuándo | 2100-01-01 00:00:00 UTC | `2026-11-14 14:00:00` |
| **Duración del concurso** (*Length of the contest*) | ventana personal, en **segundos**; vacío = concurso tradicional | vacío | vacío, o `10800` |

Lo que ve el concursante según la hora:

- **Antes del inicio**: puede entrar y leer los mensajes, pero la página
  dice *La competencia no ha comenzado aún.* (*The contest hasn't started
  yet.*) y no ve los problemas.
- **Entre inicio y fin**: ve los enunciados, envía, hace pruebas y juega
  tokens. Un reloj a la izquierda cuenta el tiempo que le queda.
- **Después del fin**: sigue viendo enunciados y sus envíos, pero no
  puede enviar (salvo modo análisis o la opción de envíos no oficiales).

El inicio no puede ser posterior al fin. Ambos son obligatorios
(*No main group start time specified.* si deja el inicio vacío).

Para dar más tiempo **a todos** durante el concurso, cambie la *Hora de
fin* y haga clic en **Actualizar**. Para dar tiempo a **un solo
concursante** (se le cortó la luz, por ejemplo), use *Tiempo extra* o
*Retraso* en su participación ([capítulo 9](09-durante-el-concurso.md)).

### Ventana por concursante (concursos tipo USACO)

Si escribe una **Duración del concurso**, el concurso pasa a tener
**ventanas personales**: entre la hora de inicio y la de fin, cada
concursante elige cuándo empezar y desde ese momento tiene esa cantidad
de segundos.

1. El concursante entra entre el inicio y el fin y ve el botón
   **¡Comenzar!** (*Start!*) en la *Carátula*.
2. Al apretarlo empieza su ventana: ve los problemas y puede enviar. Su
   hora de comienzo queda guardada en su participación (*Hora del primer
   ingreso*, *Time of first login*).
3. La ventana termina al cumplirse la duración o a la **hora de fin** del
   concurso, lo que ocurra primero. Quien empieza tarde tiene menos
   tiempo.
4. Quien nunca aprieta *¡Comenzar!* antes del fin ya no puede competir.

Ejemplo: un selectivo que se rinde cualquier momento del fin de semana,
3 horas por persona: inicio `2026-11-20 00:00:00`, fin `2026-11-22
23:59:59`, duración `10800`.

> **Errores comunes.** La duración va en **segundos**: `3` o `180` no son
> 3 horas. Un `0` se acepta pero deja una ventana de cero segundos. Para
> un concurso tradicional (todos a la misma hora), deje el campo
> **vacío**.

### Modo análisis (Analysis mode)

Después del concurso, el modo análisis reabre el sistema para que los
concursantes practiquen y revisen: pueden enviar, los envíos se evalúan
como siempre y ven el **resultado completo** de todos sus envíos, pero
todo lo enviado es **no oficial** (no cambia el ranking). No hay tokens ni
pruebas de usuario.

| Campo | Qué hace | Por defecto |
|---|---|---|
| **Habilitado** (*Enabled*) | activa el modo análisis | desmarcada |
| **Inicio del modo análisis (en *zona*)** (*Analysis mode start time (in zone)*) | desde cuándo | 2100-01-01 00:00:00 UTC |
| **Fin del modo análisis (en *zona*)** (*Analysis mode end time (in zone)*) | hasta cuándo | 2100-01-01 00:00:00 UTC |

Reglas:

- El inicio del análisis **no puede ser anterior a la hora de fin** del
  concurso, y el fin del análisis no puede ser anterior a su inicio. Esto
  se exige **aunque el modo esté deshabilitado**: si mueve la hora de fin
  del concurso más allá de un inicio de análisis ya escrito, **La
  operación falló.** (`groups_check1`). Corrija las dos horas a la vez.
- A un concursante con tiempo extra o retraso el análisis le empieza
  cuando termina su tiempo. Si va a dar tiempo extra, deje un margen entre
  el fin del concurso y el inicio del análisis.
- Los envíos del análisis cuentan para la *Cantidad máxima de envíos*
  ([sección 8](#8-límites)).

Ejemplo para la OI 2026: habilitado, del `2026-11-14 16:00:00` al
`2026-11-21 23:59:59`.

---

## 8. Límites

Último bloque, **Límites** (*Limits*). Todos son opcionales: **vacío =
sin límite**. No admiten `0` (dé **La operación falló.**).

| Campo | Qué hace | Unidad | Ejemplo |
|---|---|---|---|
| **Cantidad máxima de envíos** (*Maximum number of submissions*) | envíos por concursante en todo el concurso, sumando todos los problemas | envíos | `100` |
| **Cantidad máxima de pruebas de usuario** (*Maximum number of user tests*) | pruebas por concursante en todo el concurso | pruebas | `200` |
| **Intervalo mínimo entre envíos** (*Minimum interval between submissions*) | espera obligatoria después de cada envío, en cualquier problema | segundos | `30` |
| **Período de gracia del intervalo mínimo entre envíos** (*Minimum submission interval grace period*) | en los últimos *N* segundos del tiempo del concursante, no se exige el intervalo anterior | segundos | `300` |
| **Intervalo mínimo entre pruebas de usuario** (*Minimum interval between user tests*) | espera obligatoria entre pruebas | segundos | `30` |

Cómo se aplican:

- Cada problema tiene sus propios límites del mismo tipo
  ([capítulo 5](05-problemas.md)). Un envío se acepta solo si cumple los
  del concurso **y** los del problema.
- La cantidad máxima cuenta **todos** los envíos del concursante en el
  concurso, también los no oficiales (por ejemplo, los del modo análisis).
- El intervalo se mide desde el último envío del concursante en
  **cualquier** problema del concurso.
- El período de gracia solo afecta al intervalo entre **envíos** (no a las
  pruebas ni a la cantidad máxima). Se cuenta hasta el final del tiempo de
  cada concursante, incluido su tiempo extra.
- Las **participaciones sin restricciones** (*Participación sin
  restricciones*, en la página de la participación) no tienen límites ni
  horario: útiles para el staff que prueba el concurso.

La *Carátula* del concursante informa la cantidad máxima de envíos y de
pruebas (por ejemplo, *Puede realizar a lo sumo 100 envíos de solución
durante esta competencia.*). Cuando un envío se rechaza, el concursante ve
el motivo; por ejemplo, con un intervalo de 30 segundos:

> **¡Envíos demasiado frecuentes!** Entre todos los problemas, puede
> realizar un envío nuevamente pasados 30 segundos desde último envío.

Por qué limitar: el intervalo mínimo evita que alguien sature el juez con
envíos seguidos (en un servidor de 2 vCPU con 30 concursantes, unos pocos
envíos por segundo ya forman cola); el período de gracia evita que esa
espera le quite a alguien el último envío en los minutos finales.

---

## 9. Grupos

### Qué es un grupo

Un **grupo** es un conjunto de participaciones que comparten **horario**:
inicio, fin, duración de la ventana personal y modo análisis. Cada
participación pertenece a un solo grupo de su concurso. Los problemas, las
reglas de ingreso, los tokens y los límites son los mismos para todos los
grupos: lo único que cambia es el horario.

Cuándo usarlos:

- **Dos turnos**: una sede rinde de 09:00 a 14:00 y otra de 15:00 a 20:00.
- **Una sede que empieza más tarde** por un problema de transporte.
- **Una zona horaria distinta**: las horas de CMS son absolutas, así que
  una sede en otra zona que deba empezar "a las 9 de su hora" necesita su
  propio grupo con el horario convertido.

### El grupo principal

Todo concurso tiene un **grupo principal** (*Main group*). Al crear el
concurso es `default`. Es especial porque:

- es el grupo que se propone al agregar un usuario al concurso, el que
  reciben los usuarios importados por CSV con *Agregarlos también al
  concurso* (*Also add them to contest*) y el de quienes se inscriben
  solos;
- sus horarios son los que usan el ranking de la administración y el
  scoreboard, y los que ven los visitantes que todavía no entraron;
- su horario se edita también desde la página del concurso.

### Lista de grupos

Menú del concurso → **Grupos** (*Groups*). La página **Lista de grupos**
(*Groups list*) muestra cada grupo con su **Inicio (en *zona*)**, **Fin
(en *zona*)**, **Duración máx. del concurso (en segundos)** (*Max. contest
length (in seconds)*), **Análisis** (*Analysis*), **Inicio del análisis** y
**Fin del análisis**. El principal lleva la marca **(grupo principal)**
(*(main group)*). La columna del nombre dice **Título** (*Name*), como en
la página del concurso.

### Crear un grupo con otro horario

Ejemplo: la OI 2026 tiene una sede, "Sur", que empieza una hora más
tarde.

1. Menú del concurso → **Grupos**.
2. En **Agregar un grupo nuevo llamado** (*Add a new group named*)
   escriba `sede-sur` y haga clic en **Agregar grupo** (*Add group*). El
   grupo nuevo se crea con una **copia de los horarios del grupo
   principal** y se abre su página.
3. En la página **Grupo sede-sur en oi2026** (*Group … in …*), sección
   **Información del grupo** (*Group information*), cambie **Hora de
   inicio** a `2026-11-14 10:00:00` y **Hora de fin** a
   `2026-11-14 15:00:00`. Los campos son los mismos que los del horario
   del concurso ([sección 7](#7-horario-del-concurso-y-modo-análisis)),
   más el nombre (que acá también aparece como **Título**).
4. Haga clic en **Actualizar**.

El nombre del grupo es libre, pero no puede repetirse dentro del
concurso.

> **Si el grupo tiene modo análisis**, recuerde que su inicio no puede ser
> anterior al fin **de ese grupo**: al atrasar el fin de `sede-sur`,
> revise también sus horas de análisis.

### Poner concursantes en un grupo

**Uno por uno:**

1. Menú del concurso → **Usuarios** (*Users*).
2. Para un usuario que todavía no está en el concurso: en **Agregar un
   usuario:** (*Add a new user:*) elija el usuario en **Elija un usuario**
   (*Select a new user*), elija el grupo en la lista de al lado (viene
   seleccionado el principal) y haga clic en **Agregar usuario** (*Add
   user*).
3. Para **mover** a alguien que ya está: haga clic en su nombre de usuario
   en la **Lista de usuarios** (*Users list*). En la página de su
   participación, sección **Información de la participación**
   (*Participation information*), cambie **Grupo** (*Group*) y haga clic
   en **Actualizar**.

**Muchos a la vez** (la importación por CSV siempre usa el grupo
principal):

1. En **Grupos**, seleccione `sede-sur` y haga clic en **Hacer grupo
   principal** (*Make main group*).
2. En la página global de **Usuarios**, sección **Importar usuarios**
   (*Import users*), suba el CSV de la sede Sur con **Agregarlos también
   al concurso** = `oi2026` y haga clic en **Importar** (*Import*)
   ([capítulo 8](08-usuarios-y-equipos.md)).
3. Vuelva a **Grupos**, seleccione `default` y haga clic en **Hacer grupo
   principal**.

> Los usuarios que ya tenían participación en el concurso no se mueven al
> importar: quedan en su grupo.

La página de cada grupo lista sus usuarios (**Usuarios**, con nombre de
usuario, nombre y apellido; hasta 1000).

### Cambiar el grupo principal

En **Grupos**, seleccione el grupo y haga clic en **Hacer grupo
principal**. No mueve a ningún concursante: solo cambia qué grupo se
propone por defecto y qué horario usan el ranking y el scoreboard.

### Eliminar un grupo

En **Grupos**, seleccione el grupo y haga clic en **Eliminar el grupo
seleccionado** (*Delete selected group*). No se puede:

- si tiene participaciones: **No se puede eliminar un grupo con
  usuarios** (*Cannot delete group containing users*). Mueva antes a sus
  concursantes;
- si es el principal: **No se puede eliminar el grupo principal de un
  concurso.** (*Cannot delete a contest's main group.*). Haga principal a
  otro grupo primero.

Si hace clic en un botón sin seleccionar un grupo aparece **Campo(s)
inválido(s)** con `Please select a valid group`.

---

## 10. Los problemas del concurso

Menú del concurso → **Problemas** (*Tasks*). La página se titula **Lista
de tareas de oi2026** (*Tasks list of oi2026*): en español la
administración llama "problemas" al menú y "tareas" a esta página; son lo
mismo.

> En esta tabla, las dos columnas aparecen como **Título** en español: la
> primera es el **nombre corto** del problema (*Name*) y la segunda su
> título (*Title*).

### Agregar problemas

Un problema pertenece a **un solo concurso** a la vez. La lista
**Agregar una tarea:** (*Add a new task:*) solo ofrece los problemas que
**no están en ningún concurso**.

1. Cree o importe los problemas ([capítulo 5](05-problemas.md) y
   [capítulo 7](07-importar.md)). Al importar un paquete puede elegir el
   concurso directamente y saltarse este paso.
2. En **Agregar una tarea:** elija el problema en **Elija una tarea**
   (*Select a new task*) y haga clic en **Agregar tarea** (*Add task*).
3. El problema se agrega **al final** de la lista. Repita para cada uno.

Si el problema que busca no aparece, está en otro concurso: quítelo de
ese concurso primero (abajo).

### Ordenar

El orden de la lista es el orden en que los concursantes ven los
problemas en el sitio y el de las columnas de los rankings. Para mover
uno:

1. Seleccione el problema con el botón redondo de su fila.
2. En **Mover la tarea seleccionada:** (*Move selected task:*) haga clic
   en **al principio** (*to the top*), **una arriba** (*up by 1*), **una
   abajo** (*down by 1*) o **al final** (*to the bottom*).

Ejemplo para la OI 2026: agregue `suma`, `grafos` y `dp` en ese orden; si
quiere que `dp` sea el primero, selecciónelo y haga clic en **al
principio**.

### Quitar un problema del concurso

Seleccione el problema y haga clic en **Quitar del concurso** (*Remove
from contest*) (bajo **Quitar la tarea seleccionada del concurso:**,
*Remove selected task from the contest:*). El problema **no se borra**:
queda sin concurso, con todos sus datos, y puede agregarse a este u otro
concurso. Para borrar un problema del todo, use la página del problema
([capítulo 5](05-problemas.md)).

Agregar, quitar y reordenar problemas queda en el **Registro de
auditoría**. Hágalo **antes** del concurso: durante el concurso los
concursantes verían cambiar la lista.

---

## 11. Eliminar un concurso

1. En la página del concurso (**General**), haga clic en **Eliminar**
   (*Remove*), a la derecha, al pie. También puede hacerlo desde **Lista
   de concursos** (*Contests list*): seleccione el concurso y haga clic en
   **Eliminar** junto a **Editar el concurso seleccionado:** (*Edit
   selected contest:*).
2. Se abre **Eliminar concurso** (*Remove Contest*), que dice cuánto se va
   a borrar, por ejemplo:

   > ¿Seguro que quiere eliminar el concurso oi2026 de CMS, con todos sus
   > datos?
   > Se eliminarán 30 participación(es) y 412 envío(s).
   > Esta operación no se puede deshacer.

3. Haga clic en **Sí, eliminar** (*Yes, remove*) para confirmar o en **No**
   para volver.

Qué se borra y qué no:

| Se borra | No se borra |
|---|---|
| el concurso y sus grupos | los **usuarios** (siguen existiendo para otros concursos) |
| todas sus participaciones, con sus envíos, pruebas de usuario, tokens, preguntas y mensajes | los **equipos** |
| sus anuncios | los problemas que **quitó del concurso antes** de eliminarlo |
| **los problemas que todavía están en el concurso**, con enunciados, adjuntos, datasets y casos de prueba | los administradores |
| | las entradas del **Registro de auditoría** (quedan, sin el vínculo al concurso) |

> **Los problemas se borran con el concurso.** Si quiere conservar un
> problema (para reutilizarlo el año próximo, por ejemplo), quítelo del
> concurso **antes** de eliminarlo ([sección 10](#10-los-problemas-del-concurso)).
> No hay papelera: haga un respaldo antes de eliminar algo importante
> ([capítulo 13](13-mantenimiento.md)).

Solo un administrador con **Todos los permisos** puede eliminar; para los
demás la página dice **No tiene permiso para eliminar un concurso.** (*You
do not have permission to remove a contest.*).

---

## 12. Configuraciones listas para usar

Los valores no mencionados quedan como vienen por defecto. Los horarios
suponen que la zona del servidor es la del lugar del concurso.

### Olimpiada de 5 horas (OI 2026: 3 problemas, 30 concursantes)

Estilo IOI moderno: sin tokens, mejor resultado por subtarea, laboratorio
con computadoras fijas y análisis al terminar.

| Bloque | Campo | Valor | Por qué |
|---|---|---|---|
| Información | Título (nombre) | `oi2026` | dirección corta |
| Información | Descripción | `Olimpiada de Informática 2026` | lo que ven todos |
| Información | Idiomas permitidos | `es` | toda la interfaz en español |
| Información | Lenguajes de programación permitidos | C11 / gcc, C++20 / g++, Python 3 / CPython | los que se enseñan; **desmarque Pascal / fpc** si no está instalado |
| Información | Permitir descargar los envíos | marcada | |
| Información | Permitir preguntas | marcada | |
| Información | Permitir pruebas de usuario | marcada | |
| Información | Decimales del puntaje | `0` | puntajes enteros |
| Información | Permitir envíos no oficiales antes del modo análisis | desmarcada | |
| Información | Zona horaria | vacío | no tiene efecto |
| Ingreso | Bloquear participaciones ocultas | marcada | las cuentas de prueba no se usan el día del concurso |
| Ingreso | Permitir el ingreso con contraseña | marcada | |
| Ingreso | Permitir la inscripción | desmarcada | lista cerrada de concursantes |
| Ingreso | Restricción de ingreso por IP | marcada | con la IP de cada puesto en su participación |
| Ingreso | Ingreso automático por IP | desmarcada | |
| Tokens | Modo de tokens | Desactivados | |
| Horario | Hora de inicio | `2026-11-14 09:00:00` | |
| Horario | Hora de fin | `2026-11-14 14:00:00` | 5 horas |
| Horario | Duración del concurso | vacío | todos a la misma hora |
| Modo análisis | Habilitado | marcada | práctica después del concurso |
| Modo análisis | Inicio | `2026-11-14 16:00:00` | margen de 2 horas por si hay tiempo extra |
| Modo análisis | Fin | `2026-11-21 23:59:59` | una semana |
| Límites | Cantidad máxima de envíos | vacío | |
| Límites | Cantidad máxima de pruebas de usuario | vacío | |
| Límites | Intervalo mínimo entre envíos | `30` | protege al juez |
| Límites | Período de gracia | `300` | sin espera en los últimos 5 minutos |
| Límites | Intervalo mínimo entre pruebas de usuario | `30` | |

Además, en **cada problema**: *Modo de tokens* = Desactivados y *Modo de
puntaje* = *Usar la suma, por subtarea, del mejor resultado de esa
subtarea entre todos los envíos (IOI 2017-)* ([capítulo 5](05-problemas.md)).

### Torneo escolar de 3 horas

Un colegio, 3 problemas fáciles, alumnos que usan Python o C++. Sin IP
fijas, sin modo análisis.

| Bloque | Campo | Valor | Por qué |
|---|---|---|---|
| Información | Título (nombre) | `escolar2026` | |
| Información | Descripción | `Torneo Escolar de Programación 2026` | |
| Información | Idiomas permitidos | `es` | |
| Información | Lenguajes de programación permitidos | C++20 / g++, Python 3 / CPython | **desmarque** C11 / gcc y Pascal / fpc |
| Información | Permitir descargar los envíos | marcada | |
| Información | Permitir preguntas | marcada | |
| Información | Permitir pruebas de usuario | desmarcada | menos carga y una pantalla menos que explicar |
| Información | Decimales del puntaje | `0` | |
| Ingreso | Bloquear participaciones ocultas | desmarcada | |
| Ingreso | Permitir el ingreso con contraseña | marcada | |
| Ingreso | Permitir la inscripción | desmarcada | cargue a los alumnos por CSV |
| Ingreso | Restricción de ingreso por IP | marcada | sin IP en las participaciones no restringe nada |
| Ingreso | Ingreso automático por IP | desmarcada | |
| Tokens | Modo de tokens | Desactivados | |
| Horario | Hora de inicio | `2026-11-20 15:00:00` | |
| Horario | Hora de fin | `2026-11-20 18:00:00` | 3 horas |
| Horario | Duración del concurso | vacío | |
| Modo análisis | Habilitado | desmarcada | |
| Límites | Cantidad máxima de envíos | `60` | 20 por problema en promedio |
| Límites | Intervalo mínimo entre envíos | `20` | |
| Límites | Período de gracia | `120` | |
| Límites | Las demás | vacío | |

En cada problema: *Modo de tokens* = Desactivados y *Modo de puntaje* =
*Usar el mejor de todos los envíos (IOI 2013-2016)* (*Use best among all
submissions (IOI 2013-2016)*), que es fácil de explicar a los alumnos.

Si prefiere que los alumnos se inscriban solos: marque **Permitir la
inscripción** y, después de la hora de inicio, desmárquela para que nadie
más se agregue.

---

## 13. Errores comunes

| Error | Consecuencia | Cómo evitarlo |
|---|---|---|
| No poner el horario al crear el concurso | queda abierto (2000–2100): los concursantes ven los problemas antes de tiempo | escriba inicio y fin apenas lo crea |
| Escribir los horarios con la zona del servidor equivocada | el concurso empieza horas antes o después | mire la zona en la etiqueta (*en America/…*); cámbiela en **Servidor** antes |
| Nombre con espacios o acentos | **La operación falló.** (`contests_name_check`) | solo letras sin acentos, números, `_`, `-` |
| Fecha sin segundos u otro formato | **Campo(s) inválido(s).** (`Can't cast … to datetime.`) | `AAAA-MM-DD HH:MM:SS` |
| `0` en un límite para decir "sin límite" | **La operación falló.** | deje el campo vacío |
| Dejar vacío un campo de tokens o los decimales | **Campo(s) inválido(s).** (`Can't cast  to int.` o `to timedelta.`) | ponga `0` u otro número |
| Confundir unidades | intervalos 60 veces más largos o cortos | *Período de generación de tokens* en **minutos**; todo lo demás en **segundos** |
| Dejar marcado un lenguaje sin compilador (Pascal / fpc por defecto) | los envíos en ese lenguaje fallan | marque solo lenguajes instalados en los jueces |
| Tokens del concurso en *Infinitos* esperando que haya tokens | no aparecen: los problemas vienen en *Desactivados* | configure también los problemas |
| Modo de puntaje por defecto sin tokens | cuenta el **último** envío, no el mejor | elija el modo de puntaje en cada problema |
| Hacer clic en **Borrar** creyendo que borra algo | solo descarta lo no guardado | |
| Confiar en el campo *Zona horaria* del concurso | no cambia nada | use la página **Servidor** |
| Renombrar el concurso ya publicado | cambia la dirección, se cierran las sesiones, cambia el enlace secreto del scoreboard | no lo renombre; cambie la *Descripción* |
| Eliminar un concurso con problemas que quería conservar | los problemas se borran | quítelos del concurso antes |

---

## 14. Qué hacer si…

### …aparece "La operación falló." al guardar

El texto en inglés nombra la regla que no se cumplió:

| Mensaje (`…constraint "X"`) | Causa | Solución |
|---|---|---|
| `contests_name_check` | el nombre tiene caracteres no permitidos | use solo `A-Z`, `a-z`, `0-9`, `_`, `-` |
| `duplicate key … "contests_name_key"` | ya existe un concurso con ese nombre | elija otro |
| `contests_max_submission_number_check`, `contests_max_user_test_number_check` | cantidad máxima `0` o negativa | vacío o un número positivo |
| `contests_min_submission_interval_check`, `contests_min_submission_interval_grace_period_check`, `contests_min_user_test_interval_check` | intervalo o período de gracia `0` o negativo | vacío o un número positivo |
| `contests_token_max_number_check`, `contests_token_gen_max_check` | *Cantidad máxima de tokens* o *Máximo de tokens acumulados* en `0` | vacío o positivo |
| `contests_check` | *Máximo de tokens acumulados* menor que la *Cantidad inicial de tokens* | suba el máximo o baje la inicial |
| `contests_token_gen_interval_check` | *Período de generación de tokens* `0` | un número positivo |
| `contests_token_gen_initial_check`, `contests_token_gen_number_check`, `contests_token_min_interval_check`, `contests_score_precision_check` | número negativo | `0` o más |
| `groups_check` | la hora de inicio es posterior a la de fin | corrija las horas |
| `groups_check1` | la hora de fin es posterior al inicio del modo análisis (aunque esté deshabilitado) | mueva el inicio y el fin del análisis después del fin del concurso |
| `groups_check2` | el fin del análisis es anterior a su inicio | corrija las horas del análisis |
| `groups_per_user_time_check` | *Duración del concurso* negativa | vacío o positivo |
| `duplicate key … "groups_contest_id_name_key"` | ya hay un grupo con ese nombre en el concurso | elija otro nombre |

No se guardó nada: corrija el campo y vuelva a hacer clic en
**Actualizar**. El resto de lo que escribió sigue en el formulario.

### …aparece "Campo(s) inválido(s)."

| Mensaje | Causa |
|---|---|
| `Can't cast X to datetime.` | fecha con otro formato o sin segundos |
| `Can't cast  to datetime.` / `No main group start time specified.` / `No main group stop time specified.` | inicio o fin vacío |
| `Can't cast X to int.` | número con letras, puntos o vacío en un campo obligatorio |
| `Can't cast X to timedelta.` | duración no numérica, o vacía en *Intervalo mínimo entre tokens* o *Período de generación de tokens* |
| `Unknown time zone X.` | *Zona horaria* del concurso inválida: déjela vacía |
| `No contest name specified.` | nombre vacío |

### …los concursantes ven "La competencia no ha comenzado aún." y ya debería haber empezado

1. Revise **Hora de inicio** en la página del concurso y la zona que
   indica la etiqueta.
2. Revise en **Grupos** a qué grupo pertenece el concursante y el inicio
   de ese grupo.
3. Si el concurso tiene **Duración del concurso**, el concursante debe
   apretar **¡Comenzar!** en la *Carátula*.
4. Revise que su participación no tenga **Retraso** (*Delay*).

### …un concursante no puede entrar ("Acceso denegado.")

Revise, en este orden: que tenga participación en el concurso (menú
**Usuarios** del concurso); que **Permitir el ingreso con contraseña**
esté marcada; si **Restricción de ingreso por IP** está marcada, la
*Dirección IP o subred* de su participación; si **Bloquear participaciones
ocultas** está marcada, que su participación no sea oculta. Más causas en
el [capítulo 14](14-solucion-de-problemas.md).

### …no aparecen los tokens

El concurso y los problemas deben estar en *Finitos* o *Infinitos*; con
cualquiera de los dos en *Desactivados* no hay tokens. Recuerde que los
problemas nuevos vienen en *Desactivados*.

### …el problema que quiero agregar no aparece en "Elija una tarea"

Está en otro concurso. Ábralo en la **Lista de tareas** de ese concurso,
haga clic en **Quitar del concurso** y vuelva a intentar.

### …no puedo eliminar un grupo

Tiene participaciones (muévalas a otro grupo desde la página de cada
participación) o es el grupo principal (haga principal a otro primero).

### …los concursantes no pueden enviar en un lenguaje

Revise que esté marcado en **Lenguajes de programación permitidos** y en
la lista del problema, y que el compilador esté instalado en los jueces
([Jueces](../jueces.md)). Si los envíos se aceptan pero fallan al
compilar, vea el [capítulo 14](14-solucion-de-problemas.md).

### …el sitio del concursante sale en inglés

Revise **Idiomas permitidos**: vacío deja elegir al navegador; un código
mal escrito (`es_CL`, `sp`) deja el sitio en inglés. Use `es` o `es-CL`.

---

**Siguiente:** [5. Problemas](05-problemas.md) ·
**Anterior:** [3. Primer acceso](03-primer-acceso.md) ·
[Índice](README.md)

# 1. Conceptos

*English: [Concepts](../../en/manual/01-concepts.md)*

Este capítulo explica lo que conviene entender antes de tocar nada: qué hace
CMS, cuáles son sus piezas, qué le pasa a un envío desde que el concursante
aprieta *Enviar* hasta que su puntaje aparece en el scoreboard, y qué
significa cada palabra que vas a ver en las pantallas. Los demás capítulos
dicen **cómo** se hace cada cosa; este dice **qué es** y **por qué funciona
así**. El [glosario](#glosario) sirve también de consulta rápida.

En todo el manual usamos el mismo ejemplo: el concurso **OI 2026**, con 3
problemas (`suma`, `caminos` y `salidas`) y 30 concursantes (`oi01` …
`oi30`).

## Contenido

1. [Qué es CMS y qué no es](#qué-es-cms-y-qué-no-es)
2. [Los tres sitios](#los-tres-sitios)
3. [Los servicios que trabajan por detrás](#los-servicios-que-trabajan-por-detrás)
4. [El viaje de un envío](#el-viaje-de-un-envío)
5. [Cómo se relacionan las piezas](#cómo-se-relacionan-las-piezas)
6. [Glosario](#glosario)
7. [El idioma de la interfaz](#el-idioma-de-la-interfaz)
8. [Horas y zonas horarias](#horas-y-zonas-horarias)
9. [El ciclo de vida de un concurso](#el-ciclo-de-vida-de-un-concurso)
10. [Qué hacer si…](#qué-hacer-si)

---

## Qué es CMS y qué no es

**CMS** (*Contest Management System*) es el sistema con el que la IOI
(Olimpiada Internacional de Informática) y muchas olimpiadas nacionales
corren sus concursos de programación. Los concursantes leen enunciados y
envían código fuente; un juez automático lo compila, lo ejecuta en una caja
aislada contra casos de prueba, calcula el puntaje y lo publica.

Esta instalación es una versión de CMS con el servidor reescrito en Go.
Conserva las páginas, el comportamiento y el modelo de datos del CMS
original (<https://github.com/cms-dev/cms>): quien conozca CMS lo va a
reconocer. Agrega, entre otras cosas, un instalador de un solo comando, la
administración traducida al español, páginas para preparar y probar
problemas desde el navegador, una página de jueces, un registro de
auditoría y actualizaciones en vivo en las tres webs.

**Lo que CMS hace:**

- deja entrar a cada concursante (usuario y contraseña, o por dirección IP);
- recibe los envíos y los juzga en una caja aislada (*sandbox*), con
  límites de tiempo y memoria;
- calcula puntajes por caso y por subtarea, con las reglas que usó la IOI
  en distintos años;
- maneja la comunicación con los concursantes: preguntas, anuncios y
  mensajes privados;
- publica un scoreboard en vivo.

**Lo que CMS no hace** (y conviene saberlo antes de empezar):

- **No escribe los problemas.** Enunciados, casos de prueba y soluciones los
  prepara tu comité. La administración ayuda a cargarlos, a generar salidas
  y a probar soluciones ([Problemas](05-problemas.md)), pero el contenido
  es tuyo.
- **No controla las computadoras ni la red de los concursantes.** Impedir
  que se copien, cortar internet o vigilar la sala es tarea de la
  organización. CMS solo puede limitar desde qué direcciones IP entra cada
  uno ([Usuarios y equipos](08-usuarios-y-equipos.md)).
- **No es un juez en línea abierto.** Cada concurso decide si la gente
  puede inscribirse sola (*Permitir la inscripción*, *Allow registration*,
  desactivado por defecto); lo normal es que el organizador cargue a los
  concursantes.
- **No es un concurso estilo ICPC.** El puntaje de un concursante es la suma
  de sus puntajes en cada problema, como en la IOI; no se cuentan
  penalizaciones por tiempo.

## Los tres sitios

Una instalación tiene **tres sitios web**, cada uno con su dirección:

| Sitio | Quién lo usa | Para qué | Instalación con dominio | Instalación por puertos |
|---|---|---|---|---|
| **Sitio del concursante** | concursantes | entrar, leer enunciados, enviar, ver resultados, preguntar | `https://concurso.ejemplo.org/` | `http://IP:8000/` |
| **Administración** | organizadores, jurado, staff | crear concursos, problemas y usuarios; seguir la evaluación; responder preguntas; ver el ranking | `https://admin.concurso.ejemplo.org/` | `http://IP:8001/` |
| **Scoreboard público** | público, entrenadores, familias | ver la tabla de posiciones en vivo | `https://ranking.concurso.ejemplo.org/` | `http://IP:8002/` |

El instalador muestra al final las direcciones reales de tu instalación
([Instalación](02-instalacion.md)).

**Cómo se relacionan:**

- **El sitio del concursante y la administración usan la misma base de
  datos.** Lo que cambias en la administración (un anuncio, el horario, un
  problema) lo ven los concursantes en la siguiente página que abren, y al
  instante si la página está abierta, gracias a las actualizaciones en vivo.
- **El scoreboard no lee la base de datos.** El *dispatcher* (ver más abajo)
  le envía por HTTP el concurso, los problemas, los usuarios y equipos
  visibles y los puntajes de los envíos oficiales; el scoreboard guarda su
  propia copia (en `/var/lib/cms/ranking` en una instalación normal). Por
  eso puede correr en otro servidor, y por eso solo muestra lo que el
  dispatcher le mandó: los puntajes llegan en cuanto se calculan, y los
  cambios de nombres, usuarios o problemas en hasta 30 segundos
  (`ranking_refresh`). Si el dispatcher se detiene, el scoreboard sigue
  mostrando lo último que recibió.
- **La administración no sabe con qué dirección llegan los concursantes y
  el público.** Los enlaces a los otros dos sitios (por ejemplo, el enlace
  secreto al scoreboard) se arman con lo que pongas en la página *Servidor*
  (*Server*): *Dirección del sitio de concursantes* (*Contestant site
  address*) y *Dirección del scoreboard* (*Scoreboard address*).
- En el sitio del concursante, la página de inicio lista los concursos y
  cada uno vive en `/<nombre corto>`: OI 2026 queda en
  `https://concurso.ejemplo.org/oi2026`. Puede haber varios concursos a la
  vez (por ejemplo, uno de práctica y el oficial).

**Para orientarte en las pantallas:**

- Menú principal de la administración: *Carátula* (*Overview*), *Uso de
  recursos* (*Resource usage*), *Jueces* (*Judges*), *Servidor* (*Server*),
  *Registro de auditoría* (*Audit log*), y las listas *Concursos*
  (*Contests*), *Problemas* (*Tasks*), *Usuarios* (*Users*) y *Equipos*
  (*Teams*).
- Al entrar a un concurso, el menú cambia: *General*, *Ranking*, *Envíos*
  (*Submissions*), *Pruebas de usuario* (*User tests*, solo si el concurso
  las permite), *Usuarios*, *Grupos* (*Groups*), *Problemas*, *Anuncios*
  (*Announcements*), *Preguntas* (*Questions*) y *Registro de auditoría*.
- Menú del concursante: *Carátula* (*Overview*), *Comunicación*
  (*Communication*), por cada problema *Enunciado* (*Statement*) y *Envíos*
  (*Submissions*), *Documentación* (*Documentation*) y *Testing* (las
  pruebas de usuario, si están permitidas). Los problemas aparecen en el
  menú recién cuando empieza el tiempo del concursante, identificados por
  su nombre corto.

## Los servicios que trabajan por detrás

Detrás de las tres webs hay varios programas (servicios). No necesitas
manejarlos a diario, pero entenderlos explica casi todos los problemas que
puedan aparecer.

| Pieza | Unidad de systemd | En palabras simples | Si se detiene… |
|---|---|---|---|
| **PostgreSQL** | `postgresql` | la base de datos: concursos, problemas, usuarios, envíos, resultados, preguntas y el registro de auditoría | nada funciona: es la pieza central |
| **Valkey** (compatible con Redis) | `valkey-server` (o `redis-server`) | memoria rápida compartida: las colas de trabajos del juez, los eventos en vivo y las notificaciones | se detienen la evaluación y las actualizaciones en vivo |
| **Almacén de archivos** | — (un directorio, `/var/lib/cms/blobs`) | enunciados, casos de prueba, archivos enviados y ejecutables, guardados por su huella SHA-256 | no se pueden leer ni guardar archivos |
| **Sitio del concursante** | `cms-contest-web` | la web de los concursantes | los concursantes no pueden entrar |
| **Administración** | `cms-admin-web` | la web del staff | el staff no puede entrar; el concurso sigue |
| **Scoreboard** | `cms-ranking-web` | la web pública con la tabla | el público no ve la tabla; el concurso sigue |
| **Dispatcher** | `cms-dispatcher` | el coordinador: decide qué compilar y qué evaluar, reparte el trabajo entre los jueces con prioridades, guarda los resultados, calcula los puntajes y los envía al scoreboard | los envíos se reciben pero quedan esperando |
| **Juez** (*worker*) | `cms-worker` | compila y ejecuta los programas dentro de *isolate*, la caja aislada; nunca toca la base de datos; puede haber varios, en esta y en otras máquinas | los envíos quedan en *Compilando...* o *Evaluando...* |
| **Monitor** | `cms-monitor` | comprueba que la base de datos y Valkey respondan y publica ese estado para el monitoreo | nada visible para los concursantes |

Algunas ideas clave:

- **Nada se pierde al reiniciar.** Cada servicio se reinicia solo si se cae
  (lo hace systemd), las colas son durables y los resultados se guardan de
  forma que repetirlos no cambia nada. Si un juez se detiene a mitad de un
  trabajo, el trabajo vuelve a la cola y lo toma otro. Además, el
  dispatcher revisa la base de datos cada minuto (`sweep_interval`) en busca
  de trabajo pendiente, así que un aviso perdido solo retrasa la
  evaluación ([Mantenimiento](13-mantenimiento.md)).
- **Un solo dispatcher activo.** Si hay más de uno, uno trabaja y los demás
  esperan para reemplazarlo; en una instalación normal hay uno.
- **Los jueces son los que miden tiempos.** Para un concurso real conviene
  que corran en núcleos o máquinas dedicadas: así la carga de las webs no
  cambia los tiempos medidos. `sudo cmsctl verify-host` revisa que la
  máquina sea apta para juzgar ([Instalación](02-instalacion.md),
  [Jueces](../jueces.md)).
- En las instalaciones con dominio, **Caddy** atiende HTTPS delante de las
  tres webs.
- Los respaldos diarios los hace `cms-backup` (un temporizador de systemd)
  ([Mantenimiento](13-mantenimiento.md)).

## El viaje de un envío

Qué pasa desde que `oi07` envía su solución de `suma` hasta que su puntaje
aparece en el scoreboard. Entre paréntesis, lo que ve el concursante.

```
 concursante ──Enviar──▶ sitio del concursante ──guarda──▶ PostgreSQL + archivos
                                  │ avisa (Valkey)
                                  ▼
                             dispatcher ──trabajos──▶ cola (Valkey) ──▶ juez(es) en isolate
                                  ▲                                        │
                                  └─────────────── resultados ◀────────────┘
                                  │ guarda resultados y puntaje (PostgreSQL)
          ┌───────────────────────┼──────────────────────────┐
          ▼                       ▼                          ▼
 página del concursante   páginas de la administración   scoreboard público
       (en vivo)                 (en vivo)                (HTTP, en vivo)
```

1. **El envío.** En la página *Envíos* (*Submissions*) de `suma`, `oi07`
   elige su archivo y el lenguaje (por ejemplo *C++20 / g++*) en *Enviar
   solución* (*Submit a solution*) y aprieta *Enviar* (*Submit*).
2. **La recepción.** El sitio del concursante comprueba que pueda enviar
   (que esté en su horario, que no haya superado el máximo de envíos ni
   esté enviando demasiado seguido, que el archivo no supere los 100 000
   bytes por defecto). Guarda los archivos en el almacén y el envío en la
   base de datos, y lo marca **oficial** si `oi07` está compitiendo. Después avisa al
   dispatcher por Valkey. (*Envío recibido* (*Submission received*).)
3. **La compilación.** El dispatcher crea un trabajo de compilación para el
   dataset activo de `suma` (y para cada dataset con juzgamiento en segundo
   plano) y lo pone en la cola; los envíos de concursantes tienen la
   prioridad más alta. Un juez libre lo toma, descarga los archivos y
   compila dentro de la caja. (*Compilando...* (*Compiling...*).) Si no
   compila, el envío termina acá con 0 puntos. (*Compilación fallida*
   (*Compilation failed*); el mensaje del compilador está en *detalles*
   (*details*).)
4. **La evaluación.** Con el ejecutable listo, el dispatcher crea **un
   trabajo por caso de prueba**. Cada juez ejecuta el programa con la
   entrada del caso, con los límites de tiempo y memoria del dataset, y
   compara la salida (con *white diff* o con el checker). Cada caso da un
   resultado entre 0 y 1 y un mensaje (*Salida correcta* (*Output is
   correct*), *Tiempo límite excedido* (*Execution timed out*)…). Con varios
   jueces, los casos de un mismo envío se evalúan en paralelo.
   (*Evaluando...* (*Evaluating...*).)
5. **La puntuación.** Con todos los casos listos, el tipo de puntaje calcula
   el **puntaje** del envío (con todos los casos) y el **puntaje público**
   (solo con los casos públicos), y el modo de puntaje del problema
   actualiza el puntaje de `oi07` en `suma`. (*Puntuando...* (*Scoring...*)
   y luego *Evaluado* (*Evaluated*).)
6. **Las pantallas.** El resultado llega sin recargar a la página de
   `oi07` y a las páginas de la administración (*Envíos*, *Ranking*).
7. **El scoreboard.** Si el envío es oficial y la participación de `oi07`
   no es oculta, el dispatcher manda su puntaje al scoreboard al momento, y
   el scoreboard lo muestra en vivo a todos los espectadores (salvo que esté
   congelado, ver [Scoreboard](11-scoreboard.md)).

> **Ojo: el concursante y el scoreboard no ven el mismo número.** El
> concursante ve el **puntaje público** de cada envío (solo los casos
> públicos), y el puntaje completo solo si usó un token en ese envío o en
> modo análisis. La administración y el scoreboard muestran el **puntaje
> completo**. Si quieres que los concursantes vean todo su resultado (como
> en la IOI actual), marca todos los casos como públicos
> ([Tipos de problema y puntaje](06-tipos-de-problema-y-puntaje.md)). Si
> en cambio tu concurso tiene casos privados, un scoreboard público revela
> resultados que los concursantes no conocen: hazlo secreto o congélalo.

Si un envío se queda en *Compilando...* o *Evaluando...*, casi siempre es
porque no hay jueces trabajando: mira la página *Jueces* de la
administración y el capítulo [Solución de problemas](14-solucion-de-problemas.md).
Por consola, `sudo cmsctl judging-status --contest oi2026` cuenta los envíos
de cada problema por estado.

## Cómo se relacionan las piezas

```
Usuarios (oi01 … oi30)        Equipos (esc-norte …)        Administradores
   │  (globales: sirven para todos los concursos)
   ▼
Concurso oi2026 («OI 2026»)
├── Grupos: default (principal), …          ← los horarios
├── Problemas: suma, caminos, salidas
│     ├── Enunciados (es, en) y Adjuntos
│     └── Datasets: «Default» (activo), …
│           ├── límites, tipo de problema, tipo de puntaje
│           ├── Managers (grader.cpp, checker…)
│           └── Casos de prueba (01 público, 02, 03 …)
├── Participaciones (usuario + grupo [+ equipo])
│     ├── Envíos → resultado por dataset → evaluación por caso
│     ├── Pruebas de usuario
│     └── Preguntas y Mensajes privados
└── Anuncios
```

Reglas que conviene recordar:

- **Usuarios, equipos y administradores son globales.** Un usuario existe
  una sola vez y participa en los concursos donde tenga una participación
  (una por concurso).
- **Un problema pertenece a un solo concurso** (o a ninguno mientras se
  prepara).
- **Los nombres cortos son únicos en toda la instalación**: no puede haber
  dos concursos `oi2026`, dos problemas `suma` (aunque estén en concursos
  distintos), dos usuarios `oi07` ni dos equipos `esc-norte`. Solo admiten
  letras, dígitos, `_` y `-`.

## Glosario

Cada término lleva su nombre en inglés, porque así aparece en la
documentación de CMS y en muchos paquetes de problemas.

- [Concurso y horarios](#concurso-y-horarios): concurso, grupo, grupo
  principal, duración por concursante, retraso y tiempo extra, modo
  análisis.
- [Problemas y datos de prueba](#problemas-y-datos-de-prueba): problema,
  enunciado, adjunto, dataset, dataset activo, caso de prueba, caso
  público, subtarea, manager, grader, stub, checker o comparador.
- [Cómo se juzga](#cómo-se-juzga): tipo de problema, tipo de puntaje,
  lenguaje, envío, envío oficial, compilación, evaluación, puntuación, modo
  de puntaje, prueba de usuario, token, nivel de retroalimentación.
- [Personas](#personas): usuario, participación, equipo, participación
  oculta, participación sin restricciones.
- [Comunicación](#comunicación): pregunta (clarificación), anuncio, mensaje
  privado.
- [Resultados](#resultados): ranking, scoreboard.
- [Administración](#administración): administrador, permisos, registro de
  auditoría.

### Concurso y horarios

**Concurso** (*contest*). Un conjunto de problemas, de participantes y de
reglas: horarios, lenguajes, límites, tokens, cómo se entra. Tiene un
**nombre corto**, que va en la dirección y en los comandos (`oi2026`), y una
**descripción**, que es el título que ven los concursantes en la barra
superior («OI 2026»). Se crea en *Concursos* (*Contests*) → *(crear un
concurso...)* (*(create new contest...)*) y se configura en *General*
([Concursos](04-concursos.md)).

> **Ojo:** un concurso recién creado tiene horario del 1 de enero de 2000 al
> 1 de enero de 2100, es decir, está **abierto desde el primer momento**.
> Pon los horarios reales antes de agregar concursantes.

**Grupo** (*group*) y **grupo principal** (*main group*). En CMS los
horarios no son del concurso sino de **grupos** de participantes: cada grupo
tiene inicio, fin, duración por concursante y modo análisis. Todo concurso
nace con un grupo `default`, que es su grupo principal; sus horarios se
editan en *General*, sección *Configuración del grupo principal* (*Main
group settings*). Los grupos adicionales sirven para turnos: por ejemplo,
OI 2026 podría tener un grupo `mañana` (9:00 a 14:00) y otro `tarde`
(15:00 a 20:00). Se administran en *Grupos* (*Groups*). El grupo principal
es el que usan la administración y el scoreboard como horario del
concurso. No se puede borrar el grupo principal ni un grupo con
participantes.

**Duración por concursante** (*per-user time*). Si el grupo tiene
*Duración del concurso* (*Length of the contest*, en segundos), cada
concursante decide cuándo empezar con el botón *¡Comenzar!* (*Start!*) y
tiene ese tiempo, sin pasarse del fin del grupo (estilo USACO). Ejemplo: un
concurso abierto toda una semana con 3 horas (10 800 s) para cada uno.
Vacío significa que todos compiten desde el inicio hasta el fin.

**Retraso y tiempo extra** (*delay*, *extra time*). Ajustes de una sola
participación, en segundos: *Retraso* (*Delay*) corre todo su horario más
tarde y *Tiempo extra* (*Extra time*) alarga su fin. Sirven para compensar
incidentes: si un laboratorio se quedó sin luz 10 minutos, se dan 600
segundos de tiempo extra a los afectados
([Durante el concurso](09-durante-el-concurso.md)).

**Modo análisis** (*analysis mode*). Un período opcional, después del fin
del concurso, en que los concursantes vuelven a entrar, ven el resultado
completo de todos sus envíos y pueden seguir enviando; esos envíos **no son
oficiales** y no cambian ningún puntaje. Se activa en el grupo: *Modo
análisis* (*Analysis mode*) → *Habilitado* (*Enabled*), con su inicio y su
fin, que no puede empezar antes del fin del concurso. Ejemplo: OI 2026
termina a las 14:00 y el análisis va de 16:00 a 20:00
([Después del concurso](12-despues-del-concurso.md)).

### Problemas y datos de prueba

**Problema** (*task*). Lo que los concursantes tienen que resolver. Tiene un
**nombre corto** (`suma`), que los concursantes ven en el menú, y un
**título** («Suma de dos números»), que ven en el enunciado. Contiene
enunciados, adjuntos, reglas de envío (lenguajes, límites, tokens,
retroalimentación, modo de puntaje) y uno o más datasets. Se crea en
*Problemas* (*Tasks*) → *(crear un problema...)* (*(create new task...)*)
o importando un paquete ([Problemas](05-problemas.md),
[Importar](07-importar.md)). El **formato de envío** por defecto es
`suma.%l`, donde `%l` representa la extensión del lenguaje.

**Enunciado** (*statement*). El texto del problema, uno por idioma, con un
código como `es`, `en` o `es_MX`. Puede ser PDF (como en CMS), Markdown
(se muestra dentro del sitio) o fuente LaTeX (se descarga). Los que marques
como principales (*¿Principal?*, *Primary?*) se destacan para todos. En la
administración están en la sección *Enunciados* (*Statements*) del
problema; el concursante los ve en *Enunciado* (*Statement*).

**Adjunto** (*attachment*). Un archivo que los concursantes descargan desde
la página del problema: entradas de ejemplo, un grader para probar en su
computadora, una plantilla. Sección *Adjuntos* (*Attachments*).

**Dataset** y **dataset activo** (*active dataset*). Un dataset es una
versión de los datos con que se juzga un problema: límites de tiempo y
memoria, tipo de problema, tipo de puntaje, managers y casos de prueba. Un
problema puede tener varios; el **activo** es el que da los puntajes
oficiales. ¿Para qué tener más de uno? Para corregir sin riesgo durante el
concurso: *Clonar* (*Clone*) el dataset, arreglar el caso equivocado en la
copia, usar *Activar el juzgamiento en segundo plano* (*Enable background
judging*) para que los envíos se juzguen también ahí sin afectar a nadie y,
cuando todo está bien, *Activar ...* (*Make Live ...*), que antes de
confirmar muestra qué puntajes cambian. Todo problema nuevo nace con un
dataset `Default` ya activo.

**Caso de prueba** (*testcase*) y **caso público**. Un par de entrada y
salida esperada con un nombre (`01`, `02`…; conviene numerar con ceros a la
izquierda porque se ordenan como texto: `10` va antes que `2`). Un caso
marcado como *Público* (*Public*) es uno cuyo resultado el concursante ve
sin gastar tokens; el de los demás casos solo lo ve con un token o en modo
análisis. Por defecto un caso **no** es público. Los concursantes nunca ven
las entradas ni las salidas de los casos, solo su resultado.

**Subtarea** (*subtask*). Un grupo de casos que vale una cantidad de puntos
y se califica en conjunto, por ejemplo «subtarea 1: N ≤ 1000, 30 puntos,
casos 01 a 05». Existe solo con los tipos de puntaje por grupos (GroupMin,
GroupMul, GroupThreshold). El concursante la ve como *Subtarea 1*
(*Subtask 1*) en los detalles de su envío. Una subtarea cuenta para el
puntaje público solo si **todos** sus casos son públicos.

**Manager**. Cualquier archivo que el jurado agrega al dataset para
compilar o evaluar: un grader, un checker, el manager de un problema
Communication, el controlador de uno Interactive, cabeceras como `suma.h`.
Sección *Managers* del problema.

**Grader**. Un programa del jurado que se compila **junto** con el código
del concursante: lee la entrada, llama a las funciones que escribió el
concursante y escribe la salida. Así el concursante solo implementa una
función (por ejemplo `long long suma(int a, int b)`). Hace falta un grader
por cada lenguaje permitido (`grader.cpp`, `grader.c`, `grader.py`,
`grader.java`…). Se usa eligiendo, en el parámetro *Compilación*
(*Compilation*) del tipo de problema, *Los envíos se compilan con un
grader* (*Submissions are compiled with a grader*) en lugar de *Los envíos
son autosuficientes* (*Submissions are self-sufficient*).

**Stub**. En la jerga de las olimpiadas, el esqueleto o grader simplificado
que se entrega a los concursantes para que prueben en su computadora
(normalmente como adjunto). CMS no tiene un campo «stub»: si un paquete
italy_yaml trae `sol/stub.<ext>`, se guarda como el grader `grader.<ext>`.
En las pruebas de usuario de un problema con grader, el concursante sube
su propio grader; por eso conviene darle uno.

**Checker** o **comparador** (*checker*, *comparator*). Lo que decide si la
salida del concursante es correcta. Se elige en el parámetro *Evaluación de
la salida* (*Output evaluation*) del tipo de problema:

| Opción | Qué hace |
|---|---|
| *Salidas comparadas ignorando espacios (white diff)* (*Outputs compared with white diff*) | iguales salvo diferencias de espacios; es la opción de un problema nuevo |
| *Salidas comparadas byte a byte* (*Outputs compared byte by byte*) | idénticas byte a byte |
| *Salidas comparadas palabra por palabra, los números con tolerancia 1e-6* (*Outputs compared token by token, numbers with a tolerance of 1e-6*) | para respuestas con decimales |
| *Las salidas las compara un comparador* (*Outputs are compared by a comparator*) | un programa del jurado llamado `checker` (protocolo de CMS): recibe la entrada, la salida correcta y la del concursante, y escribe un puntaje entre 0 y 1 y un mensaje |
| *Las salidas las revisa un checker de testlib* (*Outputs are checked by a testlib checker*) | un checker escrito con testlib, el estándar de Codeforces/Polygon |

### Cómo se juzga

**Tipo de problema** (*task type*). Cómo se compila y se ejecuta un envío
([detalle](06-tipos-de-problema-y-puntaje.md)):

| Tipo | En pocas palabras | Ejemplo en OI 2026 |
|---|---|---|
| Batch | un programa lee la entrada y escribe la salida, solo o con grader | `suma`, `caminos` |
| BatchAndOutput | como Batch, pero algunos casos se pueden responder enviando directamente el archivo de salida | — |
| OutputOnly | no se envía código: el concursante envía archivos de salida (`output_<caso>.txt`), y los que faltan se toman de su envío anterior | `salidas` |
| Communication | el programa del concursante conversa con un manager del jurado que tiene la entrada en secreto | juegos de adivinanzas |
| Interactive | un controlador del jurado lanza instancias de la solución y conversa con ellas (agregado reciente en CMS, menos probado) | — |
| TwoSteps | dos programas unidos por un tubo; CMS desaconseja usarlo en problemas nuevos (mejor Communication) | — |

**Tipo de puntaje** (*score type*). Cómo el resultado de cada caso (entre 0
y 1) se convierte en puntos. Campos *Tipo de puntaje* (*Score Type*) y
*Parámetros del puntaje* (*Score Parameters*) del dataset:

| Tipo | Regla | Parámetros (ejemplo) |
|---|---|---|
| Sum | cada caso vale el parámetro multiplicado por su resultado | `5`: 5 puntos por caso |
| GroupMin | por subtareas: cada una vale sus puntos multiplicados por el **peor** resultado de sus casos | `[[30, 5], [70, 15]]`: 30 puntos los 5 primeros casos, 70 los 15 siguientes |
| GroupMul | por subtareas: puntos multiplicados por el producto de los resultados | como GroupMin |
| GroupThreshold | por subtareas: todos los puntos si cada resultado es mayor que 0 y no supera un umbral (para checkers que devuelven un costo) | `[[30, 5, 1.5], …]` |

> **Ojo:** un problema creado con *(crear un problema...)* empieza con
> **Sum de parámetro 100, es decir, 100 puntos por caso**: con 20 casos el
> máximo sería 2000. Cambia el parámetro (5 para que 20 casos sumen 100) o
> usa subtareas. Un paquete en el formato propio (`problem.yaml`) sin
> puntaje, en cambio, reparte 100 puntos entre sus casos
> ([Importar](07-importar.md), [Formato de paquete](../formato-de-paquete.md)).

**Lenguaje** (*language*). Un lenguaje de programación con sus comandos de
compilación y ejecución. Vienen incluidos: C11 / gcc, C++11 / g++, C++14 /
g++, C++17 / g++, C++20 / g++, C# / Mono, Go, Haskell / ghc, Java / JDK,
Kotlin / JDK, Pascal / fpc, PHP, Python 3 / CPython, Python 3 / PyPy y
Rust. Cada concurso elige los suyos en *Lenguajes de programación
permitidos* (*Allowed programming languages*) y cada problema puede
restringirlos más. Los concursantes ven los comandos exactos en la
descripción del problema (*Comandos de compilación*, *Compilation
commands*).

> **Ojo:** un concurso nuevo permite solo **C11 / gcc, C++20 / g++ y
> Pascal / fpc**. Si quieres Python o Java, márcalos. Y cada lenguaje
> permitido necesita su compilador instalado en **todos** los jueces: con
> la instalación reducida de compiladores (`--compilers basic`: C, C++,
> Python 3 y Java) Pascal no está instalado ([Instalación](02-instalacion.md)).

**Envío** (*submission*). El código que manda un concursante para un
problema, con su hora, lenguaje y archivos. Se juzga en el dataset activo
(y en los que tengan juzgamiento en segundo plano). El concursante envía y
sigue sus envíos en *Envíos* (*Submissions*); el staff los ve en *Envíos*
del concurso, con filtros por problema, usuario, estado, oficial y
lenguaje.

**Envío oficial y no oficial** (*official*, *unofficial*). **Oficial** es el
que se hace mientras el concursante compite: cuenta para el puntaje y el
scoreboard. **No oficial** es el que se hace en modo análisis, o después del
horario del concursante si el concurso lo permite (*Permitir envíos no
oficiales antes del modo análisis*, *Allow unofficial submission before
analysis mode*): se juzga, pero no cuenta. El concursante los ve en tablas
separadas (*Envíos oficiales* / *Envíos no oficiales*). Desde la página de
un envío, la administración puede cambiarlo con *Hacer oficial* (*Make
official*) o *Hacer no oficial* (*Make unofficial*).

**Compilación** (*compilation*). El primer paso: el juez compila el código en
la caja aislada con los comandos del lenguaje. Estados: *Compilando...*
(*Compiling...*) y, si falla, *Compilación fallida* (*Compilation failed*),
que vale 0 puntos.

**Evaluación** (*evaluation*). Ejecutar el programa compilado con cada caso
de prueba, dentro de la caja, con el *Límite de tiempo* (*Time limit*) y el
*Límite de memoria* (*Memory limit*) del dataset. Cada caso produce un
resultado (de 0 a 1) y un mensaje. Estado: *Evaluando...* (*Evaluating...*).

**Puntuación** (*scoring*). El cálculo del puntaje del envío con el tipo de
puntaje, una vez evaluados todos los casos. Estados: *Puntuando...*
(*Scoring...*) y *Evaluado* (*Evaluated*). El puntaje del concurso es la
suma de los puntajes de cada problema, redondeada a *Decimales del puntaje*
(*Score decimal places*).

**Modo de puntaje** (*score mode*). Cómo se pasa de los puntajes de los
envíos al puntaje del concursante en el problema. Campo *Modo de puntaje*
(*Score mode*) del problema:

| Opción | Puntaje del problema |
|---|---|
| *Usar el mejor entre los envíos con ficha y el último (IOI 2010-2012)* (*Use best among tokened and last submissions (IOI 2010-2012)*) | el mejor entre los envíos con token y el último envío; **es el valor por defecto** |
| *Usar el mejor de todos los envíos (IOI 2013-2016)* (*Use best among all submissions (IOI 2013-2016)*) | el mejor envío |
| *Usar la suma, por subtarea, del mejor resultado de esa subtarea entre todos los envíos (IOI 2017-)* (*Use the sum over each subtask of the best result for that subtask across all submissions (IOI 2017-)*) | para cada subtarea, el mejor resultado de cualquier envío, sumados |

> **Ojo:** con el modo por defecto y sin tokens, **solo cuenta el último
> envío**: un concursante que al final manda una solución peor pierde
> puntos. Para un concurso al estilo de la IOI actual, elige la tercera
> opción.

**Prueba de usuario** (*user test*). El concursante ejecuta su programa en el
servidor con una entrada propia y recibe la salida, sin compararla con
nada y sin puntaje: sirve para medir tiempos en la máquina real o
descartar errores de compilación. Página *Testing* del concursante, botón
*Enviar un test* (*Submit a test*); en la administración, *Pruebas de
usuario* (*User tests*). Se habilitan con *Permitir pruebas de usuario*
(*Allow user tests*, activado por defecto). No existen en problemas
OutputOnly.

**Token**. Un permiso que el concursante gasta en uno de sus envíos para ver
su resultado completo (todos los casos y el puntaje real); con el modo de
puntaje por defecto, además, ese envío pasa a contar. Hay tokens del
concurso y tokens de cada problema, y para usar uno hacen falta los dos.
Cada conjunto puede estar *Desactivados* (*Disabled*), *Infinitos*
(*Infinite*) o *Finitos* (*Finite*); con finitos se define, por ejemplo,
2 tokens al empezar y 2 más cada 30 minutos. Por defecto el concurso tiene
tokens infinitos y cada problema los tiene desactivados, así que **no hay
tokens** hasta que los actives en el problema. El concursante usa uno con
*¡Usar token!* (*Play!*) en su tabla de envíos, que después dice *Token
utilizado* (*Played*) ([Concursos](04-concursos.md)).

**Nivel de retroalimentación** (*feedback level*). Cuánto detalle ven los
concursantes de los casos que pueden ver (los públicos, o todos tras usar un
token). Campo *Nivel de retroalimentación* (*Feedback level*) del problema:

| Opción | Qué ve el concursante |
|---|---|
| *Completo* (*Full*) | cada caso con su resultado, tiempo y memoria |
| *Restringido* (*Restricted*), por defecto | en una subtarea no resuelta del todo, los casos hasta el primero con el peor resultado |
| *Restringido OI* (*OI Restricted*) | en una subtarea no resuelta del todo, solo el primer caso con el peor resultado (la regla de la IOI) |

En modo análisis el concursante ve siempre todo.

### Personas

**Usuario** (*user*). Una persona con nombre de usuario (`oi07`), nombre,
apellido, contraseña y correo opcional. Los usuarios son **globales**: el
mismo usuario puede participar en varios concursos. Se manejan en
*Usuarios* (*Users*) del menú principal, uno por uno o importando un CSV
(*Importar usuarios*, *Import users*)
([Usuarios y equipos](08-usuarios-y-equipos.md)).

**Participación** (*participation*). La inscripción de un usuario en un
concurso. Guarda su grupo, su equipo, una contraseña propia para ese
concurso (opcional), las direcciones IP desde donde puede entrar, su retraso
y tiempo extra, si es oculta o sin restricciones, y todo lo que hace en el
concurso: envíos, pruebas, preguntas y mensajes. Sin participación un
usuario no puede entrar a ese concurso (salvo que el concurso permita
inscribirse). Se manejan en *Usuarios* dentro del menú del concurso.
Ejemplo: `oi07` tiene una participación en `practica` y otra en `oi2026`.

**Equipo** (*team*). Un grupo de concursantes con un código y un nombre
(`esc-norte`, «Escuela del Norte»): una delegación, un colegio, una región.
Se muestra en el scoreboard (con su bandera, si la cargas) y, si el concurso permite
inscribirse, el concursante elige el suyo. No suma puntajes: la tabla sigue
siendo por persona. Menú *Equipos* (*Teams*).

**Participación oculta** (*hidden participation*). Se juzga normalmente, pero
no aparece en el *Ranking* de la administración, en sus descargas ni en el
scoreboard público. Sirve para cuentas de prueba del staff o concursantes
fuera de competencia. Si además el concurso tiene *Bloquear participaciones
ocultas* (*Block hidden participations*), no pueden entrar. Los envíos de
prueba que se hacen desde la administración usan un usuario oculto
especial, `_preparer`, que se crea solo.

**Participación sin restricciones** (*unrestricted participation*). Puede
ver los problemas y enviar fuera del horario del concurso, sin los límites
de cantidad ni de intervalo de envíos y pruebas. Sirve para que el jurado
pruebe el sitio del concursante antes del inicio. Sus envíos hechos fuera
de su horario no son oficiales.

### Comunicación

**Pregunta** o **clarificación** (*question*). Una consulta privada de un
concursante al jurado, desde *Comunicación* (*Communication*): asunto (hasta
50 caracteres), texto (hasta 2000) y *Enviar la pregunta* (*Ask question*).
El staff la ve al instante en *Preguntas* (*Questions*) del concurso, que
en el menú muestra cuántas faltan responder. Ahí puede *Responder*
(*Reply*) con una *Respuesta predefinida:* (*Precompiled answer:*) o con un
texto propio, *Tomar* (*Claim*) la pregunta para que nadie más la responda
a la vez, o *Ignorar* (*Ignore*). La respuesta la ve solo quien preguntó;
si le sirve a todos, publica además un anuncio. Se habilitan con *Permitir
preguntas* (*Allow questions*, activado por defecto)
([Durante el concurso](09-durante-el-concurso.md)).

**Anuncio** (*announcement*). Un aviso para todos los concursantes de un
concurso, escrito en Markdown, desde *Anuncios* (*Announcements*) →
*Agregar anuncio* (*Add announcement*). Aparece al instante en el sitio del
concursante con una notificación. Ejemplo: «En `caminos`, N ≤ 100 000 (el
enunciado decía 10 000)».

**Mensaje privado** (*private message*). Un aviso del staff a **un** solo
concursante, desde la página de su participación, sección *Mensajes*
(*Messages*) → *Enviar* (*Send*). El concursante no puede contestarlo (para
eso están las preguntas). Ejemplo: «Te dimos 10 minutos de tiempo extra por
el corte de luz».

### Resultados

**Ranking**. La página *Ranking* de cada concurso en la administración: la
tabla con los puntajes completos de cada participante no oculto en cada
problema, que se descarga en texto o CSV y trae el enlace secreto al
scoreboard ([Después del concurso](12-despues-del-concurso.md)).

**Scoreboard** (*ranking* público). El tercer sitio: la tabla de posiciones
en vivo para el público, con los puntajes completos de los envíos oficiales
de las participaciones no ocultas. Puede ser pública, secreta (solo con un
enlace) o solo para administradores, y puede **congelarse** (*freeze*): el
público deja de ver los cambios de la última parte del concurso mientras el
staff sigue viendo todo ([Scoreboard](11-scoreboard.md)).

### Administración

**Administrador** (*admin*) y **permisos**. Las cuentas del staff para entrar
a la administración, distintas de los usuarios concursantes. Cada una tiene
uno de estos niveles:

| Permiso | Puede | `cmsctl add-admin --role` |
|---|---|---|
| *Todos los permisos* (*All permissions*) | todo | `all` |
| *Permisos de mensajería* (*Messaging permissions*) | responder, tomar e ignorar preguntas; enviar anuncios y mensajes | `messaging` |
| *Permisos de preparador de problemas* (*Problem preparer permissions*) | crear y modificar problemas, sus datasets y casos, y probarlos; no toca usuarios ni concursos ni borra problemas | `tasks` |
| ninguno | ver todas las páginas sin cambiar nada | `readonly` |

Siempre queda al menos un administrador habilitado con todos los permisos.
Las cuentas se manejan haciendo clic en tu nombre, arriba en el menú de la
izquierda (*Hola, …*, *Hello, …*) ([Primer acceso](03-primer-acceso.md)).

**Registro de auditoría** (*Audit log*). Cada cambio hecho desde la
administración queda anotado: quién, desde qué dirección, cuándo, qué
cambió (valor anterior y nuevo). Hay uno general en el menú principal y
uno por concurso ([Mantenimiento](13-mantenimiento.md)).

## El idioma de la interfaz

Cada sitio elige su idioma de forma distinta:

| Sitio | Idiomas | Cómo se elige |
|---|---|---|
| Sitio del concursante | los 25 de CMS (entre ellos español `es` y español de Chile `es_CL`) más inglés | 1) el selector de la barra superior, si el concursante eligió uno; 2) si no, el idioma preferido del navegador; 3) si no, el primero disponible |
| Administración | español e inglés | solo el idioma preferido del navegador; no hay selector |
| Scoreboard | inglés | siempre en inglés, como en CMS |

**Para ver la administración en español**, pon el español como primer
idioma en la configuración de idiomas de tu navegador y recarga la página.
Cualquier variante sirve (`es-AR`, `es-MX`…).

**Para que los concursantes vean el sitio en español**, no hace falta nada si
sus navegadores prefieren el español. Si quieres asegurarlo, escribe `es` en
*Idiomas permitidos* (*Allowed localizations*) del concurso: se ofrecen
solo los idiomas que empiezan con ese código. `es` deja las dos variantes
de español (`es` y `es_CL`), así que el selector sigue apareciendo; solo
desaparece cuando queda un único idioma.

Cosas que conviene saber:

- Se traduce la **interfaz**, no el contenido: enunciados, títulos,
  anuncios y respuestas se muestran como los escribiste.
- Este manual usa las etiquetas de `es`. Un navegador configurado como
  español de Chile recibe la variante `es_CL`, con algunas palabras
  distintas (por ejemplo *Vista general* en lugar de *Carátula*, *Iniciar
  sesión* en lugar de *Acceder*).
- Algunas palabras quedan en inglés también en español, tal como en CMS:
  *Testing*, *Ranking*, *Dataset*, *Managers* y los nombres de los tipos de
  problema y de puntaje.
- Las respuestas predefinidas a las preguntas (*Yes*, *No*, *Invalid
  Question (not a Yes/No Question)*, *No Comment/Please refer to task
  statement*) le llegan al concursante **en inglés**, como en CMS. Si
  prefieres español, responde con un texto propio.

## Horas y zonas horarias

**Hay una sola zona horaria para toda la instalación.** Las fechas se guardan
internamente en UTC y los sitios las muestran en la zona del servidor:

1. Al instalar, `timezone` de `/etc/cms/cms.yaml` toma la zona de la
   máquina (o UTC si no la encuentra).
2. En la administración, *Servidor* (*Server*) → *Zona horaria* (*Time
   zone*) la reemplaza (por ejemplo `America/Santiago` o
   `America/Mexico_City`); vacía vuelve a la de `cms.yaml`. Los cambios
   llegan a los sitios en 30 segundos como máximo.

Qué significa en la práctica:

- **En la administración**, los horarios se escriben y se muestran en esa
  zona, y la etiqueta lo dice: *Hora de inicio (en America/Santiago)*
  (*Start time (in America/Santiago)*). El formato es `AAAA-MM-DD HH:MM:SS`,
  por ejemplo `2026-11-14 09:00:00`.
- **En el sitio del concursante**, el inicio y el fin se muestran en esa
  zona, junto a *Hora del servidor:* (*Server time:*) y la cuenta regresiva.
  Los envíos y mensajes muestran el **tiempo de concurso** (cuánto pasó
  desde que el concursante empezó) y la hora completa al pasar el mouse.
- **La página del concurso tiene un campo *Zona horaria* (*Timezone*)**, y
  los usuarios también tienen uno: se conservan por compatibilidad con
  CMS, pero **no cambian lo que se muestra**. La que vale es la de
  *Servidor*.
- En un concurso en línea con gente de varios países, todos ven las horas
  en la zona del servidor: dilo en un anuncio.
- Los horarios se comparan con el reloj del servidor: mantenlo sincronizado
  (NTP) antes del concurso.

## El ciclo de vida de un concurso

Lista de control de punta a punta, con el capítulo que explica cada paso.

**1. Preparar** (días o semanas antes)

- [ ] Instalar y verificar el servidor y los jueces (`sudo cmsctl
  verify-host`) → [Instalación](02-instalacion.md)
- [ ] Entrar a la administración, crear las cuentas del staff con el
  permiso justo, revisar *Servidor* (zona horaria y direcciones) →
  [Primer acceso](03-primer-acceso.md)
- [ ] Crear el concurso y **poner los horarios antes de agregar
  concursantes**; elegir lenguajes, idiomas, preguntas, pruebas de usuario
  y tokens → [Concursos](04-concursos.md)
- [ ] Cargar los problemas (desde el navegador o importando paquetes),
  con enunciados, casos, casos públicos, tipo de problema y de puntaje,
  modo de puntaje y retroalimentación → [Problemas](05-problemas.md),
  [Tipos de problema y puntaje](06-tipos-de-problema-y-puntaje.md),
  [Importar](07-importar.md)
- [ ] Cargar usuarios, equipos y participaciones (grupos, IP, cuentas
  ocultas del staff) → [Usuarios y equipos](08-usuarios-y-equipos.md)
- [ ] Decidir la visibilidad y el congelamiento del scoreboard →
  [Scoreboard](11-scoreboard.md)

**2. Probar** (el ensayo)

- [ ] Enviar en cada problema soluciones correctas, parciales, lentas e
  incorrectas (*Envíos de prueba*, *Test submissions*) y comprobar que den
  lo esperado → [Problemas](05-problemas.md)
- [ ] Entrar al sitio del concursante con una cuenta de prueba oculta y sin
  restricciones: leer, enviar, hacer una pregunta, responderla →
  [Sitio del concursante](10-sitio-del-concursante.md),
  [Durante el concurso](09-durante-el-concurso.md)
- [ ] Mirar el scoreboard con esa cuenta de prueba visible → [Scoreboard](11-scoreboard.md)
- [ ] Hacer un respaldo antes del día del concurso → [Mantenimiento](13-mantenimiento.md)

**3. Correr** (el día del concurso)

- [ ] Seguir *Carátula*, *Envíos* y *Jueces*; responder *Preguntas*;
  publicar *Anuncios*; compensar incidentes con tiempo extra →
  [Durante el concurso](09-durante-el-concurso.md)
- [ ] Si algo falla → [Solución de problemas](14-solucion-de-problemas.md)

**4. Cerrar** (después)

- [ ] Descongelar el scoreboard, revisar y descargar el ranking final,
  abrir el modo análisis, respaldar y archivar →
  [Después del concurso](12-despues-del-concurso.md),
  [Mantenimiento](13-mantenimiento.md)

## Qué hacer si…

**…los concursantes ven 0 puntos aunque su solución es correcta.** Ven el
puntaje **público**, que solo cuenta los casos públicos (y, con subtareas,
solo las subtareas con todos sus casos públicos). Si ningún caso es
público, ese puntaje no les dice nada. Marca casos públicos o activa los
tokens ([Tipos de problema y puntaje](06-tipos-de-problema-y-puntaje.md)).

**…el scoreboard muestra un puntaje distinto del que ve el concursante.** Es
lo esperado: el scoreboard muestra el puntaje completo y el concursante el
público (ver [El viaje de un envío](#el-viaje-de-un-envío)). Si además está
congelado, el público no ve los últimos cambios.

**…un concursante no aparece en el scoreboard.** Su participación es oculta,
o se agregó hace menos de 30 segundos. Solo cuentan los envíos oficiales.

**…los envíos se quedan en *Compilando...* o *Evaluando...*.** No hay jueces
trabajando (detenidos, desactivados o sin el compilador del lenguaje).
Revisa *Jueces* y [Solución de problemas](14-solucion-de-problemas.md).

**…el concurso aparece abierto aunque no lo abrí.** Un concurso nuevo está
abierto de 2000 a 2100: pon sus horarios en *General*
([Concursos](04-concursos.md)).

**…las horas se ven corridas.** Revisa *Servidor* → *Zona horaria*; el campo
*Zona horaria* del concurso no se usa
([Horas y zonas horarias](#horas-y-zonas-horarias)).

**…la administración aparece en inglés.** Tu navegador prefiere el inglés:
pon el español primero en sus idiomas ([El idioma de la interfaz](#el-idioma-de-la-interfaz)).

**…no puedo crear un problema `suma` en otro concurso.** Los nombres cortos
de problema son únicos en toda la instalación: usa otro (`suma2026`).

**…un concursante no puede elegir Python.** El concurso no lo permite
(un concurso nuevo solo trae C11, C++20 y Pascal) o el problema lo
restringe. Márcalo en *Lenguajes de programación permitidos* y confirma
que los jueces tengan Python instalado.

**…un concursante perdió puntos por mandar una solución peor al final.** El
problema usa el modo de puntaje por defecto (el mejor entre los envíos con
token y el último). Cambiar el *Modo de puntaje* del problema recalcula al
momento los puntajes de ese problema; si lo haces durante un concurso,
avísalo con un anuncio. Lo mejor es elegir la regla IOI 2017- al preparar
el problema.

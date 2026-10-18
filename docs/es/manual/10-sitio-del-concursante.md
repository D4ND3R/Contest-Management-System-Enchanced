# 10. El sitio del concursante

*English: [The contestant site](../../en/manual/10-contestant-site.md)*

Lo que ven y hacen los concursantes. Sirve para explicarles el sistema
antes del concurso (al final hay una hoja para imprimir) y para entender
sus preguntas. La referencia técnica está en
[../sitio-concursante.md](../sitio-concursante.md).

## Contenido

1. [Entrar](#1-entrar)
2. [La barra superior](#2-la-barra-superior)
3. [La carátula](#3-la-carátula)
4. [La página de un problema](#4-la-página-de-un-problema)
5. [Enviar una solución](#5-enviar-una-solución)
6. [Ver los resultados](#6-ver-los-resultados)
7. [Tokens](#7-tokens)
8. [Pruebas (Testing)](#8-pruebas-testing)
9. [Comunicación: preguntas y anuncios](#9-comunicación-preguntas-y-anuncios)
10. [Horarios por concursante y modo análisis](#10-horarios-por-concursante-y-modo-análisis)
11. [Hoja para los concursantes](#hoja-para-los-concursantes)

---

## 1. Entrar

1. El concursante abre la dirección del sitio (`https://tu-dominio` o
   `http://IP:8000`).
2. Si hay varios concursos activos, elige uno en *Seleccione un certamen*
   (*Choose a contest*).
3. Escribe su *Nombre de usuario* y *Password* y pulsa *Acceder* (*Login*).

Si el concurso permite registrarse solo, aparece *Registrarse*
(*Register*) ([cap. 4](04-concursos.md)).

## 2. La barra superior

- El **tiempo**: *Tiempo restante:* (*Time left:*) durante el concurso, o
  cuánto falta para empezar.
- El **idioma** de la interfaz (selector).
- *Salir* (*Logout*).

El contador usa la hora del servidor: no importa si el reloj de la
computadora está mal.

## 3. La carátula

*Carátula* (*Overview*): el estado del concurso («El concurso está en
curso»), los problemas con sus límites de tiempo y memoria y el puntaje
actual de cada uno.

## 4. La página de un problema

Cada problema tiene su página en el menú:

- *Descargar enunciado* (*Download task statement*): el PDF, en el idioma
  principal y en los preferidos del concursante.
- *Adjuntos* (*Attachments*): ejemplos, plantillas, graders de ejemplo.
- El formulario para enviar y la lista de envíos anteriores.

## 5. Enviar una solución

1. En la página del problema, sección *Enviar solución* (*Submit a
   solution*).
2. Elige el archivo de su computadora y el lenguaje.
3. Pulsa *Enviar* (*Submit*).

Si el problema pide varios archivos, hay un campo por archivo. También se
puede enviar un zip con todos.

Límites que pueden aparecer: cantidad máxima de envíos, tiempo mínimo entre
envíos. Si se excede, el sitio lo dice y no acepta el envío.

## 6. Ver los resultados

En *Envíos anteriores* (*Previous submissions*) cada envío muestra su
estado (*Compilando...*, *Evaluando...*, *Puntuando...*) y luego su
puntaje. Se actualiza solo, sin recargar.

*detalles* (*details*) abre el resultado de la compilación y, según el
nivel de retroalimentación del problema, el resultado de los casos
públicos: correcto, incorrecto, tiempo excedido, memoria excedida, error en
ejecución…

Si dice *Compilación fallida*, el detalle muestra la *Salida de la
compilación* (*Compilation output*) con el error del compilador.

## 7. Tokens

Si el concurso usa **tokens**, un concursante puede «gastar» un token en un
envío para ver su resultado completo (todos los casos, no sólo los
públicos). Los tokens son limitados y se generan con el tiempo
([cap. 4](04-concursos.md#6-tokens)). En la lista de envíos aparece la
opción de usar un token en cada envío elegible.

## 8. Pruebas (Testing)

*Testing* permite ejecutar la solución con una entrada propia sin que
cuente como envío: el concursante sube su programa y un archivo de
entrada, y ve la salida, el tiempo y la memoria. Útil para comprobar que
compila y corre en el servidor. Puede tener límites de cantidad.

## 9. Comunicación: preguntas y anuncios

*Comunicación* (*Communication*):

- **Anuncios** de los organizadores para todos.
- **Preguntas**: escribe un asunto y un texto y pulsa *Enviar la pregunta*
  (*Ask question*). La respuesta aparece ahí mismo con una notificación.
- **Mensajes** privados de los organizadores.

Cuando llega un anuncio, una respuesta o un mensaje, aparece una
notificación en cualquier página del sitio.

## 10. Horarios por concursante y modo análisis

- Si el concurso tiene **ventana por concursante** (cada uno tiene X horas
  desde que empieza), el concursante ve *¡Comenzar!* (*Start!*) y su tiempo
  empieza a correr al pulsarlo.
- En **modo análisis** (después del concurso) puede seguir enviando para
  practicar; esos envíos no cuentan para el resultado oficial.

## Hoja para los concursantes

Puedes imprimir esta hoja y entregarla con las credenciales.

---

> **Cómo usar el sistema del concurso**
>
> **Dirección:** ______________________________
>
> **Tu usuario:** ______________ **Tu contraseña:** ______________
>
> 1. Abre la dirección en el navegador y entra con tu usuario y contraseña.
> 2. Arriba ves el **tiempo restante**. El reloj es el del servidor.
> 3. En el menú, cada **problema** tiene su página: descarga el enunciado
>    y los adjuntos.
> 4. Para **enviar**: en la página del problema elige tu archivo y el
>    lenguaje, y pulsa *Enviar*. El nombre del archivo puede ser cualquiera.
> 5. Tu envío aparece en *Envíos anteriores*: espera unos segundos y verás
>    el puntaje. Pulsa *detalles* para ver el resultado de la compilación y
>    de los casos públicos.
> 6. **Testing** sirve para probar tu programa con tu propia entrada sin
>    gastar envíos.
> 7. ¿Dudas sobre el enunciado? Ve a **Comunicación**, escribe tu pregunta
>    y pulsa *Enviar la pregunta*. Lee también los **anuncios**.
> 8. Puedes enviar varias veces: tu puntaje en cada problema se calcula
>    según las reglas del concurso (normalmente, lo mejor que lograste en
>    cada subtarea), así que enviar una versión peor no te quita puntos.
> 9. No compartas tu contraseña. Al terminar, pulsa *Salir*.

---

**Siguiente:** [11. Scoreboard](11-scoreboard.md) ·
**Anterior:** [9. Durante el concurso](09-durante-el-concurso.md) ·
[Índice](README.md)

# 9. Durante el concurso

*English: [During the contest](../../en/manual/09-during-the-contest.md)*

Qué mirar y qué hacer mientras el concurso está en marcha: los envíos, las
preguntas, los anuncios, los incidentes más comunes y una lista de control
para el día del concurso.

## Contenido

1. [Lista de control del día del concurso](#1-lista-de-control-del-día-del-concurso)
2. [La Carátula: el estado de un vistazo](#2-la-carátula-el-estado-de-un-vistazo)
3. [Los envíos](#3-los-envíos)
4. [El detalle de un envío](#4-el-detalle-de-un-envío)
5. [Reevaluar](#5-reevaluar)
6. [Preguntas (clarificaciones)](#6-preguntas-clarificaciones)
7. [Anuncios](#7-anuncios)
8. [Mensajes privados](#8-mensajes-privados)
9. [El ranking de la administración](#9-el-ranking-de-la-administración)
10. [Jueces y uso de recursos](#10-jueces-y-uso-de-recursos)
11. [Registro de auditoría](#11-registro-de-auditoría)
12. [Cambiar horarios o dar tiempo durante el concurso](#12-cambiar-horarios-o-dar-tiempo-durante-el-concurso)
13. [Incidentes: qué hacer si…](#13-incidentes-qué-hacer-si)

---

## 1. Lista de control del día del concurso

**El día anterior**

- [ ] `sudo cmsctl doctor` sin ningún `FAIL`.
- [ ] Cada problema probado con *Envíos de prueba* ([cap. 5](05-problemas.md#13-probar-el-problema-con-soluciones)).
- [ ] Horario del concurso y zona horaria revisados ([cap. 4](04-concursos.md)).
- [ ] Lenguajes permitidos = los que tienen grader.
- [ ] Todos los concursantes con participación ([cap. 8](08-usuarios-y-equipos.md)); cuentas de prueba como ocultas.
- [ ] Un usuario de prueba entró al sitio del concursante, vio los enunciados y envió una solución.
- [ ] Un respaldo hecho: `sudo systemctl start cms-backup.service` ([cap. 13](13-mantenimiento.md)).
- [ ] Credenciales impresas o enviadas.

**30 minutos antes**

- [ ] `sudo cmsctl doctor` otra vez.
- [ ] La *Carátula* muestra los jueces conectados y la cola vacía.
- [ ] Quien responda preguntas tiene su cuenta con permisos de mensajería.

**Al empezar**

- [ ] Los concursantes ven «El concurso está en curso» y el tiempo restante.
- [ ] Llegan los primeros envíos y se evalúan en segundos.

**Durante**

- [ ] Mira la *Carátula* y *Preguntas* cada pocos minutos.
- [ ] Responde las preguntas rápido; si la respuesta sirve a todos, publica un anuncio.

**Al terminar**

- [ ] Ningún envío pendiente en la *Carátula*.
- [ ] Preguntas sin responder: respóndelas o ignóralas.
- [ ] Sigue el [capítulo 12](12-despues-del-concurso.md).

## 2. La Carátula: el estado de un vistazo

*Carátula* (*Overview*), arriba en el menú, muestra:

- **Estado de los envíos**: cuántos compilan, se evalúan, están puntuados o
  tuvieron problemas («Cannot compile (please check)», «Cannot evaluate
  (please check)»: revisa esos envíos).
- **Estado de los jueces** y **de la cola**: si hay evaluaciones esperando y
  desde cuándo.
- **Registros**: avisos y errores de los servicios.

Todo se actualiza solo. Si la cola crece y no baja, mira la sección 10.

## 3. Los envíos

Página *Envíos* (*Submissions*) del concurso: todos los envíos, los más
nuevos arriba, con usuario, problema, hora, estado y puntaje. Se puede
filtrar por problema, usuario, estado, lenguaje y si son oficiales.

Estados de un envío:

| Estado | Qué significa |
|---|---|
| *Compilando...* (*Compiling...*) | en la cola o compilándose |
| *Compilación fallida* (*Compilation failed*) | no compiló; el concursante ve la salida del compilador |
| *Evaluando...* (*Evaluating...*) | corriendo los casos |
| *Puntuando...* (*Scoring...*) | calculando el puntaje |
| *Puntuados* (*Scored*) | terminado, con su puntaje |

Un envío que queda en *Compilando* o *Evaluando* más de un par de minutos
indica un problema con los jueces (sección 13).

## 4. El detalle de un envío

Al hacer clic en un envío ves:

- los archivos enviados (descargables);
- la **compilación**: resultado, salida del compilador, tiempo, memoria;
- la **evaluación** caso por caso: resultado, tiempo, memoria, mensaje del
  checker; tanto como lo ve el administrador como el concursante;
- el puntaje y, si se usó, el token;
- *Hacer no oficial* (*Make unofficial*): el envío deja de contar para el
  puntaje (por ejemplo, si se envió por error a otro problema y se
  acordó anularlo).

## 5. Reevaluar

Reevaluar vuelve a compilar y/o ejecutar envíos. Se hace cuando cambiaste
algo del problema (un caso, un checker, un límite) o cuando un juez falló.

| Desde | Botón | Afecta |
|---|---|---|
| el detalle de un envío | *Reevaluar* (*Reevaluate*) en compilación o en un caso | ese envío |
| la participación de un usuario | reevaluar | todos sus envíos |
| el dataset de un problema | *Reevaluar todos los envíos de este dataset* | todos los envíos del problema con ese dataset |
| *Envíos* del concurso | *Reevaluar todos los envíos de este concurso (en todos los datasets)* | todo el concurso (puede tardar) |

Si sólo cambiaste los casos de prueba o el checker, basta reevaluar la
**evaluación**; si cambiaste el grader o los lenguajes, la **compilación**.
Para corregir un problema con resultados comparables antes de aplicarlos,
usa un dataset nuevo ([cap. 5](05-problemas.md#14-cambiar-un-problema-durante-el-concurso)).

Los puntajes y el scoreboard se actualizan solos cuando termina.

## 6. Preguntas (clarificaciones)

Los concursantes preguntan desde *Comunicación* en su sitio. Llegan a
*Preguntas* (*Questions*) del concurso con una notificación.

Para responder una:

1. *Tomar* (*Claim*) la pregunta, para que otro organizador no la responda
   a la vez. *Soltar* (*Unclaim*) la libera.
2. Elige una *Respuesta predefinida* (*Precompiled answer*) — «Sí», «No»,
   «Pregunta inválida (no es de sí o no)» o «Sin comentarios / ver el
   enunciado» — o escribe una *Respuesta alternativa* (*Alternative
   answer*). Las predefinidas se guardan en inglés (como en CMS) y cada
   concursante las ve en su idioma.
3. *Responder* (*Reply*). El concursante la ve en su página con una
   notificación.

*Ignorar* (*Ignore*) la saca de la lista de pendientes sin responder
(preguntas repetidas o fuera de lugar); *Dejar de ignorar* la recupera.

Consejos:

- Responde lo mínimo y lo mismo a todos. «Sin comentarios» es válido si la
  respuesta está en el enunciado.
- Si la duda es de todos (un error en el enunciado), publica un **anuncio**.

## 7. Anuncios

*Anuncios* (*Announcements*) del concurso: *Asunto* (*Subject*) y *Texto*
(*Text*) → *Agregar anuncio* (*Add announcement*). Todos los concursantes
lo ven con una notificación. Se puede *Eliminar*.

Ejemplo: «Corrección en el problema Baches: en el ejemplo 2 la salida es 7,
no 8. El enunciado ya está corregido.»

## 8. Mensajes privados

Para escribirle a un solo concursante: su participación → *Mensajes* →
*Asunto*, *Texto* → *Enviar*. Lo ve como notificación.

## 9. El ranking de la administración

*Ranking* del concurso: la tabla completa con todos los puntajes (también
los ocultos), por problema y total. Se puede ordenar por columna y
descargar en CSV o texto. Ahí también está el **enlace secreto** al
scoreboard público ([cap. 11](11-scoreboard.md)).

## 10. Jueces y uso de recursos

*Jueces* (*Judges*): cada juez (worker) con su estado (*Running*, *Idle*,
*Broken*, *Disabled*…), qué está evaluando, desde cuándo no da noticias y la
cola de evaluaciones. Un juez *Broken* indica que su sandbox no funciona
(la columna *Reason* dice por qué). Desde aquí se puede deshabilitar un
juez o reencolar trabajos.

*Uso de recursos* (*Resource usage*): CPU, memoria y disco de cada
servicio. Si el disco se llena, nada funciona: mira que quede espacio.

## 11. Registro de auditoría

*Registro de auditoría* (*Audit log*): cada cambio hecho en la
administración, con quién, cuándo y qué. Útil para reconstruir qué pasó
(«¿quién cambió el horario?»).

## 12. Cambiar horarios o dar tiempo durante el concurso

- **A un concursante**: *Tiempo extra* o *Retraso* en su participación
  ([cap. 8](08-usuarios-y-equipos.md#7-la-participación-tiempo-extra-oculta-sin-restricciones)).
- **A un grupo** (un turno): horario del grupo ([cap. 4](04-concursos.md#9-grupos)).
- **A todos**: el fin del concurso en su página. Publica un anuncio.

Los contadores de los concursantes se actualizan solos.

## 13. Incidentes: qué hacer si…

**…un concursante no puede entrar.**
1. ¿Usa el usuario y la contraseña correctos? Míralos en su usuario.
2. ¿Tiene participación en este concurso? *Usuarios* del concurso.
3. ¿El concurso restringe por IP y entra desde otra red?
4. ¿Tiene contraseña propia en la participación?

**…los envíos se quedan en «Evaluando...».**
1. `sudo cmsctl doctor`: mira la sección «Valkey and judges».
2. Si dice «no judge is connected» o un juez *Broken*:
   `sudo systemctl restart cms-worker` y `sudo journalctl -u cms-worker -n 50`.
3. Nada se pierde: cuando el juez vuelve, evalúa lo pendiente.

**…un caso de prueba está mal.** Dataset nuevo, corregir, comparar,
activar ([cap. 5](05-problemas.md#14-cambiar-un-problema-durante-el-concurso)),
y un anuncio.

**…el servidor se reinició.** Todo sobrevive a los reinicios: las colas, los
envíos y los resultados. Al volver, los servicios arrancan solos y siguen
donde estaban. Compruébalo con `sudo cmsctl doctor`.

**…un concursante tuvo un corte de luz.** Dale *Tiempo extra* en su
participación.

**…la página de administración está lenta.** Mira *Uso de recursos*. Evita
reevaluar todo el concurso durante la prueba si no es necesario.

---

**Siguiente:** [10. Sitio del concursante](10-sitio-del-concursante.md) ·
**Anterior:** [8. Usuarios y equipos](08-usuarios-y-equipos.md) ·
[Índice](README.md)

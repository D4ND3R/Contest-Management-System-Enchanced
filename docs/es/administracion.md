# Administración

*English: [../en/administration.md](../en/administration.md).*

La administración (`cms admin-web`) es el equivalente del AdminWebServer de
CMS: las mismas páginas, con el mismo HTML, CSS y JavaScript, más las
páginas nuevas de este fork (preparación de problemas, jueces, servidor y
registro de auditoría). Esta guía explica cómo se configura, quién puede
hacer qué, cómo se registra cada cambio y qué difiere de CMS.

## Configuración

Sección `admin_web` de `cms.yaml` (cada clave tiene su variable
`CMS_ADMIN_WEB_*`; ver [Configuración](configuracion.md)):

| Clave | Por defecto | Qué hace |
|---|---|---|
| `listen` | `127.0.0.1:8889` | dirección del sitio |
| `ops_listen` | `127.0.0.1:9189` | métricas, estado detallado y pprof (no lo expongas) |
| `num_proxies` | `0` | proxies de confianza delante del sitio (dirección del cliente y HTTPS, como en el sitio del concursante) |
| `cookie_duration` | `10h` | cuánto dura una sesión sin actividad |

Además necesita `secret_key` (la misma en todos los servicios), la base
de datos, Valkey (notificaciones, eventos en vivo, colas y jueces) y el
almacén de archivos (`storage`).

El sitio sirve `/static/…` (los archivos de CMS y `live.js`), `/source`
(el código de la versión que corre, enlazado al pie de cada página) y
`/healthz`. Todas las URLs son relativas, como en CMS.

## Administradores y permisos

Cada administrador tiene un usuario, un nombre, una contraseña (bcrypt;
también se aceptan las `plaintext:` importadas de CMS), un interruptor de
habilitado y tres permisos:

| Permiso | Puede |
|---|---|
| Todos los permisos (`permission_all`) | todo |
| Mensajería (`permission_messaging`) | responder, ignorar y tomar preguntas; mandar anuncios y mensajes privados |
| Preparador de problemas (`permission_tasks`, nuevo) | crear y cambiar problemas, enunciados, adjuntos, datasets, managers y casos de prueba de cualquier concurso, y probar envíos en ellos; no usuarios, ni concursos, ni su configuración, ni borrar problemas |
| ninguno | ver todas las páginas sin cambiar nada |

Un administrador deshabilitado no puede entrar. Siempre queda al menos un
administrador habilitado con todos los permisos: el sitio no deja
deshabilitarlo, quitarle el permiso ni borrarlo. Cualquiera puede cambiar
su propio nombre, usuario y contraseña en la página **Admins**.

Mientras no exista el instalador (F9), el primer administrador se crea en
la base de datos:

```sql
INSERT INTO admins (name, username, authentication, enabled, permission_all)
VALUES ('Admin', 'admin', 'plaintext:una-contraseña-larga', true, true);
```

y después se le pone una contraseña nueva desde la página del
administrador (queda guardada con bcrypt).

## Sesiones y entrada

- La sesión dura `cookie_duration` desde la última página abierta (las
  páginas que se actualizan solas no la extienden, salvo las
  notificaciones, como en CMS). La cookie es `HttpOnly` y
  `SameSite=Lax`.
- Deshabilitar a un administrador, borrarlo o cambiarle la contraseña
  cierra sus sesiones en el siguiente pedido.
- Los intentos de entrada tienen límite por dirección y por usuario (10
  fallos seguidos y después uno cada 6 s) y las comprobaciones bcrypt
  pasan por una cola con una por núcleo (D42): un ataque de fuerza bruta
  recibe 429 sin frenar al resto del servidor.
- Todo formulario lleva el token CSRF; los pedidos de otros sitios se
  rechazan.

## Registro de auditoría

Cada cambio hecho desde la administración queda en la tabla `audit_log`,
en la misma transacción que el cambio (si el cambio no se guarda, la
entrada tampoco): quién (usuario y dirección), cuándo, qué acción
(`contest.update`, `task.create`, `submission.reevaluate`…), sobre qué
objeto y de qué concurso, con los campos que cambiaron (valor anterior y
nuevo). Las contraseñas y los secretos aparecen como `[redacted]`.

La página **Registro de auditoría** (menú principal, y en el menú de cada
concurso la del concurso) muestra las entradas más recientes primero, de a
50, con filtros por concurso, administrador y objeto (tipo e id).

## Página Servidor

La página **Servidor** guarda en la base de datos (`server_settings`):

- **Zona horaria**: la de todas las fechas de los tres sitios (D6). Vacía
  usa `timezone` de `cms.yaml`. Los otros sitios la leen cada 30 segundos.
- **Dirección del sitio de concursantes** y **del scoreboard**: la
  administración no sabe con qué nombre llegan concursantes y
  espectadores, así que los enlaces a esos sitios se arman con estos
  valores. Una dirección completa (`https://concurso.ejemplo.org/`) se usa
  tal cual; `:8888` significa «el host de esta página en el puerto 8888»,
  que funciona en las instalaciones por puertos se entre por el nombre o
  por la IP.

Solo los administradores con todos los permisos pueden cambiarla.

## Tiempo real

Junto al nombre del administrador hay un indicador verde, amarillo o rojo
(conectado, reconectando, sin conexión), como en el sitio del concursante.
Las páginas reciben eventos en vivo (`/events` y `/contest/<id>/events`):
las preguntas nuevas muestran la notificación al instante y actualizan el
contador del menú, y el resumen recarga las colas y los jueces al cambiar.
Sin conexión, las páginas vuelven a consultar como CMS (notificaciones cada
15 s, resumen cada 5 s). Detrás de un proxy, `*/events` no debe
comprimirse ni guardarse en búfer (ver [qué necesita un
proxy](sitio-concursante.md#qué-necesita-un-proxy)).

## Páginas

| Página | Qué muestra |
|---|---|
| Resumen (`/`, `/contest/<id>/overview`) | estado de los envíos del concurso, cola de trabajos (los primeros 100 de cada prioridad), jueces y lo que corre cada uno |
| Uso de recursos | los servicios vistos desde este servidor: la administración, PostgreSQL, Valkey, el dispatcher activo y los jueces de cada máquina. Las estadísticas de CPU, memoria y disco son del servicio `monitor` (sus métricas); CMS las graficaba con ResourceService, que no existe acá |
| Admins | lista, alta, edición y baja de administradores |
| Servidor, Registro de auditoría | ver arriba |

Las demás páginas (concursos, grupos, usuarios, equipos, problemas,
datasets, envíos, comunicación, ranking, jueces y preparación de
problemas) se documentan a medida que se completan.

## Diferencias con CMS

- Textos traducibles (español e inglés, elegido por `Accept-Language`);
  en inglés las páginas son las de CMS.
- Permiso de preparador de problemas, página Servidor, registro de
  auditoría, indicador en vivo y menú con Jueces, Servidor y Registro de
  auditoría.
- Los manejadores en línea (`onclick=`) pasaron a `aws_handlers.js` por la
  política de seguridad de contenido (D10).
- No hay LogService: la sección «Registros» del resumen indica que cada
  servicio escribe en su salida estándar (`journalctl`, `docker compose
  logs`). No hay aviso de `secret_key` por defecto: no existe una clave
  por defecto (D5).
- Los jueces se identifican por nombre (CMS los numera).
- Las notificaciones de «Operación exitosa» son de cada administrador
  (CMS se las da al primero que consulta).
- Las fechas de los formularios están en la zona horaria del servidor
  (CMS usa UTC).

## Concursos, usuarios, equipos y comunicación

- **Concursos**: la lista, crear (solo el nombre; se crea el grupo
  `default` como grupo principal), eliminar (con todo su contenido, también
  sus tareas) y la página de configuración con todas las opciones del
  original: idiomas, lenguajes, descargas, preguntas, pruebas de usuario,
  envíos no oficiales antes del análisis, ingreso (ocultos, contraseña,
  inscripción, restricción e ingreso automático por IP), tokens, límites,
  decimales del puntaje y los horarios del grupo principal. Los horarios se
  muestran y se leen en la zona horaria del servidor (la página Servidor),
  que las etiquetas indican.
- **Grupos**: cada grupo tiene su inicio, fin, duración por concursante y
  modo análisis. Un grupo nuevo copia los horarios del principal. No se
  puede eliminar un grupo con usuarios ni el grupo principal.
- **Tareas del concurso**: agregar una tarea sin concurso, quitarla y
  moverla (arriba, abajo, al principio, al final). Requiere el permiso
  "todo".
- **Usuarios**: lista paginada (100 por página), alta, edición (la
  contraseña en texto plano o cifrada con bcrypt; con una contraseña
  cifrada, dejar el campo vacío la conserva), baja (con sus
  participaciones y envíos) y sus participaciones.
- **Equipos**: lista, alta, edición, baja (las participaciones quedan sin
  equipo) e **importación desde CSV** en la página de equipos: una fila
  por equipo `código,nombre` (una primera fila `code,name` se ignora; un
  nombre vacío toma el código). Un código existente cambia de nombre. Si
  alguna fila está mal (código inválido o repetido, columnas de más o de
  menos) no se importa nada y el aviso dice qué líneas fallaron.
- **Participaciones**: agregar usuarios a un concurso en un grupo; la
  página de cada participación cambia grupo, equipo, contraseña propia
  (vacía = la del usuario), oculta, sin restricciones, IP o subredes,
  hora del primer ingreso, retraso y tiempo extra; muestra sus envíos,
  preguntas y mensajes privados.
- **Comunicación por concurso**: anuncios (Markdown, con vista previa),
  preguntas (respuestas rápidas o texto, tomar/soltar, ignorar/dejar de
  ignorar) y mensajes privados. Requiere el permiso "todo" o
  "mensajería". El contador de preguntas sin responder del menú, la página
  de preguntas y la de cada participación se actualizan en vivo; los
  concursantes reciben al instante anuncios, mensajes y respuestas.
- **Ranking**: la tabla (de la caché de puntajes `task_scores`, sin
  recalcular), las descargas en texto y CSV con el formato del original, y
  el **enlace secreto al scoreboard público**:
  `<dirección del scoreboard>?secret=<token>`. El token es
  `config.RankingLinkToken(nombre del concurso)`: los 16 primeros bytes
  (en hexadecimal) de `DeriveKey(secret_key, "ranking-web/secret-link",
  nombre del concurso)`. Es el mismo en todos los servicios, cambia si
  cambia `secret_key` o el nombre del concurso, y no se guarda en ningún
  lado. La dirección del scoreboard se configura en la página Servidor.

Cada cambio queda en el registro de auditoría y publica los eventos en
vivo correspondientes (cambios del concurso, de una participación, del
scoreboard).

## Problemas y datasets

Las páginas de problemas y datasets son las del CMS original: lista de
problemas, agregar y eliminar (eliminar requiere el permiso **todo**),
página del problema con todas sus opciones, enunciados y adjuntos,
datasets (agregar, clonar con o sin resultados, renombrar, borrar,
activar con la vista previa de los cambios de puntaje y el aviso a los
concursantes, juzgamiento en segundo plano), tipo de problema y de
puntaje con sus formularios de parámetros, managers y casos (agregar uno,
agregar desde un zip con plantillas de nombres, borrar, descargar todos en
un zip). El preparador de problemas (`permission_tasks`) puede hacer todo
eso salvo eliminar problemas y moverlos entre concursos. Diferencias con
CMS: la vista previa de la activación muestra a lo sumo 5000 envíos que
cambian; los casos de un zip se escriben en una sola transacción.

La preparación desde el navegador (importar paquetes con vista previa,
`problem.yaml`, archivos uno por uno, casos uno por uno con salidas
generadas, generador, envíos de prueba y lista de lo que falta) está en
[Crear un problema](crear-problema.md).

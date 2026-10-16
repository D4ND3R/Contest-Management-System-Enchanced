# Sitio del concursante

*English: [../en/contestant-site.md](../en/contestant-site.md).*

El sitio del concursante (`cms contest-web`) es el equivalente del
ContestWebServer de CMS: las mismas páginas, con el mismo HTML, CSS y
JavaScript, las mismas URLs y la misma API JSON. Esta guía explica cómo se
configura, cómo entran los concursantes, cómo se elige el idioma y qué
necesita un proxy para las actualizaciones en vivo.

## URLs

| `contest_web.contest` | Qué se sirve |
|---|---|
| vacío (por defecto) | `/` lista los concursos; cada uno vive en `/<nombre>` (`/demo`, `/demo/documentation`…) |
| `demo` | el concurso `demo` en `/` (`/`, `/documentation`…) |

En los dos casos el sitio sirve además:

- `/static/…`: los archivos de CMS (Bootstrap 2, `cws_utils.js`,
  iconos) y `live.js`;
- `/docs/…`: la documentación de los lenguajes, desde
  `contest_web.docs_path` (solo archivos; un directorio da 404);
- `/source`: redirige al código fuente de la versión que corre (AGPL), y
  cada página lo enlaza al pie, junto a la licencia;
- `/healthz`: estado mínimo para balanceadores (`{"status":"ok"}`). Las
  métricas y el estado detallado están en `contest_web.ops_listen`.

Todas las URLs son relativas, como en CMS: el sitio funciona igual detrás
de un proxy que lo publica en un subdirectorio.

## Configuración

Sección `contest_web` de `cms.yaml` (cada clave tiene su variable
`CMS_CONTEST_WEB_*`; ver [Configuración](configuracion.md)):

| Clave | Por defecto | Qué hace |
|---|---|---|
| `listen` | `127.0.0.1:8888` | dirección del sitio |
| `ops_listen` | `127.0.0.1:9188` | métricas, estado detallado y pprof (no lo expongas) |
| `contest` | vacío | sirve un solo concurso en `/` (ver arriba) |
| `num_proxies` | `0` | proxies de confianza delante del sitio: con 1, la dirección del concursante es la última de `X-Forwarded-For` (y `X-Forwarded-Proto` dice si llegó por HTTPS) |
| `cookie_duration` | `3h` | cuánto dura una sesión sin actividad |
| `max_submission_length` | `100000` | bytes por archivo de un envío |
| `max_input_length` | `5000000` | bytes de la entrada de una prueba de usuario |
| `admin_token` | vacío | permite a los administradores entrar como un concursante por la API (`admin_token` de `api/login`); vacío lo desactiva |
| `docs_path` | `/usr/share/cms/docs` | directorio servido en `/docs` |

Además el sitio necesita `secret_key` (la misma en todos los servicios:
firma las cookies y los tokens CSRF), la base de datos, Valkey
(notificaciones pendientes y eventos en vivo) y el almacén de archivos
(`storage`).

**Zona horaria.** Todas las horas se muestran en la zona horaria del
servidor: la de `cms.yaml` (`timezone`) o la que se elija en la página
Servidor de la administración, que el sitio vuelve a leer cada 30
segundos. Las zonas por usuario o por concurso de CMS se importan pero no
cambian lo que se muestra. Donde CMS muestra la hora de un envío o de un
mensaje, se muestra el **tiempo de concurso** (cuánto pasó desde que el
concursante empezó), con la hora completa al pasar el mouse.

## Cómo entran los concursantes

El formulario de entrada aparece en la página del concurso. Lo que vale
para cada concurso se configura en el concurso (administración) y en cada
participación:

- **Contraseña.** `Permitir contraseña` (`allow_password_authentication`)
  activa el formulario. La contraseña se guarda como en CMS:
  `bcrypt:<hash>` o `plaintext:<texto>`. Si la participación tiene
  contraseña propia, se usa esa en lugar de la del usuario.
- **Registro.** Con `allow_registration` aparece "Registrarse": se crea un
  usuario nuevo (con la contraseña en bcrypt) o un usuario existente se une
  al concurso con su contraseña. Si hay equipos, elegir uno es obligatorio.
- **Restricción por IP** (`ip_restriction`): una participación con
  direcciones (`ip`, redes como `10.0.0.0/24`) solo puede entrar desde
  ellas; sin direcciones, desde cualquiera; con la lista vacía, desde
  ninguna.
- **Entrada automática por IP** (`ip_autologin`): quien llega desde la
  dirección exacta (`/32`) de una participación entra sin formulario. Si
  dos participaciones tienen la misma dirección, ninguna entra (es un error
  de configuración y queda en el log).
- **Participaciones ocultas** (`block_hidden_participations`): las
  participaciones ocultas no pueden entrar.
- **Sin restricciones** (`unrestricted` en la participación): ve los
  problemas y envía fuera del horario del concurso.

La sesión es una cookie `<concurso>_login` firmada, `HttpOnly` y
`SameSite=Lax` (y `Secure` por HTTPS). Como en CMS, cada página la renueva:
vence después de `cookie_duration` **sin actividad** (las consultas en
segundo plano, como las notificaciones, no la renuevan). Cambiar la
contraseña del concursante cierra sus sesiones. "Salir" la borra.

Los intentos de entrada tienen un límite: 10 seguidos por usuario y
concurso y después uno cada 6 segundos (respuesta 429), y un límite amplio
por dirección, porque un laboratorio entero puede salir por una sola
dirección.

## Horarios

Los horarios están en el **grupo** de cada participación (el grupo
principal del concurso por defecto), como en CMS:

- inicio y fin; un concursante con `delay_time` empieza y termina más
  tarde, y `extra_time` alarga su fin;
- **ventana por usuario** (`per_user_time`): el concursante elige cuándo
  empieza con el botón "¡Comenzar!" y tiene ese tiempo, sin pasar del fin
  del concurso;
- **modo análisis** (inicio y fin propios): después del concurso se puede
  volver a ver y enviar fuera de concurso.

La página de inicio explica en qué fase está cada uno, y el reloj de la
izquierda cuenta el tiempo que queda.

## Idioma de la interfaz

Las traducciones son las de CMS (25 idiomas) más las de este proyecto. El
idioma se elige como en CMS:

1. el selector de la barra superior guarda una cookie `language`;
2. si no, el mejor idioma del navegador (`Accept-Language`);
3. si no, el primero disponible.

El concurso puede limitar los idiomas (`allowed_localizations`, por
prefijo: `es` incluye `es` y `es_CL`). Con un solo idioma no se muestra el
selector.

## Actualizaciones en vivo

Las páginas se actualizan solas: un anuncio, un mensaje o una respuesta
aparecen al instante, y los resultados de los envíos se refrescan sin
recargar. Junto al nombre del concursante hay un indicador:

| Color | Significa |
|---|---|
| verde | conectado: las novedades llegan al instante |
| amarillo | conectando o reconectando (por ejemplo, después de reiniciar el servidor) |
| rojo | desconectado: la página busca novedades cada 15–30 segundos, como CMS, y sigue intentando conectarse (entre 1 y 60 segundos) |

Las páginas nunca dependen solo de los eventos: si se pierde alguno, la
página recarga lo que muestra. Una pestaña oculta más de un minuto cierra
su conexión y la abre de nuevo al volver.

### Qué necesita un proxy

Los eventos llegan por `GET <concurso>/events` (server-sent events: una
respuesta HTTP que no termina). El servidor manda un `ping` apenas se abre
y después cada 15 segundos, y responde con `X-Accel-Buffering: no` y
`Cache-Control: no-cache, no-transform`. El proxy debe:

- **no acumular** la respuesta (nginx lo respeta por el encabezado; en
  otros proxies, desactivar el buffering para `*/events`);
- **no comprimirla**: excluir `*/events` de gzip/zstd;
- **no cortar** conexiones inactivas antes de 15 segundos (mejor 60 o
  más);
- preferentemente hablar **HTTP/2** con los navegadores: con HTTP/1.1 cada
  pestaña ocupa una de las 6 conexiones por servidor.

Caddy:

```
concurso.example.org {
	@noevents not path */events
	encode @noevents zstd gzip
	reverse_proxy 127.0.0.1:8888
}
```

(`reverse_proxy` de Caddy no acumula los server-sent events.)

nginx:

```
location / {
    proxy_pass http://127.0.0.1:8888;
    proxy_http_version 1.1;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
location ~ /events$ {
    proxy_pass http://127.0.0.1:8888;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    gzip off;
    proxy_read_timeout 1h;
}
```

Con un proxy delante, poner `num_proxies: 1` para que el sitio vea la
dirección real de los concursantes (restricción y entrada por IP, límites
de intentos).

## Problemas, envíos y pruebas

**Descripción.** La página de cada problema muestra sus enunciados, sus
límites, su tipo, los comandos de compilación de sus lenguajes, sus reglas
de tokens y sus adjuntos (con su tipo y tamaño), como CMS. CMS solo tiene
enunciados PDF; aquí un enunciado también puede ser **Markdown** (se
muestra dentro del sitio, sin HTML crudo) o fuente **LaTeX** (se descarga
como `.tex`): el sitio los distingue por su contenido. Con varios
enunciados se muestran primero los principales del problema y los de los
idiomas preferidos del concursante.

**Enviar.** Un archivo por elemento del formato de envío, con el lenguaje
elegido en la lista (al elegir un archivo la lista se reduce a los
lenguajes de su extensión, como en CMS) o, en problemas de varios
archivos, un `.zip` con todos. Los límites son los de CMS, con sus
mensajes:

| Ajuste (concurso o problema) | Efecto |
|---|---|
| `max_submission_number` | envíos en total (concurso) o en el problema |
| `min_submission_interval` | tiempo entre envíos; no se aplica en los últimos `min_submission_interval_grace_period` del tiempo del concursante |
| `contest_web.max_submission_length` | bytes por archivo |

Los límites se vuelven a comprobar al guardar el envío, así que envíos
simultáneos no pueden saltarlos. Los envíos son oficiales mientras el
concursante compite; en modo análisis (y después de su tiempo, si
`allow_unofficial_submission_before_analysis_mode`) no son oficiales y no
cuentan.

**Envíos.** La página de envíos lista los últimos 500 del problema, del
más nuevo al más viejo. Su estado y sus puntajes se actualizan solos (como
en CMS, y al instante con las actualizaciones en vivo). "detalles" muestra
el puntaje por subtarea y los casos que permite el `feedback_level` del
problema (`full`, `restricted`, `oi_restricted`; todo en modo análisis o
después de usar un token) y la salida de la compilación. Los archivos se
pueden descargar si el concurso lo permite
(`submissions_download_allowed`).

**Tokens.** Las reglas de CMS: tokens del concurso y del problema, número
inicial, generación, máximo, total e intervalo mínimo. Un token muestra el
resultado completo de un envío y lo hace contar para el puntaje. Dos
pedidos simultáneos nunca usan más tokens de los disponibles.

**Pruebas.** La página Testing (`allow_user_tests`) ejecuta una solución
con una entrada del concursante en los problemas cuyo tipo lo permite (no
en los de solo salida), con `max_user_test_number`,
`min_user_test_interval` y `contest_web.max_input_length`. Las pruebas se
numeran por problema desde la más vieja; la página lista las últimas 200
de cada problema.

## Comunicación

La página Comunicación muestra los anuncios del concurso, las preguntas
del concursante con sus respuestas y los mensajes privados del staff, de
la más nueva a la más antigua. Los anuncios, las respuestas y los mensajes
son Markdown; las preguntas, texto plano. Aparecen sin recargar: se
muestra una notificación y las listas de la página se actualizan solas (en
esa página el contador de "sin leer" queda en cero). Cuando la novedad
es la primera de su tipo (la página no tenía lista de mensajes, por
ejemplo), la página se recarga antes de 10 segundos, esperando si se está
escribiendo una pregunta. El formulario para preguntar aparece cuando el
concurso permite preguntas (`allow_questions`):

- el asunto tiene a lo sumo 50 caracteres y el texto 2000, como en CMS
  (una pregunta más larga se rechaza con "¡La pregunta es demasiado larga!");
- el asunto sugiere los nombres de los problemas una vez empezado el
  concurso;
- un concursante puede hacer 10 preguntas seguidas y después una cada 20
  segundos ("¡Demasiadas preguntas!"), para que nadie inunde al staff.

El staff ve las preguntas nuevas al instante en la administración.

Los programas (scripts, herramientas escritas para CMS) usan la
[API JSON](api.md).

## Seguridad

- Cada formulario lleva un token CSRF (`_xsrf`, como CMS) y las peticiones
  de otro sitio se rechazan.
- Política de seguridad de contenido estricta: solo los scripts del sitio
  y los de las páginas con su nonce; los manejadores en línea de CMS
  (`onchange=`…) están en `static/cws_handlers.js`.
- Las cookies no viajan a JavaScript (`HttpOnly`) salvo la del token CSRF,
  como en CMS.

## Diferencias con CMS

- Notificaciones del sitio ("Envío recibido"…) guardadas en Valkey: las ve
  cualquier réplica del sitio, no solo la que las generó.
- La cookie de sesión no contiene la contraseña ni su hash, sino una huella
  firmada; dura `cookie_duration` desde la última página.
- Entrada limitada en frecuencia; registro limitado por dirección; un
  concursante puede hacer 10 preguntas seguidas y después una cada 20
  segundos.
- Indicador y actualizaciones en vivo (las listas de la página
  Comunicación se actualizan solas); enlace al código fuente en el pie.
- Una sola zona horaria; tiempo de concurso en lugar de la hora.
- Enunciados en Markdown y LaTeX además de PDF.
- Las listas están acotadas: los últimos 500 envíos y 200 pruebas de cada
  problema (los más viejos conservan sus URLs).
- Se muestran los casos públicos de una subtarea que también tiene casos
  privados ("detalles" y la API); CMS falla ahí con "Score details
  temporarily unavailable.".
- Las horas de las filas de envíos se escriben en el idioma de la página
  (CMS las escribe en inglés).

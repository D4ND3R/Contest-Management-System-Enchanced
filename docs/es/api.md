# API JSON del sitio del concursante

*English: [../en/api.md](../en/api.md).*

El sitio del concursante responde la misma API JSON que el
ContestWebServer de CMS (`docs/API.rst` de CMS), así que los scripts y
herramientas escritos para CMS funcionan sin cambios: entran, listan los
problemas, envían, siguen sus envíos y leen el detalle de los resultados.
Un administrador también puede actuar en nombre de un concursante
(suplantación), por ejemplo para cargar envíos desde otro sistema.

La API es para programas: sus mensajes están en inglés y no se traducen.
Las páginas del concursante se describen en
[Sitio del concursante](sitio-concursante.md).

## URLs

Las rutas son relativas al concurso, como todas las páginas del sitio:

| `contest_web.contest` | URL de entrada |
|---|---|
| vacío (por defecto) | `https://concurso.ejemplo.org/<concurso>/api/login` |
| `demo` | `https://concurso.ejemplo.org/api/login` |

Los ejemplos usan `$SITE` para la URL del concurso
(`https://concurso.ejemplo.org/demo`).

## Respuestas y errores

Todas las respuestas son JSON (`Content-Type: application/json;
charset=utf-8`). Los errores tienen un código distinto de 200 y un objeto
con el campo `error` (y `details` cuando se rechaza un envío):

| Código | Cuándo | `error` |
|---|---|---|
| 400 | un parámetro booleano que no es `0` ni `1` | `Cannot parse boolean argument <nombre>` |
| 400 | `api/login` de un concursante que ya entró, con otro usuario | `Logged in as <usuario> but trying to login as <otro>` |
| 403 | usuario, contraseña o token de administración incorrectos | `Login failed` |
| 403 | sin datos de entrada, o vencidos | `An authenticated user is required` |
| 403 | fuera del concurso (lista de problemas, envíos) | `The contest is not open` |
| 403 | antes del concurso (detalles) | `Wrong contest phase` |
| 403 | `full_details` sin suplantación | `Admin impersonation required` |
| 404 | problema desconocido | `Task not found` (`Not found` en `submission_list`) |
| 404 | envío desconocido o de otra persona | `Submission not found` |
| 422 | envío rechazado | el motivo, con `details` (ver [Enviar](#enviar)) |
| 429 | demasiados intentos de entrada | `Too many login attempts` (con `Retry-After`) |
| 500 | error del servidor | `Internal Server Error` |

## Autenticación

### Entrar

`POST api/login` con los campos `username` y `password` (codificados como
URL o multipart, como los manda un formulario HTML):

```sh
curl -s -d username=alice -d password=secreta "$SITE/api/login"
```

```json
{"login_data": "1.1792065600.eyJ1Ijo…"}
```

Las demás llamadas mandan ese valor en el encabezado
`X-CMS-Authorization`:

```sh
LOGIN=$(curl -s -d username=alice -d password=secreta "$SITE/api/login" | jq -r .login_data)
curl -s -H "X-CMS-Authorization: $LOGIN" "$SITE/api/task_list"
```

Lo que vale para el formulario de entrada vale para la API: el concurso
debe permitir la autenticación con contraseña, se aplican la restricción
por IP y el bloqueo de participaciones ocultas, y también los límites de
entrada (10 intentos seguidos por usuario y después uno cada 6 segundos; un
429 dice cuánto esperar). Los datos de entrada vencen `cookie_duration`
(3 horas por defecto) después de entrar: hay que volver a entrar cuando una
llamada responde 403 `An authenticated user is required`. Cambiar la
contraseña del concursante los invalida.

Otras formas de entrar, como en CMS:

- **Entrada por IP**: en un concurso con `ip_autologin`, las peticiones
  desde la dirección de una participación ya están autenticadas (no hace
  falta el encabezado; `api/login` responde
  `{"login_data": "Already-Logged-In"}`).
- **Cookie de sesión**: un navegador que entró con el formulario puede
  llamar a la API con su cookie; `api/login` devuelve entonces el valor de
  la cookie, que también sirve como `X-CMS-Authorization`. Las peticiones
  POST sin el encabezado necesitan el token CSRF (`_xsrf`), como cualquier
  formulario.

Una llamada a `api/login` de alguien que ya entró devuelve sus datos de
entrada (el encabezado que mandó, o la cookie), o 400 si `username` nombra
a otro usuario.

### Suplantación

Con `contest_web.admin_token` definido en `cms.yaml` (vacío la desactiva),
un administrador entra como cualquier participante mandando `admin_token`
en lugar de la contraseña:

```sh
ADMIN=$(curl -s -d username=alice -d admin_token="$TOKEN" "$SITE/api/login" | jq -r .login_data)
```

Las peticiones con esos datos de entrada:

- no tienen restricción por IP (pero la entrada por IP, cuando coincide,
  va primero) ni bloqueo de participaciones ocultas, y funcionan aunque el
  concurso no permita la autenticación con contraseña;
- pueden usar `full_details`;
- aceptan estos parámetros con el valor `1` (en la URL o en el
  formulario):

| Parámetro | Llamadas | Efecto |
|---|---|---|
| `override_phase_check` | todas | ignora los tiempos del concurso (por ejemplo, enviar después del final) |
| `override_official` | `submit` | el envío es oficial aunque sea fuera del concurso (con `0`, no oficial durante él) |
| `override_max_number` | `submit` | ignora el máximo de envíos |
| `override_min_interval` | `submit` | ignora el tiempo mínimo entre envíos |

Para los demás participantes estos parámetros no tienen efecto. El token
debe ser secreto y largo (`cms ctl gen-secret` genera uno): abre todas las
cuentas de todos los concursos de la instalación.

## Llamadas

| Llamada | Cuándo | Respuesta |
|---|---|---|
| `POST api/login` | siempre | `{"login_data": "…"}` |
| `GET api/task_list` | en competencia o en modo análisis | los problemas |
| `POST api/<problema>/submit` | en competencia o en modo análisis | `{"id": "…"}` |
| `GET api/<problema>/submission_list` | en competencia o en modo análisis | los envíos al problema |
| `GET api/<problema>/submissions/<id>/details` | desde el inicio | los detalles que el concursante puede ver |
| `GET api/<problema>/submissions/<id>/full_details` | desde el inicio, solo con suplantación | todos los detalles |

"En competencia" incluye la ventana propia del concursante (concursos con
`per_user_time`), las demoras y el tiempo extra; los participantes sin
restricciones pueden usarlas en cualquier momento.

### Lista de problemas

`GET api/task_list` lista los problemas en el orden del concurso:

```json
{"tasks": [
  {"name": "sum", "statements": ["en", "es"], "submission_format": ["sum.%l"]},
  {"name": "quiz", "statements": ["en"], "submission_format": ["output_000.txt", "output_001.txt"]}
]}
```

`statements` son los idiomas con enunciado (se descargan de
`tasks/<problema>/statements/<idioma>` con el mismo encabezado).
`submission_format` lista los archivos que hay que mandar: `%l` representa
la extensión del lenguaje de programación (`sum.%l` es `sum.cpp`,
`sum.py`…); los nombres sin ella son archivos de salida.

### Enviar

`POST api/<problema>/submit` con un formulario multipart: un archivo por
elemento del formato de envío, en un campo con exactamente ese nombre, y
opcionalmente el campo `language` (como aparece en el formulario de envío,
por ejemplo `C++17 / g++`; sin él, el lenguaje se deduce de la extensión):

```sh
curl -s -H "X-CMS-Authorization: $LOGIN" \
     -F "sum.%l=@sum.cpp" -F "language=C++17 / g++" "$SITE/api/sum/submit"
```

```json
{"id": "4926046975063954292"}
```

En lugar de archivos sueltos se puede mandar un solo archivo comprimido
(zip, tar, tar.gz o tar.bz2) en un campo llamado `submission`. Los límites
son los del formulario: el máximo de envíos y el tiempo mínimo entre ellos
(del concurso y del problema), `contest_web.max_submission_length` por
archivo. Un envío rechazado responde 422 con los mensajes de CMS:

```json
{"error": "Submissions too frequent!",
 "details": "Among all tasks, you can submit again after 60 seconds from last submission."}
```

| `error` | `details` (ejemplos) |
|---|---|
| `Too many submissions!` | `You have reached the maximum limit of at most 3 submissions among all tasks.` |
| `Submissions too frequent!` | `For this task, you can submit again after 120 seconds from last submission.` |
| `Invalid submission format!` | `Please select the correct files.` (nombres de campo incorrectos, lenguaje desconocido, archivos que faltan) |
| `Submission too big!` | `Each source file must be at most 100000 bytes long.` |
| `Invalid archive format!` | `The submitted archive could not be opened.` |

El envío es oficial mientras el concursante compite y no oficial en modo
análisis, como en el sitio. El id es el que muestran las páginas (un número
de 63 bits como texto: no hay que convertirlo a número de JavaScript).

### Envíos de un problema

`GET api/<problema>/submission_list` lista los envíos del concursante al
problema, del más antiguo al más nuevo:

```json
{"list": [{"id": "4926046975063954292"}, {"id": "1291403789260491089"}]}
```

El estado y el puntaje de un envío están en las páginas del sitio, con el
mismo encabezado: `GET tasks/<problema>/submissions/<id>` (JSON con
`status`, `public_score`, `task_public_score`…) y
`GET tasks/<problema>/submissions/<id>/details` (el HTML que ve el
concursante).

### Detalles de un envío

`GET api/<problema>/submissions/<id>/details` devuelve los detalles del
puntaje de un envío ya puntuado tal como el concursante puede verlos;
`{"details": null}` mientras no está puntuado. Sin token (y fuera del modo
análisis) son los detalles de los casos públicos; con token, o en modo
análisis, los de todos los casos. El nivel de retroalimentación del
problema los filtra, como en la página de detalles:

| Nivel | Casos que se muestran |
|---|---|
| `full` | todos, con tiempo y memoria |
| `restricted` | en una subtarea no resuelta del todo, los que van hasta el primero con el peor puntaje, los demás solo por nombre; en una subtarea resuelta, todos |
| `oi_restricted` | en una subtarea no resuelta del todo, solo el primero con el peor puntaje; en una subtarea resuelta, todos |

En modo análisis el nivel es siempre `full`. Los problemas con subtareas
(`GroupMin`, `GroupMul`, `GroupThreshold`) dan una lista de subtareas:

```json
{"details": [
  {"idx": 0, "score_fraction": 0.0, "score": 0.0, "max_score": 40,
   "testcases": [{"idx": "000", "outcome": "Correct", "text": ["Output is correct"]},
                 {"idx": "001"}]},
  {"idx": 1, "score_fraction": 1.0, "score": 60.0, "max_score": 60,
   "testcases": [{"idx": "002"}, {"idx": "003"}]}
]}
```

`Sum` da directamente la lista de casos. `text` es un mensaje y sus
argumentos, sin traducir (`["Output is correct"]`, o un formato como
`["Execution killed with signal %s", "11"]`). En los detalles públicos, una
subtarea con casos privados no tiene `score`, `score_fraction` ni
`max_score` (CMS responde 500 ahí).

`GET api/<problema>/submissions/<id>/full_details` (solo con suplantación)
devuelve todos los detalles con retroalimentación completa, incluidos
`time`, `time_limit`, `time_limit_was_exceeded` y `memory` de cada caso:

```json
{"details": [
  {"idx": 1, "score_fraction": 1.0, "score": 60.0, "max_score": 60,
   "testcases": [{"idx": "003", "outcome": "Correct", "text": ["Execution timed out"],
                  "time": 1.2, "time_limit": 1.0, "time_limit_was_exceeded": true, "memory": null}]}
]}
```

## Diferencias con CMS

- El JSON es compacto (sin espacios) y los números conservan su valor pero
  no siempre su escritura: `40` puede venir como `40.0` en CMS y como `40`
  aquí.
- Los detalles públicos de una subtarea con casos privados se devuelven
  sin las claves del puntaje; CMS falla con 500.
- `api/login` desde un navegador que entró con la cookie devuelve la
  cookie; CMS falla con 500.
- La entrada por la API tiene los límites del formulario de entrada (429).
- Los errores del servidor son JSON (`{"error": "Internal Server Error"}`)
  en lugar de la página de error HTML.
- Los datos de entrada son un valor firmado con una huella de la
  contraseña, no los de CMS (que contienen la contraseña): los datos de
  entrada de una instalación de CMS no sirven aquí; hay que volver a
  entrar.

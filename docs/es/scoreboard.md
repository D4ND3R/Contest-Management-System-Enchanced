# Scoreboard público

*English: [../en/scoreboard.md](../en/scoreboard.md).*

`cms ranking-web` es el scoreboard público (RankingWebServer de CMS): la
página y el JavaScript de cmsranking, sin cambios salvo jQuery servido
localmente, el enlace "Source code" (AGPL) y los manejadores en línea
movidos a JavaScript por la Content-Security-Policy. Como el original, no
accede a la base ni a Valkey: el dispatcher activo le envía los datos por
HTTP (ProxyService de CMS), así que puede correr en un host público aparte.

## Configuración

`ranking_web` en `cms.yaml` (todas las claves en `config/cms.sample.yaml`;
cada una también con `CMS_RANKING_WEB_*`):

| Clave | Por defecto | Significado |
|---|---|---|
| `listen` | `127.0.0.1:8890` | dirección del sitio |
| `ops_listen` | `127.0.0.1:9190` | `/metrics`, `/healthz`, pprof |
| `num_proxies` | `0` | proxies inversos delante (dirección del cliente, HTTPS) |
| `data_dir` | `/var/lib/cms/ranking` | estado, logo, fotos y banderas |
| `username` | `cms` | usuario de las escrituras (HTTP basic) |
| `password` | vacío | contraseña de las escrituras; vacío: derivada de `secret_key` |
| `visibility` | `public` | `public`, `secret` o `admins` (abajo) |
| `show_id_column` | `false` | mostrar la columna del nombre de usuario |

`secret_key` es obligatoria y debe ser la de los demás servicios: de ella
se derivan los enlaces secretos, los enlaces de administrador, la clave de
la cookie y la contraseña por defecto.

El dispatcher envía a cada entrada de `dispatcher.rankings`:

```yaml
dispatcher:
  rankings:
    - url: http://127.0.0.1:8890/   # como el dispatcher llega al ranking
      username: ""                  # vacío: cms
      password: ""                  # vacío: derivada de secret_key
      contests: [ioi2026_dia1]      # vacío: todos los concursos
      freeze: 1h                    # 0: sin congelamiento
  ranking_refresh: 30s
```

Con la misma `secret_key` en los dos lados no hacen falta credenciales.
Para hablar con un RankingWebServer de CMS (o dejar que un ProxyService o
`cmsRWSHelper` de CMS escriban aquí) se pone el mismo usuario y contraseña
en los dos lados; también se aceptan en la URL
(`http://usuario:clave@host:8890/`). `cms ctl config show` oculta las
contraseñas.

Se envía lo mismo que en CMS: el concurso (descripción, inicio y fin del
grupo principal, precisión), sus problemas (título, puntaje máximo y
encabezados de subtareas del dataset activo, modo de puntaje), los usuarios
y equipos de las participaciones visibles, y los envíos oficiales con su
puntaje en el dataset activo (con el detalle por subtarea) y sus tokens.
Las participaciones ocultas y los envíos no oficiales nunca se envían. Los
puntajes salen en cuanto se guardan; concursos, problemas, usuarios y
equipos se releen cada `ranking_refresh` y solo se envía lo que cambió.

No se pierde nada: cuando un dispatcher pasa a ser el activo (arranque,
failover), después de cada error (reintentando con espera de 1 s a 1 min)
y cada 10 minutos se envía todo de nuevo; el scoreboard ignora lo que no
cambió. Métricas: `cms_dispatcher_ranking_up{ranking}` y
`cms_dispatcher_ranking_requests_total{ranking,result}`.

Como en CMS, nunca se borra nada de un ranking: para quitar un usuario,
problema o concurso se usa la API REST (borra también lo que depende de
él):

```sh
curl -u cms:CONTRASEÑA -X DELETE http://127.0.0.1:8890/users/USUARIO
```

Las claves son los nombres de CMS con los caracteres que no son letras ni
dígitos codificados como `_` y su código hexadecimal (`-` es `_2d`, `_` es
`_5f`).

## Datos

`data_dir` guarda `state.json` (una instantánea escrita de forma atómica) y
`journal.jsonl` (las escrituras desde la instantánea, cada una escrita y
sincronizada a disco antes de confirmarla). Los reinicios no pierden nada;
para respaldar, copiar el directorio con el servicio detenido, o copiar
`state.json` y después `journal.jsonl`. Un `data_dir` con la estructura del
`lib_dir` de CMS (`contests/`, `tasks/`... con un JSON por entidad) se
importa en el primer arranque.

El logo va en `data_dir/logo.png` (o `.jpg`, `.gif`, `.bmp`; 200×160
recomendado), las banderas en `data_dir/flags/<clave del equipo>.png` y
las fotos en `data_dir/faces/<clave del usuario>.png`.

## Visibilidad

- **public**: todos ven el scoreboard (como CMS).
- **secret**: solo quien abrió el enlace secreto de alguno de sus
  concursos, y los administradores. La administración muestra el enlace en
  la página de ranking del concurso: `<dirección del scoreboard>?secret=<token>`,
  con el token derivado de `secret_key` y del nombre del concurso (cambia
  si cambia cualquiera de los dos). La dirección del scoreboard se
  configura en la página Servidor; en instalaciones por puertos (`":8890"`)
  el enlace usa el host con el que navega el administrador. Quien lo abre
  recibe una cookie (12 h) y se lo redirige a la dirección sin el token.
- **admins**: solo administradores. Un enlace de administrador es
  `<dirección del scoreboard>?admin=<token>` con un token de
  `config.RankingAdminToken` (firmado con una clave derivada de
  `secret_key`, válido 10 minutos); el navegador conserva el acceso 12 h en
  una cookie firmada. Los clientes con las credenciales de escritura (HTTP
  basic) también cuentan como administradores.

Los archivos estáticos (JavaScript, estilos, imágenes por defecto) son
públicos en todos los modos; la página, los datos, los eventos, el logo,
las fotos y las banderas no. Los envíos y subchanges (`/submissions/`,
`/subchanges/`) solo los leen administradores en todos los modos.

## Congelamiento

Con `freeze: 1h` en una entrada de `dispatcher.rankings`, los concursos
enviados a ese ranking llevan `"freeze": <fin − 1 h>` (una extensión del
formato de concurso de CMS; un RankingWebServer de CMS la ignora). El
público deja de ver los puntajes, tokens y envíos hechos desde ese momento;
los envíos anteriores siguen contando aunque se evalúen después. Los
administradores ven todo. Para descongelar, poner `freeze: 0` y reiniciar
los dispatchers: el concurso se reenvía sin el campo y todos los
espectadores reciben los puntajes nuevos al instante. Sin reiniciar:

```sh
curl -u cms:CONTRASEÑA -X PUT -H 'Content-Type: application/json' \
  -d '{"name":"…","begin":…,"end":…,"score_precision":0}' \
  http://127.0.0.1:8890/contests/CLAVE_DEL_CONCURSO
```

(el dispatcher volverá a poner el congelamiento en su siguiente lectura si
no se cambió también su configuración).

Cada ranking puede tener su congelamiento: por ejemplo uno público con
`freeze: 1h` y uno interno sin congelar.

## Proxies inversos y tiempo real

La página recibe los cambios como server-sent events en `/events` (los
mismos eventos que CMS: `contest`, `task`, `team`, `user`, `score`, y
`reload` cuando hay que recargar la página). El servidor manda un ping de
inmediato y cada 15 s, y `X-Accel-Buffering: no`. Detrás de un proxy:

- no almacenar ni comprimir `*/events` (nginx: `proxy_buffering off` o el
  encabezado anterior, y dejar la ruta fuera de `gzip`; Caddy: excluirla de
  `encode`);
- el timeout de lectura del proxy debe ser mayor que 15 s;
- servirlo por HTTP/2 (TLS en el proxy) para que el stream abierto de cada
  espectador no ocupe una de las 6 conexiones HTTP/1.1 del navegador;
- funciona bajo un prefijo (`location ^~ /ranking/ { proxy_pass http://127.0.0.1:8890/; }`):
  todas las direcciones de la página son relativas;
- configurar `num_proxies` para que las cookies sean `Secure` detrás de
  HTTPS.

Las respuestas de datos (`/scores`, `/history` y las listas) se calculan
una vez por cambio y se comparten entre todos los espectadores.

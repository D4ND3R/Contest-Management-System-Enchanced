# Configuración

Todos los servicios de una instalación leen el **mismo** archivo
`cms.yaml`. Se busca en este orden:

1. `--config ARCHIVO` (antes del subcomando: `cms --config /ruta/cms.yaml contest-web`);
2. la variable `CMS_CONFIG`;
3. `./cms.yaml` y `/etc/cms/cms.yaml`.

Sin archivo se usan los valores por defecto más las variables de entorno
(cómodo en contenedores). [`config/cms.sample.yaml`](../../config/cms.sample.yaml)
documenta cada opción con su valor por defecto.

## Variables de entorno

Cada opción se sobrescribe con `CMS_` más su ruta en mayúsculas, con `_`
entre niveles; el entorno gana sobre el archivo:

| Opción | Variable |
|---|---|
| `database.url` | `CMS_DATABASE_URL` |
| `contest_web.listen` | `CMS_CONTEST_WEB_LISTEN` |
| `sandbox.trusted.max_time_sec` | `CMS_SANDBOX_TRUSTED_MAX_TIME_SEC` |

Agregando `_FILE` el valor se lee de un archivo (secretos de Docker):
`CMS_SECRET_KEY_FILE=/run/secrets/cms_secret`. La lista completa:
`cms ctl config env`.

Las duraciones se escriben `90s`, `3h`, `1h30m`; un número solo son segundos.
Una clave desconocida en el archivo es un error (así un typo no pasa
inadvertido).

## La clave secreta (`secret_key`)

Firma las cookies y los tokens CSRF y deriva las llaves de los enlaces
secretos del scoreboard. Debe ser **la misma en todos los servicios**; las
tres webs no arrancan sin ella. Se genera **una sola vez** por instalación:

```sh
cms ctl gen-secret
```

## Comprobar la configuración

```sh
cms ctl config check   # valida y avisa si falta secret_key
cms ctl config show    # configuración efectiva, con los secretos ocultos
```

## Puertos por defecto

| Servicio | Sitio | Operaciones (métricas, salud, pprof) |
|---|---|---|
| contest-web | 127.0.0.1:8888 | 127.0.0.1:9188 |
| admin-web | 127.0.0.1:8889 | 127.0.0.1:9189 |
| ranking-web | 127.0.0.1:8890 | 127.0.0.1:9190 |
| dispatcher | — | 127.0.0.1:9191 |
| worker | — | 127.0.0.1:9192 |
| monitor | — | 127.0.0.1:9193 |

Los puertos de operaciones exponen `/metrics` (Prometheus), `/healthz` con
el detalle de cada dependencia y `/debug/pprof/`: déjelos en localhost o en
una red de administración. Los sitios públicos solo responden
`{"status":"ok"}` en `/healthz`.

## Zona horaria

`timezone` es la zona inicial del servidor. Habrá **una sola zona para toda
la instalación**; cuando la administración la cambie en la página Servidor,
el valor guardado en la base reemplaza al del archivo.

## Código fuente (AGPL)

Cada sitio enlaza en `/source` al código de la versión que corre. Si
modifica el programa, ponga en `source_url` la dirección de su repositorio.

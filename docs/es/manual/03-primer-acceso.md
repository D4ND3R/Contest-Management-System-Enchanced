# 3. Primer acceso y cuentas de administrador

*English: [First access and admin accounts](../../en/manual/03-first-steps.md)*

Cómo entrar por primera vez a la administración, recorrer el menú, crear
cuentas para tu equipo con los permisos justos y recuperar el acceso si se
pierde una contraseña.

## Contenido

1. [Entrar por primera vez](#1-entrar-por-primera-vez)
2. [Ver la administración en español](#2-ver-la-administración-en-español)
3. [Un recorrido por el menú](#3-un-recorrido-por-el-menú)
4. [Cuentas para tu equipo y sus permisos](#4-cuentas-para-tu-equipo-y-sus-permisos)
5. [Cambiar tu contraseña](#5-cambiar-tu-contraseña)
6. [Recuperar el acceso (contraseña perdida)](#6-recuperar-el-acceso-contraseña-perdida)
7. [La página Servidor](#7-la-página-servidor)
8. [Antes de seguir: revisa la instalación](#8-antes-de-seguir-revisa-la-instalación)
9. [Qué hacer si…](#9-qué-hacer-si)

---

## 1. Entrar por primera vez

1. Abre la dirección de la **administración** que mostró el instalador:
   `https://admin.tu-dominio` o `http://IP-DEL-SERVIDOR:8001/`.
2. Escribe el usuario `admin` (o el que diste con `--admin-user`) y la
   contraseña que mostró el instalador.
3. Pulsa *Acceder* (*Login*).

Arriba a la derecha verás *Hola, Admin* (*Hello,*): tu nombre es un enlace a
la lista de administradores.

> La sesión se cierra sola tras un tiempo sin abrir páginas. Varios
> intentos fallidos seguidos frenan nuevos intentos desde esa dirección
> durante unos segundos: es una protección contra ataques, no un error.

## 2. Ver la administración en español

La administración no tiene selector de idioma: usa el idioma preferido de
tu navegador. Para verla en español, pon el español primero en la
configuración de idiomas del navegador y recarga la página. Más detalles
en el [capítulo 1](01-conceptos.md#el-idioma-de-la-interfaz).

## 3. Un recorrido por el menú

El menú de la izquierda tiene dos partes:

**Arriba, siempre visible:**

| Menú | Para qué |
|---|---|
| *Carátula* (*Overview*) | estado general: envíos pendientes, jueces, cola, avisos |
| *Uso de recursos* (*Resource usage*) | CPU, memoria y disco de cada servicio |
| *Jueces* (*Judges*) | estado de las máquinas que evalúan ([cap. 9](09-durante-el-concurso.md)) |
| *Servidor* (*Server*) | direcciones públicas de los sitios (sección 7) |
| *Registro de auditoría* (*Audit log*) | quién cambió qué y cuándo |
| *Concursos* (*Contests*) | elegir un concurso o *(crear un concurso...)* ([cap. 4](04-concursos.md)) |
| *Problemas* (*Tasks*) | todos los problemas ([cap. 5](05-problemas.md)) |
| *Usuarios* (*Users*) | todas las personas, importar CSV ([cap. 8](08-usuarios-y-equipos.md)) |
| *Equipos* (*Teams*) | equipos o delegaciones |

**Al elegir un concurso** aparecen sus páginas: *General*, *Ranking*,
*Envíos* (*Submissions*), *Pruebas de usuario* (*User tests*), *Usuarios*,
*Grupos* (*Groups*), *Problemas*, *Anuncios* (*Announcements*), *Preguntas*
(*Questions*) y *Registro de auditoría*
([cap. 4](04-concursos.md) y [cap. 9](09-durante-el-concurso.md)).

Las páginas se **actualizan solas**: un indicador junto a tu nombre está
verde cuando las actualizaciones en vivo funcionan, amarillo mientras se
conecta y rojo si se perdió la conexión (entonces la página se consulta
cada tanto, igual funciona).

## 4. Cuentas para tu equipo y sus permisos

Lo recomendable es que **cada persona tenga su propia cuenta**: así el
[registro de auditoría](09-durante-el-concurso.md) dice quién hizo cada
cambio, y puedes dar a cada uno sólo lo que necesita.

| Permiso | Puede | Ejemplo |
|---|---|---|
| *Todos los permisos* (*All permissions*) | todo | coordinador del concurso |
| *Permisos de mensajería* (*Messaging permissions*) | responder, ignorar y tomar preguntas; mandar anuncios y mensajes privados | quien atiende las preguntas durante la prueba |
| *Permisos de preparador de problemas* (*Problem preparer permissions*) | crear y modificar problemas, enunciados, adjuntos, datasets y casos, y probar soluciones; no usuarios ni concursos | el autor de un problema |
| ninguno | ver todo sin cambiar nada | un observador |

**Para crear una cuenta:**

1. Pulsa tu nombre (arriba, *Hola, …*): se abre la *Lista de
   administradores* (*Admins list*).
2. Pulsa *Agregar un administrador* (*Add a new admin*).
3. Completa el nombre, el usuario (letras, dígitos, `_` y `-`) y una
   contraseña, marca los permisos y pulsa *Crear* (*Create*).
4. Entrégale el usuario y la contraseña por un medio seguro; podrá
   cambiarla él mismo (sección 5).

También desde la consola:

```sh
sudo cmsctl add-admin --name "María Pérez" --role messaging maria
```

genera una contraseña y la muestra una sola vez. `--role` puede ser `all`,
`messaging`, `tasks` o `readonly`.

**Desactivar o borrar una cuenta:** en la página del administrador,
desmarca *Habilitado* (*Enabled*) para que no pueda entrar, o usa *Eliminar
este administrador* (*Delete this admin*). CMS no permite dejar el sitio sin
ningún administrador habilitado con todos los permisos.

## 5. Cambiar tu contraseña

1. Pulsa tu nombre y luego tu usuario en la lista.
2. Escribe la contraseña nueva en el campo de contraseña. Si lo dejas
   vacío, se conserva la anterior.
3. Pulsa *Actualizar* (*Update*).

Cambiar la contraseña cierra las otras sesiones abiertas de esa cuenta.

## 6. Recuperar el acceso (contraseña perdida)

La contraseña del primer administrador se muestra **una sola vez** al
instalar y no se guarda en ningún lado. Si se pierde, desde el servidor:

```sh
sudo cmsctl admin-password admin
```

Genera una contraseña nueva para `admin` y la muestra una sola vez:

```
password: Ab3dEf6hIj9kLm2nOp5q
The password is shown only now: store it safely.
```

- Cambia `admin` por el usuario que corresponda. Si no recuerdas los
  usuarios, el comando con un nombre inexistente los lista.
- `--enable` además vuelve a habilitar una cuenta deshabilitada.
- Para poner una contraseña elegida por ti sin que quede en el historial de
  la consola: `sudo cmsctl admin-password admin --password-file -` y
  escríbela (termina con Enter y Ctrl+D).

Si ya no queda ningún administrador, crea uno nuevo:
`sudo cmsctl add-admin admin`.

> **Nunca compartas contraseñas por chat o en capturas de pantalla.** Si
> una contraseña quedó expuesta, cámbiala (sección 5).

## 7. La página Servidor

*Servidor* (*Server*) guarda las direcciones públicas del sitio de los
concursantes y del scoreboard. La administración las usa para mostrar
enlaces (por ejemplo, «abrir el scoreboard»). El instalador las configura
solo; cámbialas si después pones un dominio o cambias de puertos. Detalles
en [../administracion.md](../administracion.md#página-servidor).

## 8. Antes de seguir: revisa la instalación

Antes de preparar el primer concurso, comprueba que todo funciona:

```sh
sudo cmsctl doctor
```

Todas las líneas deberían ser `PASS` (es normal un `WARN` de «no hay
concursos todavía»). Cada `FAIL` o `WARN` trae una línea `fix:` con la
solución. Ver el [capítulo 14](14-solucion-de-problemas.md).

## 9. Qué hacer si…

**…la página de la administración no abre.** Revisa la dirección y el
puerto; corre `sudo cmsctl doctor`. Si usas cortafuegos, el puerto debe
estar abierto ([cap. 2](02-instalacion.md#9-el-cortafuegos)).

**…dice *Failed to log in.*** Usuario o contraseña incorrectos. Recupera la
contraseña con `sudo cmsctl admin-password USUARIO`.

**…la administración sale en inglés.** Configura el idioma del navegador
(sección 2).

**…un compañero no puede hacer algo («forbidden»).** Le falta el permiso:
dáselo desde su página de administrador.

---

**Siguiente:** [4. Concursos](04-concursos.md) ·
**Anterior:** [2. Instalación](02-instalacion.md) ·
[Índice](README.md)

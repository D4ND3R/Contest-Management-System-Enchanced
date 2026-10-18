# 8. Usuarios, equipos y participaciones

*English: [Users, teams and participations](../../en/manual/08-users-and-teams.md)*

Cómo registrar a los concursantes (uno por uno o todos de una vez con un
CSV), cómo agruparlos en equipos y cómo ajustar la participación de cada
uno en un concurso (tiempo extra, oculto, sin restricciones…).

## Contenido

1. [Tres ideas: usuario, participación, equipo](#1-tres-ideas-usuario-participación-equipo)
2. [Importar usuarios desde un CSV (recomendado)](#2-importar-usuarios-desde-un-csv-recomendado)
3. [Preparar el CSV en una planilla](#3-preparar-el-csv-en-una-planilla)
4. [Crear un usuario a mano](#4-crear-un-usuario-a-mano)
5. [Los campos de un usuario](#5-los-campos-de-un-usuario)
6. [Agregar usuarios a un concurso](#6-agregar-usuarios-a-un-concurso)
7. [La participación: tiempo extra, oculta, sin restricciones…](#7-la-participación-tiempo-extra-oculta-sin-restricciones)
8. [Equipos](#8-equipos)
9. [Entregar las credenciales](#9-entregar-las-credenciales)
10. [Borrar usuarios](#10-borrar-usuarios)
11. [Errores comunes](#11-errores-comunes)

---

## 1. Tres ideas: usuario, participación, equipo

- Un **usuario** es una persona: nombre, apellido, nombre de usuario y
  contraseña. Existe una vez aunque participe en muchos concursos.
- Una **participación** es «este usuario está en este concurso», con su
  grupo (horario), su equipo y ajustes propios (tiempo extra, etc.). **Un
  usuario sin participación en un concurso no puede entrar a ese
  concurso.**
- Un **equipo** (*team*) agrupa usuarios, por ejemplo por colegio o región:
  el scoreboard muestra la bandera o el nombre del equipo.

## 2. Importar usuarios desde un CSV (recomendado)

1. Prepara un archivo CSV con una fila por usuario y las columnas
   `username,password,first_name,last_name` y, opcionalmente, `email`:

   ```csv
   username,password,first_name,last_name,email
   ana01,Kq7-mzt-42,Ana,Pérez,ana@colegio.cl
   beto02,Rv3-plx-91,Alberto,Soto,
   carla03,Tm8-dqe-55,Carla,Núñez,
   ```

   - La primera fila (la que empieza con `username`) es opcional y se
     ignora.
   - El correo es opcional (puede quedar vacío o no estar la columna).
2. En la administración: *Usuarios* (*Users*) → al final de la lista,
   *Importar usuarios* (*Import users*).
3. En *Archivo CSV* (*CSV file*) elige el archivo.
4. En *Agregarlos también al concurso* (*Also add them to contest*) elige
   el concurso (o *None* para sólo crear los usuarios).
5. Pulsa *Importar* (*Import*).

Resultado: «Usuarios importados: 3 usuario(s) creados, 0 actualizados, 3
agregados al concurso».

Reglas:

- **Todo o nada**: si alguna fila tiene un error, no se importa ninguna y
  el aviso dice qué líneas corregir (por ejemplo «line 3: invalid username
  "Ana Pérez" (letters, digits, _ and - only)»).
- El **nombre de usuario** sólo admite letras sin tilde, dígitos, `_` y
  `-`. Sin espacios ni `ñ`.
- La **contraseña** no puede estar vacía. Se guarda en texto plano (como en
  los importadores del CMS original), para que puedas volver a
  entregársela a un concursante que la olvidó.
- Un nombre de usuario que ya existe **se actualiza** con los datos nuevos
  (útil para corregir una lista).
- Los usuarios agregados al concurso entran en su grupo principal.
  Los que ya participaban quedan igual.
- Máximo 10 000 filas por archivo.

## 3. Preparar el CSV en una planilla

En Excel, LibreOffice Calc o Google Sheets:

1. Columnas A a E: `username`, `password`, `first_name`, `last_name`,
   `email`.
2. **Generar contraseñas aleatorias**: en la columna B, por ejemplo
   `=CONCATENAR(CAR(ALEATORIO.ENTRE(65;90));ALEATORIO.ENTRE(1000;9999);CAR(ALEATORIO.ENTRE(97;122)))`
   (en Google Sheets, `CHAR` y `RANDBETWEEN`). Luego **copia y pega como
   valores** para que no cambien cada vez.
3. **Generar usuarios** a partir del nombre: mejor un código fijo
   (`d4oi001`, `d4oi002`…) que derivarlo del nombre (tildes y espacios no
   están permitidos).
4. Guarda como **CSV UTF-8 delimitado por comas**:
   - Excel: *Guardar como* → *CSV UTF-8 (delimitado por comas)*.
   - LibreOffice: *Guardar como* → *Texto CSV*, juego de caracteres
     *Unicode (UTF-8)*, separador de campo `,`.
   - Google Sheets: *Archivo* → *Descargar* → *Valores separados por
     comas*.

> **Cuidado:** Excel en español a veces guarda con **punto y coma** (`;`)
> como separador. Abre el archivo con un editor de texto y comprueba que
> los campos estén separados por comas. Si ves `;`, cambia el separador al
> guardar.

## 4. Crear un usuario a mano

*Usuarios* → *(crear un usuario...)* (*(create new user...)*): completa los
campos de la sección 5 y créalo. Después agrégalo al concurso (sección 6).

## 5. Los campos de un usuario

| Campo | Qué es | Ejemplo |
|---|---|---|
| *Nombre* (*First name*) | nombre que se muestra | `Ana` |
| *Apellido* (*Last name*) | apellido | `Pérez` |
| *Nombre de usuario* (*Username*) | para entrar; letras, dígitos, `_`, `-` | `ana01` |
| Contraseña | texto plano (*Plain text*) o cifrada (*Hashed (bcrypt)*) | |
| *Correo electrónico* (*E-mail*) | opcional | |
| *Zona horaria* (*Timezone*) | para mostrarle las horas en su hora local | `America/Santiago` |
| *Idiomas preferidos* (*Preferred languages*) | códigos de idioma de los enunciados que se le destacan, del más al menos preferido | `es, en` |

Con contraseña cifrada (bcrypt) nadie puede volver a leerla: si el
concursante la olvida hay que ponerle una nueva.

## 6. Agregar usuarios a un concurso

- **Al importar el CSV**, con *Agregarlos también al concurso*.
- **Desde el concurso**: página *Usuarios* del concurso → *Agregar un
  usuario:* (*Add a new user:*) → elegir → *Agregar usuario* (*Add user*).
- **Desde el usuario**: su página → *Agregar participación* (*Add
  participation*) → elegir el concurso.

La lista *Usuarios* del concurso muestra todos los participantes y su
grupo. Al hacer clic en uno se abre su participación.

## 7. La participación: tiempo extra, oculta, sin restricciones…

En la página de la participación (*Información de la participación*):

| Campo | Para qué | Cuándo usarlo |
|---|---|---|
| *Grupo* (*Group*) | su horario ([cap. 4](04-concursos.md#9-grupos)) | rinde en otro turno |
| *Equipo* (*Team*) | el equipo que representa | delegaciones |
| Contraseña de la participación | una contraseña sólo para este concurso (vacía = la del usuario). **Atención: se guarda en texto plano.** | concursos con contraseñas distintas |
| *Participación oculta* (*Hidden participation*) | no aparece en el ranking ni en el scoreboard | invitados, pruebas, fuera de concurso |
| *Participación sin restricciones* (*Unrestricted participation*) | ignora los límites de tiempo y de envíos | cuentas de prueba de los organizadores |
| *Dirección IP o subred* (*IP address or subnet*) | sólo puede entrar desde esa IP/red (si el concurso restringe por IP) | laboratorios con IP fija |
| *Retraso* (*Delay*) | su concurso empieza y termina más tarde | llegó tarde por un motivo justificado |
| *Tiempo extra* (*Extra time*) | tiempo adicional al final | corte de luz, necesidades especiales |

Ejemplo: un concursante perdió 10 minutos por un corte de luz. En su
participación, *Tiempo extra* = 10 minutos → *Actualizar*. Su contador se
ajusta en el acto.

## 8. Equipos

*Equipos* (*Teams*) → *(crear un equipo...)*: un **código** corto (letras,
dígitos, `_`, `-`; por ejemplo `CL-RM`) y un **nombre** (`Región
Metropolitana`).

Para muchos equipos, al final de la lista *Importar equipos* (*Import
teams*) con un CSV `codigo,nombre`:

```csv
code,name
CL-RM,Región Metropolitana
CL-VA,Valparaíso
```

Luego asigna el equipo en cada participación. La bandera del scoreboard se
configura en el scoreboard ([cap. 11](11-scoreboard.md)).

## 9. Entregar las credenciales

CMS no imprime credenciales. Lo práctico:

1. Conserva la planilla con la que hiciste el CSV.
2. Combina correspondencia (Word, Docs) para imprimir una tarjeta por
   concursante con su usuario, contraseña y la dirección del sitio, o
   envíalas por correo.
3. Entrega junto con la hoja para concursantes del
   [capítulo 10](10-sitio-del-concursante.md#hoja-para-los-concursantes).

## 10. Borrar usuarios

*Usuarios* → seleccionar → *Eliminar* (*Remove*). Pide confirmación y
**borra también sus participaciones y envíos**. Para sacarlo sólo de un
concurso, usa *Eliminar del concurso* (*Remove from contest*) en la página
*Usuarios* del concurso.

## 11. Errores comunes

| Problema | Causa | Arreglo |
|---|---|---|
| «invalid username» al importar | espacios, tildes o `ñ` en el usuario | usa códigos (`ana01`) |
| «expected 4 or 5 columns» | separador `;` o comas dentro de un nombre | guarda con comas; un nombre con coma va entre comillas `"Pérez, Ana"` |
| La primera línea da error | un encabezado con otros nombres de columna | el encabezado debe empezar con `username`, o quítalo |
| El concursante no puede entrar | no tiene participación en ese concurso, o la contraseña de la participación es otra | revisa la página *Usuarios* del concurso |
| Aparece en el scoreboard alguien de prueba | no se marcó *Participación oculta* | márcala: desaparece del scoreboard en segundos |

---

**Siguiente:** [9. Durante el concurso](09-durante-el-concurso.md) ·
**Anterior:** [7. Importar](07-importar.md) ·
[Índice](README.md)

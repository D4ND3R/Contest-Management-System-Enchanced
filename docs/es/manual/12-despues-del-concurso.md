# 12. Después del concurso

*English: [After the contest](../../en/manual/12-after-the-contest.md)*

Cerrar bien un concurso: comprobar que todo está evaluado, publicar y
exportar los resultados, guardar la evidencia, permitir que los
concursantes sigan practicando y dejar el sistema listo para el próximo.

## Contenido

1. [Comprobar que todo está evaluado](#1-comprobar-que-todo-está-evaluado)
2. [Responder lo pendiente](#2-responder-lo-pendiente)
3. [Publicar los resultados](#3-publicar-los-resultados)
4. [Exportar los resultados](#4-exportar-los-resultados)
5. [Guardar la evidencia: respaldo](#5-guardar-la-evidencia-respaldo)
6. [Dejar practicar: modo análisis](#6-dejar-practicar-modo-análisis)
7. [Archivar un problema para reutilizarlo](#7-archivar-un-problema-para-reutilizarlo)
8. [Preparar el próximo concurso](#8-preparar-el-próximo-concurso)
9. [Datos personales](#9-datos-personales)

---

## 1. Comprobar que todo está evaluado

Antes de dar resultados, ningún envío debe estar pendiente:

- La *Carátula* (*Overview*) no muestra envíos compilando ni evaluando, y
  ninguno «Cannot compile/evaluate (please check)».
- Desde la consola:

  ```sh
  sudo cmsctl judging-status
  ```

  cuenta los envíos de cada problema por estado. Todos deben estar
  puntuados (*scored*) o con compilación fallida. «Gave up» son envíos que
  fallaron por problemas del juez: reevalúalos ([cap. 9](09-durante-el-concurso.md#5-reevaluar)).

## 2. Responder lo pendiente

En *Preguntas*, responde o ignora las preguntas pendientes, para que no
queden concursantes esperando.

## 3. Publicar los resultados

- Si el scoreboard estaba **congelado**, descongélalo
  ([cap. 11](11-scoreboard.md#4-congelar-el-scoreboard-al-final)) en el
  momento de la premiación.
- Si estaba en modo `secret`, pásalo a `public` o comparte el enlace
  secreto.

## 4. Exportar los resultados

En la administración, *Ranking* del concurso: «Download as csv, text». El
CSV se abre en cualquier planilla y trae una fila por concursante con el
puntaje de cada problema y el total, para el acta o el certificado.

## 5. Guardar la evidencia: respaldo

Haz un respaldo completo (base de datos, archivos y configuración) justo al
terminar:

```sh
sudo cmsctl backup --label final-d4oi2026
```

Queda en `/var/lib/cms/backups/`. Cópialo **fuera del servidor**:

```sh
scp usuario@servidor:/var/lib/cms/backups/cms-backup-*final-d4oi2026*.tar.gz .
```

El respaldo contiene las claves de la configuración: guárdalo con el mismo
cuidado que el servidor. Restaurarlo: [cap. 13](13-mantenimiento.md#restaurar-un-respaldo).

## 6. Dejar practicar: modo análisis

El **modo análisis** permite que los concursantes sigan enviando después
del concurso, sin afectar los resultados oficiales. Se activa en la página
del concurso con sus fechas de inicio y fin ([cap. 4](04-concursos.md#7-horario-del-concurso-y-modo-análisis)).

## 7. Archivar un problema para reutilizarlo

En la página del problema, *Descargar como paquete* (*Download as a
package*) baja un zip con todo el problema en el formato propio, listo para
importarlo en otro concurso o en otra instalación. Desde la consola:

```sh
sudo cmsctl export-task baches baches.zip
```

## 8. Preparar el próximo concurso

- **Concurso nuevo con otros problemas**: crea otro concurso; los usuarios
  se reutilizan.
- **Borrar los concursos de prueba**: en *Concursos*, elige el concurso y
  elimínalo. **Borra también sus participaciones y envíos.** Desaparece del
  scoreboard solo ([cap. 11](11-scoreboard.md#6-lo-borrado-desaparece-solo)).
- **Empezar todo de cero** (sin concursos, usuarios ni envíos): una
  instalación limpia con `--clean` ([cap. 2](02-instalacion.md#8-instalación-limpia---clean)),
  que respalda antes de borrar.

## 9. Datos personales

CMS guarda nombres, correos, contraseñas en texto plano (si se importaron
así), direcciones IP y el código enviado por los concursantes. Después del
concurso:

- guarda el respaldo final en un lugar seguro y con acceso restringido;
- borra los concursantes que ya no necesites;
- si publicas el ranking, publica sólo lo necesario.

---

**Siguiente:** [13. Mantenimiento](13-mantenimiento.md) ·
**Anterior:** [11. Scoreboard](11-scoreboard.md) ·
[Índice](README.md)

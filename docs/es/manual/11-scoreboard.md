# 11. El scoreboard público

*English: [The public scoreboard](../../en/manual/11-scoreboard.md)*

El **scoreboard** (tabla de posiciones pública) muestra en vivo los
puntajes de los concursantes. Es el mismo del CMS original. Este capítulo
explica quién puede verlo, cómo congelarlo al final, cómo ponerle logo y
banderas, y qué hacer si muestra algo que no debería. La referencia técnica
está en [../scoreboard.md](../scoreboard.md).

## Contenido

1. [Qué muestra y cómo se actualiza](#1-qué-muestra-y-cómo-se-actualiza)
2. [Cambiar la configuración del scoreboard](#2-cambiar-la-configuración-del-scoreboard)
3. [Quién puede verlo](#3-quién-puede-verlo)
4. [Congelar el scoreboard al final](#4-congelar-el-scoreboard-al-final)
5. [Logo, banderas y fotos](#5-logo-banderas-y-fotos)
6. [Lo borrado desaparece solo](#6-lo-borrado-desaparece-solo)
7. [Empezar el scoreboard de cero](#7-empezar-el-scoreboard-de-cero)
8. [Qué hacer si…](#8-qué-hacer-si)

---

## 1. Qué muestra y cómo se actualiza

- Una fila por concursante (usuario visible con participación en el
  concurso), con su equipo, el puntaje de cada problema y el total.
- Al hacer clic en un concursante, la historia de sus envíos.
- Se actualiza solo en cuanto se puntúa un envío.

No aparecen: las participaciones **ocultas**, los envíos **no oficiales** ni
los concursos sin horario. Las páginas de la administración tienen su
propio *Ranking* completo ([cap. 9](09-durante-el-concurso.md#9-el-ranking-de-la-administración)).

El scoreboard está siempre en inglés, como en el CMS original.

## 2. Cambiar la configuración del scoreboard

La configuración está en `/etc/cms/cms.yaml` (el instalador la conserva al
reinstalar). Para cambiarla:

```sh
sudo nano /etc/cms/cms.yaml          # o el editor que prefieras
sudo cmsctl config check             # revisa que el archivo sea válido
sudo systemctl restart cms-ranking-web cms-dispatcher
```

Las dos secciones que importan:

```yaml
ranking_web:
  visibility: public      # public, secret o admins (sección 3)
  show_id_column: false   # mostrar la columna con el nombre de usuario
dispatcher:
  rankings:
    - url: http://127.0.0.1:8002/
      freeze: 0           # congelamiento (sección 4)
      prune: true         # borrar lo que ya no existe (sección 6)
```

## 3. Quién puede verlo

| `visibility` | Quién lo ve |
|---|---|
| `public` (por defecto) | cualquiera con la dirección |
| `secret` | sólo quien tenga el **enlace secreto** del concurso, y los administradores |
| `admins` | sólo administradores |

**El enlace secreto** está en la página *Ranking* del concurso, en la
administración: «Secret link to the public scoreboard (whoever has it can
see the scoreboard)». Compártelo sólo con quienes deban verlo (por ejemplo,
los entrenadores). Para que aparezca, la dirección pública del scoreboard
debe estar en la página *Servidor* ([cap. 3](03-primer-acceso.md#7-la-página-servidor)).

## 4. Congelar el scoreboard al final

Es común «congelar» el scoreboard la última hora: el público deja de ver
los cambios y el resultado final se revela en la ceremonia.

1. Antes de esa última hora, en `/etc/cms/cms.yaml`:
   ```yaml
   dispatcher:
     rankings:
       - url: http://127.0.0.1:8002/
         freeze: 1h
   ```
2. `sudo systemctl restart cms-dispatcher`.
3. Desde una hora antes del fin, el público no ve los puntajes nuevos. Los
   administradores ven todo.
4. Para revelar: `freeze: 0` y `sudo systemctl restart cms-dispatcher`.
   Todos ven los puntajes finales al instante.

## 5. Logo, banderas y fotos

Los archivos van en `/var/lib/cms/ranking`:

| Qué | Archivo |
|---|---|
| logo | `/var/lib/cms/ranking/logo.png` (200×160 recomendado) |
| bandera de un equipo | `/var/lib/cms/ranking/flags/<código del equipo>.png` |
| foto de un concursante | `/var/lib/cms/ranking/faces/<usuario>.png` |

Ejemplo:

```sh
sudo cp logo.png /var/lib/cms/ranking/logo.png
sudo mkdir -p /var/lib/cms/ranking/flags
sudo cp chile.png /var/lib/cms/ranking/flags/CL.png
sudo chown -R cms:cms /var/lib/cms/ranking
```

Si el código o el usuario tiene caracteres que no son letras ni dígitos, el
nombre de archivo los codifica (`-` es `_2d`): `CL-RM` → `CL_2dRM.png`.

## 6. Lo borrado desaparece solo

Si borras un concurso, un problema, un usuario o un equipo en la
administración, o marcas una participación como **oculta**, desaparece del
scoreboard **por sí solo en menos de un minuto** (el scoreboard se
sincroniza con la base de datos cada 30 segundos). No hace falta hacer
nada.

Esto se puede desactivar (`prune: false`) si un mismo scoreboard muestra
concursos de varias instalaciones.

## 7. Empezar el scoreboard de cero

Si el scoreboard quedó en un estado raro (por ejemplo, después de pruebas),
puedes vaciarlo: el CMS le vuelve a enviar todo lo que existe. No se pierde
nada, porque los datos reales están en la base de datos.

```sh
sudo systemctl stop cms-ranking-web
sudo mkdir -p /var/lib/cms/ranking-viejo
sudo mv /var/lib/cms/ranking/state.json /var/lib/cms/ranking/journal.jsonl /var/lib/cms/ranking-viejo/ 2>/dev/null
sudo systemctl start cms-ranking-web
sudo systemctl restart cms-dispatcher
```

El logo, las banderas y las fotos se conservan. Cuando compruebes que se ve
bien: `sudo rm -rf /var/lib/cms/ranking-viejo`.

## 8. Qué hacer si…

**…el scoreboard está vacío.** ¿El concurso tiene horario? ¿Hay
participaciones no ocultas? ¿El dispatcher llega al scoreboard? `sudo
cmsctl doctor` lo revisa (línea «scoreboard push»).

**…sigue apareciendo un concurso borrado.** Espera un minuto. Si sigue, mira
que `prune` no esté en `false` y que el dispatcher funcione (`sudo cmsctl
doctor`); como último recurso, la sección 7.

**…aparece un usuario de prueba.** Marca su participación como oculta.

**…«refuses the dispatcher's credentials» en doctor.** El scoreboard y el
dispatcher deben tener la misma `secret_key` (lo normal si los instaló el
instalador).

---

**Siguiente:** [12. Después del concurso](12-despues-del-concurso.md) ·
**Anterior:** [10. Sitio del concursante](10-sitio-del-concursante.md) ·
[Índice](README.md)

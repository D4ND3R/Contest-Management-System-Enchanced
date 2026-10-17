# Primeros pasos

*English: [../en/getting-started.md](../en/getting-started.md).*

1. **Instalar** en una máquina limpia con Ubuntu o Debian
   ([instalación](instalacion.md)):

   ```sh
   curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
     | sudo bash -s -- --domain concurso.ejemplo.org
   ```

   o `--http-ports 8000,8001,8002` sin dominio. Al final de la salida están
   las tres direcciones y la **contraseña del administrador** (se muestra una
   sola vez).
2. **Entrar** a la administración y crear un concurso, o importarlo
   ([importar](importar.md)): `sudo -u cms cmsctl import-contest DIR`.
3. **Agregar problemas** (importar un paquete o [crearlo a mano](crear-problema.md))
   y **usuarios** (importación CSV), y sumarlos al concurso.
4. **Verificar el juez**: `sudo cmsctl verify-host`; después enviar las
   soluciones de ejemplo y ver llegar los resultados en vivo.
5. Abrir el **scoreboard** y el sitio del concursante desde otra máquina.
6. Antes del concurso: un respaldo (`sudo systemctl start cms-backup.service`),
   un ensayo de la vuelta atrás de una actualización en una máquina de prueba
   ([actualización y respaldos](actualizacion-y-respaldos.md)) y los
   comandos de [operación](operacion.md) a mano.

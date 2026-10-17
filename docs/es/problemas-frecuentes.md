# Problemas frecuentes

*English: [../en/troubleshooting.md](../en/troubleshooting.md).*

| Síntoma | Causa y solución |
|---|---|
| Un servicio no arranca: `the database schema is outdated` | migraciones pendientes: `sudo -u cms cms --config /etc/cms/cms.yaml migrate` (upgrade lo hace) |
| `the database is at schema version N but this binary only knows M` | un binario viejo sobre una base más nueva: instalar el nuevo, o restaurar un respaldo hecho con esta versión |
| Los sitios no arrancan por `secret_key` | debe estar y ser la misma en todos (D5). Nunca generar una por servicio |
| Los envíos quedan en "Compilando…" | no hay juez conectado (página Jueces) o falló su sandbox: `journalctl -u cms-worker`, `sudo cmsctl verify-host` |
| `verify-host` da FAIL en cgroups | la máquina no tiene cgroups v2 (kernel viejo, contenedor sin `--cgroupns=private`): el juez no puede limitar la memoria |
| WARN de SMT, governor o swap | tiempos más ruidosos: ver [Jueces](jueces.md#verificar-el-host) |
| El scoreboard queda vacío | `dispatcher.rankings` debe apuntar a ranking-web (`http://127.0.0.1:PUERTO/`); ambos servicios necesitan la misma `secret_key` (las credenciales se derivan de ella); logs de `cms-dispatcher` |
| No llegan las actualizaciones en vivo detrás de un proxy | el proxy guarda o comprime `*/events`: ver [qué necesita un proxy](sitio-concursante.md#qué-necesita-un-proxy) |
| No se emite el certificado HTTPS | el DNS de los tres nombres debe apuntar a la máquina y los puertos 80/443 estar abiertos: `journalctl -u caddy` |
| Los enlaces de la administración a los otros sitios están mal | página Servidor: las direcciones públicas (`:PUERTO` en instalaciones por puertos) |
| `upgrade failed at "health"` | un servicio no quedó sano en 2 minutos; se volvió todo atrás. Ver `journalctl -u cms-<servicio>` de la versión nueva, o pasar `--ignore-health worker` si solo falla un juez sin cgroups v2 |
| `restore`: `other sessions are connected` | detener los servicios antes (`systemctl stop cms.target`) |
| `restore`: `migration … differs from the one of the backup` | restaurar con la versión que hizo el respaldo (o una release más nueva de la misma línea) |
| Los archivos restaurados no se pueden leer desde los servicios | correr restore como el usuario `cms` (`sudo -u cms cmsctl restore …`) |
| Sin acceso después de que el instalador activó ufw | primero habilita el puerto SSH que encuentra en `sshd -T`; desde la consola: `ufw allow <puerto>/tcp` |

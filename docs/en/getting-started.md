# Getting started

*Español: [../es/primeros-pasos.md](../es/primeros-pasos.md).*

1. **Install** on a clean Ubuntu or Debian machine
   ([installation](installation.md)):

   ```sh
   curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
     | sudo bash -s -- --domain contest.example.org
   ```

   or `--http-ports 8000,8001,8002` without a domain. The end of the output
   has the three addresses and the **administrator password** (shown once).
2. **Log in** to the administration and create a contest, or import one
   ([importing](importing.md)): `sudo -u cms cmsctl import-contest DIR`.
3. **Add tasks** (import a package, or create one by hand) and **users**
   (CSV import), and add them to the contest.
4. **Check the judge**: `sudo cmsctl verify-host`, then submit the sample
   solutions and watch the results arrive live.
5. Open the **scoreboard** and the contestant site from another machine.
6. Before the contest: a backup (`sudo systemctl start cms-backup.service`),
   a rehearsal of the upgrade rollback on a test machine
   ([upgrades and backups](upgrades-and-backups.md)) and the
   [operation](operation.md) commands at hand.

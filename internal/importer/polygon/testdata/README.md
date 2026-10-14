# Polygon fixtures

- `polycontest/`: a Polygon contest package (`contest.xml`, `problems/`,
  `contestants.txt`) with two problems:
  - `polyaplusb`: a testlib checker (`files/check.cpp`, copied to
    `check.cpp` as Polygon does), test groups and points in `problem.xml`,
    `files/cms_conf.py` (GroupMin subtasks, task settings), statements in
    English and Spanish, a test with CR LF and one with a UTF-8 byte order
    mark (converted as dos2unix does). Its `files/testlib.h` has an
    `#error`: the checker must be compiled with the testlib.h of CMS.
  - `polypairs`: no checker (white diff), input and output files, two
    testsets (`pretests`, `tests`), a Russian statement.
- `upstream/`: the rows that the original CMS (schema 49) stored for these
  tasks, for the tests to compare with. Made with dos2unix 7.5.1 installed:

  ```sh
  cp -r polycontest /tmp/ref && export CMS_CONFIG=.../cms.toml
  cmsImportTask -L polygon_task /tmp/ref/polycontest/problems/polyaplusb
  cmsImportTask -L polygon_task /tmp/ref/polycontest/problems/polypairs
  psql .../refcms -At -v task=polyaplusb -f upstream/dump_task.sql > upstream/polyaplusb.json
  ```

  Digests there are SHA-1 (CMS); the tests hash our files with SHA-1 to
  compare. The checker compiled with g++ 13.3 was the same binary in both.
  The upstream contest and user loaders fail with schema 49 (`Contest` has
  no `start`, `User` no `hidden`), so there is nothing to compare for them.

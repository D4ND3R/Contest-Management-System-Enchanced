# TPS fixtures

Tasks as `tps export CMS 1` leaves them (the format the `tps_task` loader of
CMS reads):

- `tpsbatch`: Batch with a grader (C++, Java, Python and a header), a CMS
  checker (`checker/checker.cpp`), three subtasks (the first one, worth 0,
  makes the groups named), statements (`en_US` is the primary one; a `.txt`
  file is not a statement) and an attachment. The solutions are ignored.
- `tpscomm`: Communication with two processes, a manager
  (`graders/manager.cpp`, compiled at import time) and stubs (stored as
  `grader.*`); the task type is written in lowercase, the time limit as a
  string, and the feedback level is `oi_restricted`.
- `tpsoutput`: OutputOnly with a checker and two subtasks; it has a
  `score_mode`, which CMS ignores (a warning here).
- `tpsbao`: BatchAndOutput with an output-optional and an output-only
  subtask, input and output files.

`upstream/` has the rows that the original CMS (schema 49) stored for them,
made with:

```sh
cp -r tpsbatch tpscomm tpsoutput tpsbao /tmp/ref && export CMS_CONFIG=.../cms.toml
cmsImportTask -L tps_task /tmp/ref/tpsbatch    # and the others
psql .../refcms -At -v task=tpsbatch -f upstream/dump_task.sql > upstream/tpsbatch.json
```

Digests there are SHA-1 (CMS); the tests hash our files with SHA-1 to
compare. The checkers and the manager compiled with g++ 13.3 were the same
binaries in both.

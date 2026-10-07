# Demo contest (italy_yaml format)

Shared fixture for the importer tests (F8), the side-by-side screenshots
against the original CMS (F5–F7) and the field reviews. Three tasks:

- `sum`: Batch, stdin/stdout, white diff, two GroupMin subtasks (`gen/GEN`).
  `sol/` has a correct, a wrong, a partially correct (fails subtask 2) and a
  too slow solution.
- `half`: Batch with a comparator (`check/checker.cpp`, compile it to
  `check/checker` before importing: `./build.sh`) that gives 0.5 to answers
  off by one, so submissions can get the PA (partially correct) verdict.
- `quiz`: OutputOnly.

Users: `alice`, `bob`, `carol`, password `demo`.

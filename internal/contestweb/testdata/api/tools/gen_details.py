# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
# Computes score details with CMS's own score types (the refcms
# installation) for the API comparisons, and prints SQL that stores them
# as results in either database (rows found by task name and opaque id):
#   - alice's submission 4926046975063954292 on sum (GroupMin [[40,2],[60,2]],
#     testcase 000 public);
#   - a new submission 1111111111111111111 of alice on half (Sum, all private).
import json
from types import SimpleNamespace as NS
from cms.grading.scoretypes import get_score_type_class

def ev(codename, outcome, text, time, mem):
    return NS(codename=codename, outcome=outcome, text=text, execution_time=time,
              execution_memory=mem, dataset=NS(time_limit=1.0))

def result(evs):
    return NS(evaluations=evs, evaluated=lambda: True, dataset=NS(time_limit=1.0),
              submission=NS(task=NS(score_precision=0)))

q = lambda v: "'" + json.dumps(v).replace("'", "''") + "'"

def update(task, opaque, st, sr):
    score, details, pscore, pdetails, ranking = st.compute_score(sr)
    print(f"UPDATE submission_results sr SET score={score}, score_details={q(details)}, public_score={pscore}, "
          f"public_score_details={q(pdetails)}, ranking_score_details=ARRAY{ranking}::text[] "
          f"FROM submissions s JOIN tasks t ON t.id = s.task_id "
          f"WHERE sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id "
          f"AND t.name = '{task}' AND s.opaque_id = {opaque};")

st = get_score_type_class("GroupMin")([[40, 2], [60, 2]], {"000": True, "001": False, "002": False, "003": False}, 0)
update("sum", 4926046975063954292, st, result([
    ev("000", 1.0, ["Output is correct"], 0.012, 1048576),
    ev("001", 0.0, ["Output isn't correct"], 0.5, 2097152),
    ev("002", 1.0, ["Output is correct"], 0.25, 3145728),
    ev("003", 1.0, ["Execution timed out"], 1.2, None)]))

print("""INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
SELECT 1111111111111111111, p.id, t.id, '2026-10-08 10:00:00+00', 'C++17 / g++', '', true
FROM participations p JOIN users u ON u.id = p.user_id JOIN tasks t ON t.contest_id = p.contest_id
WHERE u.username = 'alice' AND t.name = 'half';
INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_text, compilation_tries,
  compilation_stdout, compilation_stderr, compilation_time, compilation_wall_clock_time, compilation_memory,
  evaluation_outcome, evaluation_tries)
SELECT s.id, t.active_dataset_id, 'ok', '{"Compilation succeeded"}', 1, '', '', 0.5, 0.6, 1048576, 'ok', 1
FROM submissions s JOIN tasks t ON t.id = s.task_id WHERE s.opaque_id = 1111111111111111111;""")
st = get_score_type_class("Sum")(33.333333333333336, {"000": False, "001": False, "002": False}, 0)
update("half", 1111111111111111111, st, result([
    ev("000", 1.0, ["Output is correct"], 0.01, 1048576),
    ev("001", 0.5, ["Output is partially correct"], 0.02, 2097152),
    ev("002", 0.0, ["Output isn't correct"], 0.03, None)]))

-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
--
-- The state of the judging core (F4, internal/judging): generations of the
-- results, what the sweep of the dispatcher needs to find unfinished work,
-- and the cache of the score of every participant on every task.

-- Fork addition (D23): the generation of a result. Every invalidation of
-- its compilation or evaluation increments it; jobs carry the generation
-- of the row they were built for (job.Job.Generation) and results are
-- written only while it still matches, so the result of a job created
-- before an invalidation is discarded in SQL. Rescoring does not change it
-- (no worker is involved).
ALTER TABLE submission_results ADD COLUMN generation bigint NOT NULL DEFAULT 0;
ALTER TABLE user_test_results ADD COLUMN generation bigint NOT NULL DEFAULT 0;

-- The sweep of the dispatcher (internal/judging Sweep) looks for the results
-- that still need a compilation, an evaluation or a score; scored results,
-- almost every row during a contest, stay out of this index, so finding the
-- unfinished ones does not read the finished ones.
CREATE INDEX ix_submission_results_unfinished ON submission_results (submission_id, dataset_id)
    WHERE score IS NULL OR score_details IS NULL OR public_score IS NULL
       OR public_score_details IS NULL OR ranking_score_details IS NULL;
-- The same for user tests, which are never scored: only those still compiling or running.
CREATE INDEX ix_user_test_results_unfinished ON user_test_results (user_test_id, dataset_id)
    WHERE compilation_outcome IS NULL OR (compilation_outcome = 'ok' AND evaluation_outcome IS NULL);

-- Fork addition: the score of a participant on a task (upstream
-- cms.grading.scoring.task_score over their official submissions, on the
-- active dataset, with the score mode and score precision of the task), so
-- that the contestant pages, the admin ranking and the scoreboard never
-- compute it per request. internal/judging keeps it up to date in the same
-- transaction as every change that affects it: a submission scored or
-- invalidated, a token played, the active dataset changed. A missing row
-- is a participant without submissions on the task: 0, not partial.
CREATE TABLE task_scores (
    participation_id integer NOT NULL REFERENCES participations (id) ON UPDATE CASCADE ON DELETE CASCADE,
    task_id          integer NOT NULL REFERENCES tasks (id) ON UPDATE CASCADE ON DELETE CASCADE,
    -- The active dataset the scores were computed on (NULL: the task had
    -- none). Informational, hence no foreign key: the sweep recomputes the
    -- rows whose dataset is not the active one any more.
    dataset_id       integer,
    -- task_score(participation, task): what admins and rankings see.
    score            double precision NOT NULL DEFAULT 0,
    -- task_score(..., public=True): what the contestant always sees.
    public_score     double precision NOT NULL DEFAULT 0,
    -- task_score(..., only_tokened=True): what the contestant knows of the
    -- full score (tokened submissions only).
    tokened_score    double precision NOT NULL DEFAULT 0,
    -- Some official submission is not scored yet (the same for the three
    -- scores): pages show the scores as provisional.
    partial          boolean NOT NULL DEFAULT false,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (participation_id, task_id)
);

-- ON DELETE CASCADE from tasks, and every score of a task (active dataset changed, score mode or precision edited).
CREATE INDEX ix_task_scores_task_id ON task_scores (task_id);

-- The subtask scores of score details, for the max_subtask score mode:
-- for a list of objects that all have "idx" and "score", the list of
-- {"idx", "score"} in the same order (all that upstream
-- _task_score_max_subtask reads); an empty list unchanged (a submission
-- that did not compile); '{}' for anything else (upstream then counts the
-- whole score as one subtask). internal/judging checks that the max_subtask
-- score is the same on these as on the full details.
CREATE FUNCTION cms_subtask_scores(details jsonb) RETURNS jsonb
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
    SELECT CASE
        WHEN details IS NULL THEN NULL
        WHEN jsonb_typeof(details) <> 'array' THEN '{}'::jsonb
        WHEN jsonb_array_length(details) = 0 THEN details
        ELSE (SELECT CASE WHEN bool_and(jsonb_typeof(e.st) = 'object' AND e.st ? 'idx' AND e.st ? 'score')
                          THEN jsonb_agg(jsonb_build_object('idx', e.st -> 'idx', 'score', e.st -> 'score') ORDER BY e.n)
                          ELSE '{}'::jsonb END
              FROM jsonb_array_elements(details) WITH ORDINALITY AS e(st, n))
    END
    $$;

-- Fork addition: the subtask scores of the score details, computed once
-- when a result is scored. The task score cache reads every submission of
-- a participant on a task whenever one of them changes; group details
-- hold every testcase (kilobytes, usually TOASTed) and these a few bytes:
-- recomputing a task of 13 000 submissions with 30 subtasks takes 60 ms
-- instead of 1.2 s.
ALTER TABLE submission_results
    ADD COLUMN subtask_scores jsonb GENERATED ALWAYS AS (cms_subtask_scores(score_details)) STORED,
    ADD COLUMN public_subtask_scores jsonb GENERATED ALWAYS AS (cms_subtask_scores(public_score_details)) STORED;

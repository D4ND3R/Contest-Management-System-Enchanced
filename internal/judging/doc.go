// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package judging is the database side of the judging core (SPEC
// R4.2-R4.5): what CMS does in EvaluationService (esoperations.py,
// write_results, invalidate_submission) and in the scoring part of
// ScoringService, with the queues replaced by internal/queue and the
// workers by internal/worker. The dispatcher drives it; the web sites and
// `cms ctl` use the notification API and the task score cache.
//
// # The loop
//
// The dispatcher learns that there is work from notifications (Notify,
// written by the contestant site, the administration and ctl) and from a
// periodic Sweep that makes lost notifications harmless. For each
// submission or user test it asks Pending for the Work: the Operations a
// worker must run (compilations, evaluations of each testcase) and the
// results that only need the database (ToScore). BuildJobs turns
// operations into job.Job values, the dispatcher enqueues them, workers
// send job.Result values back, and Apply stores a batch of them in one
// transaction, scores what got complete, updates the task score cache and
// returns what changed and the next operations.
//
// # Consistency
//
//   - Results are idempotent and the first one wins (D21): a result
//     delivered twice changes nothing the second time.
//   - Every result row has a generation (migration 0004, D23). Jobs carry
//     the generation of the row they were built for (BuildJobs creates the
//     row if needed); invalidating the compilation or the evaluation of a
//     result increments it, so the result of a job that was in flight is
//     discarded in SQL (Dropped, DropStale) instead of overwriting the new
//     one.
//   - Apply, Score and Invalidate lock the result rows they change (and
//     share-lock their tasks, so that the active dataset does not change
//     under them), and then the task score rows, always in the same order:
//     two dispatchers (or a dispatcher and an admin) working on the same
//     submission run one after the other and the second sees what the
//     first committed.
//   - Failed jobs (an infrastructure failure, never a verdict) count tries
//     as in upstream EvaluationService, with D23's rule: the tries of a
//     result are the highest attempt that failed, so a failure delivered
//     twice counts once. After Options.Tries (by default the upstream
//     MaxCompilationTries, MaxEvaluationTries...) failures the operation
//     is not scheduled again (GaveUp) and, as in CMS, the
//     submission keeps showing "Compiling..." or "Evaluating..." until an
//     admin reevaluates it; `cms ctl judging-status` counts them.
//     Differences with upstream: an evaluation that failed because a file
//     is gone (a tombstone) counts as a try unless the executable is the
//     tombstone, in which case the result is recompiled (upstream retried
//     it forever); failed evaluations are retried right away (Apply returns
//     the operation with Retry set) instead of at the next sweep.
//
// # The task score cache
//
// task_scores (migration 0004) holds, for every participant and task with
// submissions, the three values of upstream scoring.task_score that the
// pages show (full, public, tokened only) and whether some submission is
// still unscored, rounded with the score precision of the task. The
// contestant pages, the admin ranking and the scoreboard read it instead
// of computing scores on every request. It is recomputed from scratch for
// the participant and task, never incrementally, in the same transaction
// as anything that changes it: a submission scored (Apply, Score), an
// invalidation (Invalidate), a token played (RefreshTaskScores, called by
// the contestant site in the transaction that inserts the token), the
// active dataset changed (ActivateDataset, RecomputeTask). The sweep
// repairs rows computed on a dataset that is not the active one any more
// (RepairTaskScores).
//
// # Datasets
//
// As in CMS, a submission is judged on the active dataset of its task and
// on every dataset with autojudge set (the "datasets to judge"); results
// on other datasets are only kept. Operations on datasets that are not
// active have the lowest priority.
package judging

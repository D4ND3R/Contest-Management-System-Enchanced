// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package job

import "time"

// The queues (internal/queue) between the dispatcher and the workers. Both
// sides must open them with the same names and number of priorities.
const (
	// JobQueue carries encoded Jobs from the dispatcher to the workers,
	// with JobPriorities priorities.
	JobQueue = "jobs"
	// ResultQueue carries encoded Results from the workers to the
	// dispatcher, with a single priority. Workers publish a result and
	// acknowledge its job in one step (queue.AckAndEnqueue).
	ResultQueue = "results"
)

// Priorities of JobQueue, highest first: the levels of upstream
// cms/io/priorityqueue.py (PRIORITY_EXTRA_HIGH ... PRIORITY_EXTRA_LOW),
// which the dispatcher assigns to operations like upstream
// cms/service/esoperations.py.
const (
	PriorityExtraHigh = iota
	PriorityHigh
	PriorityMedium
	PriorityLow
	PriorityExtraLow
	// JobPriorities is the number of priorities of JobQueue.
	JobPriorities
)

// JobVisibility is the visibility timeout of JobQueue: a job whose worker
// shows no sign of life (queue.Extend) for this long goes to another
// worker. Workers extend their running jobs every third of it, so a job
// of a worker that died is judged again within a minute (N3).
const JobVisibility = 30 * time.Second

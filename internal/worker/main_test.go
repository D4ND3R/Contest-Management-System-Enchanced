// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// helperEnv turns the test binary into a worker process that the crash
// tests kill (helperMain); the other variables configure it.
const (
	helperEnv           = "CMS_WORKER_TEST_HELPER"
	helperRedisEnv      = "CMS_WORKER_TEST_REDIS"
	helperPrefixEnv     = "CMS_WORKER_TEST_PREFIX"
	helperVisibilityEnv = "CMS_WORKER_TEST_VISIBILITY"
)

func TestMain(m *testing.M) {
	// The sandbox tests use the test binary as the seccomp launcher.
	sandbox.MaybeRunLauncher()
	if name := os.Getenv(helperEnv); name != "" {
		if err := helperMain(name); err != nil {
			fmt.Fprintln(os.Stderr, "worker helper:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// helperMain runs a worker named name whose jobs never end, until the
// process is killed.
func helperMain(name string) error {
	opt, err := redis.ParseURL(os.Getenv(helperRedisEnv))
	if err != nil {
		return err
	}
	vis, err := time.ParseDuration(os.Getenv(helperVisibilityEnv))
	if err != nil {
		return err
	}
	c := kv.New(redis.NewClient(opt), os.Getenv(helperPrefixEnv))
	ctx := context.Background()
	jobs, results, err := openQueues(ctx, c, vis)
	if err != nil {
		return err
	}
	exec := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		<-ctx.Done()
		res.Fail(ctx.Err())
		return ctx.Err()
	}}
	w, err := New(c, jobs, results, exec, Options{Name: name, Concurrency: 1, Boxes: 1,
		Heartbeat: 50 * time.Millisecond, PollTimeout: 200 * time.Millisecond})
	if err != nil {
		return err
	}
	return w.Run(ctx)
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package registry

// Disabled workers (CMS EvaluationService.disable_worker and
// enable_worker, from the overview and the judges page of the
// administration): the set "workers:disabled" holds their names. A
// disabled worker finishes the jobs it is running and takes no new ones
// (it checks the set at every heartbeat) until it is enabled again. The
// set has no expiry: a worker restarted while disabled stays disabled.

import (
	"context"
	"fmt"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

const disabledKey = "workers:disabled"

// SetDisabled disables (or enables) the worker name. It reports whether
// the state changed (false: it already was in that state).
func SetDisabled(ctx context.Context, c *kv.Client, name string, disabled bool) (bool, error) {
	key := c.Key(disabledKey)
	var n int64
	var err error
	if disabled {
		n, err = c.Redis().SAdd(ctx, key, name).Result()
	} else {
		n, err = c.Redis().SRem(ctx, key, name).Result()
	}
	if err != nil {
		return false, fmt.Errorf("registry: disabling %q: %w", name, err)
	}
	return n > 0, nil
}

// IsDisabled reports whether the worker name is disabled.
func IsDisabled(ctx context.Context, c *kv.Client, name string) (bool, error) {
	ok, err := c.Redis().SIsMember(ctx, c.Key(disabledKey), name).Result()
	if err != nil {
		return false, fmt.Errorf("registry: reading the disabled workers: %w", err)
	}
	return ok, nil
}

// DisabledNames returns the names of the disabled workers (running or
// not).
func DisabledNames(ctx context.Context, c *kv.Client) (map[string]bool, error) {
	names, err := c.Redis().SMembers(ctx, c.Key(disabledKey)).Result()
	if err != nil {
		return nil, fmt.Errorf("registry: reading the disabled workers: %w", err)
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out, nil
}

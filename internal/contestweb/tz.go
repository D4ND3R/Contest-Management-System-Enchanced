// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// TimezoneSetting is the key of server_settings holding the server time
// zone (a JSON string), set on the Server page of the administration; it
// overrides the timezone of cms.yaml (D6).
const TimezoneSetting = "timezone"

// tzRefresh is how often the time zone is read again.
const tzRefresh = 30 * time.Second

// tzCache keeps the server time zone (D6): every page shows times in it.
// It is read again in the background at most every tzRefresh, so pages
// never wait for it.
type tzCache struct {
	def  *time.Location
	load func(context.Context) (*time.Location, error)

	cur        atomic.Pointer[time.Location]
	mu         sync.Mutex
	at         time.Time
	refreshing bool
}

func newTZCache(def *time.Location, load func(context.Context) (*time.Location, error)) *tzCache {
	if def == nil {
		def = time.UTC
	}
	c := &tzCache{def: def, load: load}
	c.cur.Store(def)
	return c
}

// Location returns the server time zone, starting a refresh when it is
// old.
func (c *tzCache) Location() *time.Location {
	c.mu.Lock()
	stale := !c.refreshing && time.Since(c.at) > tzRefresh
	if stale {
		c.refreshing = true
	}
	c.mu.Unlock()
	if stale {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.refresh(ctx)
		}()
	}
	return c.cur.Load()
}

// refresh reads the time zone now; on errors the current one stays.
func (c *tzCache) refresh(ctx context.Context) {
	loc, err := c.load(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	c.at = time.Now()
	if err == nil {
		if loc == nil {
			loc = c.def
		}
		c.cur.Store(loc)
	}
}

// loadTimezone reads the time zone of server_settings (nil: the one of
// cms.yaml).
func (s *Server) loadTimezone(ctx context.Context) (*time.Location, error) {
	row, err := s.st.Q.GetServerSetting(ctx, TimezoneSetting)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var name string
	if err := json.Unmarshal(row.Value, &name); err != nil || name == "" {
		return nil, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		s.log.Warn("invalid server time zone in server_settings", "timezone", name, "err", err)
		return nil, nil
	}
	return loc, nil
}

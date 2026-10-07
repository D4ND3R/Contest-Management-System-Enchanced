// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package kv wraps the Valkey/Redis client used for job streams and live
// events. Every key goes through Key, which adds the installation prefix,
// so several installations (or parallel tests) can share one server.
package kv

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Client is a prefixed Valkey/Redis client.
type Client struct {
	rdb    redis.UniversalClient
	prefix string
}

// Open connects and pings the server.
func Open(ctx context.Context, cfg config.Redis) (*Client, error) {
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("redis.url: %w", err)
	}
	c := &Client{rdb: redis.NewClient(opt), prefix: cfg.KeyPrefix}
	if err := c.Ping(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("connecting to Valkey: %w", err)
	}
	return c, nil
}

// New wraps an existing client.
func New(rdb redis.UniversalClient, prefix string) *Client {
	return &Client{rdb: rdb, prefix: prefix}
}

// Key joins parts with ":" and adds the installation prefix.
func (c *Client) Key(parts ...string) string {
	return c.prefix + strings.Join(parts, ":")
}

// Prefix returns the installation prefix.
func (c *Client) Prefix() string { return c.prefix }

// Redis returns the underlying client. Callers must build keys with Key.
func (c *Client) Redis() redis.UniversalClient { return c.rdb }

// Ping checks the connection.
func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// Close closes the connection pool.
func (c *Client) Close() error { return c.rdb.Close() }

// DeletePrefix removes every key under the installation prefix. Used by
// tests and by `cms ctl` maintenance commands; never by services.
func (c *Client) DeletePrefix(ctx context.Context) error {
	if c.prefix == "" {
		return fmt.Errorf("refusing to delete every key: no prefix set")
	}
	iter := c.rdb.Scan(ctx, 0, c.prefix+"*", 500).Iterator()
	var batch []string
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) == 500 {
			if err := c.rdb.Unlink(ctx, batch...).Err(); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(batch) > 0 {
		return c.rdb.Unlink(ctx, batch...).Err()
	}
	return nil
}

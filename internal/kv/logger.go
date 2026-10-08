// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package kv

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// SetLogger sends the client library's own messages (lost connections,
// pool problems) to log as warnings, instead of plain text on stderr that
// breaks the JSON log format. The setting is global to the process.
func SetLogger(log *slog.Logger) {
	if log != nil {
		redis.SetLogger(redisLogger{log})
	}
}

type redisLogger struct{ log *slog.Logger }

func (l redisLogger) Printf(ctx context.Context, format string, v ...any) {
	l.log.WarnContext(ctx, "valkey client", "detail", fmt.Sprintf(format, v...))
}

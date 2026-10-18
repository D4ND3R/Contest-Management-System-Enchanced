// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestWithConfigHint(t *testing.T) {
	t.Setenv("CMS_DATABASE_URL", "")
	t.Setenv("CMS_DATABASE_URL_FILE", "")
	auth := fmt.Errorf("connecting to the database: %w", &pgconn.PgError{Severity: "FATAL", Code: "28P01",
		Message: `password authentication failed for user "cms"`})
	got := withConfigHint(auth, "")
	if !errors.Is(got, auth) || !strings.Contains(got.Error(), "no configuration file was found") ||
		!strings.Contains(got.Error(), "sudo cmsctl") {
		t.Errorf("auth error without a file: %v", got)
	}
	if got := withConfigHint(auth, "/etc/cms/cms.yaml"); got != auth {
		t.Errorf("with a file the error is unchanged, got %v", got)
	}
	other := errors.New("connecting to the database: connection refused")
	if got := withConfigHint(other, ""); got != other {
		t.Errorf("other errors are unchanged, got %v", got)
	}
	t.Setenv("CMS_DATABASE_URL", "postgres://cms@db/cms")
	if got := withConfigHint(auth, ""); got != auth {
		t.Errorf("with the database in the environment the error is unchanged, got %v", got)
	}
	if withConfigHint(nil, "") != nil {
		t.Error("nil error")
	}
}

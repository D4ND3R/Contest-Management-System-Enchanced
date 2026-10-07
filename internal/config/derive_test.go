// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	secret := []byte("0123456789abcdef")
	// Pinned: changing the derivation silently invalidates every derived
	// credential (judge tokens, scoreboard links), so it must be a
	// deliberate change of deriveDomain. Computed independently:
	// HMAC-SHA256(secret, "cms-enchanced/derive/v1" ‖ be32(10) ‖ "blob-token").
	const want = "4fc806eb2e6c3d7b454262a1e0905d81ad7b07e02ffeca44d7c27ddc7778fa5b"
	if got := hex.EncodeToString(DeriveKey(secret, "blob-token")); got != want {
		t.Errorf("DeriveKey = %s, want %s", got, want)
	}
	keys := map[string][]byte{}
	for name, k := range map[string][]byte{
		"purpose":             DeriveKey(secret, "blob-token"),
		"other purpose":       DeriveKey(secret, "scoreboard-link"),
		"context":             DeriveKey(secret, "scoreboard-link", "blob-token"),
		"split context a|bc":  DeriveKey(secret, "scoreboard-link", "a", "bc"),
		"split context ab|c":  DeriveKey(secret, "scoreboard-link", "ab", "c"),
		"empty context":       DeriveKey(secret, "scoreboard-link", ""),
		"purpose in context":  DeriveKey(secret, "scoreboard-linkblob-token"),
		"other secret":        DeriveKey([]byte("fedcba9876543210"), "blob-token"),
		"raw HMAC of purpose": hmacOf(secret, "blob-token"),
	} {
		if len(k) != 32 {
			t.Errorf("%s: %d bytes", name, len(k))
		}
		for other, ok := range keys {
			if bytes.Equal(k, ok) {
				t.Errorf("%s and %s derive the same key", name, other)
			}
		}
		keys[name] = k
	}
	if !bytes.Equal(DeriveKey(secret, "x", "y"), DeriveKey(secret, "x", "y")) {
		t.Error("DeriveKey is not deterministic")
	}
	c := Default()
	c.SecretKey = hex.EncodeToString(secret)
	if !bytes.Equal(c.DeriveKey("blob-token"), DeriveKey(secret, "blob-token")) {
		t.Error("Config.DeriveKey does not use secret_key")
	}
}

func hmacOf(secret []byte, msg string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

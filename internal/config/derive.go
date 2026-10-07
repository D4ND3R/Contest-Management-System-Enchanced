// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// deriveDomain starts every message DeriveKey authenticates.
const deriveDomain = "cms-enchanced/derive/v1"

// DeriveKey returns the 32-byte key of one purpose derived from secret:
// HMAC-SHA256(secret, deriveDomain ‖ purpose ‖ context...), where purpose
// and every context string are prefixed with their length. secret_key is
// never used directly: cookies, CSRF tokens, scoreboard links and the blob
// server token each derive their own key with a distinct, fixed purpose.
// The encoding is injective, so no two (purpose, context) pairs share a
// key, even when context comes from users (contest names...) and even if
// a purpose were also used as a context string.
func DeriveKey(secret []byte, purpose string, context ...string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(deriveDomain))
	var n [4]byte
	for _, part := range append([]string{purpose}, context...) {
		binary.BigEndian.PutUint32(n[:], uint32(len(part)))
		m.Write(n[:])
		m.Write([]byte(part))
	}
	return m.Sum(nil)
}

// DeriveKey derives a key of secret_key (see the function DeriveKey).
func (c *Config) DeriveKey(purpose string, context ...string) []byte {
	return DeriveKey(c.SecretKeyBytes(), purpose, context...)
}

// isHexKey reports whether s is the lowercase hex encoding of n bytes.
func isHexKey(s string, n int) bool {
	if len(s) != 2*n {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && hex.EncodeToString(b) == s
}

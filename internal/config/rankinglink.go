// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import "encoding/hex"

// RankingLinkPurpose is the DeriveKey purpose of the secret scoreboard
// links (R6.4, R7.4).
const RankingLinkPurpose = "ranking-web/secret-link"

// RankingLinkToken returns the secret of the scoreboard link of a contest
// (R6.4, R7.4): the lowercase hex encoding of the first 16 bytes (128
// bits) of DeriveKey(secret_key, RankingLinkPurpose, contestName), 32
// characters that are safe in URLs. The administration shows it in the
// link of the ranking page (<scoreboard address>?secret=<token>); the
// public scoreboard computes it again and compares in constant time
// (crypto/subtle). It is the same in every service sharing secret_key
// (D5), changes with secret_key and with the contest name.
func (c *Config) RankingLinkToken(contestName string) string {
	key := c.DeriveKey(RankingLinkPurpose, contestName)
	return hex.EncodeToString(key[:16])
}

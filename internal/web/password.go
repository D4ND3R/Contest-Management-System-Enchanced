// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscommon/crypto.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package web

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the cost of the passwords hashed by the sites (CMS
// BCRYPT_ROUNDS).
const BcryptCost = 12

// PasswordCost is the cost HashPassword uses; tests lower it (a hash at
// cost 12 takes seconds under the race detector).
var PasswordCost = BcryptCost

// ErrBadAuthentication marks an authentication string CMS cannot read.
var ErrBadAuthentication = errors.New("invalid authentication string")

// ParseAuthentication splits "<method>:<payload>" (CMS
// parse_authentication).
func ParseAuthentication(authentication string) (method, payload string, err error) {
	method, payload, ok := strings.Cut(authentication, ":")
	if !ok {
		return "", "", fmt.Errorf("%w: no method", ErrBadAuthentication)
	}
	return method, payload, nil
}

// ValidatePassword checks a password against the authentication string on
// record (CMS validate_password): "bcrypt:<hash>" or "plaintext:<pw>".
// Other strings are an error (a configuration problem), never a match.
func ValidatePassword(authentication, password string) (bool, error) {
	method, payload, err := ParseAuthentication(authentication)
	if err != nil {
		return false, err
	}
	switch method {
	case "bcrypt":
		return bcrypt.CompareHashAndPassword([]byte(payload), bcryptInput(password)) == nil, nil
	case "plaintext":
		return subtle.ConstantTimeCompare([]byte(payload), []byte(password)) == 1, nil
	default:
		return false, fmt.Errorf("%w: unknown method %q", ErrBadAuthentication, method)
	}
}

// HashPassword returns the authentication string of a password (CMS
// hash_password): method is "bcrypt" (the default when empty) or
// "plaintext".
func HashPassword(password, method string) (string, error) {
	switch method {
	case "", "bcrypt":
		h, err := bcrypt.GenerateFromPassword(bcryptInput(password), PasswordCost)
		if err != nil {
			return "", err
		}
		return "bcrypt:" + string(h), nil
	case "plaintext":
		return "plaintext:" + password, nil
	default:
		return "", fmt.Errorf("%w: unknown method %q", ErrBadAuthentication, method)
	}
}

// bcryptInput is what Python's bcrypt (as CMS uses it) reads: at most 72
// bytes.
func bcryptInput(password string) []byte {
	b := []byte(password)
	if len(b) > 72 {
		b = b[:72]
	}
	return b
}

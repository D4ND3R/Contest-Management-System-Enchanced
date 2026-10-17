// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from the argument readers of cms/server/admin/handlers/base.py
// (argument_reader, parse_*, get_bool, get_password...) of CMS:
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>

package adminweb

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// Form reads the arguments of a form as CMS's argument readers: an
// absent argument leaves the destination as it is (the stored value is
// kept), an empty one stores the "empty" value (nil, "" or an empty
// list) and anything else is parsed. Parse errors are collected with
// CMS's messages ("Can't cast x to int."); check Err before writing:
//
//	f := c.Form()
//	f.String(&u.FirstName, "first_name")
//	f.OptInt32(&p.MaxSubmissionNumber, "max_submission_number")
//	f.Bool(&p.AllowQuestions, "allow_questions")
//	if err := f.Err(); err != nil {
//		c.NotifyError(N("Invalid field(s)"), err.Error())
//		c.Redirect(fallback)
//		return
//	}
type Form struct {
	c    *Ctx
	errs []error
}

// Form returns the reader of the request's arguments (query and body).
func (c *Ctx) Form() *Form { return &Form{c: c} }

// Err returns the parse errors, nil if none.
func (f *Form) Err() error { return errors.Join(f.errs...) }

// Fail records an error of the caller's own validation (CMS's assert).
func (f *Form) Fail(format string, args ...any) {
	f.errs = append(f.errs, fmt.Errorf(format, args...))
}

// Get returns the argument (Tornado get_argument: the last value,
// cleaned and stripped) and whether it was sent.
func (f *Form) Get(name string) (string, bool) { return f.c.Arg(name) }

// Has reports whether the argument was sent.
func (f *Form) Has(name string) bool {
	_, ok := f.Get(name)
	return ok
}

// String stores the argument (CMS get_string: empty is "").
func (f *Form) String(dst *string, name string) {
	if v, ok := f.Get(name); ok {
		*dst = v
	}
}

// OptString stores the argument, nil when empty (CMS get_string with
// empty=None).
func (f *Form) OptString(dst **string, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	if v == "" {
		*dst = nil
		return
	}
	*dst = &v
}

// Required returns the argument and records an error ("No <what>
// specified.") when it is absent or empty (CMS's asserts).
func (f *Form) Required(name, what string) string {
	v, _ := f.Get(name)
	if v == "" {
		f.Fail("No %s specified.", what)
	}
	return v
}

// StringList stores a comma-separated list (CMS parse_string_list: items
// stripped, empty ones dropped; empty is an empty list).
func (f *Form) StringList(dst *[]string, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	*dst = ParseStringList(v)
}

// ParseStringList is CMS's parse_string_list.
func ParseStringList(v string) []string {
	out := []string{}
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Bool stores a checkbox (CMS get_bool: bool(get_argument(name, False))):
// checked if sent with a non-empty value, unchecked otherwise.
func (f *Form) Bool(dst *bool, name string) {
	v, _ := f.Get(name)
	*dst = v != ""
}

func (f *Form) parseInt(name, v string, bits int) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, bits)
	if err != nil {
		f.Fail("Can't cast %s to int.", v)
		return 0, false
	}
	return n, true
}

// Int32 stores an integer; an empty value is an error (the column is not
// nullable).
func (f *Form) Int32(dst *int32, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	if n, ok := f.parseInt(name, v, 32); ok {
		*dst = int32(n)
	}
}

// OptInt32 stores an integer, nil when empty (CMS get_int).
func (f *Form) OptInt32(dst **int32, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	if v == "" {
		*dst = nil
		return
	}
	if n, ok := f.parseInt(name, v, 32); ok {
		x := int32(n)
		*dst = &x
	}
}

// Float stores a number; an empty value is an error.
func (f *Form) Float(dst *float64, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		f.Fail("Can't cast %s to float.", v)
		return
	}
	*dst = x
}

// parseSeconds parses a number of seconds as Python's
// timedelta(seconds=float(v)) (microsecond precision).
func parseSeconds(v string, unit time.Duration) (time.Duration, bool) {
	x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, false
	}
	us := math.Round(x * float64(unit/time.Microsecond))
	if math.Abs(us) > float64(math.MaxInt64/int64(time.Microsecond)) {
		return 0, false
	}
	return time.Duration(us) * time.Microsecond, true
}

// Duration stores a number of seconds (CMS parse_timedelta_sec); an
// empty value is an error (the column is not nullable).
func (f *Form) Duration(dst *time.Duration, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	d, ok := parseSeconds(v, time.Second)
	if !ok {
		f.Fail("Can't cast %s to timedelta.", v)
		return
	}
	*dst = d
}

// OptDuration stores a number of seconds, nil when empty (CMS
// get_timedelta_sec).
func (f *Form) OptDuration(dst **time.Duration, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	if v == "" {
		*dst = nil
		return
	}
	d, ok := parseSeconds(v, time.Second)
	if !ok {
		f.Fail("Can't cast %s to timedelta.", v)
		return
	}
	*dst = &d
}

// DurationMin stores a number of minutes (CMS parse_timedelta_min); an
// empty value is an error.
func (f *Form) DurationMin(dst *time.Duration, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	d, ok := parseSeconds(v, time.Minute)
	if !ok {
		f.Fail("Can't cast %s to timedelta.", v)
		return
	}
	*dst = d
}

// FormDatetimeLayout is how forms show and read times: CMS's
// "%Y-%m-%d %H:%M:%S[.%f]", in the server time zone (D6; CMS uses UTC).
const FormDatetimeLayout = "2006-01-02 15:04:05"

// ParseFormDatetime parses a time of a form in loc (CMS parse_datetime).
func ParseFormDatetime(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	layout := FormDatetimeLayout
	if strings.Contains(v, ".") {
		layout += ".999999"
	}
	t, err := time.ParseInLocation(layout, v, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("Can't cast %s to datetime.", v)
	}
	return t.UTC(), nil
}

// Datetime stores a time; an empty value is an error.
func (f *Form) Datetime(dst *time.Time, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	t, err := ParseFormDatetime(v, f.c.RC.TZ)
	if err != nil {
		f.errs = append(f.errs, err)
		return
	}
	*dst = t
}

// OptDatetime stores a time, nil when empty (CMS get_datetime).
func (f *Form) OptDatetime(dst **time.Time, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	if v == "" {
		*dst = nil
		return
	}
	t, err := ParseFormDatetime(v, f.c.RC.TZ)
	if err != nil {
		f.errs = append(f.errs, err)
		return
	}
	*dst = &t
}

// IPNetworks stores a comma-separated list of networks (CMS
// parse_ip_networks: an address or an address with a /bits mask whose
// host bits are zero); empty is nil.
func (f *Form) IPNetworks(dst *[]netip.Prefix, name string) {
	v, ok := f.Get(name)
	if !ok {
		return
	}
	if v == "" {
		*dst = nil
		return
	}
	var out []netip.Prefix
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		p, err := parseNetwork(s)
		if err != nil {
			f.Fail("Can't cast %s to an IP network.", s)
			return
		}
		out = append(out, p)
	}
	*dst = out
}

func parseNetwork(s string) (netip.Prefix, error) {
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, errors.New("invalid address")
		}
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil || p.Masked() != p {
		return netip.Prefix{}, errors.New("invalid network")
	}
	return p, nil
}

// Password stores a password with the hashing method of the form (the
// "password" and "method" arguments; CMS get_password): a non-empty
// password is hashed with the method; an empty one keeps a hashed
// password whose method did not change, else unsets it (allowUnset: the
// participation inherits the user's) or stores the empty password.
// old is the current authentication string ("" if none).
func (f *Form) Password(dst **string, old string, allowUnset bool) {
	oldMethod := ""
	if old != "" {
		if m, _, err := web.ParseAuthentication(old); err == nil {
			oldMethod = m
		}
	}
	password, _ := f.Get("password")
	method, _ := f.Get("method")
	var auth string
	var err error
	switch {
	case password != "":
		auth, err = web.HashPassword(password, method)
	case oldMethod != "" && oldMethod != "plaintext" && method == oldMethod:
		return
	case allowUnset:
		*dst = nil
		return
	default:
		auth, err = web.HashPassword("", method)
	}
	if err != nil {
		f.Fail("%s", err.Error())
		return
	}
	*dst = &auth
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// EnvPrefix starts the name of every override variable.
const EnvPrefix = "CMS_"

// Setting describes one leaf of the configuration.
type Setting struct {
	Path   string // YAML path, e.g. "database.url"
	Env    string // override variable, e.g. "CMS_DATABASE_URL"
	Secret bool
	field  reflect.Value
}

// Settings lists every leaf setting of c, in declaration order.
func Settings(c *Config) []Setting {
	var out []Setting
	walk(reflect.ValueOf(c).Elem(), nil, false, &out)
	return out
}

var durationType = reflect.TypeOf(Duration(0))

func walk(v reflect.Value, path []string, secret bool, out *[]Setting) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		p := append(append([]string{}, path...), key)
		fv := v.Field(i)
		sec := secret || f.Tag.Get("secret") == "true"
		if fv.Kind() == reflect.Struct && fv.Type() != durationType {
			walk(fv, p, sec, out)
			continue
		}
		*out = append(*out, Setting{
			Path:   strings.Join(p, "."),
			Env:    EnvPrefix + strings.ToUpper(strings.Join(p, "_")),
			Secret: sec,
			field:  fv,
		})
	}
}

// applyEnv overrides settings from NAME or, if unset, from the file named
// by NAME_FILE.
func applyEnv(c *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) error {
	for _, s := range Settings(c) {
		val, ok := lookup(s.Env)
		if !ok {
			file, fok := lookup(s.Env + "_FILE")
			if !fok || file == "" {
				continue
			}
			data, err := readFile(file)
			if err != nil {
				return fmt.Errorf("%s_FILE: %w", s.Env, err)
			}
			val = strings.TrimRight(string(data), "\r\n")
		}
		if err := setFromString(s.field, val); err != nil {
			return fmt.Errorf("%s: %w", s.Env, err)
		}
	}
	return nil
}

func setFromString(f reflect.Value, s string) error {
	if f.Type() == durationType {
		d, err := parseDuration(s)
		if err != nil {
			return err
		}
		f.SetInt(int64(d))
		return nil
	}
	switch f.Kind() {
	case reflect.String:
		f.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid boolean %q", s)
		}
		f.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer %q", s)
		}
		f.SetInt(n)
	case reflect.Float64:
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("invalid number %q", s)
		}
		f.SetFloat(x)
	case reflect.Slice:
		if f.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported list type %s", f.Type())
		}
		var items []string
		for _, it := range strings.Split(s, ",") {
			if it = strings.TrimSpace(it); it != "" {
				items = append(items, it)
			}
		}
		f.Set(reflect.ValueOf(items))
	default:
		return fmt.Errorf("unsupported setting type %s", f.Type())
	}
	return nil
}

// Redacted returns a copy of c safe to print: secrets are replaced and the
// password of URLs is hidden.
func (c *Config) Redacted() *Config {
	cp := *c
	for _, s := range Settings(&cp) {
		if !s.Secret || s.field.Kind() != reflect.String || s.field.String() == "" {
			continue
		}
		v := s.field.String()
		if strings.Contains(v, "://") {
			s.field.SetString(redactURL(v))
		} else {
			s.field.SetString("<redacted>")
		}
	}
	return &cp
}

// redactURL hides the password (or a lone token) of a scheme://user:pass@host URL.
func redactURL(u string) string {
	scheme, rest, _ := strings.Cut(u, "://")
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return u
	}
	userinfo := rest[:at]
	if user, _, ok := strings.Cut(userinfo, ":"); ok {
		userinfo = user + ":<redacted>"
	} else {
		userinfo = "<redacted>"
	}
	return scheme + "://" + userinfo + rest[at:]
}

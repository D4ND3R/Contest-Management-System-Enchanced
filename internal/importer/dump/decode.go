// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// contest.json is one JSON object: "_version" is the version of the data
// model of CMS that wrote it, "_objects" the keys of the roots of the
// export (contests, users and tasks without a contest), and every other
// key an object of the model, {"_class": "Task", "name": ..., "contest":
// "0", "datasets": ["8"], ...}: its columns (no ids nor foreign keys) and
// its relationships as keys of other objects of the file. The keys are
// local to the file: a dump carries no database ids.

// Version is the version of the data model of CMS this importer reads
// natively (schema 49); older dumps are updated first (updaters.go).
const Version = 49

// OldestVersion is the oldest version of the data model whose dumps can
// be imported: the dumps of CMS 1.2 and later. Older dumps must be updated
// with cmsDumpUpdater of CMS first.
const OldestVersion = 14

// rawDump is contest.json split into its objects, still encoded.
type rawDump struct {
	// version is "_version" (0 if missing, as upstream).
	version int
	// meta counts the keys that are not objects ("_version"...).
	meta int
	// roots is "_objects".
	roots []string
	// objects in the order of the file.
	objects []rawObject
}

type rawObject struct {
	key string
	raw json.RawMessage
}

// readRaw reads contest.json, keeping each object encoded: the version,
// which says how to decode them, comes last (the keys are sorted).
func readRaw(r io.Reader) (*rawDump, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	d := &rawDump{}
	seen := make(map[string]bool)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, syntaxErr(err)
		}
		key, _ := tok.(string)
		if seen[key] {
			return nil, fmt.Errorf("%s: duplicate key %q", modelName, key)
		}
		seen[key] = true
		if strings.HasPrefix(key, "_") {
			d.meta++
		}
		switch {
		case key == "_version":
			var n json.RawMessage
			if err := dec.Decode(&n); err != nil {
				return nil, fmt.Errorf("%s: _version: %w", modelName, syntaxErr(err))
			}
			v, err := strconv.Atoi(string(n))
			if err != nil {
				return nil, fmt.Errorf("%s: _version %s is not an integer", modelName, n)
			}
			d.version = v
		case key == "_objects":
			if err := dec.Decode(&d.roots); err != nil {
				return nil, fmt.Errorf("%s: _objects: %w", modelName, syntaxErr(err))
			}
		case strings.HasPrefix(key, "_"):
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, syntaxErr(err)
			}
		default:
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, fmt.Errorf("%s: object %s: %w", modelName, key, syntaxErr(err))
			}
			d.objects = append(d.objects, rawObject{key: key, raw: raw})
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: data after the end of the object", modelName)
	}
	return d, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return syntaxErr(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("%s: %w: expected %q, found %v", modelName, ErrNotDump, want, tok)
	}
	return nil
}

func syntaxErr(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("%s: invalid JSON: %w", modelName, err)
}

// keyLess orders the keys of a dump as numbers (cmsDumpExporter numbers
// them in the order it reaches the objects), then as strings.
func keyLess(a, b string) bool {
	na, ea := strconv.ParseUint(a, 10, 63)
	nb, eb := strconv.ParseUint(b, 10, 63)
	switch {
	case ea == nil && eb == nil:
		return na < nb
	case ea == nil:
		return true
	case eb == nil:
		return false
	}
	return a < b
}

// decodeObjects calls fn with every object of the dump at the current
// version (updating older dumps first), in the order of their keys. fn may
// keep nothing of obj: it is reused.
func (d *rawDump) decodeObjects(warn func(format string, args ...any), fn func(key string, obj *object) error) error {
	switch {
	case d.version > Version:
		return fmt.Errorf("%w: the dump was written by a newer CMS (data model version %d; this importer reads up to %d)",
			ErrVersion, d.version, Version)
	case d.version < OldestVersion:
		return fmt.Errorf("%w: the dump was written by CMS 1.1 or older (data model version %d; this importer reads %d to %d): "+
			"update it with cmsDumpUpdater of CMS first", ErrVersion, d.version, OldestVersion, Version)
	case d.version == Version:
		sort.SliceStable(d.objects, func(i, j int) bool { return keyLess(d.objects[i].key, d.objects[j].key) })
		var obj object
		for i := range d.objects {
			o := &d.objects[i]
			obj = object{}
			if err := json.Unmarshal(o.raw, &obj); err != nil {
				return fmt.Errorf("%s: object %s: %w", modelName, o.key, typeErr(err))
			}
			o.raw = nil // release it
			if err := fn(o.key, &obj); err != nil {
				return err
			}
		}
		return nil
	}

	// An older dump: the updaters of CMS work on generic objects.
	objs, err := d.updated(warn)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(objs))
	for k := range objs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j]) })
	var obj object
	for _, k := range keys {
		b, err := json.Marshal(objs[k])
		if err != nil {
			return fmt.Errorf("%s: object %s: %w", modelName, k, err)
		}
		delete(objs, k)
		obj = object{}
		if err := json.Unmarshal(b, &obj); err != nil {
			return fmt.Errorf("%s: object %s: %w", modelName, k, typeErr(err))
		}
		if err := fn(k, &obj); err != nil {
			return err
		}
	}
	return nil
}

// updated decodes the objects of an older dump as generic values and
// runs the updaters of CMS on them.
func (d *rawDump) updated(warn func(format string, args ...any)) (objects, error) {
	objs := make(objects, len(d.objects))
	order := make([]string, len(d.objects))
	for i := range d.objects {
		o := &d.objects[i]
		order[i] = o.key
		dec := json.NewDecoder(bytes.NewReader(o.raw))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil || m == nil {
			return nil, fmt.Errorf("%s: object %s is not a JSON object", modelName, o.key)
		}
		if _, ok := m["_class"].(string); !ok {
			return nil, fmt.Errorf("%s: object %s has no _class", modelName, o.key)
		}
		objs[o.key] = m
		o.raw = nil
	}
	d.objects = nil
	if err := update(objs, order, d.meta, d.version, warn); err != nil {
		return nil, err
	}
	return objs, nil
}

// ErrVersion: the dump has a data model this importer cannot read.
var ErrVersion = errors.New("unsupported dump version")

// typeErr shortens the errors of encoding/json about a field of the
// wrong type.
func typeErr(err error) error {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return fmt.Errorf("field %s: a JSON %s where a %s was expected", te.Field, te.Value, describeType(te.Type.String()))
	}
	return err
}

func describeType(t string) string {
	switch {
	case strings.HasSuffix(t, "string"):
		if strings.Contains(t, "[]") {
			return "list of strings"
		}
		return "string"
	case strings.HasSuffix(t, "bool"):
		return "boolean"
	case strings.Contains(t, "ref"):
		return "key of another object"
	}
	return t
}

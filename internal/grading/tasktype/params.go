// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/ParameterTypes.py and the parameter handling of
// cms/grading/tasktypes/abc.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package tasktype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParamKind is the type of a parameter (upstream ParameterType classes).
type ParamKind string

// The kinds of parameters.
const (
	// ParamString is a JSON string (a text field).
	ParamString ParamKind = "string"
	// ParamInt is a JSON integer (a text field).
	ParamInt ParamKind = "int"
	// ParamFloat is a JSON number (a text field).
	ParamFloat ParamKind = "float"
	// ParamBool is a JSON boolean (a checkbox).
	ParamBool ParamKind = "bool"
	// ParamChoice is one of the strings of Choices (a select).
	ParamChoice ParamKind = "choice"
	// ParamCollection is a JSON list with one value per sub-parameter
	// (a table of fields).
	ParamCollection ParamKind = "collection"
)

// Choice is a value of a choice parameter and its description.
type Choice struct {
	Value       string
	Description string
}

// Param describes a parameter of a task type (upstream ParameterType), for
// validation and for the forms of the admin site. Name, Description and
// the descriptions of the choices are the English texts of CMS.
type Param struct {
	Kind ParamKind
	// Name is shown to the admins ("Compilation"); ShortName names the
	// form fields ("compilation").
	Name, ShortName string
	Description     string
	// Choices of a ParamChoice, in the order to show them.
	Choices []Choice
	// Params are the sub-parameters of a ParamCollection.
	Params []Param
}

// validate checks a JSON value against the parameter (upstream
// ParameterType.validate). Unlike CMS, booleans are not integers, and a
// choice must be a string.
func (p *Param) validate(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	switch p.Kind {
	case ParamString:
		var s string
		if !isString(raw) || json.Unmarshal(raw, &s) != nil {
			return fmt.Errorf("Invalid value for string parameter %s", p.Name)
		}
	case ParamBool:
		if string(raw) != "true" && string(raw) != "false" {
			return fmt.Errorf("Invalid value for bool parameter %s", p.Name)
		}
	case ParamInt:
		if _, err := decodeInt(raw); err != nil {
			return fmt.Errorf("Invalid value for int parameter %s", p.Name)
		}
	case ParamFloat:
		if _, err := decodeFloat(raw); err != nil {
			return fmt.Errorf("Invalid value for float parameter %s", p.Name)
		}
	case ParamChoice:
		var s string
		if !isString(raw) || json.Unmarshal(raw, &s) != nil || !p.hasChoice(s) {
			v := string(raw)
			if isString(raw) {
				v = s
			}
			return fmt.Errorf("Invalid choice %s for parameter %s", v, p.Name)
		}
	case ParamCollection:
		var items []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
			return fmt.Errorf("Parameter %s should be a list", p.Name)
		}
		if len(items) != len(p.Params) {
			return fmt.Errorf("Invalid value for parameter %s", p.Name)
		}
		for i := range p.Params {
			if err := p.Params[i].validate(items[i]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("parameter %s: unknown kind %q", p.Name, p.Kind)
	}
	return nil
}

func (p *Param) hasChoice(v string) bool {
	for _, c := range p.Choices {
		if c.Value == v {
			return true
		}
	}
	return false
}

// parseForm reads the value of the parameter from the fields of a form
// (upstream ParameterType.parse_handler and parse_string).
func (p *Param) parseForm(get func(string) (string, bool), prefix string) (any, error) {
	field := prefix + p.ShortName
	if p.Kind == ParamCollection {
		values := make([]any, len(p.Params))
		for i := range p.Params {
			v, err := p.Params[i].parseForm(get, fmt.Sprintf("%s%s_%d_", prefix, p.ShortName, i))
			if err != nil {
				return nil, err
			}
			values[i] = v
		}
		return values, nil
	}
	s, ok := get(field)
	if p.Kind == ParamBool {
		// A checkbox: present means true.
		return ok, nil
	}
	if !ok {
		return nil, fmt.Errorf("missing argument %s", field)
	}
	switch p.Kind {
	case ParamString:
		return s, nil
	case ParamInt:
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid literal for int() with base 10: %q", s)
		}
		return n, nil
	case ParamFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("could not convert string to float: %q", s)
		}
		return f, nil
	case ParamChoice:
		if !p.hasChoice(s) {
			return nil, fmt.Errorf("Value %s doesn't match any allowed choice.", s)
		}
		return s, nil
	}
	return nil, fmt.Errorf("parameter %s: unknown kind %q", p.Name, p.Kind)
}

// checkParameters validates the parameters of a task type (upstream
// TaskType.validate_parameters) and returns the value of each one.
func checkParameters(name string, params []Param, raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("Task type parameters for %s are not a list", name)
	}
	if len(values) != len(params) {
		return nil, fmt.Errorf("Task type %s should have %d parameters, received %d", name, len(params), len(values))
	}
	for i := range params {
		if err := params[i].validate(values[i]); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// ParseForm builds the JSON parameters of a task type from the fields of
// an admin form (upstream TaskType.parse_handler): parameter p is the field
// prefix+p.ShortName; the sub-parameter i of a collection c is
// prefix+c.ShortName+"_"+i+"_"+its short name; a boolean is true when its
// field is present. get returns the value of a field and whether it is
// present. The result is validated like stored parameters (Get).
func ParseForm(name string, get func(field string) (string, bool), prefix string) (json.RawMessage, error) {
	params, err := Params(name)
	if err != nil {
		return nil, err
	}
	values := make([]any, len(params))
	for i := range params {
		v, err := params[i].parseForm(get, prefix)
		if err != nil {
			return nil, fmt.Errorf("Invalid parameter %s: %v.", params[i].Name, err)
		}
		values[i] = v
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	if _, err := Get(name, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

// decodeInt decodes a JSON integer (no fraction, no exponent; not a
// boolean) that fits in an int64.
func decodeInt(raw json.RawMessage) (int64, error) {
	raw = bytes.TrimSpace(raw)
	if !isNumber(raw) || bytes.ContainsAny(raw, ".eE") {
		return 0, errors.New("not an integer")
	}
	return strconv.ParseInt(string(raw), 10, 64)
}

// decodeFloat decodes a JSON number (integer or not).
func decodeFloat(raw json.RawMessage) (float64, error) {
	raw = bytes.TrimSpace(raw)
	if !isNumber(raw) {
		return 0, errors.New("not a number")
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, errors.New("not a finite number")
	}
	return f, nil
}

func isNumber(raw json.RawMessage) bool {
	return len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9')
}

// decodeString decodes a parameter already validated as a string.
func decodeString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// decodeStrings decodes a collection of strings already validated.
func decodeStrings(raw json.RawMessage) []string {
	var s []string
	_ = json.Unmarshal(raw, &s)
	return s
}

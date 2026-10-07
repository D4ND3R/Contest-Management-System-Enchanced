// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package i18n

import (
	"fmt"
	"strconv"
	"strings"
)

// Format fills the Python-style named placeholders of a CMS message:
// "%(name)s", "%(n)d", "%(x)g" (and "%(x).2f"), with "%%" for a literal %.
// Keeping this syntax keeps the msgids identical to the upstream catalogs.
// Unknown names are left as they are, so a bad translation cannot crash a
// page.
func Format(msg string, args map[string]any) string {
	if !strings.Contains(msg, "%") {
		return msg
	}
	var b strings.Builder
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if c != '%' || i+1 >= len(msg) {
			b.WriteByte(c)
			continue
		}
		if msg[i+1] == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		if msg[i+1] != '(' {
			b.WriteByte(c)
			continue
		}
		end := strings.IndexByte(msg[i:], ')')
		if end < 0 {
			b.WriteString(msg[i:])
			break
		}
		name := msg[i+2 : i+end]
		// Conversion spec: optional ".N" precision then one verb letter.
		j := i + end + 1
		spec := ""
		for j < len(msg) && (msg[j] == '.' || (msg[j] >= '0' && msg[j] <= '9')) {
			spec += string(msg[j])
			j++
		}
		if j >= len(msg) {
			b.WriteString(msg[i:])
			break
		}
		verb := msg[j]
		v, ok := args[name]
		if !ok {
			b.WriteString(msg[i : j+1])
			i = j
			continue
		}
		switch verb {
		case 's':
			b.WriteString(fmt.Sprint(v))
		case 'd':
			b.WriteString(fmt.Sprintf("%d", toInt(v)))
		case 'g', 'f':
			f := toFloat(v)
			if verb == 'g' && spec == "" {
				// Python's %g: 6 significant digits, no trailing zeros.
				b.WriteString(strconv.FormatFloat(f, 'g', 6, 64))
			} else {
				b.WriteString(fmt.Sprintf("%"+spec+string(verb), f))
			}
		default:
			b.WriteString(msg[i : j+1])
		}
		i = j
	}
	return b.String()
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case int32:
		return int64(x)
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	}
	n, _ := strconv.ParseInt(fmt.Sprint(v), 10, 64)
	return n
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
	return f
}

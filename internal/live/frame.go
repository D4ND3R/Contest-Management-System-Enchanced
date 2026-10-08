// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"strconv"
	"time"
)

// FormatID returns the event id of a sequence number: lowercase
// hexadecimal, like the ids of CMS RankingWebServer (microseconds since
// the epoch).
func FormatID(seq uint64) string { return strconv.FormatUint(seq, 16) }

// ParseID parses an event id written by FormatID.
func ParseID(id string) (uint64, bool) {
	if id == "" || len(id) > 16 {
		return 0, false
	}
	seq, err := strconv.ParseUint(id, 16, 64)
	return seq, err == nil
}

func nowMicros() uint64 { return uint64(time.Now().UnixMicro()) }

// appendFrame appends a server-sent event: the id (when id > 0), the
// event name and data, one "data:" line per line of data (as CMS
// format_event: "\r\n", "\n" and "\r" all end a line).
func appendFrame(b []byte, id uint64, event string, data []byte) []byte {
	if id > 0 {
		b = append(b, "id: "...)
		b = strconv.AppendUint(b, id, 16)
		b = append(b, '\n')
	}
	b = append(b, "event: "...)
	b = append(b, event...)
	b = append(b, '\n')
	start := 0
	for i := 0; i < len(data); i++ {
		if c := data[i]; c == '\n' || c == '\r' {
			b = appendData(b, data[start:i])
			if c == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	b = appendData(b, data[start:])
	return append(b, '\n')
}

func appendData(b, line []byte) []byte {
	b = append(b, "data: "...)
	b = append(b, line...)
	return append(b, '\n')
}

// frameLen is an upper bound of the length of the frame of data.
func frameLen(event string, data []byte) int {
	return len("id: ffffffffffffffff\nevent: \ndata: \n\n") + len(event) + len(data)
}

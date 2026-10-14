// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"bufio"
	"errors"
	"io"
	"os"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// Upstream runs `dos2unix -q` on every test file before storing it (when
// the program is installed: otherwise it logs an error and stores the
// files as they are), changing the package in place. Here the files are
// converted while they are read, the same way dos2unix 7 does it, so the
// result does not depend on the machine and the package is not touched:
//
//   - a file holding a control character other than tab, line feed,
//     form feed and carriage return is binary and kept as it is;
//   - otherwise a leading UTF-8 byte order mark is removed and every
//     CR LF becomes LF (a lone CR stays).
//
// UTF-16 files (which dos2unix converts to UTF-8) are kept as they are.

// unixFile returns the file at path as dos2unix would leave it.
func unixFile(path string) *importer.File {
	return importer.FileFromFunc(path, func() (io.ReadCloser, error) { return openUnix(path) })
}

// openUnix opens the file at path converted as dos2unix would: a first
// pass decides whether it needs it; files that do not are returned as
// they are.
func openUnix(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	convert, bom, err := needsUnix(f)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	if !convert {
		return f, nil
	}
	br := bufio.NewReaderSize(f, 64<<10)
	if bom {
		if _, err := br.Discard(len(utf8BOM)); err != nil {
			f.Close()
			return nil, err
		}
	}
	return struct {
		io.Reader
		io.Closer
	}{&crlfReader{r: br}, f}, nil
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// needsUnix reads r to the end and reports whether dos2unix would change
// it, and whether it starts with a UTF-8 byte order mark.
func needsUnix(r io.Reader) (convert, bom bool, err error) {
	buf := make([]byte, 64<<10)
	first := true
	var crlf, prevCR bool
	for {
		n, err := r.Read(buf)
		chunk := buf[:n]
		if first && n > 0 {
			// A short first read cannot hide the mark: Read of a file
			// returns as much as it can.
			bom = len(chunk) >= len(utf8BOM) && chunk[0] == utf8BOM[0] && chunk[1] == utf8BOM[1] && chunk[2] == utf8BOM[2]
			first = false
		}
		for _, c := range chunk {
			if c < 0x20 {
				switch c {
				case '\n':
					if prevCR {
						crlf = true
					}
				case '\t', '\f', '\r':
				default:
					return false, false, nil // binary: left alone
				}
			}
			prevCR = c == '\r'
		}
		if errors.Is(err, io.EOF) {
			return crlf || bom, bom, nil
		}
		if err != nil {
			return false, false, err
		}
	}
}

// crlfReader turns CR LF into LF.
type crlfReader struct {
	r *bufio.Reader
}

func (c *crlfReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		n, err := c.r.Read(p)
		w := 0
		for i := 0; i < n; i++ {
			if p[i] == '\r' {
				if i+1 < n {
					if p[i+1] == '\n' {
						continue
					}
				} else if next, perr := c.r.Peek(1); perr == nil && next[0] == '\n' {
					continue
				}
			}
			p[w] = p[i]
			w++
		}
		// A chunk that was only the CR of a CR LF gave nothing: read on.
		if w > 0 || err != nil || n == 0 {
			return w, err
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/server/contest/submission/
// file_retrieval_test.py of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package submission

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type nameContent struct{ codename, filename, content string }

func summarize(t *testing.T, files []ReceivedFile) []nameContent {
	t.Helper()
	out := make([]nameContent, len(files))
	for i := range files {
		b, err := files[i].Content()
		if err != nil {
			t.Fatal(err)
		}
		out[i] = nameContent{files[i].Codename, files[i].Filename, string(b)}
	}
	return out
}

// sameFiles compares as Python's assertCountEqual.
func sameFiles(t *testing.T, got []ReceivedFile, want ...nameContent) {
	t.Helper()
	g := summarize(t, got)
	sortNC := func(s []nameContent) {
		slices.SortFunc(s, func(a, b nameContent) int {
			return strings.Compare(a.codename+"\x00"+a.filename+"\x00"+a.content, b.codename+"\x00"+b.filename+"\x00"+b.content)
		})
	}
	sortNC(g)
	sortNC(want)
	if !slices.Equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
}

func zipOf(t *testing.T, method uint16, entries ...[2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: e[0], Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func tarOf(t *testing.T, gz bool, entries ...[2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	var out io.Writer = &b
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(&b)
		out = zw
	}
	w := tar.NewWriter(out)
	for _, e := range entries {
		if err := w.WriteHeader(&tar.Header{Name: e[0], Size: int64(len(e[1])), Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

var threeFiles = [][2]string{{"foo.c", "some content"}, {"foo", "some other content"}, {"foo.%l", "more content"}}

func threeWant() []nameContent {
	return []nameContent{{"", "foo.c", "some content"}, {"", "foo", "some other content"}, {"", "foo.%l", "more content"}}
}

func TestExtractZip(t *testing.T) {
	files, err := ExtractFilesFromArchive(zipOf(t, zip.Deflate, threeFiles...), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, threeWant()...)
}

func TestExtractTarGz(t *testing.T) {
	files, err := ExtractFilesFromArchive(tarOf(t, true, threeFiles...), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, threeWant()...)
	// And an uncompressed tar.
	files, err = ExtractFilesFromArchive(tarOf(t, false, threeFiles...), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, threeWant()...)
}

func TestExtractFailure(t *testing.T) {
	for _, data := range [][]byte{[]byte("this is not a valid archive"), nil, {0x1f, 0x8b, 1, 2}, []byte("BZh9garbage")} {
		_, err := ExtractFilesFromArchive(data, 0, 0)
		var ia *InvalidArchive
		if !errors.As(err, &ia) || ia.TooBig || ia.TooManyFiles {
			t.Errorf("%q: got %v", data, err)
		}
	}
}

func TestExtractDirectories(t *testing.T) {
	data := zipOf(t, zip.Deflate, [2]string{"toplevel", "some content"}, [2]string{"nested/once", "some other content"},
		[2]string{"two/levels/deep", "more content"}, [2]string{"many/levels/deep", "moar content"}, [2]string{"adir/", ""})
	files, err := ExtractFilesFromArchive(data, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, nameContent{"", "toplevel", "some content"}, nameContent{"", "once", "some other content"},
		nameContent{"", "deep", "more content"}, nameContent{"", "deep", "moar content"})
}

func TestExtractCornerCases(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"foo\x00bar", "foo"},
		{"", ""},
		{"foo//bar", "bar"},
		{"../foo/bar", "bar"},
		{"/foo/bar", "bar"},
	}
	for _, c := range cases {
		files, err := ExtractFilesFromArchive(zipOf(t, zip.Store, [2]string{c.name, "some content"}), 0, 0)
		if err != nil {
			t.Fatalf("%q: %v", c.name, err)
		}
		sameFiles(t, files, nameContent{"", c.want, "some content"})
	}
}

func inflate(t *testing.T, b64 string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantArchiveError(t *testing.T, data []byte, maxSize int64, maxFiles int, tooBig bool) {
	t.Helper()
	start := time.Now()
	_, err := ExtractFilesFromArchive(data, maxSize, maxFiles)
	var ia *InvalidArchive
	if !errors.As(err, &ia) {
		t.Fatalf("got %v", err)
	}
	if tooBig && !ia.TooBig || !tooBig && !ia.TooManyFiles {
		t.Errorf("got %+v", ia)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

// A tar.bz2 with one file of 64 GB of null bytes.
const tarBz2Bomb = `
eJztyu9rzHEAAODPjHabezHaSSm+Yy1WlHKxpI5km1d34/vi9uaKKeUNI8kL2khRy3UK+XG1ue4uKaF
TvNBerNTe2isvpDVLFNNaebMS/4Q3z+vnOTR0unf3wXz3sXw1f6625kqyMDpTzDStzGUy2bAQfcqGKJ
GIQql1R9/4wLcT4XL5SbTlaaIycWe2pTK8Zzpc+vm98PXRQO54fLgex6nc3h/PHkfbTlYbcS5e3/5y8
OaHdFv/pq2T6za8qBzp6Xo+9+vLvpmR97W29JkD2dr4cmNt5u61d1dfLS1/XNjfe33jjULLxbe333R2
FUv9fUsdqdKpdPFzeedi6vd8SIYQxkJzYvXscM+qoWK99TznnHPOOeecc84555xzzjnnnHPOOeecc84
555xzzjnnnHPOOeecc84555xzzjnnnHPOOeecc84555xzzjnnnHPOOeecc84555xzzjnnnHPOOeecc8
4555xzzjnnnHPOOeecc84555xzzjnnnHPOOeecc845/w/+sD79erF5pfzX2/+N5NSD6v3JpsZgtHnX/
K2zE53bL9w7+gctbrLz`

// A block of a bzip2 stream of a tar with empty files named "x".
const tarBz2Block = `
MUFZJlNZFfIt8gXzb9uAyIBAAHcAAADgAB5ACAAwAVgAUyYmQZGFMmJkGRgUqmo2poaD1GgVKm4FSpg
CRQ7gqVPYKlTwCpUyBUqdgKlTIKlTIJFDYBUqbAqVOAVKmAJFDAKlTYCiSuQVKmAUSVgFSpyCpU8gqV
OAVKmAVKnIKlTqCpU3BIofQVKnwFSpoFSpoFSpoFSp+BUqZBUqfw==`

// A zip with one file of 16 GB of null bytes (bzip2 method).
const zipBomb = `
eJzt2r9qwlAUx/Fzm1hUpEixm4MKCi6pbi1d/IdLEIJOyeKojxA3FScHkVBwdejSZ3Ap4t7JUQQHVx9
Bk6LgUDfH7+9wzzmXzyscy9R0Q0Ri8vG8cpLbWvp4jpKEuEriEkT338H4W6XidN+LZTvXsp8iu61qT0
ryIxJOSaHfqFfm2e9of4bjOI7jOI7jOI7jOI7jOI7jOI7jOI7jOI7jOI7jOI7jOI7j9/Pxb+9FOutrX
3vT/Gcz9Lbw8svRcLMvupapHgzt9o38JV+DoP97MW+ZocdgKr+q/sy8Br8TACk9vA==`

func TestZipBombSizeTarBz2(t *testing.T) {
	wantArchiveError(t, inflate(t, tarBz2Bomb), 1_000_000, 0, true)
}

func TestZipBombCountTarBz2(t *testing.T) {
	block, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(tarBz2Block), ""))
	if err != nil {
		t.Fatal(err)
	}
	end, _ := hex.DecodeString("1772453850905d4ab55d")
	data := append([]byte("BZh9"), bytes.Repeat(block, 1000)...)
	data = append(data, end...)
	wantArchiveError(t, data, 0, 1000, false)
}

func TestZipBombSizeZip(t *testing.T) {
	wantArchiveError(t, inflate(t, zipBomb), 1_000_000, 0, true)
}

func TestZipBombCountZip(t *testing.T) {
	var entries [][2]string
	for i := range 1000 {
		entries = append(entries, [2]string{strconv.Itoa(i), "x"})
	}
	// Both limits are hit; the number of files first.
	wantArchiveError(t, zipOf(t, zip.Store, entries...), 500, 50, false)
}

// multipartForm builds the files of a multipart form: field, file name,
// content triples, in order.
func multipartForm(t *testing.T, parts ...[3]string) map[string][]*multipart.FileHeader {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, p := range parts {
		fw, err := w.CreateFormFile(p[0], p[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(p[2])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.WriteField("language", "C"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(&b, w.Boundary()).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = form.RemoveAll() })
	return form.File
}

func TestExtractFromFormEmpty(t *testing.T) {
	files, err := ExtractFilesFromForm(nil, nil, 0, 0)
	if err != nil || len(files) != 0 {
		t.Errorf("got %v, %v", files, err)
	}
}

func TestExtractFromFormSuccess(t *testing.T) {
	form := multipartForm(t, [3]string{"foo.%l", "foo.py", "some python stuff"},
		[3]string{"bar.%l", "bar.c", "one file in C"}, [3]string{"bar.%l", "bar.cxx", "the same file in C++"})
	form["baz"] = nil // empty lists have no effect
	files, err := ExtractFilesFromForm(form, []string{"foo.%l", "bar.%l"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := summarize(t, files)
	want := []nameContent{{"foo.%l", "foo.py", "some python stuff"}, {"bar.%l", "bar.c", "one file in C"},
		{"bar.%l", "bar.cxx", "the same file in C++"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (in the order of the format)", got, want)
	}
	if c := comment(files); c != "foo.py,bar.c,bar.cxx" {
		t.Errorf("comment %q", c)
	}
}

func TestExtractFromFormNotArchive(t *testing.T) {
	form := multipartForm(t, [3]string{"submission", "sub.zip", "this is an archive"}, [3]string{"foo.%l", "foo.c", "this is something else"})
	files, err := ExtractFilesFromForm(form, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, nameContent{"submission", "sub.zip", "this is an archive"}, nameContent{"foo.%l", "foo.c", "this is something else"})

	form = multipartForm(t, [3]string{"submission", "sub.zip", "this is an archive"}, [3]string{"submission", "sub2.zip", "this is another one"})
	files, err = ExtractFilesFromForm(form, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, nameContent{"submission", "sub.zip", "this is an archive"}, nameContent{"submission", "sub2.zip", "this is another one"})
}

func TestExtractFromFormArchive(t *testing.T) {
	form := multipartForm(t, [3]string{"submission", "archive.zip", string(zipOf(t, zip.Deflate, threeFiles...))})
	files, err := ExtractFilesFromForm(form, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameFiles(t, files, threeWant()...)

	form = multipartForm(t, [3]string{"submission", "archive.zip", "this is not a valid archive"})
	_, err = ExtractFilesFromForm(form, nil, 0, 0)
	var ia *InvalidArchive
	if !errors.As(err, &ia) {
		t.Errorf("got %v", err)
	}
}

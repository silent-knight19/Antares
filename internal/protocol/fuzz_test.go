package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

// fuzzSeeds covers the shapes captured from redis 7.4.11 (parse_test.go corpus
// plus truncations) so the fuzzer starts from the quirks the parser must keep.
var fuzzSeeds = []string{
	"*1\r\n$4\r\nPING\r\n",
	"PING\r\n",
	"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n",
	"*2\r\n$4\r\nECHO\r\n$0\r\n\r\n",
	"*3\r\n$3\r\nSET\r\n$2\r\n\r\n\r\n$1\r\nv\r\n",
	"\r\nPING\r\n",
	"*0\r\n*-1\r\n*-2\r\n*1\r\n$4\r\nPING\r\n",
	"*1\r\n$4\r\nPING\r\n*1\r\n$4\r\nPING\r\n",
	"PING\r\n*1\r\n$4\r\nPING\r\nPING\r\n",
	"GET \"a b\"\r\n",
	"GET 'a b'\r\n",
	"ECHO \"a\\x42\"\r\n",
	"ECHO \"a\\nb\"\r\n",
	"ECHO \"a\\\\b\"\r\n",
	"ECHO 'a\\nb'\r\n",
	"ECHO   a    b\r\n",
	"$4\r\n",
	"*1\rX$4\r\nPING\r\n",
	"*1\r\n$4\rXPING\r\nPING\r\n",
	"*1\r\n$4\r\nPINGXX",
	"PING\n",
	"*\r\n",
	"*1\r\n$\r\n",
	"*abc\r\n",
	"*2147483648\r\n",
	"*2147483647\r\n",
	"*3\r\n$3\r\nSET\r\n$-1\r\n$1\r\nv\r\n",
	"*1\r\n$abc\r\n",
	"*2\r\n$4\r\nECHO\r\n$536870913\r\n",
	"*1\r\n$536870912\r\n",
	"GET \"a b\r\n",
	"GET \"\"\r\n",
	"GET ''\r\n",
	" PING\r\n",
	"a\x00b c\r\n",
	"*1\r\n\xff\r\n",
	"*2\r\n$1\r\na\r\n$1\r\nb\r\n",
	"\x00\r\n",
	"*1\r\n$4\r\nPI",
	"*1\r\n$4\r\nPINGX",
}

// FuzzReadRequest: no input may crash or hang, and a successfully parsed
// request must re-encode as multibulk and parse back byte-identical
// (encoder/parser agreement).
func FuzzReadRequest(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := ReadRequest(bufio.NewReader(bytes.NewReader(data)))
		if err != nil {
			return
		}
		if len(req) == 0 {
			t.Fatal("ReadRequest returned a request with no arguments")
		}
		for i, a := range req {
			if a == nil {
				t.Fatalf("arg %d is nil", i)
			}
		}
		enc := GetEncoder()
		defer enc.Release()
		enc.ArrayHeader(len(req))
		for _, a := range req {
			enc.Bulk(a)
		}
		again, err := ReadRequest(bufio.NewReader(bytes.NewReader(enc.Bytes())))
		if err != nil {
			t.Fatalf("re-parse of encoded request: %v (args %q)", err, req)
		}
		if len(again) != len(req) {
			t.Fatalf("arg count changed: %d -> %d", len(req), len(again))
		}
		for i := range req {
			if !bytes.Equal(req[i], again[i]) {
				t.Fatalf("arg %d changed: %q -> %q", i, req[i], again[i])
			}
		}
	})
}

// A declared 512 MB bulk with no payload must fail fast without a large
// allocation: the parser only ever allocates for bytes actually received.
func TestReadBulkTruncatedHugeLength(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	br := bufio.NewReader(strings.NewReader("*1\r\n$536870912\r\n"))
	_, err := ReadRequest(br)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
		t.Fatalf("ReadRequest allocated %d bytes for a payload that was never sent; want < 8 MB", alloc)
	}
}

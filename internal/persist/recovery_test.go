package persist

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFrames(t *testing.T, path string, frames ...[]byte) {
	t.Helper()
	var buf []byte
	for _, frame := range frames {
		buf = AppendFrame(buf, frame)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write frames: %v", err)
	}
}

func TestRecoverClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clean.aof")
	writeFrames(t, path, []byte("A"), []byte("B"), []byte("C"))
	var got []string
	report, err := RecoverFile(path, false, func(payload []byte) error {
		got = append(got, string(payload))
		return nil
	})
	if err != nil {
		t.Fatalf("RecoverFile: %v", err)
	}
	if report.Records != 3 || report.Truncated || report.ValidBytes == 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(got) != 3 || got[0] != "A" || got[2] != "C" {
		t.Fatalf("records = %v", got)
	}
}

func TestRecoverMissingFile(t *testing.T) {
	report, err := RecoverFile(filepath.Join(t.TempDir(), "missing.aof"), false, nil)
	if err != nil || report.Records != 0 || report.Truncated {
		t.Fatalf("missing file report = %+v, err = %v", report, err)
	}
}

func TestRecoverTornTailMatrix(t *testing.T) {
	clean := func() []byte {
		var buf []byte
		for _, frame := range [][]byte{[]byte("A"), []byte("B"), []byte("C")} {
			buf = AppendFrame(buf, frame)
		}
		return buf
	}
	tests := []struct {
		name        string
		corrupt     func([]byte) []byte
		wantRecords int
	}{
		{"partial trailing header", func(b []byte) []byte { return b[:len(b)-2] }, 2},
		{"partial payload", func(b []byte) []byte {
			third := AppendFrame(nil, []byte("C"))
			return append(b[:len(b)-len(third)], third[:len(third)-2]...)
		}, 2},
		{"trailing garbage", func(b []byte) []byte { return append(b, 0xde, 0xad, 0xbe) }, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "torn.aof")
			if err := os.WriteFile(path, tc.corrupt(clean()), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			var records []string
			report, err := RecoverFile(path, false, func(payload []byte) error {
				records = append(records, string(payload))
				return nil
			})
			if err != nil {
				t.Fatalf("RecoverFile: %v", err)
			}
			if !report.Truncated || report.Corrupt {
				t.Fatalf("report = %+v", report)
			}
			if report.Records != tc.wantRecords {
				t.Fatalf("records = %d (%v), want %d", report.Records, records, tc.wantRecords)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Size() != report.ValidBytes {
				t.Fatalf("file size %d != valid bytes %d", info.Size(), report.ValidBytes)
			}
			if report.Reason == "" || report.TruncatedBytes <= 0 {
				t.Fatalf("missing truncation detail: %+v", report)
			}
		})
	}
}

func TestRecoverCorruptFrameRefused(t *testing.T) {
	clean := func() []byte {
		var buf []byte
		for _, frame := range [][]byte{[]byte("A"), []byte("B"), []byte("C")} {
			buf = AppendFrame(buf, frame)
		}
		return buf
	}
	tests := []struct {
		name        string
		corrupt     func([]byte) []byte
		wantRecords int
	}{
		{"checksum mismatch second frame", func(b []byte) []byte {
			out := append([]byte(nil), b...)
			second := FrameHeaderSize + 1
			out[second+FrameHeaderSize] ^= 0xff
			return out
		}, 1},
		{"zero length second frame", func(b []byte) []byte {
			out := append([]byte(nil), b...)
			second := FrameHeaderSize + 1
			for i := 0; i < 4; i++ {
				out[second+i] = 0
			}
			return out
		}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "corrupt.aof")
			data := tc.corrupt(clean())
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}

			var records []string
			report, err := RecoverFile(path, false, func(payload []byte) error {
				records = append(records, string(payload))
				return nil
			})
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("RecoverFile error = %v, want ErrCorrupt", err)
			}
			if !report.Corrupt || report.Truncated {
				t.Fatalf("report = %+v", report)
			}
			if report.Records != tc.wantRecords {
				t.Fatalf("records = %d (%v), want %d", report.Records, records, tc.wantRecords)
			}
			if info, statErr := os.Stat(path); statErr != nil || info.Size() != int64(len(data)) {
				t.Fatalf("refused recovery changed the file: %v", statErr)
			}

			report, err = RecoverFile(path, true, func(payload []byte) error {
				records = append(records, string(payload))
				return nil
			})
			if err != nil {
				t.Fatalf("repairing RecoverFile: %v", err)
			}
			if !report.Truncated || !report.Corrupt || report.Records != tc.wantRecords {
				t.Fatalf("repair report = %+v", report)
			}
			if info, statErr := os.Stat(path); statErr != nil || info.Size() != report.ValidBytes {
				t.Fatalf("repair did not truncate: %v", statErr)
			}
		})
	}
}

func TestRecoverApplyErrorLeavesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apply.aof")
	writeFrames(t, path, []byte("A"), []byte("B"))
	wantErr := errors.New("boom")
	report, err := RecoverFile(path, false, func([]byte) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("RecoverFile error = %v, want %v", err, wantErr)
	}
	if report.Truncated {
		t.Fatalf("apply error truncated the file: %+v", report)
	}
}

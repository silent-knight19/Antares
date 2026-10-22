package persist

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzRecoverFile feeds arbitrary bytes to the AOF reader. Recovery must never
// panic, must never silently keep a corrupt suffix, and truncating to the
// reported valid prefix must make the next recovery clean and identical.
func FuzzRecoverFile(f *testing.F) {
	valid := AppendFrame(nil, EncodeCommand([]byte("SET"), []byte("k"), []byte("v")))
	badCRC := append([]byte(nil), valid...)
	badCRC[4] ^= 0xff
	seeds := [][]byte{
		nil,
		valid,
		append(append([]byte(nil), valid...), AppendFrame(nil, EncodeCommand([]byte("DEL"), []byte("k")))...),
		valid[:4],
		valid[:10],
		badCRC,
		{0, 0, 0, 0, 0, 0, 0, 0},
		{0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0},
		append(append([]byte(nil), valid...), "garbage"...),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}

		var payloads [][]byte
		report, err := recoverFrames(bytes.NewReader(data), int64(len(data)), func(p []byte) error {
			payloads = append(payloads, append([]byte(nil), p...))
			return nil
		}, false)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("recover: %v", err)
			}
			if !report.Corrupt {
				t.Fatalf("ErrCorrupt without a corrupt report: %#v", report)
			}
		} else if report.Corrupt {
			t.Fatalf("corrupt report without an error: %#v", report)
		} else if report.Truncated {
			checkPrefix(t, data, report, payloads)
		} else {
			checkRecovery(t, data, report, payloads)
		}

		// Repair accepts the same prefix and reports the discarded tail; the
		// truncated file must then recover cleanly with the same payloads.
		var repairedPayloads [][]byte
		repaired, err := recoverFrames(bytes.NewReader(data), int64(len(data)), func(p []byte) error {
			repairedPayloads = append(repairedPayloads, append([]byte(nil), p...))
			return nil
		}, true)
		if err != nil {
			t.Fatalf("recover with repair: %v", err)
		}
		if repaired.Records != report.Records || repaired.ValidBytes != report.ValidBytes {
			t.Fatalf("repair changed the valid prefix: %#v vs %#v", repaired, report)
		}
		if report.Truncated || report.Corrupt {
			if !repaired.Truncated || repaired.TruncatedBytes <= 0 {
				t.Fatalf("repair did not report the discarded tail: %#v", repaired)
			}
		} else if repaired.Truncated || repaired.Corrupt {
			t.Fatalf("repair invented damage on a clean file: %#v", repaired)
		}
		if len(repairedPayloads) != len(payloads) {
			t.Fatalf("repair payload count %d != %d", len(repairedPayloads), len(payloads))
		}
		for i := range payloads {
			if !bytes.Equal(payloads[i], repairedPayloads[i]) {
				t.Fatalf("payload %d changed during repair", i)
			}
		}

		truncated := data[:repaired.ValidBytes]
		var cleanPayloads [][]byte
		clean, err := recoverFrames(bytes.NewReader(truncated), int64(len(truncated)), func(p []byte) error {
			cleanPayloads = append(cleanPayloads, append([]byte(nil), p...))
			return nil
		}, false)
		if err != nil || clean.Truncated || clean.Corrupt {
			t.Fatalf("truncated file is not clean: %#v, %v", clean, err)
		}
		if clean.Records != report.Records || clean.ValidBytes != report.ValidBytes {
			t.Fatalf("clean pass changed state: %#v vs %#v", clean, report)
		}
		if len(cleanPayloads) != len(payloads) {
			t.Fatalf("clean pass payload count %d != %d", len(cleanPayloads), len(payloads))
		}
		for i := range payloads {
			if !bytes.Equal(payloads[i], cleanPayloads[i]) {
				t.Fatalf("payload %d changed after truncation", i)
			}
		}
	})
}

func checkRecovery(t *testing.T, data []byte, report RecoveryReport, payloads [][]byte) {
	t.Helper()
	checkPrefix(t, data, report, payloads)
	if report.ValidBytes != int64(len(data)) {
		t.Fatalf("clean recovery consumed %d of %d bytes", report.ValidBytes, len(data))
	}
}

func checkPrefix(t *testing.T, data []byte, report RecoveryReport, payloads [][]byte) {
	t.Helper()
	if report.Records != len(payloads) {
		t.Fatalf("records %d != applied payloads %d", report.Records, len(payloads))
	}
	var sum int64
	for _, p := range payloads {
		sum += FrameHeaderSize + int64(len(p))
	}
	if sum != report.ValidBytes {
		t.Fatalf("payload bytes %d != valid bytes %d", sum, report.ValidBytes)
	}
	if report.Truncated {
		if report.Reason == "" || report.TruncatedBytes <= 0 {
			t.Fatalf("truncated report is incomplete: %#v", report)
		}
		if report.ValidBytes+report.TruncatedBytes != int64(len(data)) {
			t.Fatalf("valid %d + truncated %d != input %d", report.ValidBytes, report.TruncatedBytes, len(data))
		}
	}
}

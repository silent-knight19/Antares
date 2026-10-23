package persist

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// ErrCorrupt reports a frame that is malformed or fails its checksum. It is
// distinct from a torn tail: the bytes are not a prefix of a real write.
var ErrCorrupt = errors.New("persist: corrupt AOF frame")

// RecoveryReport describes the valid prefix recovered from an AOF. When
// Truncated is true, the bad tail (TruncatedBytes) was removed from the file.
// Corrupt marks a non-tail failure (bad length or checksum).
type RecoveryReport struct {
	Records        int
	ValidBytes     int64
	Truncated      bool
	TruncatedBytes int64
	Reason         string
	Corrupt        bool
}

// RecoverFile replays complete, checksum-valid frames in order through apply.
// An incomplete final frame (a torn write) is truncated, matching the torn-tail
// policy in ADR-004. A corrupt frame — bad length or checksum — is not a torn
// tail; recovery refuses it with ErrCorrupt unless repair is true, in which case
// the corrupt tail is discarded. A missing file is an empty log, not an error.
func RecoverFile(path string, repair bool, apply func(payload []byte) error) (RecoveryReport, error) {
	var report RecoveryReport
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return report, err
	}
	report, err = recoverFrames(bufio.NewReaderSize(f, 64<<10), info.Size(), apply, repair)
	if err != nil {
		f.Close()
		return report, err
	}
	if err := f.Close(); err != nil {
		return report, err
	}
	if !report.Truncated {
		return report, nil
	}
	if err := os.Truncate(path, report.ValidBytes); err != nil {
		return report, err
	}
	syncDir(path)
	return report, nil
}

// RecoverReader replays valid frames in order from r through apply.
// If size <= 0, the input is treated as a stream that ends at io.EOF.
func RecoverReader(r io.Reader, size int64, apply func(payload []byte) error) (RecoveryReport, error) {
	return recoverFrames(r, size, apply, false)
}

// recoverFrames is the in-memory recovery core. size bounds the payload
// allocation: a header claiming more bytes than the input holds is a torn
// write, not a reason to reserve a gigabyte. When size <= 0, input is streamed.
func recoverFrames(r io.Reader, size int64, apply func(payload []byte) error, repair bool) (RecoveryReport, error) {
	var report RecoveryReport
	var offset int64
	for {
		var header [FrameHeaderSize]byte
		_, err := io.ReadFull(r, header[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			report.Reason = "partial frame header"
			break
		}
		if err != nil {
			return report, err
		}

		n := binary.LittleEndian.Uint32(header[0:4])
		if n == 0 || n > MaxFramePayload {
			report.Reason = "invalid frame length"
			report.Corrupt = true
			break
		}
		if size > 0 && int64(n) > size-offset-FrameHeaderSize {
			report.Reason = "partial frame payload"
			break
		}
		var payload []byte
		if size > 0 || n <= 64<<10 {
			payload = make([]byte, int(n))
			if _, err := io.ReadFull(r, payload); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					report.Reason = "partial frame payload"
					break
				}
				return report, err
			}
		} else {
			buf := bytes.NewBuffer(make([]byte, 0, 64<<10))
			lr := io.LimitReader(r, int64(n))
			copied, err := io.Copy(buf, lr)
			if err != nil {
				return report, err
			}
			if copied < int64(n) {
				report.Reason = "partial frame payload"
				break
			}
			payload = buf.Bytes()
		}
		if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(header[4:8]) {
			report.Reason = "frame checksum mismatch"
			report.Corrupt = true
			break
		}
		if err := apply(payload); err != nil {
			return report, fmt.Errorf("persist: replay at byte %d: %w", offset, err)
		}
		report.Records++
		offset += FrameHeaderSize + int64(n)
		report.ValidBytes = offset
	}

	if report.Reason == "" {
		return report, nil
	}
	if report.Corrupt && !repair {
		return report, fmt.Errorf("persist: %s at byte %d (repair discards the tail): %w", report.Reason, offset, ErrCorrupt)
	}
	report.Truncated = true
	if size > offset {
		report.TruncatedBytes = size - offset
	} else {
		report.TruncatedBytes = 0
	}
	return report, nil
}

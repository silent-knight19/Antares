package persist

import (
	"bufio"
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
	br := bufio.NewReaderSize(f, 64<<10)
	var offset int64
	for {
		var header [FrameHeaderSize]byte
		_, err := io.ReadFull(br, header[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			report.Reason = "partial frame header"
			break
		}
		if err != nil {
			f.Close()
			return report, err
		}

		n := binary.LittleEndian.Uint32(header[0:4])
		if n == 0 || n > MaxFramePayload {
			report.Reason = "invalid frame length"
			report.Corrupt = true
			break
		}
		payload := make([]byte, int(n))
		if _, err := io.ReadFull(br, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				report.Reason = "partial frame payload"
				break
			}
			f.Close()
			return report, err
		}
		if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(header[4:8]) {
			report.Reason = "frame checksum mismatch"
			report.Corrupt = true
			break
		}
		if err := apply(payload); err != nil {
			f.Close()
			return report, fmt.Errorf("persist: replay at byte %d: %w", offset, err)
		}
		report.Records++
		offset += FrameHeaderSize + int64(n)
		report.ValidBytes = offset
	}

	if report.Reason == "" {
		if err := f.Close(); err != nil {
			return report, err
		}
		return report, nil
	}

	if report.Corrupt && !repair {
		f.Close()
		return report, fmt.Errorf("persist: %s at byte %d (repair discards the tail): %w", report.Reason, offset, ErrCorrupt)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return report, err
	}
	report.Truncated = true
	report.TruncatedBytes = info.Size() - offset
	if err := f.Close(); err != nil {
		return report, err
	}
	if err := os.Truncate(path, offset); err != nil {
		return report, err
	}
	syncDir(path)
	return report, nil
}

package persist

import (
	"encoding/binary"
	"hash/crc32"
	"strconv"
)

const (
	// FrameHeaderSize is the 4-byte little-endian length plus the 4-byte
	// little-endian IEEE CRC32 that precede every RESP command payload.
	FrameHeaderSize = 8

	// MaxFramePayload keeps a single AOF record below 1 GiB. Strings are
	// already capped at 512 MiB, so this only rejects corrupt headers.
	MaxFramePayload = 1 << 30
)

// AppendFrame appends one framed payload to dst and returns the new slice.
func AppendFrame(dst, payload []byte) []byte {
	var header [FrameHeaderSize]byte
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[4:8], crc32.ChecksumIEEE(payload))
	dst = append(dst, header[:]...)
	return append(dst, payload...)
}

func intDigits(n int) int {
	if n < 10 {
		return 1
	}
	if n < 100 {
		return 2
	}
	if n < 1000 {
		return 3
	}
	if n < 10000 {
		return 4
	}
	if n < 100000 {
		return 5
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

// EncodeCommand renders a Redis command as a RESP2 array of bulk strings.
// It is the payload format shared by AOF propagation and snapshot writes.
func EncodeCommand(args ...[]byte) []byte {
	size := 1 + intDigits(len(args)) + 2
	for _, arg := range args {
		size += 1 + intDigits(len(arg)) + 2 + len(arg) + 2
	}
	buf := make([]byte, 0, size)
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(len(args)), 10)
	buf = append(buf, '\r', '\n')
	for _, arg := range args {
		buf = append(buf, '$')
		buf = strconv.AppendInt(buf, int64(len(arg)), 10)
		buf = append(buf, '\r', '\n')
		buf = append(buf, arg...)
		buf = append(buf, '\r', '\n')
	}
	return buf
}

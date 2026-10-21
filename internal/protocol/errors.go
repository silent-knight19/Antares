package protocol

import "strings"

// Error-reply bodies in Redis's exact wording, pinned by string-equality
// tests against captures from the digest-pinned Redis 7.4.11 (PROJECT_LOG
// P1.5). Encoder.Error adds the leading '-' and the CRLF.

// argsCap is Redis's 128-byte budget for the quoted-args group in the
// unknown-command error (processCommand, server.c).
const argsCap = 128

// ProtocolErrorReply returns the wire body for a ProtocolError with Msg,
// e.g. "ERR Protocol error: invalid multibulk length".
func ProtocolErrorReply(msg string) string { return "ERR Protocol error: " + msg }

// SyntaxError is Redis's generic command-option rejection.
const SyntaxError = "ERR syntax error"

// NotInteger is Redis's string2ll failure reply for command arguments.
const NotInteger = "ERR value is not an integer or out of range"

// NotFloat is Redis's string2ld failure reply for command arguments.
const NotFloat = "ERR value is not a valid float"

// IncrementOverflow is Redis's integer INCR/DECR overflow reply.
const IncrementOverflow = "ERR increment or decrement would overflow"

// DecrementOverflow is Redis's DECRBY reply when the decrement cannot be
// negated.
const DecrementOverflow = "ERR decrement would overflow"

// FloatResult is Redis's INCRBYFLOAT reply when the result is NaN or infinite.
const FloatResult = "ERR increment would produce NaN or Infinity"

// StringTooLong is Redis's APPEND reply when the value would exceed
// proto-max-bulk-len (512 MiB by default).
const StringTooLong = "ERR string exceeds maximum allowed size (proto-max-bulk-len)"

// WrongType is Redis's reply when a command meets a key of another kind.
const WrongType = "WRONGTYPE Operation against a key holding the wrong kind of value"

// List pop count errors. Redis parses LPOP/RPOP counts with a positive-only
// helper, while LREM accepts negatives but rejects the int64 minimum.
const (
	CountPositive = "ERR value is out of range, must be positive"
	CountRange    = "ERR value is out of range, value must between -9223372036854775807 and 9223372036854775807"
)

// RandomCountTooLarge is used when HRANDFIELD/SRANDMEMBER would build more
// than the server's bounded random-reply size.
const RandomCountTooLarge = "ERR count is out of range"

// OutOfMemory is Redis's reply for a command that may grow memory (denyoom)
// when maxmemory is set and the keyspace is already over the limit.
const OutOfMemory = "OOM command not allowed when used memory > 'maxmemory'."

// List LSET replies.
const (
	NoSuchKey       = "ERR no such key"
	IndexOutOfRange = "ERR index out of range"
)

// InvalidExpireTime returns Redis's per-command expiry rejection, e.g.
// "ERR invalid expire time in 'set' command".
func InvalidExpireTime(command string) string {
	return "ERR invalid expire time in '" + command + "' command"
}

// WrongArity returns Redis's arity error; name is the canonical lowercase
// command name.
func WrongArity(name string) string {
	return "ERR wrong number of arguments for '" + name + "' command"
}

// UnknownCommand returns Redis's unknown-command error. The name and the
// quoted-args group are each cut at 128 bytes (captured: a 200-byte name
// yields a 128-byte name), and an argument is cut to fit the budget left in
// the group.
func UnknownCommand(name string, args ...[]byte) string {
	if len(name) > argsCap {
		name = name[:argsCap]
	}
	var sb strings.Builder
	for _, a := range args {
		if sb.Len() >= argsCap {
			break
		}
		if budget := argsCap - sb.Len(); len(a) > budget {
			a = a[:budget]
		}
		sb.WriteByte('\'')
		sb.Write(a)
		sb.WriteString("' ")
	}
	return "ERR unknown command '" + name + "', with args beginning with: " + sb.String()
}

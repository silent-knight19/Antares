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

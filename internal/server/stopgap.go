package server

import (
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
)

// StopgapHandler answers PING/ECHO with capture-exact replies and errors so
// the differential smoke and tests can run against real clients; the command
// executor replaces it at P2.2. It lives here (not in package main) so tests
// and the differential harness can start a full in-process server.
func StopgapHandler(req protocol.Request, out *protocol.Encoder) {
	switch strings.ToUpper(string(req[0])) {
	case "PING":
		switch len(req) {
		case 1:
			out.Simple("PONG")
		case 2:
			out.Bulk(req[1])
		default:
			out.Error(protocol.WrongArity("ping"))
		}
	case "ECHO":
		if len(req) == 2 {
			out.Bulk(req[1])
		} else {
			out.Error(protocol.WrongArity("echo"))
		}
	default:
		out.Error(protocol.UnknownCommand(string(req[0]), req[1:]...))
	}
}

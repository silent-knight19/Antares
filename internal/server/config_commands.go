package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
)

const (
	minHz    = 1
	maxHz    = 500
	maxHzArg = 1<<31 - 1
)

func registerConfigCommands(reg *Registry, e *Executor) {
	reg.Register(Command{Name: "config", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		configCommand(e, args, out)
	}})
}

func configCommand(e *Executor, args [][]byte, out *protocol.Encoder) {
	switch strings.ToUpper(string(args[0])) {
	case "GET":
		if len(args) != 2 {
			out.Error("ERR wrong number of arguments for 'config|get' command")
			return
		}
		if strings.EqualFold(string(args[1]), "hz") {
			out.ArrayHeader(2)
			out.Bulk([]byte("hz"))
			out.Bulk([]byte(strconv.Itoa(e.hz)))
			return
		}
		out.ArrayHeader(0)
	case "SET":
		if len(args) < 3 {
			out.Error("ERR wrong number of arguments for 'config|set' command")
			return
		}
		if len(args)%2 == 0 {
			out.Error(protocol.SyntaxError)
			return
		}
		hz := e.hz
		for i := 1; i < len(args); i += 2 {
			name := string(args[i])
			if !strings.EqualFold(name, "hz") {
				out.Error(fmt.Sprintf("ERR Unknown option or number of arguments for CONFIG SET - '%s'", name))
				return
			}
			n, ok := protocol.ParseInt(args[i+1])
			if !ok {
				out.Error("ERR CONFIG SET failed (possibly related to argument 'hz') - argument couldn't be parsed into an integer")
				return
			}
			if n < 0 || n > maxHzArg {
				out.Error("ERR CONFIG SET failed (possibly related to argument 'hz') - argument must be between 0 and 2147483647 inclusive")
				return
			}
			hz = int(min(max(n, minHz), maxHz))
		}
		e.setHz(hz)
		out.Simple("OK")
	default:
		out.Error(fmt.Sprintf("ERR unknown subcommand '%s'. Try CONFIG HELP.", string(args[0])))
	}
}

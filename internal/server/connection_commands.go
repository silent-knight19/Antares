package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
)

func registerConnectionCommands(reg *Registry, e *Executor) {
	reg.Register(Command{Name: "hello", Arity: -1, Handler: func(args [][]byte, out *protocol.Encoder) {
		helloCommand(args, out)
	}})
	reg.Register(Command{Name: "client", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		clientCommand(e, args, out)
	}})
}

func helloCommand(args [][]byte, out *protocol.Encoder) {
	version := int64(2)
	if len(args) > 0 {
		n, ok := parseHelloVersion(args[0])
		if !ok {
			out.Error("ERR Protocol version is not an integer or out of range")
			return
		}
		if n != 2 {
			out.Error("NOPROTO unsupported protocol version")
			return
		}
	}

	for i := 1; i < len(args); i++ {
		option := string(args[i])
		switch strings.ToUpper(option) {
		case "SETNAME":
			if i+1 >= len(args) {
				out.Error(fmt.Sprintf("ERR Syntax error in HELLO option '%s'", option))
				return
			}
			if !validClientString(args[i+1]) {
				out.Error("ERR Client names cannot contain spaces, newlines or special characters.")
				return
			}
			i++
		case "AUTH":
			if i+2 >= len(args) {
				out.Error(fmt.Sprintf("ERR Syntax error in HELLO option '%s'", option))
				return
			}
			out.Error("WRONGPASS invalid username-password pair or user is disabled.")
			return
		default:
			out.Error(fmt.Sprintf("ERR Syntax error in HELLO option '%s'", option))
			return
		}
	}

	out.ArrayHeader(14)
	out.Bulk([]byte("server"))
	out.Bulk([]byte("redis"))
	out.Bulk([]byte("version"))
	out.Bulk([]byte(redisVersion))
	out.Bulk([]byte("proto"))
	out.Int(version)
	out.Bulk([]byte("id"))
	out.Int(1)
	out.Bulk([]byte("mode"))
	out.Bulk([]byte("standalone"))
	out.Bulk([]byte("role"))
	out.Bulk([]byte("master"))
	out.Bulk([]byte("modules"))
	out.ArrayHeader(0)
}

func parseHelloVersion(b []byte) (int64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	start := 0
	if b[0] == '-' {
		start = 1
		if len(b) == 1 {
			return 0, false
		}
	}
	if b[start] == '0' && len(b)-start > 1 {
		return 0, false
	}
	for _, c := range b[start:] {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	return n, err == nil
}

func clientCommand(e *Executor, args [][]byte, out *protocol.Encoder) {
	sub := strings.ToUpper(string(args[0]))
	switch sub {
	case "ID":
		if len(args) != 1 {
			clientArity("id", out)
			return
		}
		out.Int(1)
	case "GETNAME":
		if len(args) != 1 {
			clientArity("getname", out)
			return
		}
		out.Null()
	case "SETNAME":
		if len(args) != 2 {
			clientArity("setname", out)
			return
		}
		if !validClientString(args[1]) {
			out.Error("ERR Client names cannot contain spaces, newlines or special characters.")
			return
		}
		out.Simple("OK")
	case "SETINFO":
		if len(args) != 3 {
			clientArity("setinfo", out)
			return
		}
		attr := string(args[1])
		if !strings.EqualFold(attr, "lib-name") && !strings.EqualFold(attr, "lib-ver") {
			out.Error(fmt.Sprintf("ERR Unrecognized option '%s'", attr))
			return
		}
		if !validClientString(args[2]) {
			out.Error(fmt.Sprintf("ERR %s cannot contain spaces, newlines or special characters.", strings.ToLower(attr)))
			return
		}
		out.Simple("OK")
	case "INFO":
		if len(args) != 1 {
			clientArity("info", out)
			return
		}
		out.Bulk([]byte(clientInfoLine(e, "client|info")))
	case "LIST":
		if len(args) != 1 {
			clientArity("list", out)
			return
		}
		out.Bulk([]byte(clientInfoLine(e, "client|list") + "\n"))
	case "GETREDIR":
		if len(args) != 1 {
			clientArity("getredir", out)
			return
		}
		out.Int(-1)
	case "NO-EVICT", "NO-TOUCH":
		if len(args) != 2 {
			clientArity(strings.ToLower(sub), out)
			return
		}
		if !strings.EqualFold(string(args[1]), "on") && !strings.EqualFold(string(args[1]), "off") {
			out.Error(protocol.SyntaxError)
			return
		}
		out.Simple("OK")
	case "UNPAUSE":
		if len(args) != 1 {
			clientArity("unpause", out)
			return
		}
		out.Simple("OK")
	default:
		out.Error(fmt.Sprintf("ERR unknown subcommand '%s'. Try CLIENT HELP.", string(args[0])))
	}
}

func clientArity(name string, out *protocol.Encoder) {
	out.Error(fmt.Sprintf("ERR wrong number of arguments for 'client|%s' command", name))
}

func validClientString(b []byte) bool {
	for _, c := range b {
		if c < '!' || c > '~' {
			return false
		}
	}
	return true
}

func clientInfoLine(e *Executor, command string) string {
	return fmt.Sprintf("id=1 addr=127.0.0.1:0 laddr=%s fd=-1 name= age=0 idle=0 flags=N db=0 sub=0 psub=0 ssub=0 multi=-1 watch=0 qbuf=0 qbuf-free=0 argv-mem=0 multi-mem=0 rbs=0 rbp=0 obl=0 oll=0 omem=0 tot-mem=0 events=r cmd=%s user=default redir=-1 resp=2 lib-name= lib-ver=", e.stats.Addr(), command)
}

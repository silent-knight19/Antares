package server

import (
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
)

// CommandHandler executes one command's arguments (the command name is
// excluded) and writes its reply to out.
type CommandHandler func(args [][]byte, out *protocol.Encoder)

// Command is one registry entry. Arity follows Redis: positive means an exact
// argument count including the name; negative means at least -Arity.
type Command struct {
	Name    string
	Arity   int
	Write   bool
	Handler CommandHandler
}

// Registry maps case-insensitive command names to handlers. It is built once
// before serving and is safe for concurrent reads.
type Registry struct {
	commands map[string]Command
}

// NewRegistry returns a registry with the P2.1 built-in commands: PING, ECHO,
// and the COMMAND stub redis-cli uses for interactive hints.
func NewRegistry() *Registry {
	r := &Registry{commands: make(map[string]Command)}
	r.Register(Command{Name: "ping", Arity: -1, Handler: pingCommand})
	r.Register(Command{Name: "echo", Arity: 2, Handler: echoCommand})
	r.Register(Command{Name: "command", Arity: -1, Handler: commandStub})
	return r
}

// Register adds cmd under its lowercased name.
func (r *Registry) Register(cmd Command) {
	if cmd.Name == "" || cmd.Handler == nil {
		panic("server: invalid command registration")
	}
	cmd.Name = strings.ToLower(cmd.Name)
	r.commands[cmd.Name] = cmd
}

// Lookup returns the command registered under name.
func (r *Registry) Lookup(name []byte) (Command, bool) {
	cmd, ok := r.commands[strings.ToLower(string(name))]
	return cmd, ok
}

// Dispatch looks up the command, checks arity, and runs its handler. Unknown
// commands and arity failures reply with the captured Redis wording.
func (r *Registry) Dispatch(req protocol.Request, out *protocol.Encoder) {
	cmd, ok := r.Lookup(req[0])
	if !ok {
		out.Error(protocol.UnknownCommand(string(req[0]), req[1:]...))
		return
	}
	if !cmd.accepts(len(req)) {
		out.Error(protocol.WrongArity(cmd.Name))
		return
	}
	cmd.Handler(req[1:], out)
}

func (cmd Command) accepts(argc int) bool {
	if cmd.Arity >= 0 {
		return argc == cmd.Arity
	}
	return argc >= -cmd.Arity
}

func pingCommand(args [][]byte, out *protocol.Encoder) {
	switch len(args) {
	case 0:
		out.Simple("PONG")
	case 1:
		out.Bulk(args[0])
	default:
		out.Error(protocol.WrongArity("ping"))
	}
}

func echoCommand(args [][]byte, out *protocol.Encoder) {
	out.Bulk(args[0])
}

// commandStub answers every COMMAND form with an empty array: enough for
// redis-cli's interactive hint request, not full introspection.
func commandStub(_ [][]byte, out *protocol.Encoder) {
	out.ArrayHeader(0)
}

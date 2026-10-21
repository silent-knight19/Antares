package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
)

const (
	minHz    = 1
	maxHz    = 500
	maxHzArg = 1<<31 - 1

	maxMemoryPolicyError = "ERR CONFIG SET failed (possibly related to argument 'maxmemory-policy') - argument(s) must be one of the following: volatile-lru, volatile-lfu, volatile-random, volatile-ttl, allkeys-lru, allkeys-lfu, allkeys-random, noeviction"
)

var memoryPolicies = map[string]bool{
	"volatile-lru": true, "volatile-lfu": true, "volatile-random": true,
	"volatile-ttl": true, "allkeys-lru": true, "allkeys-lfu": true,
	"allkeys-random": true, "noeviction": true,
}

type configItem struct {
	name  string
	value string
}

func registerConfigCommands(reg *Registry, e *Executor) {
	reg.Register(Command{Name: "config", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		configCommand(e, args, out)
	}})
}

func configCommand(e *Executor, args [][]byte, out *protocol.Encoder) {
	switch strings.ToUpper(string(args[0])) {
	case "GET":
		if len(args) < 2 {
			out.Error("ERR wrong number of arguments for 'config|get' command")
			return
		}
		items := e.configItems()
		seen := make(map[string]bool)
		matched := make([]configItem, 0)
		for _, pattern := range args[1:] {
			p := strings.ToLower(string(pattern))
			for _, item := range items {
				if seen[item.name] {
					continue
				}
				if strings.EqualFold(p, item.name) || globMatch([]byte(p), []byte(item.name)) {
					seen[item.name] = true
					matched = append(matched, item)
				}
			}
		}
		out.ArrayHeader(len(matched) * 2)
		for _, item := range matched {
			out.Bulk([]byte(item.name))
			out.Bulk([]byte(item.value))
		}
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
		maxmemory := e.maxmemory
		policy := e.maxmemoryPolicy
		maxclients := e.stats.MaxClients.Load()
		for i := 1; i < len(args); i += 2 {
			name := string(args[i])
			switch strings.ToLower(name) {
			case "hz":
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
			case "maxmemory":
				n, ok := parseMemory(args[i+1])
				if !ok {
					out.Error("ERR CONFIG SET failed (possibly related to argument 'maxmemory') - argument must be a memory value")
					return
				}
				maxmemory = n
			case "maxmemory-policy":
				p := strings.ToLower(string(args[i+1]))
				if !memoryPolicies[p] {
					out.Error(maxMemoryPolicyError)
					return
				}
				policy = p
			case "maxclients":
				n, ok := protocol.ParseInt(args[i+1])
				if !ok {
					out.Error("ERR CONFIG SET failed (possibly related to argument 'maxclients') - argument couldn't be parsed into an integer")
					return
				}
				if n < 1 || n > math.MaxUint32 {
					out.Error("ERR CONFIG SET failed (possibly related to argument 'maxclients') - argument must be between 1 and 4294967295 inclusive")
					return
				}
				maxclients = n
			default:
				out.Error(fmt.Sprintf("ERR Unknown option or number of arguments for CONFIG SET - '%s'", name))
				return
			}
		}
		e.setHz(hz)
		e.maxmemory = maxmemory
		e.maxmemoryPolicy = policy
		e.stats.MaxClients.Store(maxclients)
		out.Simple("OK")
	default:
		out.Error(fmt.Sprintf("ERR unknown subcommand '%s'. Try CONFIG HELP.", string(args[0])))
	}
}

func (e *Executor) configItems() []configItem {
	return []configItem{
		{"hz", strconv.Itoa(e.hz)},
		{"maxmemory", strconv.FormatInt(e.maxmemory, 10)},
		{"maxmemory-policy", e.maxmemoryPolicy},
		{"maxclients", strconv.FormatInt(e.stats.MaxClients.Load(), 10)},
		{"proto-max-bulk-len", strconv.Itoa(protocol.MaxBulkLen)},
		{"client-query-buffer-limit", strconv.Itoa(protocol.MaxRequestBytes)},
		{"databases", "16"},
		{"appendonly", e.appendonlyValue()},
		{"appendfsync", e.appendfsyncValue()},
		{"save", "3600 1 300 100 60 10000"},
	}
}

func parseMemory(b []byte) (int64, bool) {
	s := string(b)
	if s == "" || s[0] == '-' {
		return 0, false
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	var mul uint64
	switch strings.ToLower(s[i:]) {
	case "", "b":
		mul = 1
	case "k":
		mul = 1000
	case "kb":
		mul = 1024
	case "m":
		mul = 1000 * 1000
	case "mb":
		mul = 1024 * 1024
	case "g":
		mul = 1000 * 1000 * 1000
	case "gb":
		mul = 1024 * 1024 * 1024
	default:
		return 0, false
	}
	n, err := strconv.ParseUint(s[:i], 10, 64)
	if err != nil || n > math.MaxInt64/mul {
		return 0, false
	}
	return int64(n * mul), true
}

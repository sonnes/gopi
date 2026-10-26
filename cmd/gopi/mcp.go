package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/mcp"
	"github.com/sonnes/gopi/pkg/tool"
)

// mcpFile is the format of an mcp.json file, as Claude Code reads it.
type mcpFile struct {
	Servers map[string]mcpServer `json:"mcpServers"`
}

// mcpServer is one server of an mcp.json file. Type is "stdio" or
// "http". The default is "stdio".
type mcpServer struct {
	Type string `json:"type"`

	// Command, Args, and Env start a stdio server. Env adds to the
	// environment of gopi.
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`

	// URL and Headers reach an HTTP server.
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// loadMCP connects to each server of the mcp.json file at path and
// returns their tools, in the order of the names. The values of the
// file expand ${VAR} from environ, and a stdio server gets environ as
// its environment. [mcp.Connect] gives the names of the tools.
//
// If a server does not connect, or has a tool name of an earlier
// server, loadMCP writes a warning to warn and continues without its
// tools. A file that gopi cannot read is an error. The close function
// closes each session.
func loadMCP(
	ctx context.Context,
	path string,
	environ []string,
	warn io.Writer,
) ([]tool.Tool, func() error, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var f mcpFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}

	getenv := envLookup(environ)
	var servers []agent.MCPServer
	for _, name := range slices.Sorted(maps.Keys(f.Servers)) {
		servers = append(servers, expandServer(name, f.Servers[name], getenv))
	}

	tools, closeMCP, err := mcp.Connect(ctx, servers, environ)
	for _, e := range joined(err) {
		fmt.Fprintf(warn, "gopi: %v. gopi continues without its tools\n", e)
	}
	return tools, closeMCP, nil
}

// joined returns the errors that err joins, or err alone.
func joined(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// expandServer returns srv as the server name, with ${VAR} expanded in
// each string.
func expandServer(name string, srv mcpServer, getenv func(string) string) agent.MCPServer {
	expand := func(s string) string { return expandEnv(s, getenv) }
	expandMap := func(m map[string]string) map[string]string {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = expand(v)
		}
		return out
	}

	args := make([]string, len(srv.Args))
	for i, a := range srv.Args {
		args[i] = expand(a)
	}
	return agent.MCPServer{
		Name:    name,
		Type:    srv.Type,
		Command: expand(srv.Command),
		Args:    args,
		Env:     expandMap(srv.Env),
		URL:     expand(srv.URL),
		Headers: expandMap(srv.Headers),
	}
}

// envVar matches ${VAR}. A $VAR without braces stays as it is, so a
// value can hold a literal dollar sign.
var envVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// envLookup returns a function that reads a variable from environ, a
// list of "KEY=value" entries. A later entry wins, as in [exec.Cmd].
func envLookup(environ []string) func(string) string {
	vars := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	return func(k string) string { return vars[k] }
}

// expandEnv replaces each ${VAR} in s with getenv(VAR). A variable that
// is not set becomes empty.
func expandEnv(s string, getenv func(string) string) string {
	return envVar.ReplaceAllStringFunc(s, func(m string) string {
		return getenv(m[2 : len(m)-1])
	})
}

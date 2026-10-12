package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sonnes/gopi/pkg/mcp"
	"github.com/sonnes/gopi/pkg/tool"
)

// mcpConnectTimeout limits the connect and the tool list of one MCP
// server, so a server that does not answer does not stop gopi.
const mcpConnectTimeout = 30 * time.Second

// mcpStderrTail is the number of bytes of stderr that gopi keeps for
// each stdio server. A failed connect shows them in its warning.
const mcpStderrTail = 4 << 10

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
// returns their tools, in the order of the names. gopi connects to the
// servers at the same time. Each tool is deferred, and its name is
// "mcp__<server>__<tool>". The values of the file expand ${VAR} from
// environ, and a stdio server gets environ as its environment.
//
// If a server does not connect, loadMCP writes a warning to warn and
// continues without its tools. If a tool of a server has the same name
// as a tool of a server before it, loadMCP also drops the later
// server with a warning. A file that gopi cannot read is an error. The
// close function closes each session.
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

	type result struct {
		session *mcpsdk.ClientSession
		tools   []tool.Tool
		err     error
	}
	names := slices.Sorted(maps.Keys(f.Servers))
	results := make([]result, len(names))
	getenv := envLookup(environ)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			srv := expandServer(f.Servers[name], getenv)
			session, tools, err := connectMCP(ctx, name, srv, environ)
			results[i] = result{
				session: session,
				tools:   tools,
				err:     err,
			}
		})
	}
	wg.Wait()

	var (
		tools    []tool.Tool
		sessions []*mcpsdk.ClientSession
		// owner maps each tool name to the name of its server.
		owner = make(map[string]string)
	)
	for i, r := range results {
		name := names[i]
		if r.err != nil {
			fmt.Fprintf(warn, "gopi: mcp server %q: %v. gopi continues without its tools\n", name, r.err)
			continue
		}
		if other, ok := sameName(r.tools, owner); ok {
			fmt.Fprintf(warn, "gopi: mcp server %q: a tool has the same name as a tool of %q. gopi continues without its tools\n", name, other)
			_ = r.session.Close()
			continue
		}
		for _, t := range r.tools {
			owner[t.Info().Name] = name
		}
		sessions = append(sessions, r.session)
		tools = append(tools, r.tools...)
	}

	closeAll := func() error {
		var errs []error
		for _, s := range sessions {
			errs = append(errs, s.Close())
		}
		return errors.Join(errs...)
	}
	return tools, closeAll, nil
}

// sameName returns the server in owner of the first tool of tools that
// owner holds. The agent rejects two tools with one name. Two server
// names, such as "a.b" and "a_b", can give the same tool name.
func sameName(tools []tool.Tool, owner map[string]string) (string, bool) {
	for _, t := range tools {
		if other, ok := owner[t.Info().Name]; ok {
			return other, true
		}
	}
	return "", false
}

// connectMCP connects to the server srv and lists its tools. If the
// list fails, connectMCP closes the session. If a stdio server does
// not connect, the error holds the end of its stderr.
func connectMCP(
	ctx context.Context,
	name string,
	srv mcpServer,
	environ []string,
) (*mcpsdk.ClientSession, []tool.Tool, error) {
	transport, stderr, err := mcpTransport(srv, environ)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "gopi"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, withStderr(err, stderr)
	}

	tools, err := mcp.Tools(
		ctx,
		session,
		mcp.Prefix("mcp__"+name+"__"),
		mcp.Deferred(),
	)
	if err != nil {
		_ = session.Close()
		return nil, nil, withStderr(err, stderr)
	}
	return session, tools, nil
}

// withStderr adds the stderr of a stdio server to err. stderr is nil
// for an HTTP server.
func withStderr(err error, stderr *tailBuffer) error {
	if stderr == nil {
		return err
	}
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return err
	}
	return fmt.Errorf("%w. stderr:\n%s", err, text)
}

// mcpTransport returns the transport of the type of srv. For a stdio
// server, it also returns the buffer that keeps the end of its stderr.
func mcpTransport(srv mcpServer, environ []string) (mcpsdk.Transport, *tailBuffer, error) {
	switch srv.Type {
	case "", "stdio":
		stderr := &tailBuffer{max: mcpStderrTail}
		cmd := exec.Command(srv.Command, srv.Args...)
		cmd.Env = slices.Clone(environ)
		for k, v := range srv.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = stderr
		return &mcpsdk.CommandTransport{Command: cmd}, stderr, nil
	case "http":
		client := &http.Client{
			Transport: headerTransport{
				headers: srv.Headers,
				base:    http.DefaultTransport,
			},
		}
		transport := &mcpsdk.StreamableClientTransport{
			Endpoint:   srv.URL,
			HTTPClient: client,
			// gopi ignores the messages that a server starts, such
			// as a change of its tools, so it opens no stream for them.
			DisableStandaloneSSE: true,
		}
		return transport, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown type %q: use stdio or http", srv.Type)
	}
}

// tailBuffer keeps the last max bytes written to it. The process of a
// stdio server writes to it while gopi reads it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if extra := len(b.buf) - b.max; extra > 0 {
		b.buf = slices.Delete(b.buf, 0, extra)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// headerTransport sets headers on each request.
type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}

// expandServer returns srv with ${VAR} expanded in each string.
func expandServer(srv mcpServer, getenv func(string) string) mcpServer {
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
	return mcpServer{
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

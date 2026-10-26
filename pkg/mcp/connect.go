package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/tool"
)

// connectTimeout limits the connect and the tool list of one server, so
// a server that does not answer does not stop the caller.
const connectTimeout = 30 * time.Second

// stderrTail is the number of bytes of stderr that Connect keeps for
// each stdio server. The error of a failed connect shows them.
const stderrTail = 4 << 10

// Connect connects to each server of servers at the same time, and
// returns their tools in the order of servers. Each tool is deferred,
// and its name is "mcp__<server>__<tool>". A stdio server gets environ
// as its environment, then its own Env. The close function closes each
// session.
//
// Connect returns the tools and the close function of each server that
// connected, also when the error is not nil. The error joins one error
// for each server that did not connect, and for each server with a
// tool name that an earlier server has. Connect drops such a server.
// So a caller can continue without a server that is down, or stop.
func Connect(
	ctx context.Context,
	servers []agent.MCPServer,
	environ []string,
) ([]tool.Tool, func() error, error) {
	type result struct {
		session *mcpsdk.ClientSession
		tools   []tool.Tool
		err     error
	}
	results := make([]result, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Go(func() {
			session, tools, err := connect(ctx, srv, environ)
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
		errs     []error
		// owner maps each tool name to the name of its server.
		owner = make(map[string]string)
	)
	for i, r := range results {
		name := servers[i].Name
		if r.err != nil {
			errs = append(errs, fmt.Errorf("mcp server %q: %w", name, r.err))
			continue
		}
		if other, ok := sameName(r.tools, owner); ok {
			errs = append(errs, fmt.Errorf("mcp server %q: a tool has the same name as a tool of %q", name, other))
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
	return tools, closeAll, errors.Join(errs...)
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

// connect connects to the server srv and lists its tools. If the list
// fails, connect closes the session. If a stdio server does not
// connect, the error holds the end of its stderr.
func connect(
	ctx context.Context,
	srv agent.MCPServer,
	environ []string,
) (*mcpsdk.ClientSession, []tool.Tool, error) {
	if srv.Name == "" {
		return nil, nil, errors.New("a name is required")
	}
	transport, stderr, err := newTransport(srv, environ)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "gopi"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, withStderr(err, stderr)
	}

	tools, err := Tools(
		ctx,
		session,
		Prefix("mcp__"+srv.Name+"__"),
		Deferred(),
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

// newTransport returns the transport of the type of srv. For a stdio
// server, it also returns the buffer that keeps the end of its stderr.
func newTransport(srv agent.MCPServer, environ []string) (mcpsdk.Transport, *tailBuffer, error) {
	switch srv.Type {
	case "", agent.MCPStdio:
		stderr := &tailBuffer{max: stderrTail}
		cmd := exec.Command(srv.Command, srv.Args...)
		cmd.Env = slices.Clone(environ)
		for k, v := range srv.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = stderr
		return &mcpsdk.CommandTransport{Command: cmd}, stderr, nil
	case agent.MCPHTTP:
		client := &http.Client{
			Transport: headerTransport{
				headers: srv.Headers,
				base:    http.DefaultTransport,
			},
		}
		transport := &mcpsdk.StreamableClientTransport{
			Endpoint:   srv.URL,
			HTTPClient: client,
			// The tools are fixed after Connect, so a message that a
			// server starts, such as a change of its tools, has no
			// reader. Connect opens no stream for them.
			DisableStandaloneSSE: true,
		}
		return transport, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown type %q: use %s or %s", srv.Type, agent.MCPStdio, agent.MCPHTTP)
	}
}

// tailBuffer keeps the last max bytes written to it. The process of a
// stdio server writes to it while Connect reads it.
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

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/ai"
)

// inboxSize is the number of messages that the reader holds for the
// run before it waits.
const inboxSize = 64

// errExited is the error of a run whose process stops.
var errExited = errors.New("codex: the app server exited")

// start starts cmd. Tests replace it to replay a trace.
var start = cliproc.Start

// exitErr returns the error for a process whose stdout ended, with the
// read error or the end of its stderr.
func (c *conn) exitErr() error {
	if err := c.p.ReadErr(); err != nil {
		return fmt.Errorf("%w: read the output: %w", errExited, err)
	}
	text := c.p.Stderr()
	if text == "" {
		return errExited
	}
	return fmt.Errorf("%w: %s", errExited, text)
}

// conn is a JSON-RPC connection to one app server. One reader
// goroutine reads the lines of stdout. It answers the requests of the
// server and sends every other message to inbox, in order. The run
// reads inbox.
type conn struct {
	p *cliproc.Proc

	// mu guards writes to stdin and nextID.
	mu     sync.Mutex
	nextID int64

	// inbox carries responses and notifications. The reader closes it
	// when stdout ends.
	inbox chan message
	// exited is closed when stdout ends.
	exited chan struct{}
	// done is closed by close and kill, so that the reader stops.
	done     chan struct{}
	doneOnce sync.Once

	// approve answers the approval requests, or is nil.
	approve agent.ApproveFunc
	// ctx ends when the reader stops. The approve function gets it.
	ctx    context.Context
	cancel context.CancelFunc

	// itemsMu guards items.
	itemsMu sync.Mutex
	// items holds the tool call of each tool item that started and did
	// not complete, by item ID. An approval request names its item, so
	// the approve function gets the same call as the log.
	items map[string]ai.ToolCall

	// hooks serves the hooks of the agent for the process, or is nil.
	hooks *hookServer
}

// bindHooks gives the hooks of the process the context of a run, or
// nil after the run.
func (c *conn) bindHooks(ctx context.Context) {
	if c.hooks != nil {
		c.hooks.bind(ctx)
	}
}

// hookErr returns the error of a hook that stopped the turn, and
// clears it.
func (c *conn) hookErr() error {
	if c.hooks == nil {
		return nil
	}
	return c.hooks.takeErr()
}

// dial starts the reader of p.
func dial(p cliproc.Pipes, approve agent.ApproveFunc) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		p:       cliproc.Open(p),
		approve: approve,
		ctx:     ctx,
		cancel:  cancel,
		inbox:   make(chan message, inboxSize),
		exited:  make(chan struct{}),
		done:    make(chan struct{}),
		items:   make(map[string]ai.ToolCall),
	}
	go c.read()
	return c
}

// read reads the lines of stdout until stdout ends.
func (c *conn) read() {
	defer close(c.exited)
	defer close(c.inbox)
	for data := range c.p.Lines() {
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		isRequest := m.Method != "" && len(m.ID) > 0
		if isRequest {
			c.answer(m)
			continue
		}
		c.track(m)
		select {
		case c.inbox <- m:
		case <-c.done:
			return
		}
	}
}

// track keeps the tool call of a tool item from item/started until
// item/completed. If item/started comes before the approval request of
// the item, the call is there when approve needs it.
func (c *conn) track(m message) {
	started := m.Method == methodItemStarted
	completed := m.Method == methodItemCompleted
	if !started && !completed {
		return
	}
	var p params
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	var it item
	if err := json.Unmarshal(p.Item, &it); err != nil {
		return
	}
	c.itemsMu.Lock()
	defer c.itemsMu.Unlock()
	if completed {
		delete(c.items, it.ID)
		return
	}
	call, ok := toolCall(it)
	if ok {
		c.items[it.ID] = call
	}
}

// The methods of the approval requests of the server.
const (
	methodCommandApproval    = "item/commandExecution/requestApproval"
	methodFileChangeApproval = "item/fileChange/requestApproval"
)

// answer responds to a request from the server. The approve function
// answers an approval request in its own goroutine, so that the reader
// goes on. Without it, or without the call of the request, the agent
// declines. It grants no extra permissions. Every other request gets a
// method-not-found error.
func (c *conn) answer(m message) {
	res := response{ID: m.ID}
	switch m.Method {
	case methodCommandApproval, methodFileChangeApproval:
		call, known := c.approvalCall(m)
		if c.approve != nil && known {
			go c.decide(m.ID, call)
			return
		}
		res.Result = decisionResult(decisionDecline)
	case "item/permissions/requestApproval":
		res.Result = map[string]any{
			"permissions": map[string]any{},
			"scope":       "turn",
		}
	default:
		res.Error = &rpcError{
			Code:    errMethodNotFound,
			Message: "codex: gopi does not handle " + m.Method,
		}
	}
	// A failed write means that the process is gone. The reader sees
	// the end of stdout next.
	_ = c.write(res)
}

// approvalCall returns the tool call that the approval request m asks
// about: the call of its item, or for a command, a call from the
// parameters of m if the item is not known. A file change request has
// no changes, so approvalCall reports false for a file change with an
// unknown item.
func (c *conn) approvalCall(m message) (ai.ToolCall, bool) {
	var p approvalParams
	_ = json.Unmarshal(m.Params, &p)

	c.itemsMu.Lock()
	call, ok := c.items[p.ItemID]
	c.itemsMu.Unlock()
	if ok {
		return call, true
	}
	if m.Method == methodFileChangeApproval {
		return ai.ToolCall{}, false
	}
	return toolCall(item{
		Type:    itemCommandExecution,
		ID:      p.ItemID,
		Command: p.Command,
	})
}

// decide sends the decision of the approve function on call, as the
// response to the request id. If the function fails, or the decision
// changes the input, the agent declines.
func (c *conn) decide(id json.RawMessage, call ai.ToolCall) {
	d, err := c.approve(c.ctx, call)
	declined := err != nil || !d.Allow || d.Input != nil
	decision := decisionAccept
	switch {
	case declined:
		decision = decisionDecline
	case d.Always:
		decision = decisionAcceptForSession
	}
	_ = c.write(response{
		ID:     id,
		Result: decisionResult(decision),
	})
}

// The decisions of an approval response.
const (
	decisionDecline          = "decline"
	decisionAccept           = "accept"
	decisionAcceptForSession = "acceptForSession"
)

// decisionResult returns the result of an approval response.
func decisionResult(decision string) map[string]string {
	return map[string]string{"decision": decision}
}

// write sends one line to the process.
func (c *conn) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.mu.Lock()
	err = c.p.Write(data)
	c.mu.Unlock()
	if err != nil {
		// A process that rejects its flags exits at once, so the first
		// write can fail before stdout ends. Stderr says why.
		return c.exitErr()
	}
	return nil
}

// request sends a request and returns its ID. It does not wait for the
// response.
func (c *conn) request(method string, params any) (int64, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	err := c.write(request{
		ID:     id,
		Method: method,
		Params: params,
	})
	return id, err
}

// call sends a request and waits for its response. It drops the
// notifications that arrive first.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	id, err := c.request(method, params)
	if err != nil {
		return err
	}
	for {
		// A select picks at random when both cases are ready, so a
		// canceled ctx wins over a message that is already there.
		if err := ctx.Err(); err != nil {
			return err
		}
		var m message
		select {
		case msg, ok := <-c.inbox:
			if !ok {
				return c.exitErr()
			}
			m = msg
		case <-ctx.Done():
			return ctx.Err()
		}
		if !m.isResponse(id) {
			continue
		}
		if m.Error != nil {
			return fmt.Errorf("codex: %s: %s", method, m.Error.Message)
		}
		return json.Unmarshal(m.Result, result)
	}
}

// initialize initializes the connection.
func (c *conn) initialize(ctx context.Context) error {
	init := initializeParams{
		ClientInfo: clientInfo{
			Name:    clientName,
			Title:   clientName,
			Version: clientVersion,
		},
	}
	var initResult json.RawMessage
	if err := c.call(ctx, "initialize", init, &initResult); err != nil {
		return err
	}
	return c.write(notification{Method: "initialized"})
}

// handshake initializes the connection, then starts the thread, or
// resumes it if params has a thread ID.
func (c *conn) handshake(ctx context.Context, params threadParams) (threadResult, error) {
	var res threadResult
	if err := c.initialize(ctx); err != nil {
		return res, err
	}
	method := "thread/start"
	if params.ThreadID != "" {
		method = "thread/resume"
	}
	err := c.call(ctx, method, params, &res)
	return res, err
}

// alive reports whether stdout is still open.
func (c *conn) alive() bool {
	select {
	case <-c.exited:
		return false
	default:
		return true
	}
}

// close stops the reader and stops the process. See
// [cliproc.Proc.Stop], which reads stdout, so that the server does not
// block on a full pipe.
func (c *conn) close() error {
	c.stop()
	err := c.p.Stop()
	c.closeHooks()
	return err
}

// kill stops the process at once.
func (c *conn) kill() {
	c.stop()
	c.p.Kill()
	c.closeHooks()
}

// stop ends the reader, the ctx of the approve function, and the ctx of
// the hooks in flight.
func (c *conn) stop() {
	c.doneOnce.Do(func() {
		close(c.done)
		c.cancel()
		if c.hooks != nil {
			c.hooks.cancel()
		}
	})
}

// closeHooks waits for the hooks in flight and removes the socket.
// Call it after the process stops, so that the wait does not delay the
// stop of the process.
func (c *conn) closeHooks() {
	if c.hooks != nil {
		c.hooks.close()
	}
}

// isResponse reports whether m is the response to the request id.
func (m message) isResponse(id int64) bool {
	if m.Method != "" || len(m.ID) == 0 {
		return false
	}
	return strings.TrimSpace(string(m.ID)) == strconv.FormatInt(id, 10)
}

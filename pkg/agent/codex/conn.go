package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
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

	// approver answers the approval requests, or is nil.
	approver ApproverFunc
	// ctx ends when the reader stops. An approver gets it.
	ctx    context.Context
	cancel context.CancelFunc
}

// dial starts the reader of p.
func dial(p cliproc.Pipes, approver ApproverFunc) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		p:        cliproc.Open(p),
		approver: approver,
		ctx:      ctx,
		cancel:   cancel,
		inbox:    make(chan message, inboxSize),
		exited:   make(chan struct{}),
		done:     make(chan struct{}),
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
		select {
		case c.inbox <- m:
		case <-c.done:
			return
		}
	}
}

// The methods of the approval requests of the server.
const (
	methodCommandApproval    = "item/commandExecution/requestApproval"
	methodFileChangeApproval = "item/fileChange/requestApproval"
)

// answer responds to a request from the server. The approver answers an
// approval request in its own goroutine, so that the reader goes on.
// Without an approver, the agent declines. It grants no extra
// permissions. Every other request gets a method-not-found error.
func (c *conn) answer(m message) {
	res := response{ID: m.ID}
	switch m.Method {
	case methodCommandApproval, methodFileChangeApproval:
		if c.approver != nil {
			go c.approve(m)
			return
		}
		res.Result = decisionResult(Decline)
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

// approve sends the decision of the approver for the approval request
// m. If the approver fails, the agent declines.
func (c *conn) approve(m message) {
	var p approvalParams
	_ = json.Unmarshal(m.Params, &p)
	req := ApprovalRequest{
		Kind:    ApprovalCommand,
		ItemID:  p.ItemID,
		Command: p.Command,
		Cwd:     p.Cwd,
		Reason:  p.Reason,
	}
	if m.Method == methodFileChangeApproval {
		req.Kind = ApprovalFileChange
	}
	d, err := c.approver(c.ctx, req)
	if err != nil {
		d = Decline
	}
	_ = c.write(response{
		ID:     m.ID,
		Result: decisionResult(d),
	})
}

// decisionResult returns the result of an approval response.
func decisionResult(d Decision) map[string]string {
	if d == Decline {
		return map[string]string{"decision": "decline"}
	}
	return map[string]string{"decision": string(d)}
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

// handshake initializes the connection, then starts the thread, or
// resumes it if params has a thread ID.
func (c *conn) handshake(ctx context.Context, params threadParams) (threadResult, error) {
	var res threadResult
	init := initializeParams{
		ClientInfo: clientInfo{
			Name:    clientName,
			Title:   clientName,
			Version: clientVersion,
		},
	}
	var initResult json.RawMessage
	if err := c.call(ctx, "initialize", init, &initResult); err != nil {
		return res, err
	}
	if err := c.write(notification{Method: "initialized"}); err != nil {
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
	return c.p.Stop()
}

// kill stops the process at once.
func (c *conn) kill() {
	c.stop()
	c.p.Kill()
}

// stop ends the reader and the ctx of the approver.
func (c *conn) stop() {
	c.doneOnce.Do(func() {
		close(c.done)
		c.cancel()
	})
}

// isResponse reports whether m is the response to the request id.
func (m message) isResponse(id int64) bool {
	if m.Method != "" || len(m.ID) == 0 {
		return false
	}
	return strings.TrimSpace(string(m.ID)) == strconv.FormatInt(id, 10)
}

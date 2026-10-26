package codex

import "encoding/json"

// The types in this file are the parts of the app-server protocol that
// the agent uses. `codex app-server generate-json-schema` writes the
// full schema. The protocol is JSON-RPC 2.0 without the "jsonrpc"
// field.

// clientName and clientVersion identify the agent in initialize.
const (
	clientName    = "gopi"
	clientVersion = "0.0.0"
)

// errMethodNotFound is the JSON-RPC error code for a request that the
// agent does not handle.
const errMethodNotFound = -32601

// message is one line from the server: a response, a notification, or
// a request.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// rpcError is the error of a response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// request is one request from the agent.
type request struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// notification is one notification from the agent.
type notification struct {
	Method string `json:"method"`
}

// response is the answer of the agent to a request from the server.
type response struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type initializeParams struct {
	ClientInfo   clientInfo `json:"clientInfo"`
	Capabilities *struct{}  `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

// threadParams are the parameters of thread/start and thread/resume.
// Only thread/resume has a ThreadID.
type threadParams struct {
	ThreadID              string         `json:"threadId,omitempty"`
	Model                 string         `json:"model,omitempty"`
	Cwd                   string         `json:"cwd,omitempty"`
	ApprovalPolicy        ApprovalPolicy `json:"approvalPolicy,omitempty"`
	Sandbox               SandboxMode    `json:"sandbox,omitempty"`
	DeveloperInstructions string         `json:"developerInstructions,omitempty"`
	// Config overrides the config of the CLI for the thread.
	Config map[string]any `json:"config,omitempty"`
}

// threadResult is the result of thread/start and thread/resume.
type threadResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model string `json:"model"`
}

// injectParams are the parameters of thread/inject_items. Items are raw
// items of the Responses API that the thread appends to its history.
type injectParams struct {
	ThreadID string       `json:"threadId"`
	Items    []injectItem `json:"items"`
}

// injectItem is one message item of thread/inject_items.
type injectItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content []injectContent `json:"content"`
}

// injectContent is one content part of an [injectItem].
type injectContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	Effort   string      `json:"effort,omitempty"`
}

type userInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// URL is the data URL of an image input.
	URL string `json:"url,omitempty"`
}

// approvalParams are the parameters of an approval request.
type approvalParams struct {
	ItemID  string `json:"itemId"`
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	Reason  string `json:"reason"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// turnResult is the result of turn/start.
type turnResult struct {
	Turn turnInfo `json:"turn"`
}

type turnInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
		// Info is the kind of the error: a string such as
		// "unauthorized", or an object.
		Info json.RawMessage `json:"codexErrorInfo"`
	} `json:"error"`
}

// errorUnauthorized is the codexErrorInfo of a turn that fails because
// the CLI is not logged in.
const errorUnauthorized = `"unauthorized"`

// The status values of a turn.
const (
	turnCompleted   = "completed"
	turnFailed      = "failed"
	turnInterrupted = "interrupted"
)

// item is one thread item. Type says which fields hold a value. The
// fields with a different shape per type stay raw.
type item struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// agentMessage
	Text string `json:"text"`

	// reasoning
	Summary json.RawMessage `json:"summary"`
	Content json.RawMessage `json:"content"`

	// commandExecution, fileChange, mcpToolCall
	Status string `json:"status"`

	// commandExecution
	Command          string  `json:"command"`
	AggregatedOutput *string `json:"aggregatedOutput"`
	ExitCode         *int    `json:"exitCode"`

	// fileChange
	Changes json.RawMessage `json:"changes"`

	// mcpToolCall
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content []json.RawMessage `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`

	// webSearch
	Query string `json:"query"`

	// collabAgentToolCall, with Tool and Status. Tool is the call in
	// camel case, such as wait or sendInput.
	ReceiverThreadIDs []string        `json:"receiverThreadIds"`
	AgentsStates      json.RawMessage `json:"agentsStates"`

	// subAgentActivity. Kind is started, interacted, interrupted, or
	// completed. The item of kind started has the ID of the
	// spawn_agent call of the model.
	Kind          string  `json:"kind"`
	AgentThreadID string  `json:"agentThreadId"`
	AgentPath     string  `json:"agentPath"`
	Model         *string `json:"model"`
}

// The item types that the agent maps.
const (
	itemAgentMessage     = "agentMessage"
	itemReasoning        = "reasoning"
	itemCommandExecution = "commandExecution"
	itemFileChange       = "fileChange"
	itemMcpToolCall      = "mcpToolCall"
	itemWebSearch        = "webSearch"
	// itemCollabAgentToolCall is a call of the model to a subagent,
	// such as wait.
	itemCollabAgentToolCall = "collabAgentToolCall"
	// itemSubAgentActivity reports the life of a subagent.
	itemSubAgentActivity = "subAgentActivity"
)

// activityStarted is the kind of the subAgentActivity item of a spawn.
const activityStarted = "started"

// The status values of a tool item that mean failure.
const (
	statusFailed   = "failed"
	statusDeclined = "declined"
)

// params holds the fields of the notifications that a turn uses.
type params struct {
	// ThreadID and TurnID say where the notification belongs. The turn
	// notifications have the turn ID in Turn.
	ThreadID string    `json:"threadId"`
	TurnID   string    `json:"turnId"`
	Turn     *turnInfo `json:"turn"`

	// Item is set on item/started and item/completed. It stays raw
	// until the notification is known to belong to the turn.
	Item json.RawMessage `json:"item"`

	// ItemID and Delta are set on the delta notifications.
	ItemID string `json:"itemId"`
	Delta  string `json:"delta"`
	// SummaryIndex and ContentIndex say which part of a reasoning item
	// a delta belongs to.
	SummaryIndex int `json:"summaryIndex"`
	ContentIndex int `json:"contentIndex"`

	// TokenUsage is set on thread/tokenUsage/updated.
	TokenUsage *struct {
		Total tokenBreakdown `json:"total"`
		Last  tokenBreakdown `json:"last"`
	} `json:"tokenUsage"`
}

// turnOf returns the turn ID of the notification, or "".
func (p params) turnOf() string {
	if p.Turn != nil {
		return p.Turn.ID
	}
	return p.TurnID
}

type tokenBreakdown struct {
	InputTokens           int `json:"inputTokens"`
	CachedInputTokens     int `json:"cachedInputTokens"`
	CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
	OutputTokens          int `json:"outputTokens"`
}

// modelListParams are the parameters of model/list. The first page has
// no cursor.
type modelListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// modelListResult is one page of model/list. NextCursor is empty on the
// last page.
type modelListResult struct {
	Data       []listedModel `json:"data"`
	NextCursor string        `json:"nextCursor"`
}

// listedModel is one model of model/list.
type listedModel struct {
	ID                        string   `json:"id"`
	DisplayName               string   `json:"displayName"`
	Hidden                    bool     `json:"hidden"`
	IsDefault                 bool     `json:"isDefault"`
	InputModalities           []string `json:"inputModalities"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

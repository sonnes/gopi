package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestCallOptions(t *testing.T) {
	p := &script{responses: []ai.Message{text("ok")}}
	a := agent.New(model(p),
		agent.MaxTokens(64),
		agent.Thinking(ai.ThinkingLow),
	)

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	require.Len(t, p.opts, 1)
	opts := p.opts[0]

	maxTokens, _ := ai.GetOption(opts, ai.MaxTokens)
	assert.Equal(t, 64, maxTokens)
	thinking, _ := ai.GetOption(opts, ai.Thinking)
	assert.Equal(t, ai.ThinkingLow, thinking)
}

func TestRunIsSequential(t *testing.T) {
	gate := make(chan struct{})
	p := &script{responses: []ai.Message{text("ok")}, gate: gate}
	a := agent.New(model(p))
	ctx := context.Background()

	first := a.Run(ctx, session.Text("one"))
	for range first.Events() {
		break
	}

	_, err := a.Run(ctx, session.Text("two")).Wait()
	require.Error(t, err)

	close(gate)
	_, err = first.Wait()
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "ok"}, msgTexts(history(a)), "the refused run added nothing")
}

func TestDuplicateToolName(t *testing.T) {
	other := tool.New("echo", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	}, tool.Description("another echo"))
	p := &script{}

	assert.PanicsWithError(t, `agent: two tools are named "echo"`, func() {
		agent.New(model(p), agent.Toolset(echo, other))
	})
}

func TestClose(t *testing.T) {
	var closed []string
	errDB := errors.New("db")
	a := agent.New(model(&script{}),
		agent.OnClose(func() error {
			closed = append(closed, "first")
			return nil
		}),
		agent.OnClose(func() error {
			closed = append(closed, "second")
			return errDB
		}),
	)

	err := a.Close()
	require.ErrorIs(t, err, errDB, "Close joins the error of each function")
	assert.Equal(t, []string{"first", "second"}, closed, "Close runs each function in order")

	require.NoError(t, a.Close(), "a second Close runs nothing")
	assert.Len(t, closed, 2)
}

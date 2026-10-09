package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestCallOptions(t *testing.T) {
	p := &script{responses: []ai.Message{text("ok")}}
	a := agent.New(model(p),
		agent.MaxTokens(64),
		agent.Thinking(ai.ThinkingLow),
	)

	_, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
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

	first := a.Run(ctx, ai.UserMessage("one"))
	for range first.Events() {
		break
	}

	_, err := a.Run(ctx, ai.UserMessage("two")).Wait()
	require.Error(t, err)

	close(gate)
	_, err = first.Wait()
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "ok"}, msgTexts(history(a)), "the refused run added nothing")
}

func TestHistory(t *testing.T) {
	p := &script{responses: []ai.Message{text("two")}}
	saved := []ai.Message{
		ai.UserMessage("first"),
		text("one"),
	}
	a := agent.New(model(p), agent.History(saved...))

	_, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("second")))
	require.NoError(t, err)

	assert.Equal(t, []string{"first", "one", "second"}, msgTexts(p.received()[0]), "the model reads the history first")
	assert.Equal(t, []string{"two"}, msgTexts(msgs), "the result has no history and no input")
	assert.Equal(t, []string{"first", "one", "second", "two"}, msgTexts(history(a)))
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

package harness_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func TestSubagentGetsNoSections(t *testing.T) {
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"Review auth.go"}`),
		reply("2 problems"),
		reply("done"),
	}}
	h := taskHarness(p, reviewer)
	c := taskConfig(t, session.NewMemoryStore())
	c.Sections = func(context.Context) ([]ai.Section, error) {
		return []ai.Section{{Name: "project", Text: "Write tests in Go."}}, nil
	}

	a := start(t, h, c)
	run(t, a, "fix it")
	assert.Equal(t, "Write tests in Go.", section(a, "project"))

	child := start(t, h, childConfig(t, c, toolResult(t, a)))
	assert.Empty(t, section(child, "project"), "the file of the subagent is its prompt")
}

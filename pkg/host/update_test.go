package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
)

// otherDriver is a driver of another kind of agent, such as Codex next
// to Claude Code.
type otherDriver struct {
	*fakeDriver
}

func TestUpdateAnIdleSession(t *testing.T) {
	f := newFixture(t)
	f.cat.RegisterTextProvider("fake2", f.model, ai.Model{ID: "n"})
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)

	got, err := f.h.Update(ctx, "s1", func(c *host.Config) {
		c.Model = "fake2/n"
		c.Effort = ai.ThinkingHigh
		c.Mode = agent.ModePlan
	})

	require.NoError(t, err)
	assert.Equal(t, "fake2/n", got.Model)
	assert.Equal(t, ai.ThinkingHigh, got.Effort)
	assert.Equal(t, agent.ModePlan, got.Mode)
	assert.Equal(t, host.StatusIdle, got.Status)
	assert.Equal(t, got, next(t, events, func(host.Event) bool { return true }).Session)

	f.send(t, "s1", "b")
	runEnd(t, events)
	c := f.lastConfig(t)
	assert.Equal(t, 2, f.driver.starts(), "the next run starts the agent again")
	assert.Equal(t, "fake2/n", c.Spec)
	assert.Equal(t, ai.ThinkingHigh, c.Effort)
	assert.Equal(t, agent.ModePlan, c.Mode)
	assert.Equal(t, []string{"a", "b"}, f.model.texts(), "the agent resumes the log")
}

func TestUpdateDuringARun(t *testing.T) {
	f, gate := gated(t)
	f.driver.plain = true
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)
	f.send(t, "s1", "b")

	_, err := f.h.Update(ctx, "s1", func(c *host.Config) {
		c.Mode = agent.ModePlan
	})

	require.NoError(t, err)
	assert.Equal(t, 1, f.driver.starts(), "the run keeps its agent")
	close(gate)
	runEnd(t, events)
	runEnd(t, events)
	assert.Equal(t, 2, f.driver.starts(), "the held message runs on a new agent")
	assert.Equal(t, agent.ModePlan, f.lastConfig(t).Mode)
}

func TestUpdateSurvivesARestart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	_, err := f.h.Update(ctx, "s1", func(c *host.Config) {
		c.Mode = agent.ModePlan
	})
	require.NoError(t, err)

	f = f.restart(t)

	got, _ := f.h.Get("s1")
	assert.Equal(t, agent.ModePlan, got.Mode)
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)
	assert.Equal(t, agent.ModePlan, f.lastConfig(t).Mode)
}

func TestUpdateRefusesAChange(t *testing.T) {
	tests := []struct {
		name   string
		change func(*host.Config)
	}{
		{name: "the ID", change: func(c *host.Config) { c.ID = "s2" }},
		{name: "the folder", change: func(c *host.Config) { c.Dir = "/other" }},
		{name: "the dirs", change: func(c *host.Config) { c.Dirs = []string{"/other"} }},
		{name: "the agent file", change: func(c *host.Config) { c.Agent = "reviewer" }},
		{name: "the kind of agent", change: func(c *host.Config) { c.Model = "other/x" }},
		{name: "a bad effort", change: func(c *host.Config) { c.Effort = "huge" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.cat.RegisterDriver("other", otherDriver{f.driver}, ai.Model{ID: "x"})
			created := f.create(t, host.Config{
				ID:  "s1",
				Dir: "/tmp",
			})

			_, err := f.h.Update(context.Background(), "s1", tt.change)

			assert.ErrorIs(t, err, host.ErrInvalid)
			got, _ := f.h.Get("s1")
			assert.Equal(t, created, got, "a refused change changes nothing")
		})
	}
}

func TestUpdateAnUnknownSession(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Update(context.Background(), "s1", func(*host.Config) {})

	assert.ErrorIs(t, err, host.ErrNotFound)
}

func TestUpdateChangesTheMCPServers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := agent.MCPServer{Name: "lw", Type: agent.MCPHTTP, URL: "http://127.0.0.1:1/mcp"}
	f.create(t, host.Config{ID: "s1", MCP: []agent.MCPServer{first}})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)
	assert.Equal(t, []agent.MCPServer{first}, f.lastConfig(t).MCP, "the driver gets the servers of the config")

	second := agent.MCPServer{Name: "lw", Type: agent.MCPHTTP, URL: "http://127.0.0.1:2/mcp"}
	_, err := f.h.Update(ctx, "s1", func(c *host.Config) {
		c.MCP = []agent.MCPServer{second}
	})
	require.NoError(t, err)
	next(t, events, func(host.Event) bool { return true })

	f.send(t, "s1", "b")
	runEnd(t, events)
	assert.Equal(t, []agent.MCPServer{second}, f.lastConfig(t).MCP, "the next start reads the new servers")
}

package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestTheHostGivesItsToolListToTheDriver(t *testing.T) {
	f := newFixture(t)

	f.create(t, host.Config{ToolNames: []string{"Read"}})

	config := f.lastConfig(t)
	assert.Equal(t, fixtureTools, config.Tools, "the driver gets the full list")
	assert.Equal(t, []string{"Read"}, config.ToolNames, "the config picks a subset")
}

func TestCreateFailsOnANameOutsideTheToolList(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "browser", "---\nname: browser\ndescription: Browse.\ntools: [Read, WebFetch]\n---\nBrowse.\n")
	tests := []struct {
		name    string
		config  host.Config
		errText string
	}{
		{
			name:    "a name of the config",
			config:  host.Config{ToolNames: []string{"Read", "lookup2"}},
			errText: `host: session s1: tool: unknown tool: "lookup2" is not in the tool list; add the tool to host.Tools`,
		},
		{
			name:    "a name of the agent file",
			config:  host.Config{Dirs: []string{dir}, Agent: "browser"},
			errText: `host: session s1: agent file "browser": tool: unknown tool: "WebFetch" is not in the tool list; add the tool to host.Tools`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			config := tt.config
			config.ID = "s1"
			config.Model = "fake/m"

			_, err := f.h.Create(context.Background(), config)

			require.ErrorIs(t, err, tool.ErrUnknownTool)
			assert.EqualError(t, err, tt.errText)
			_, ok := f.h.Get("s1")
			assert.False(t, ok, "the host keeps no session")
		})
	}
}

func TestUpdateFailsOnANameOutsideTheToolList(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})

	_, err := f.h.Update(context.Background(), "s1", func(c *host.Config) {
		c.ToolNames = []string{"lookup2"}
	})

	assert.ErrorIs(t, err, tool.ErrUnknownTool)
}

func TestTheHarnessFailsOnANameOutsideTheToolList(t *testing.T) {
	cat := catalog.New()
	cat.RegisterTextProvider("fake", &fakeModel{}, ai.Model{ID: "m"})
	cat.DefaultDriver(harness.New(harness.Catalog(cat)))
	h, err := host.Open(context.Background(), cat, host.Tools(tool.Func("lookup", nil)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, h.Close()) })

	_, err = h.Create(context.Background(), host.Config{
		Model:     "fake/m",
		Dir:       t.TempDir(),
		ToolNames: []string{"lookup2"},
	})

	assert.ErrorIs(t, err, tool.ErrUnknownTool)
}

func TestOpenNeedsAToolList(t *testing.T) {
	cat := catalog.New()

	_, err := host.Open(context.Background(), cat)

	assert.ErrorIs(t, err, host.ErrInvalid)
	assert.ErrorContains(t, err, "host.Tools")
}

func TestAnEmptyToolListGivesNoTool(t *testing.T) {
	f := newFixture(t)
	h, err := host.Open(context.Background(), f.cat, host.Tools())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, h.Close()) })

	_, err = h.Create(context.Background(), host.Config{Model: "fake/m"})
	require.NoError(t, err)

	assert.Empty(t, f.lastConfig(t).Tools)
}

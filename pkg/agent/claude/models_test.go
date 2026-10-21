package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/stdiorr"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog/models"
)

func TestDriver_ListModels(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/list_models.trace")
	lister, ok := claude.Driver().(ai.ModelLister)
	require.True(t, ok)

	got, err := lister.ListModels(context.Background())

	require.NoError(t, err)
	all := []ai.ThinkingLevel{
		ai.ThinkingLow,
		ai.ThinkingMedium,
		ai.ThinkingHigh,
		ai.ThinkingXHigh,
		ai.ThinkingMax,
	}
	some := []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh, ai.ThinkingMax}
	assert.Equal(t, []ai.Model{
		{ID: "default", Name: "Default (recommended)", Reasoning: true, ThinkingLevels: all},
		{ID: "opus", Name: "Opus 5.5", Reasoning: true, ThinkingLevels: some},
		{ID: "sonnet", Name: "Sonnet 5.5", Reasoning: true, ThinkingLevels: all},
		{
			ID:             "claude-haiku-4-5-20251001",
			Name:           "Haiku 4.5",
			Reasoning:      true,
			ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff},
		},
		{ID: "claude-opus-5-5", Name: "Opus 5.5", Reasoning: true, ThinkingLevels: some},
		{ID: "claude-sonnet-5-5", Name: "Sonnet 5.5", Reasoning: true, ThinkingLevels: all},
	}, got, "each alias, then each model that an alias resolves to, named by an alias other than default")

	cmds := starts.Cmds()
	require.Len(t, cmds, 1)
	assert.Contains(t, cmds[0].Args, "--input-format", "the CLI starts as for a run")
	assert.False(t, slices.ContainsFunc(cmds[0].Args, func(arg string) bool {
		return len(arg) >= 8 && arg[:8] == "--resume"
	}), "a list resumes no session")
}

func TestDriver_ListModelsExitError(t *testing.T) {
	claude.ReplayExitError(t, errors.New("signal: terminated"), "testdata/list_models.trace")

	got, err := claude.Driver().(ai.ModelLister).ListModels(context.Background())

	require.NoError(t, err, "a list that arrived does not depend on how the CLI exits")
	assert.NotEmpty(t, got)
}

func TestDriver_ListModelsCanceled(t *testing.T) {
	claude.Replay(t, nil, "testdata/list_models_hang.trace")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := claude.Driver().(ai.ModelLister).ListModels(ctx)

	require.ErrorIs(t, err, context.Canceled)
}

func TestDriver_ListModelsTimeout(t *testing.T) {
	claude.Replay(t, nil, "testdata/list_models_hang.trace")
	claude.SetListTimeout(t, 10*time.Millisecond)

	_, err := claude.Driver().(ai.ModelLister).ListModels(context.Background())

	require.ErrorIs(t, err, context.DeadlineExceeded, "a CLI that never answers does not block the list")
}

func TestDriver_ListModelsError(t *testing.T) {
	claude.Replay(t, nil, "testdata/list_models_error.trace")

	_, err := claude.Driver().(ai.ModelLister).ListModels(context.Background())

	require.EqualError(t, err, "claude: list models: claude: initialize: not logged in")
}

// keepModels keeps only the models of the initialize response, and
// drops the account and the commands of the machine that recorded it.
func keepModels(line []byte) []byte {
	var m struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Response  struct {
				Models json.RawMessage `json:"models"`
			} `json:"response"`
		} `json:"response"`
	}
	err := json.Unmarshal(line, &m)
	isInit := err == nil && m.Type == "control_response" && m.Response.RequestID == "req_1"
	if !isInit {
		return scrubLine(line)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return out
}

// TestRecordedListModels runs the real CLI. Record it with
// `make record-cli AGENT=claude RUN=TestRecordedListModels`.
func TestRecordedListModels(t *testing.T) {
	home, _ := os.UserHomeDir()
	claude.Replay(t, []stdiorr.Scrub{
		keepModels,
		scrubDir(home, "/home/me"),
	}, "testdata/TestRecordedListModels.trace")

	got, err := claude.Driver().(ai.ModelLister).ListModels(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, "default", got[0].ID, "the CLI lists its default model first")
	for _, m := range got {
		assert.NotEmpty(t, m.Name, m.ID)
	}
}

func TestModels(t *testing.T) {
	models := claude.Models()

	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	assert.Equal(t, "default", ids[0], "the default alias comes first")
	assert.Subset(t, ids, []string{"opus", "sonnet", "haiku", "claude-opus-5-5"})

	models[0].ID = "changed"
	assert.Equal(t, "default", claude.Models()[0].ID, "each call returns a new list")
}

func TestModels_FromFile(t *testing.T) {
	assert.Equal(t, models.Of("claude-code"), claude.Models())
}

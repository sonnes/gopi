package sections_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
)

var (
	project = ai.Section{Name: "project", Text: "# project\n\nWrite tests in Go."}
	memory  = ai.Section{Name: "memory", Text: "# memory\n\nThe memory is empty."}
	noted   = ai.Section{Name: "memory", Text: "# memory\n\napi-port.md: the API port"}
)

// want returns a sections function that returns sections.
func want(sections ...ai.Section) func(context.Context) ([]ai.Section, error) {
	return func(context.Context) ([]ai.Section, error) { return sections, nil }
}

func TestForCLI(t *testing.T) {
	started := []ai.Message{
		ai.SystemMessage(project, memory),
		ai.UserMessage("Add a test."),
	}
	tests := []struct {
		name       string
		fn         func(context.Context) ([]ai.Section, error)
		msgs       []ai.Message
		wantBase   *ai.Message
		wantChange *ai.Message
	}{
		{
			name:     "an empty history gets the base",
			fn:       want(project, memory),
			wantBase: &ai.Message{Role: ai.RoleSystem, Content: []ai.Content{project, memory}},
		},
		{
			name: "the same sections need no message",
			fn:   want(project, memory),
			msgs: started,
		},
		{
			name:       "a change has only the changed section",
			fn:         want(project, noted),
			msgs:       started,
			wantChange: &ai.Message{Role: ai.RoleSystem, Content: []ai.Content{noted}},
		},
		{
			name:       "a history with no base gets each section as a change",
			fn:         want(project),
			msgs:       []ai.Message{ai.UserMessage("Add a test.")},
			wantChange: &ai.Message{Role: ai.RoleSystem, Content: []ai.Content{project}},
		},
		{
			name: "no function adds no prompt",
			msgs: started,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, change, err := sections.ForCLI(context.Background(), tt.fn, tt.msgs)

			require.NoError(t, err)
			assert.Equal(t, tt.wantBase, base)
			assert.Equal(t, tt.wantChange, change)
		})
	}
}

func TestForCLIErrors(t *testing.T) {
	failed := func(context.Context) ([]ai.Section, error) { return nil, errors.New("no project") }

	_, _, err := sections.ForCLI(context.Background(), failed, nil)
	assert.ErrorContains(t, err, "no project")

	_, _, err = sections.ForCLI(context.Background(), want(memory, noted), nil)
	assert.ErrorContains(t, err, `two sections are named "memory"`)
}

func TestText(t *testing.T) {
	history := []ai.Message{
		ai.SystemMessage(project, memory),
		ai.UserMessage("Add a test."),
		ai.SystemMessage(noted),
	}

	assert.Equal(t,
		"# project\n\nWrite tests in Go.\n\n# memory\n\nThe memory is empty.",
		sections.Text(history),
		"the text is the base, and a later change does not alter it",
	)
	assert.Empty(t, sections.Text([]ai.Message{ai.UserMessage("Hi")}))
}

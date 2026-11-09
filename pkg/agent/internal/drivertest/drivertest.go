// Package drivertest holds the cases that the driver of each CLI agent
// must pass for the fields of [agent.Config]. A driver package runs them
// with [Run]. It is a separate package, so that a driver package does
// not link testify.
package drivertest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

// sessionID is the session of each case.
const sessionID = "s1"

// Suite is the setup of one driver for the cases.
type Suite struct {
	// Driver is the driver of the cases.
	Driver agent.Driver
	// Tools are the tools of the config of each case that names no tools
	// of its own.
	Tools []tool.Tool
	// UnsupportedHook is a hook at a point that the CLI does not have.
	UnsupportedHook agent.Hook
	// SkillDir is the folder of the skills in the folder of the
	// definitions of a session.
	SkillDir string
	// WriteDefs makes r write the definitions of its config, as the start
	// of its CLI does.
	WriteDefs func(t *testing.T, r agent.Runner)
}

// Run runs every case as a subtest of t.
func Run(t *testing.T, s Suite) {
	t.Run("Rejects", s.rejects)
	t.Run("Defs", s.defs)
}

func (s Suite) rejects(t *testing.T) {
	bash := tool.Named(tool.Bash)
	tests := []struct {
		name   string
		config agent.Config
		// wantIs is the error that the error of Start wraps, or nil.
		wantIs error
		// wantText is a part of the error of Start, for an error with no
		// sentinel.
		wantText string
	}{
		{
			name:     "a skill that the Defs do not have",
			config:   agent.Config{Skills: []string{"deploy"}},
			wantText: `"deploy"`,
		},
		{
			name: "a tool of the caller that ToolNames names",
			config: agent.Config{
				Tools:     []tool.Tool{bash, tool.Func("echo", nil)},
				ToolNames: []string{"bash", "echo"},
			},
			wantIs: tool.ErrUnknownTool,
		},
		{
			name: "a name outside the tools",
			config: agent.Config{
				Tools:     []tool.Tool{bash},
				ToolNames: []string{"web_search"},
			},
			wantIs: tool.ErrUnknownTool,
		},
		{
			name:   "a hook at a point that the CLI does not have",
			config: agent.Config{Hooks: []agent.Hook{s.UnsupportedHook}},
			wantIs: errors.ErrUnsupported,
		},
		{
			name:     "a hook with no point",
			config:   agent.Config{Hooks: []agent.Hook{agent.BaseHook{}}},
			wantText: "implements no point",
		},
		{
			name:     "an unknown mode",
			config:   agent.Config{Mode: "auto"},
			wantText: `"auto"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.SessionID = sessionID
			if tt.config.Tools == nil {
				tt.config.Tools = s.Tools
			}

			_, err := s.Driver.Start(context.Background(), tt.config)

			require.Error(t, err)
			if tt.wantIs != nil {
				assert.ErrorIs(t, err, tt.wantIs)
				return
			}
			assert.NotErrorIs(t, err, errors.ErrUnsupported)
			assert.ErrorContains(t, err, tt.wantText)
		})
	}
}

func (s Suite) defs(t *testing.T) {
	inCode := def.Set{Skills: []def.Skill{{
		Name:        "deploy",
		Description: "D.",
		Body:        "Do it.",
	}}}
	tests := []struct {
		name      string
		defs      def.Source
		skills    []string
		wantSkill bool
	}{
		{
			name:      "a skill of a folder",
			defs:      storefs.Dir(skillDir(t)),
			skills:    []string{"deploy"},
			wantSkill: true,
		},
		{
			name:      "a skill in code",
			defs:      inCode,
			wantSkill: true,
		},
		{
			name: "no Defs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			folder := filepath.Join(os.TempDir(), "gopi-defs", sessionID)

			r, err := s.Driver.Start(context.Background(), agent.Config{
				SessionID: sessionID,
				Store:     session.NewMemoryStore(),
				Tools:     s.Tools,
				Defs:      tt.defs,
				Skills:    tt.skills,
			})
			require.NoError(t, err)
			s.WriteDefs(t, r)

			if !tt.wantSkill {
				assert.NoDirExists(t, folder, "a session with no definitions has no folder")
				require.NoError(t, r.Close())
				return
			}
			assert.FileExists(t, filepath.Join(folder, s.SkillDir, "deploy", "SKILL.md"))
			require.NoError(t, r.Close())
			assert.NoDirExists(t, folder, "the folder lives for the process of the CLI")
		})
	}
}

// skillDir writes a folder with the skill deploy.
func skillDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skill := filepath.Join(dir, "skills", "deploy")
	require.NoError(t, os.MkdirAll(skill, 0o755))
	text := "---\ndescription: Deploy.\n---\nDeploy it.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(text), 0o644))
	return dir
}

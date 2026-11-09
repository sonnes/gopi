package permission_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/permission"
)

func TestMerge(t *testing.T) {
	workspace := permission.Rules{
		Allow: []string{"Bash(go test *)"},
		Deny:  []string{"Read(~/.ssh/**)"},
	}
	project := permission.Rules{
		Allow: []string{"Bash(make *)"},
		Ask:   []string{"Bash(git push *)"},
	}

	got := permission.Merge(workspace, permission.Rules{}, project)

	assert.Equal(t, permission.Rules{
		Allow: []string{"Bash(go test *)", "Bash(make *)"},
		Ask:   []string{"Bash(git push *)"},
		Deny:  []string{"Read(~/.ssh/**)"},
	}, got)
	assert.Equal(t, permission.Rules{}, permission.Merge(), "no layer gives no rule")
}

func TestRulesJSON(t *testing.T) {
	data, err := json.Marshal(permission.Rules{Ask: []string{"Bash(git push *)"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ask":["Bash(git push *)"]}`, string(data))

	var r permission.Rules
	require.NoError(t, json.Unmarshal([]byte(`{"allow":["Read"],"deny":["WebFetch"]}`), &r))
	assert.Equal(t, permission.Rules{Allow: []string{"Read"}, Deny: []string{"WebFetch"}}, r)
}

func TestCheckRule(t *testing.T) {
	valid := []string{
		"Bash(go test *)",
		`Bash(rm \*.tmp)`,
		"Edit(//w/app/a.go)",
		"Read(~/work/**)",
		"WebFetch",
		"WebFetch(domain:go.dev)",
		"web_fetch(domain:go.dev)",
		"mcp__lw__tasks_create",
	}
	for _, rule := range valid {
		t.Run(rule, func(t *testing.T) {
			assert.NoError(t, permission.CheckRule(rule))
		})
	}

	invalid := []string{
		"Bash(go test",
		"WebFetch(go.dev)",
		"WebFetch(domain:)",
		"TodoWrite(x)",
	}
	for _, rule := range invalid {
		t.Run("invalid "+rule, func(t *testing.T) {
			err := permission.CheckRule(rule)
			require.Error(t, err)
			assert.Contains(t, err.Error(), rule)
		})
	}
}

func TestRulesValidate(t *testing.T) {
	r := permission.Rules{
		Allow: []string{"Bash(go test *)", "Bash(go test"},
		Deny:  []string{"TodoWrite(x)"},
	}

	err := r.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bash(go test")
	assert.Contains(t, err.Error(), "TodoWrite(x)")
	assert.NoError(t, permission.Rules{Allow: []string{"Read"}}.Validate())
}

func TestRulesCheck(t *testing.T) {
	dir := t.TempDir()
	rules := permission.Rules{
		Allow: []string{"Bash(go test *)", "Edit(/src/**)", "WebFetch(domain:go.dev)"},
		Ask:   []string{"Bash(git push *)", "Edit(/src/secret.go)"},
		Deny:  []string{"Bash(git push --force *)", "Read(//etc/**)"},
	}
	tests := []struct {
		name     string
		call     ai.ToolCall
		want     permission.Match
		wantRule string
	}{
		{
			name: "an Allow rule",
			call: bashCall(t, "go test ./..."),
			want: permission.AllowMatch,
		},
		{
			name:     "an Ask rule",
			call:     bashCall(t, "git push origin main"),
			want:     permission.AskMatch,
			wantRule: "Bash(git push *)",
		},
		{
			name:     "Deny wins over Ask",
			call:     bashCall(t, "git push --force origin"),
			want:     permission.DenyMatch,
			wantRule: "Bash(git push --force *)",
		},
		{
			name: "no rule",
			call: bashCall(t, "make build"),
			want: permission.NoMatch,
		},
		{
			name: "a / path is from the folder",
			call: pathCall(t, "edit_file", filepath.Join(dir, "src", "a.go")),
			want: permission.AllowMatch,
		},
		{
			name:     "Ask wins over Allow",
			call:     pathCall(t, "write_file", filepath.Join(dir, "src", "secret.go")),
			want:     permission.AskMatch,
			wantRule: "Edit(/src/secret.go)",
		},
		{
			name: "a file_path input of Claude Code",
			call: call(t, "edit_file", map[string]any{"file_path": filepath.Join(dir, "src", "b.go")}),
			want: permission.AllowMatch,
		},
		{
			name:     "a Deny rule on a read",
			call:     call(t, "read_file", map[string]any{"file_path": "/etc/hosts"}),
			want:     permission.DenyMatch,
			wantRule: "Read(//etc/**)",
		},
		{
			name: "a domain",
			call: call(t, "web_fetch", map[string]any{"url": "https://go.dev/doc", "prompt": "x"}),
			want: permission.AllowMatch,
		},
		{
			name: "another domain",
			call: call(t, "web_fetch", map[string]any{"url": "https://pkg.go.dev/fmt"}),
			want: permission.NoMatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rule, err := rules.Check(tt.call, dir)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantRule, rule)
		})
	}
}

func TestRulesCheckBadRule(t *testing.T) {
	rules := permission.Rules{Deny: []string{"Bash(rm"}}

	_, _, err := rules.Check(bashCall(t, "rm a"), t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bash(rm")
}

func TestDomainRuleOnABadURL(t *testing.T) {
	rules := permission.Rules{Deny: []string{"WebFetch(domain:evil.test)"}}
	bad := call(t, "web_fetch", map[string]any{"url": "::not a url"})

	got, _, err := rules.Check(bad, t.TempDir())

	require.NoError(t, err)
	assert.Equal(t, permission.DenyMatch, got, "a URL that does not parse cannot pass a Deny rule")

	allow := permission.Rules{Allow: []string{"WebFetch(domain:evil.test)"}}
	got, _, err = allow.Check(bad, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, permission.NoMatch, got, "a URL that does not parse matches no Allow rule")
}

func TestEscapedStarInACommand(t *testing.T) {
	rules := permission.Rules{Allow: []string{`Bash(rm \*.tmp)`}}

	literal, _, err := rules.Check(bashCall(t, "rm *.tmp"), t.TempDir())
	require.NoError(t, err)
	other, _, err := rules.Check(bashCall(t, "rm a.tmp"), t.TempDir())
	require.NoError(t, err)

	assert.Equal(t, permission.AllowMatch, literal, `\* is a literal *`)
	assert.Equal(t, permission.NoMatch, other)
}

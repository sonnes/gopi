package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

// testHost returns a host on the catalog of m, with the harness as the
// default driver, and with opts.
func testHost(t *testing.T, m models, opts ...host.Option) *host.Host {
	t.Helper()
	m.cat.DefaultDriver(harness.New(harness.Catalog(m.cat)))
	opts = append([]host.Option{host.Tools(harness.Coding()...)}, opts...)
	h, err := host.Open(t.Context(), m.cat, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// writeAgent writes the agent file name with the frontmatter lines
// front to agents/ of dir.
func writeAgent(t *testing.T, dir, name, front string) {
	t.Helper()
	path := filepath.Join(dir, "agents", name+".md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	text := "---\nname: " + name + "\ndescription: a test agent\n" + front + "---\nYou review.\n"
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
}

func TestNewModels(t *testing.T) {
	_, err := newModels([]catalog.Detector{
		detector("a", false),
		detector("b", false),
	})

	require.EqualError(t, err, "no provider: set A_KEY or B_KEY")
}

func TestOpenSession(t *testing.T) {
	one := ai.Model{ID: "one", MaxTokens: 100}
	two := ai.Model{ID: "two", MaxTokens: 200}
	next := ai.Model{ID: "vendor/next", Name: "Next"}

	tests := []struct {
		name      string
		opts      options
		agents    map[string]string
		detectors []catalog.Detector
		wantSpec  string
		wantModel ai.Model
		wantErr   string
	}{
		{
			name: "no spec gives the first model of the first provider",
			detectors: []catalog.Detector{
				detector("a", true, one, two),
				detector("b", true, two),
			},
			wantSpec:  "a/one",
			wantModel: one,
		},
		{
			name: "no spec skips a provider that is not found",
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name: "no spec skips a provider with no models",
			detectors: []catalog.Detector{
				detector("router", true),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name: "a full spec",
			opts: options{model: "b/two"},
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name: "a bare model ID",
			opts: options{model: "two"},
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "two",
			wantModel: two,
		},
		{
			name: "a model that the provider lists",
			opts: options{model: "router/vendor/next"},
			detectors: []catalog.Detector{
				listingDetector("router", []ai.Model{next}, nil),
			},
			wantSpec:  "router/vendor/next",
			wantModel: next,
		},
		{
			name: "a model that the provider does not list",
			opts: options{model: "router/vendor/nope"},
			detectors: []catalog.Detector{
				listingDetector("router", []ai.Model{next}, nil),
			},
			wantErr: `model "router/vendor/nope": catalog: unknown model "router/vendor/nope": provider "router" has no model "vendor/nope": list the models with "gopi models router"`,
		},
		{
			name: "a bare ID that no provider lists",
			opts: options{model: "nope"},
			detectors: []catalog.Detector{
				listingDetector("router", nil, nil),
			},
			wantErr: `model "nope": catalog: unknown model "nope": list the models with "gopi models"`,
		},
		{
			name: "a provider that gopi does not know",
			opts: options{model: "nope/one"},
			detectors: []catalog.Detector{
				detector("a", true, one),
			},
			wantErr: `model "nope/one": catalog: unknown model "nope/one": no provider "nope": use one of a`,
		},
		{
			name: "a bare ID that two providers list",
			opts: options{model: "shared"},
			detectors: []catalog.Detector{
				listingDetector("one", []ai.Model{{ID: "shared"}}, nil),
				listingDetector("two", []ai.Model{{ID: "shared"}}, nil),
			},
			wantErr: `ambiguous model "shared": use one of one/shared, two/shared`,
		},
		{
			name: "a failed list",
			opts: options{model: "router/vendor/next"},
			detectors: []catalog.Detector{
				listingDetector("router", nil, errors.New("boom")),
			},
			wantErr: "\"gopi models router\"\nboom",
		},
		{
			name: "no spec and no provider with models",
			detectors: []catalog.Detector{
				detector("router", true),
			},
			wantErr: "no default model: give a spec with --model",
		},
		{
			name: "a bare ID with a slash that a found provider serves",
			opts: options{model: "a/x"},
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("router", true, ai.Model{ID: "a/x"}),
			},
			wantSpec:  "a/x",
			wantModel: ai.Model{ID: "a/x"},
		},
		{
			name: "a spec of a provider that is not found",
			opts: options{model: "a/one"},
			detectors: []catalog.Detector{
				detector("a", false, one),
				detector("b", true, two),
			},
			wantErr: `model "a/one": provider "a" has no credential: set A_KEY`,
		},
		{
			name:   "the agent file gives the model",
			opts:   options{agent: "reviewer"},
			agents: map[string]string{"reviewer": "model: b/two\n"},
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "b/two",
			wantModel: two,
		},
		{
			name:   "the flag wins over the agent file",
			opts:   options{agent: "reviewer", model: "a/one"},
			agents: map[string]string{"reviewer": "model: b/two\n"},
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "a/one",
			wantModel: one,
		},
		{
			name:   "an agent file that inherits gets the first model",
			opts:   options{agent: "reviewer"},
			agents: map[string]string{"reviewer": "model: inherit\n"},
			detectors: []catalog.Detector{
				detector("a", true, one),
				detector("b", true, two),
			},
			wantSpec:  "a/one",
			wantModel: one,
		},
		{
			name: "an agent file that no folder has",
			opts: options{agent: "nobody"},
			detectors: []catalog.Detector{
				detector("a", true, one),
			},
			wantErr: `no agent file is named "nobody"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defsDir := t.TempDir()
			for name, front := range tt.agents {
				writeAgent(t, defsDir, name, front)
			}
			o := tt.opts
			o.root = t.TempDir()
			o.dirs = []string{defsDir}
			m, err := newModels(tt.detectors)
			require.NoError(t, err)
			h := testHost(t, m, host.Defs(defs(o)))

			sess, err := openSession(t.Context(), h, o, m)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSpec, sess.Model)
			got, err := m.cat.LanguageModel(sess.Model)
			require.NoError(t, err)
			assert.Equal(t, tt.wantModel, got.Model)
		})
	}
}

func TestOpenSession_ListsOnlyTheProviderOfASpec(t *testing.T) {
	next := ai.Model{ID: "vendor/next", Name: "Next"}
	var routerCalls, otherCalls int
	m, err := newModels([]catalog.Detector{
		countingDetector("other", nil, errors.New("401"), &otherCalls),
		countingDetector("router", []ai.Model{next}, nil, &routerCalls),
	})
	require.NoError(t, err)
	h := testHost(t, m)

	sess, err := openSession(t.Context(), h, options{model: "router/vendor/next", root: t.TempDir()}, m)

	require.NoError(t, err)
	assert.Equal(t, "router/vendor/next", sess.Model)
	assert.Equal(t, 1, routerCalls)
	assert.Equal(t, 0, otherCalls, "a full spec lists only its provider")
}

func TestOpenSession_Env(t *testing.T) {
	tests := []struct {
		name         string
		spec         string
		env          map[string]string
		wantSpec     string
		wantModel    ai.Model
		wantProvider ai.TextProvider
	}{
		{
			name:         "the Anthropic key comes first",
			env:          map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "k"},
			wantSpec:     "anthropic/" + repoFile.Of("anthropic")[0].ID,
			wantModel:    repoFile.Of("anthropic")[0],
			wantProvider: &anthropic.Provider{},
		},
		{
			name:         "the OpenAI key alone picks OpenAI",
			env:          map[string]string{"OPENAI_API_KEY": "k"},
			wantSpec:     "openai/" + repoFile.Of("openai")[0].ID,
			wantModel:    repoFile.Of("openai")[0],
			wantProvider: &openai.Provider{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := []string{
				"ANTHROPIC_API_KEY",
				"OPENAI_API_KEY",
				"OPENROUTER_API_KEY",
				"FIREWORKS_API_KEY",
			}
			for _, key := range keys {
				t.Setenv(key, tt.env[key])
			}
			m, err := newModels(env.Detectors(repoFile))
			require.NoError(t, err)
			h := testHost(t, m)

			sess, err := openSession(t.Context(), h, options{model: tt.spec, root: t.TempDir()}, m)

			require.NoError(t, err)
			assert.Equal(t, tt.wantSpec, sess.Model)
			lm, err := m.cat.LanguageModel(sess.Model)
			require.NoError(t, err)
			assert.Equal(t, tt.wantModel, lm.Model)
			assert.IsType(t, tt.wantProvider, lm.Provider)
		})
	}
}

func TestOpenSession_Resume(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	open := func(t *testing.T) (*host.Host, models) {
		t.Helper()
		m, err := newModels([]catalog.Detector{
			detector("a", true, ai.Model{ID: "one"}, ai.Model{ID: "two"}),
		})
		require.NoError(t, err)
		store, err := storefs.Sessions(dir)
		require.NoError(t, err)
		return testHost(t, m, host.Store(store)), m
	}
	first := options{
		sessionID: "ada",
		model:     "a/two",
		mode:      "acceptEdits",
		allow:     []string{"bash(go test *)"},
		root:      root,
	}
	h, m := open(t)
	_, err := openSession(t.Context(), h, first, m)
	require.NoError(t, err)
	require.NoError(t, h.Close())

	h, m = open(t)
	resumed, err := openSession(t.Context(), h, options{sessionID: "ada", root: root}, m)

	require.NoError(t, err)
	assert.Equal(t, "a/two", resumed.Model, "a resumed session keeps its model")
	assert.Equal(t, "acceptEdits", string(resumed.Mode), "a resumed session keeps its mode")

	changed, err := openSession(t.Context(), h, options{sessionID: "ada", model: "a/one", root: root}, m)
	require.NoError(t, err)
	assert.Equal(t, "a/one", changed.Model, "--model changes the model of the session")

	_, err = openSession(t.Context(), h, options{sessionID: "ada", root: t.TempDir()}, m)
	require.ErrorIs(t, err, host.ErrInvalid, "a session works in one folder")
}

// script is a text provider that answers each call with the next
// message of replies, and then with the text "done". If err is set,
// each call fails with it.
type script struct {
	mu      sync.Mutex
	replies []ai.Message
	err     error
}

func (s *script) StreamText(context.Context, ai.Model, []ai.Message, ai.Options) *ai.EventStream {
	s.mu.Lock()
	next := ai.AssistantMessage(ai.Text{Text: "done"})
	next.StopReason = ai.StopReasonStop
	if len(s.replies) > 0 {
		next = s.replies[0]
		s.replies = s.replies[1:]
	}
	err := s.err
	s.mu.Unlock()

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		if err != nil {
			return nil, err
		}
		push(ai.Event{Type: ai.EventStart})
		return &next, nil
	})
}

// writeCall returns an assistant message that calls write_file.
func writeCall(id string) ai.Message {
	m := ai.AssistantMessage(ai.ToolCall{
		ID:    id,
		Name:  "write_file",
		Input: []byte(`{"path":"notes.txt","content":"hi"}`),
	})
	m.StopReason = ai.StopReasonToolUse
	return m
}

// scriptModels returns the models of a catalog with the provider fake
// of p and its model "test".
func scriptModels(t *testing.T, p ai.TextProvider) models {
	t.Helper()
	m, err := newModels([]catalog.Detector{{
		Provider: "fake",
		Source:   "FAKE_KEY",
		Models:   []ai.Model{{ID: "test"}},
		Detect:   func() (ai.TextProvider, bool) { return p, true },
	}})
	require.NoError(t, err)
	return m
}

// lines collects the events that a run prints.
type lines struct {
	mu     sync.Mutex
	events []agent.Event
}

func (l *lines) print(e agent.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func TestRunPrompt_KeepsTheGrantOfAlways(t *testing.T) {
	p := &script{replies: []ai.Message{
		writeCall("call_1"),
		ai.AssistantMessage(ai.Text{Text: "wrote it"}),
		writeCall("call_2"),
	}}
	m := scriptModels(t, p)
	h := testHost(t, m)
	sess, err := openSession(t.Context(), h, options{root: t.TempDir()}, m)
	require.NoError(t, err)
	var asked []agent.ApprovalRequest
	approve := func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		asked = append(asked, req)
		return agent.Decision{Allow: true, Always: true}, nil
	}

	require.NoError(t, runPrompt(t.Context(), h, sess.ID, &lines{}, approve, m, "write the notes"))
	require.NoError(t, runPrompt(t.Context(), h, sess.ID, &lines{}, approve, m, "again"))

	require.Len(t, asked, 1, "the grant of Always allows the second call")
	assert.NotEmpty(t, asked[0].Rule)
	log, err := h.Log(t.Context(), sess.ID)
	require.NoError(t, err)
	grants := session.Value[[]string]{Namespace: "gopi.permission", Key: "grants"}
	kept, _, err := grants.Get(log)
	require.NoError(t, err)
	assert.Equal(t, []string{asked[0].Rule}, kept, "the session keeps the grant")
}

func TestRunPrompt_DeniesACallWithNoAnswer(t *testing.T) {
	p := &script{replies: []ai.Message{writeCall("call_1")}}
	m := scriptModels(t, p)
	h := testHost(t, m)
	root := t.TempDir()
	sess, err := openSession(t.Context(), h, options{root: root}, m)
	require.NoError(t, err)
	approve := func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
		return agent.Decision{}, errors.New("no answer: the input ended")
	}

	err = runPrompt(t.Context(), h, sess.ID, &lines{}, approve, m, "write the notes")

	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(root, "notes.txt"))
}

func TestRunPrompt_ReturnsTheErrorOfARun(t *testing.T) {
	p := &script{err: errors.New("boom")}
	m := scriptModels(t, p)
	h := testHost(t, m)
	sess, err := openSession(t.Context(), h, options{root: t.TempDir()}, m)
	require.NoError(t, err)
	out := &lines{}

	err = runPrompt(t.Context(), h, sess.ID, out, nil, m, "hi")

	require.ErrorIs(t, err, errShown)
	require.ErrorContains(t, err, "boom")
	require.NotEmpty(t, out.events)
	assert.IsType(t, agent.EventAgentEnd{}, out.events[len(out.events)-1])
}

func TestNewTools(t *testing.T) {
	tests := []struct {
		name    string
		names   []string
		want    []string
		wantErr error
	}{
		{
			name: "no list gives every tool",
			want: []string{
				"read_file",
				"write_file",
				"edit_file",
				"bash",
				"grep",
				"find",
				"todo_write",
				"ask_user_question",
			},
		},
		{
			name:  "a list picks tools",
			names: []string{"grep", "read_file"},
			want:  []string{"read_file", "grep"},
		},
		{
			name:  "an empty list gives no tool",
			names: []string{},
		},
		{
			name:    "an unknown tool",
			names:   []string{"read_file", "nope"},
			wantErr: tool.ErrUnknownTool,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, err := newTools(tt.names, terminal{})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			var got []string
			for _, tl := range tools {
				got = append(got, tl.Info().Name)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolNames(t *testing.T) {
	assert.Equal(t, []string{"grep", "read_file"}, toolNames(" grep, read_file,"))
	assert.Equal(t, []string{}, toolNames(""), "an empty value gives no tool")
}

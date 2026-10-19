package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/harness/subagent"
)

// agentExt is the extension of an agent file.
const agentExt = ".md"

var (
	errNoName        = errors.New("the agent has no name")
	errNoAgentDetail = errors.New("the agent has no description")
)

// AgentDir reads subagents from the Markdown files under one directory
// of an fs.FS. [Agents] returns one.
type AgentDir struct {
	fsys fs.FS
	dir  string
}

// Agents returns a reader that reads one subagent from each *.md file
// under dir, at any depth, as Claude Code does:
//
//	dir/<file>.md         one agent
//	dir/<team>/<file>.md  one agent
//
// A file needs a name and a description in its frontmatter. A file
// without them is not an agent, such as a README.
func Agents(fsys fs.FS, dir string) AgentDir {
	return AgentDir{
		fsys: fsys,
		dir:  dir,
	}
}

// ReadAgents reads each agent file in path order. It skips a malformed
// file. It keeps each agent, also when two files give the same name.
// The caller picks the agent that wins.
func (d AgentDir) ReadAgents(ctx context.Context) ([]subagent.Agent, error) {
	names, err := d.agentFiles()
	if err != nil {
		return nil, err
	}

	var out []subagent.Agent
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		data, err := fs.ReadFile(d.fsys, name)
		if err != nil {
			return nil, fmt.Errorf("files: read %s: %w", name, err)
		}
		a, err := parseAgent(data)
		if err != nil {
			continue
		}
		a.Path = name
		out = append(out, a)
	}
	return out, nil
}

// agentFiles returns the path of each *.md file under dir, sorted. A
// dir that does not exist has no files.
func (d AgentDir) agentFiles() ([]string, error) {
	var names []string
	walk := func(name string, e fs.DirEntry, err error) error {
		isMissingRoot := name == d.dir && errors.Is(err, fs.ErrNotExist)
		if isMissingRoot {
			return fs.SkipAll
		}
		if err != nil {
			return fmt.Errorf("files: read %s: %w", name, err)
		}
		if !e.IsDir() && path.Ext(name) == agentExt {
			names = append(names, name)
		}
		return nil
	}
	if err := fs.WalkDir(d.fsys, d.dir, walk); err != nil {
		return nil, err
	}

	slices.Sort(names)
	return names, nil
}

// parseAgent reads an agent file.
func parseAgent(data []byte) (subagent.Agent, error) {
	f, prompt, err := parseFile(data)
	if err != nil {
		return subagent.Agent{}, err
	}

	name, err := f.text("name")
	if err != nil {
		return subagent.Agent{}, err
	}
	if name == "" {
		return subagent.Agent{}, errNoName
	}
	description, err := f.text("description")
	if err != nil {
		return subagent.Agent{}, err
	}
	if description == "" {
		return subagent.Agent{}, errNoAgentDetail
	}
	description = strings.ReplaceAll(description, `\n`, "\n")

	tools, err := agentTools(f)
	if err != nil {
		return subagent.Agent{}, err
	}
	disallowed, err := optionalList(f, "disallowedTools")
	if err != nil {
		return subagent.Agent{}, err
	}
	skills, err := optionalList(f, "skills")
	if err != nil {
		return subagent.Agent{}, err
	}
	model, err := f.text("model")
	if err != nil {
		return subagent.Agent{}, err
	}
	mode, err := f.text("permissionMode")
	if err != nil {
		return subagent.Agent{}, err
	}
	maxTurns := f.count("maxTurns")

	return subagent.Agent{
		Name:        name,
		Description: description,
		Tools:       tools,
		Disallowed:  disallowed,
		Model:       model,
		Mode:        mode,
		MaxTurns:    maxTurns,
		Skills:      skills,
		Prompt:      prompt,
		Meta:        f.meta(),
	}, nil
}

// agentTools reads the "tools" key. A missing key and "*" alone give
// nil: all the tools of the parent. A key with no value gives an empty
// list that is not nil: no tools.
func agentTools(f fields) ([]string, error) {
	tools, ok, err := f.list("tools")
	if err != nil || !ok {
		return nil, err
	}
	isAll := len(tools) == 1 && tools[0] == star
	if isAll {
		return nil, nil
	}
	return tools, nil
}

// optionalList reads a list key. A missing key and an empty list both
// give nil.
func optionalList(f fields, key string) ([]string, error) {
	list, _, err := f.list(key)
	if len(list) == 0 {
		return nil, err
	}
	return list, err
}

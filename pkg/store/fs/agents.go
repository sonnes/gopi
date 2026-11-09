package fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
)

// agentExt is the extension of an agent file.
const agentExt = ".md"

var (
	errNoName        = errors.New("the agent has no name")
	errNoAgentDetail = errors.New("the agent has no description")
)

// readAgents reads one agent from each *.md file under dir, at any
// depth, in path order, as Claude Code does:
//
//	dir/<file>.md         one agent
//	dir/<team>/<file>.md  one agent
//
// A file needs a name and a description in its frontmatter. A file
// without them is not an agent, such as a README, and readAgents skips
// it. It keeps each agent, also when two files give the same name.
func readAgents(ctx context.Context, fsys fs.FS, dir string) ([]def.Agent, error) {
	names, err := agentFiles(fsys, dir)
	if err != nil {
		return nil, err
	}

	var out []def.Agent
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("fs: read %s: %w", name, err)
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
func agentFiles(fsys fs.FS, dir string) ([]string, error) {
	var names []string
	walk := func(name string, e fs.DirEntry, err error) error {
		isMissingRoot := name == dir && errors.Is(err, fs.ErrNotExist)
		if isMissingRoot {
			return fs.SkipAll
		}
		if err != nil {
			return fmt.Errorf("fs: read %s: %w", name, err)
		}
		if !e.IsDir() && path.Ext(name) == agentExt {
			names = append(names, name)
		}
		return nil
	}
	if err := fs.WalkDir(fsys, dir, walk); err != nil {
		return nil, err
	}

	slices.Sort(names)
	return names, nil
}

// parseAgent reads an agent file.
func parseAgent(data []byte) (def.Agent, error) {
	f, prompt, err := parseFile(data)
	if err != nil {
		return def.Agent{}, err
	}

	name, err := f.text("name")
	if err != nil {
		return def.Agent{}, err
	}
	if name == "" {
		return def.Agent{}, errNoName
	}
	description, err := f.text("description")
	if err != nil {
		return def.Agent{}, err
	}
	if description == "" {
		return def.Agent{}, errNoAgentDetail
	}
	description = strings.ReplaceAll(description, `\n`, "\n")

	tools, err := agentTools(f)
	if err != nil {
		return def.Agent{}, err
	}
	disallowed, err := optionalList(f, "disallowedTools")
	if err != nil {
		return def.Agent{}, err
	}
	skills, err := optionalList(f, "skills")
	if err != nil {
		return def.Agent{}, err
	}
	model, err := f.text("model")
	if err != nil {
		return def.Agent{}, err
	}
	effort, err := f.text("effort")
	if err != nil {
		return def.Agent{}, err
	}
	mode, err := f.text("permissionMode")
	if err != nil {
		return def.Agent{}, err
	}
	maxTurns := f.count("maxTurns")

	return def.Agent{
		Name:        name,
		Description: description,
		Tools:       tools,
		Disallowed:  disallowed,
		Model:       model,
		Effort:      ai.ThinkingLevel(effort),
		Mode:        mode,
		MaxTurns:    maxTurns,
		Skills:      skills,
		Prompt:      prompt,
		Extra:       f.meta(),
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

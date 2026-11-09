// Package grep is the grep tool. The tool searches the content of files
// with rg.
package grep

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
	"github.com/sonnes/gopi/pkg/sandbox/internal/output"
	"github.com/sonnes/gopi/pkg/sandbox/internal/search"
	"github.com/sonnes/gopi/pkg/tool"
)

const (
	// grepDefaultLimit is the number of lines that the grep tool returns
	// when the model gives no limit.
	grepDefaultLimit = 100
	// grepMaxColumns is the length of a line, in bytes, after which the
	// grep tool cuts the line. Without the cut, one line of a minified
	// file can fill the context of the model.
	grepMaxColumns = 500
	// grepLongLine is the text that the grep tool puts after a line that
	// it cut. rg prints the same text.
	grepLongLine = " [... omitted end of long line]"
)

// grepInput is the input of the grep tool.
type grepInput struct {
	Pattern    string `json:"pattern" jsonschema:"regex pattern"`
	Path       string `json:"path,omitempty" jsonschema:"file or directory to search, default the working directory"`
	Glob       string `json:"glob,omitempty" jsonschema:"file filter, e.g. *.go"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"case-insensitive search"`
	Limit      *int   `json:"limit,omitempty" jsonschema:"max lines, default 100"`
}

// Name is the name of the tool.
const Name = tool.Grep

// description is the text that the model reads for the tool.
//
//go:embed description.md
var description string

// New returns the grep tool on box. The tool searches the content of
// files with rg, and returns each matching line as path:line:text.
func New(box sandbox.Sandbox) tool.Tool {
	return tool.New(Name, func(ctx context.Context, in grepInput) (string, error) {
		return grep(ctx, box, in)
	}, tool.Description(description))
}

func grep(ctx context.Context, box sandbox.Sandbox, in grepInput) (string, error) {
	if in.Pattern == "" {
		return "", errors.New("pattern is required")
	}
	root, err := search.NewRoot(box, in.Path)
	if err != nil {
		return "", err
	}

	args := []string{
		"--no-config",
		"--color=never",
		"--sort=path",
		"--no-heading",
		"--with-filename",
		"--line-number",
		"--max-columns=" + strconv.Itoa(grepMaxColumns),
		"--max-columns-preview",
	}
	if in.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if in.Glob != "" {
		args = append(args, "--null", "--glob", search.SkipHidden)
	}
	args = append(args, "--regexp", in.Pattern)

	res, err := root.RunRG(ctx, args)
	if err != nil {
		return "", err
	}
	if res.ExitCode == search.ExitNotFound {
		return walkGrep(ctx, box, root, in)
	}

	if in.Glob != "" {
		res.Stdout = []byte(root.FilterGlob(string(res.Stdout), in.Glob, true))
	}
	out, ok := search.Output(res, search.Limit(in.Limit, grepDefaultLimit))
	if !ok {
		return "", search.Error(res)
	}
	if out == "" {
		return "No matches found", nil
	}
	return out, nil
}

// walkGrep is the grep tool without rg. It walks the directory on this
// host, reads each file through box, and matches each line with a Go
// regular expression. Go and rg have the same regular expression
// syntax. The output has the form of the rg output. If box is a
// [host.Sizer], the walk skips a file over [maxWalkFileBytes].
func walkGrep(ctx context.Context, box sandbox.Sandbox, root search.Root, in grepInput) (string, error) {
	re, err := grepRegexp(in)
	if err != nil {
		return "", err
	}
	if in.Glob != "" && !doublestar.ValidatePattern(in.Glob) {
		return "", fmt.Errorf("invalid glob: %s", in.Glob)
	}
	files, err := root.Walk()
	if err != nil {
		return "", err
	}

	limit := search.Limit(in.Limit, grepDefaultLimit)
	var out []string
	for _, f := range files {
		if len(out) > limit || ctx.Err() != nil {
			break
		}
		if in.Glob != "" && !search.MatchGlob(in.Glob, f.Rel) {
			continue
		}
		if tooLarge(box, f.Path) {
			continue
		}
		data, err := box.ReadFile(ctx, f.Path)
		if err != nil {
			continue
		}
		out = grepFile(data, f.Path, re, out, limit)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if len(out) == 0 {
		return "No matches found", nil
	}
	return search.CapLines(strings.Join(out, "\n"), limit), nil
}

// tooLarge reports whether the file at path is over maxWalkFileBytes.
// It asks the size through box, so the rules of box apply. A file whose
// size box cannot give is not too large, and its read decides.
func tooLarge(box sandbox.Sandbox, path string) bool {
	sizer, ok := box.(host.Sizer)
	if !ok {
		return false
	}
	size, err := sizer.FileSize(path)
	return err == nil && size > maxWalkFileBytes
}

// grepFile adds each line of data that matches re to out, as
// display:line:text. It matches at most maxLineBytes of a line. It skips
// a binary file, as rg does. It stops after limit+1 lines in out, so
// that [search.CapLines] adds its note.
func grepFile(data []byte, display string, re *regexp.Regexp, out []string, limit int) []string {
	if output.IsBinary(data) {
		return out
	}
	n := 0
	for line := range bytes.Lines(data) {
		if len(out) > limit {
			break
		}
		n++
		line = bytes.TrimSuffix(line, []byte("\n"))
		line = line[:min(len(line), maxLineBytes)]
		if re.Match(line) {
			out = append(out, display+":"+strconv.Itoa(n)+":"+cutLine(string(line)))
		}
	}
	return out
}

// maxLineBytes is the most bytes of one line that grepFile matches.
const maxLineBytes = 64 * 1024

// maxWalkFileBytes is the size of the largest file that the walk
// searches. The walk reads the whole file into memory, so it skips a
// larger file, such as a log. rg has no limit.
const maxWalkFileBytes = 64 * 1024 * 1024

// cutLine cuts a line that is longer than grepMaxColumns, as rg does. It
// does not cut a UTF-8 character in two.
func cutLine(line string) string {
	if len(line) <= grepMaxColumns {
		return line
	}
	n := grepMaxColumns
	for n > 0 && !utf8.RuneStart(line[n]) {
		n--
	}
	return line[:n] + grepLongLine
}

// grepRegexp compiles the pattern of in with its options.
func grepRegexp(in grepInput) (*regexp.Regexp, error) {
	pattern := in.Pattern
	if in.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	return re, nil
}

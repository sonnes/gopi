// Package grep is the grep tool. The tool searches the content of files
// with rg.
package grep

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
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

func (in grepInput) limit() int {
	if in.Limit != nil && *in.Limit > 0 {
		return *in.Limit
	}
	return grepDefaultLimit
}

// Name is the name of the tool.
const Name = "grep"

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
	base, err := search.Base(box, in.Path)
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

	res, err := search.RunRG(ctx, box, base, args)
	if err != nil {
		return "", err
	}
	if res.ExitCode == search.ExitNotFound {
		hp, ok := box.(host.Pather)
		if !ok {
			return "", search.ErrNoRG
		}
		return walkGrep(ctx, hp, base, in)
	}

	if in.Glob != "" {
		res.Stdout = []byte(search.FilterGlob(string(res.Stdout), base, in.Glob, true))
	}
	out, ok := search.Output(res, in.limit())
	if !ok {
		return "", search.Error(res)
	}
	if out == "" {
		return "No matches found", nil
	}
	return out, nil
}

// walkGrep is the grep tool without rg. It walks the directory on this
// host and matches each line with a Go regular expression. Go and rg
// have the same regular expression syntax. The output has the form of
// the rg output.
func walkGrep(ctx context.Context, hp host.Pather, base string, in grepInput) (string, error) {
	re, err := grepRegexp(in)
	if err != nil {
		return "", err
	}
	if in.Glob != "" && !doublestar.ValidatePattern(in.Glob) {
		return "", fmt.Errorf("invalid glob: %s", in.Glob)
	}

	root, err := hp.HostPath(base)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}

	type file struct{ host, display string }
	var files []file
	if info.IsDir() {
		rels, err := search.WalkFiles(hp, root)
		if err != nil {
			return "", err
		}
		for _, rel := range rels {
			if in.Glob != "" && !search.MatchGlob(in.Glob, rel) {
				continue
			}
			files = append(files, file{
				host:    filepath.Join(root, filepath.FromSlash(rel)),
				display: path.Join(base, rel),
			})
		}
	} else {
		files = []file{{host: root, display: base}}
	}

	limit := in.limit()
	var out []string
	for _, f := range files {
		if len(out) > limit || ctx.Err() != nil {
			break
		}
		out = grepFile(f.host, f.display, re, out, limit)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if len(out) == 0 {
		return "No matches found", nil
	}
	return search.CapLines(strings.Join(out, "\n"), limit), nil
}

// grepFile adds each line of the host file that matches re to out, as
// display:line:text. It reads one line at a time, and it keeps at most
// maxLineBytes of a line, so a large file does not fill the memory. It
// skips a binary file, as rg does. It stops after limit+1 lines in out,
// so that [search.CapLines] adds its note.
func grepFile(hostPath, display string, re *regexp.Regexp, out []string, limit int) []string {
	file, err := os.Open(hostPath)
	if err != nil {
		return out
	}
	defer file.Close()

	r := bufio.NewReader(file)
	head, _ := r.Peek(binarySniff)
	if bytes.IndexByte(head, 0) >= 0 {
		return out
	}
	for n := 1; len(out) <= limit; n++ {
		line, err := readLine(r)
		if line == "" && err != nil {
			break
		}
		if re.MatchString(line) {
			out = append(out, display+":"+strconv.Itoa(n)+":"+cutLine(line))
		}
		if err != nil {
			break
		}
	}
	return out
}

// binarySniff is how many bytes grepFile reads to find a NUL, the sign
// of a binary file.
const binarySniff = 8000

// maxLineBytes is the most bytes of one line that grepFile matches. The
// rest of a longer line is read and dropped.
const maxLineBytes = 64 * 1024

// readLine returns the next line of r without its newline. It keeps at
// most maxLineBytes of the line.
func readLine(r *bufio.Reader) (string, error) {
	var b []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(b) < maxLineBytes {
			b = append(b, chunk[:min(len(chunk), maxLineBytes-len(b))]...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return strings.TrimSuffix(string(b), "\n"), err
	}
}

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

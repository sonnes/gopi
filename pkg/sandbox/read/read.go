// Package read is the read_file tool. The tool returns the text of a
// file, or an image or a PDF as an [ai.File].
package read

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/internal/host"
	"github.com/sonnes/gopi/pkg/sandbox/internal/output"
	"github.com/sonnes/gopi/pkg/tool"
)

const (
	// maxImageBytes is the size of the largest image that the tool
	// returns. A provider rejects a larger image, and the image then
	// stays in the history of the run.
	maxImageBytes = 5 * 1024 * 1024
	// maxPDFBytes is the size of the largest PDF that the tool returns.
	maxPDFBytes = 32 * 1024 * 1024
	// maxFileBytes is the size of the largest file that the tool reads.
	// The tool reads the whole file into memory, but one read returns
	// at most [output.MaxBytes] of it. For a larger file, such as a log,
	// the model reads a part with bash.
	maxFileBytes = 64 * 1024 * 1024
)

// mediaLimits are the file types that the tool returns as an
// [ai.File], with the size of the largest file of each type.
var mediaLimits = map[string]int{
	"image/png":       maxImageBytes,
	"image/jpeg":      maxImageBytes,
	"image/gif":       maxImageBytes,
	"image/webp":      maxImageBytes,
	"application/pdf": maxPDFBytes,
}

// readInput is the input of the read_file tool.
type readInput struct {
	Path   string `json:"path" jsonschema:"file path"`
	Offset *int   `json:"offset,omitempty" jsonschema:"line to start at, 1-indexed"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"max lines"`
}

// Name is the name of the tool.
const Name = tool.ReadFile

// description is the text that the model reads for the tool.
//
//go:embed description.md
var description string

// New returns the read_file tool on box. For text, the tool returns at
// most [output.MaxLines] lines and [output.MaxBytes] bytes, with a note that gives the
// offset of the next read. It finds an image or a PDF by its content,
// not by its name, and returns it as an [ai.File]. The text has no line
// numbers, so the model copies text into edit_file without a prefix to
// remove.
func New(box sandbox.Sandbox) tool.Tool {
	t := &reader{box: box}
	return tool.New(Name, t.run, tool.Description(description))
}

type reader struct {
	box sandbox.Sandbox
}

func (t *reader) run(ctx context.Context, in readInput) (ai.ToolResult, error) {
	if err := t.checkSize(in.Path); err != nil {
		return ai.ToolResult{}, err
	}
	data, err := t.box.ReadFile(ctx, in.Path)
	if err != nil {
		return ai.ToolResult{}, err
	}

	mediaType := http.DetectContentType(data)
	if limit, ok := mediaLimits[mediaType]; ok {
		return media(data, mediaType, limit)
	}
	if output.IsBinary(data) {
		return ai.ToolResult{}, fmt.Errorf("%s is a binary file: %s reads text, images, and PDF files", in.Path, Name)
	}

	text, err := readText(in, string(data))
	if err != nil {
		return ai.ToolResult{}, err
	}
	return textResult(text), nil
}

// checkSize returns an error for a file over maxFileBytes in a
// sandbox that gives the size of a file, before the tool reads the
// file.
func (t *reader) checkSize(path string) error {
	sizer, ok := t.box.(host.Sizer)
	if !ok {
		return nil
	}
	size, err := sizer.FileSize(path)
	if err != nil {
		return err
	}
	if size > maxFileBytes {
		return fmt.Errorf(
			"%s is too large: %d bytes, and the limit is %d. Read a part of it with bash, for example sed -n '1,100p' %s",
			path,
			size,
			maxFileBytes,
			output.Quote(path),
		)
	}
	return nil
}

// media returns a file of mediaType as an [ai.File], after a line that
// names the type.
func media(data []byte, mediaType string, limit int) (ai.ToolResult, error) {
	if len(data) > limit {
		return ai.ToolResult{}, fmt.Errorf("the %s file is too large: %d bytes, and the limit is %d", mediaType, len(data), limit)
	}
	return ai.ToolResult{
		Content: []ai.Content{
			ai.Text{Text: "Read " + mediaType + " file"},
			ai.File{MediaType: mediaType, Data: data},
		},
	}, nil
}

// readText returns the lines of text from the offset of in. It stops at
// the limit of in, at [output.MaxLines], or at [output.MaxBytes], and
// then adds a note with the offset of the next read.
func readText(in readInput, text string) (string, error) {
	if text == "" {
		return "(empty file)", nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	total := len(lines)

	// An offset below 1 starts at the first line, and a limit below 1
	// has no limit. The schema has no bounds, so the tool corrects them.
	start := 0
	if in.Offset != nil && *in.Offset > 1 {
		start = *in.Offset - 1
	}
	if start >= total {
		return "", fmt.Errorf("offset %d is past the end of the file (%d lines)", start+1, total)
	}

	end := total
	if in.Limit != nil && *in.Limit > 0 {
		end = min(start+*in.Limit, total)
	}

	if len(lines[start]) > output.MaxBytes {
		return fmt.Sprintf(
			"[Line %d is %d bytes, over the 50KB limit. Use bash: sed -n '%dp' %s | head -c %d. The next line is offset %d.]",
			start+1,
			len(lines[start]),
			start+1,
			output.Quote(in.Path),
			output.MaxBytes,
			start+2,
		), nil
	}

	shown, byBytes := fit(lines[start:end])
	out := strings.Join(shown, "\n")
	last := start + len(shown)

	switch {
	case last < end && byBytes:
		return out + fmt.Sprintf("\n\n[Showing lines %d-%d of %d (50KB limit). Use offset=%d to continue.]", start+1, last, total, last+1), nil
	case last < end:
		return out + fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", start+1, last, total, last+1), nil
	case last < total:
		return out + fmt.Sprintf("\n\n[%d more lines in file. Use offset=%d to continue.]", total-last, last+1), nil
	default:
		return out, nil
	}
}

// fit returns the first lines that fit in [output.MaxLines] and
// [output.MaxBytes], with a newline between two lines. byBytes is true
// if MaxBytes stopped it.
func fit(lines []string) (shown []string, byBytes bool) {
	size := 0
	for i, line := range lines {
		if i == output.MaxLines {
			return lines[:i], false
		}
		size += len(line)
		if i > 0 {
			size++
		}
		if size > output.MaxBytes {
			return lines[:i], true
		}
	}
	return lines, false
}

// textResult returns text as a result.
func textResult(text string) ai.ToolResult {
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: text}}}
}

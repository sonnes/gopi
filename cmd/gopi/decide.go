package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// decideOptions holds the flags of gopi decide.
type decideOptions struct {
	// model is the spec of a decision model.
	model string
	// json prints the response as JSON instead of a table.
	json bool
	// yesNo, choice, score, and box each hold one question per value.
	yesNo  []string
	choice []string
	score  []string
	box    []string
	// questions is the path of a JSON file with a list of ai.Question.
	questions string
	// images are the paths of the images of the input.
	images []string
}

// The columns of the answer table.
const (
	tableMinWidth = 0
	tableTabWidth = 0
	tablePadding  = 2
)

// decideCommand returns the "gopi decide" command. It asks closed
// questions about the input on one decision model, and prints one row
// for each answer.
func decideCommand() *cli.Command {
	return &cli.Command{
		Name:      "decide",
		Usage:     "ask closed questions about the input on a decision model, such as openai/gpt-6-luna or openrouter/typesafe/jev-1.14. The input is the arguments. With no arguments, it is stdin. The answers are in the order --questions, --yesno, --choice, --score, --box",
		ArgsUsage: "[input]",
		// An instruction or an option can hold a comma, so a comma must
		// not split the value of a flag.
		DisableSliceFlagSeparator: true,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "yesno",
				Usage: "a yes-or-no question, as name=instructions. The flag can repeat",
			},
			&cli.StringSliceFlag{
				Name:  "choice",
				Usage: "a choice question, as name=instructions|a|b|c. The flag can repeat",
			},
			&cli.StringSliceFlag{
				Name:  "score",
				Usage: "a score question, as name=instructions|lowest|...|highest. The flag can repeat",
			},
			&cli.StringSliceFlag{
				Name:  "box",
				Usage: "a question that finds a target in an image, as name=instructions. The flag can repeat",
			},
			&cli.StringFlag{
				Name:  "questions",
				Usage: "the path of a JSON file with a list of questions. Its questions go before the questions of the other flags",
			},
			&cli.StringSliceFlag{
				Name:  "image",
				Usage: "the path of an image of the input. The flag can repeat",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			opts := decideOptionsOf(c)
			text := strings.Join(c.Args().Slice(), " ")
			if text == "" && !isTerminal(os.Stdin) {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				text = strings.TrimSpace(string(data))
			}
			ds := homeDetectors(loadModels(ctx, c.Root().ErrWriter, os.Getenv))
			return runDecide(ctx, c.Root().Writer, ds, opts, text)
		},
	}
}

// decideOptionsOf reads the flags of gopi decide, and the root flags
// --model and --json.
func decideOptionsOf(c *cli.Command) decideOptions {
	return decideOptions{
		model:     c.String("model"),
		json:      c.Bool("json"),
		yesNo:     c.StringSlice("yesno"),
		choice:    c.StringSlice("choice"),
		score:     c.StringSlice("score"),
		box:       c.StringSlice("box"),
		questions: c.String("questions"),
		images:    c.StringSlice("image"),
	}
}

// runDecide asks the questions of o about text and the images of o, on
// the model of o, and writes the answers to out.
func runDecide(
	ctx context.Context,
	out io.Writer,
	ds []catalog.Detector,
	o decideOptions,
	text string,
) error {
	if o.model == "" {
		return errors.New("no model: set --model to a decision model, such as openai/gpt-6-luna")
	}
	questions, err := parseQuestions(o)
	if err != nil {
		return err
	}
	input, err := decideInput(text, o.images)
	if err != nil {
		return err
	}

	m, err := newModels(ds)
	if err != nil {
		return err
	}
	var dm ai.DecisionModel
	err = m.retry(ctx, func() error {
		var err error
		dm, err = m.cat.DecisionModel(o.model)
		return err
	})
	if err != nil {
		return err
	}

	opts := make([]ai.Options, 0, len(questions))
	for _, q := range questions {
		opts = append(opts, q)
	}
	resp, err := dm.Decide(ctx, input, opts...)
	if err != nil {
		return err
	}
	if o.json {
		enc := json.NewEncoder(out)
		return enc.Encode(resp)
	}
	return printAnswers(out, questions, resp)
}

// decideInput returns the input of a decision: one user message with
// the text and the images.
func decideInput(text string, images []string) ([]ai.Message, error) {
	var content []ai.Content
	if text != "" {
		content = append(content, ai.Text{Text: text})
	}
	for _, path := range images {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--image: %w", err)
		}
		content = append(content, ai.File{
			MediaType: http.DetectContentType(data),
			Data:      data,
		})
	}
	if len(content) == 0 {
		return nil, errors.New("no input: give the text as arguments or on stdin")
	}
	return []ai.Message{{
		Role:    ai.RoleUser,
		Content: content,
	}}, nil
}

// parseQuestions returns the questions of the file of o, then the
// questions of the flags, in the order yes-or-no, choice, score, and
// box.
func parseQuestions(o decideOptions) ([]ai.Question, error) {
	var out []ai.Question
	if o.questions != "" {
		data, err := os.ReadFile(o.questions)
		if err != nil {
			return nil, fmt.Errorf("--questions: %w", err)
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("--questions %s: %w", o.questions, err)
		}
	}
	for _, v := range o.yesNo {
		name, instructions, ok := strings.Cut(v, "=")
		if !ok {
			return nil, fmt.Errorf("--yesno %q: use name=instructions", v)
		}
		out = append(out, ai.YesNo(name, instructions))
	}
	for _, v := range o.choice {
		name, instructions, ids, ok := cutList(v)
		if !ok {
			return nil, fmt.Errorf("--choice %q: use name=instructions|a|b", v)
		}
		options := make([]ai.ChoiceOption, 0, len(ids))
		for _, id := range ids {
			options = append(options, ai.ChoiceOption{ID: id})
		}
		out = append(out, ai.Choice(name, instructions, options...))
	}
	for _, v := range o.score {
		name, instructions, labels, ok := cutList(v)
		if !ok {
			return nil, fmt.Errorf("--score %q: use name=instructions|lowest|...|highest", v)
		}
		levels := make([]ai.ScoreLevel, 0, len(labels))
		for _, label := range labels {
			levels = append(levels, ai.ScoreLevel{Label: label})
		}
		out = append(out, ai.Score(name, instructions, levels...))
	}
	for _, v := range o.box {
		name, instructions, ok := strings.Cut(v, "=")
		if !ok {
			return nil, fmt.Errorf("--box %q: use name=instructions", v)
		}
		out = append(out, ai.Box(name, instructions))
	}
	if len(out) == 0 {
		return nil, errors.New("no question: use --yesno, --choice, --score, --box, or --questions")
	}
	return out, nil
}

// listSeparator splits the instructions and the items of --choice and
// --score. Instructions and items often hold a comma or a colon, and
// seldom a bar.
const listSeparator = "|"

// cutList splits name=instructions|a|b.
func cutList(v string) (name, instructions string, list []string, ok bool) {
	name, rest, ok := strings.Cut(v, "=")
	if !ok {
		return "", "", nil, false
	}
	instructions, items, ok := strings.Cut(rest, listSeparator)
	if !ok {
		return "", "", nil, false
	}
	for item := range strings.SplitSeq(items, listSeparator) {
		list = append(list, strings.TrimSpace(item))
	}
	return name, instructions, list, true
}

// printAnswers writes one row for each answer: its name, its answer,
// and the confidence. A refusal has "-" for its confidence, so each row
// has the same columns. Then it writes the usage. The answer at index i
// answers questions[i].
func printAnswers(out io.Writer, questions []ai.Question, resp *ai.DecisionResponse) error {
	w := tabwriter.NewWriter(out, tableMinWidth, tableTabWidth, tablePadding, ' ', 0)
	for i, a := range resp.Answers {
		if a.Refused {
			fmt.Fprintf(w, "%s\trefused\t-\n", a.Name)
			continue
		}
		value := answerValue(a, questions[i].Levels)
		fmt.Fprintf(w, "%s\t%s\t%.2f\n", a.Name, value, a.Confidence)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, usage(resp.Usage))
	return err
}

// answerValue returns the answer of a, as text. A score names the level
// nearest to it.
func answerValue(a ai.Answer, levels []ai.ScoreLevel) string {
	switch a.Kind {
	case ai.KindYesNo:
		if a.Yes() {
			return "yes"
		}
		return "no"
	case ai.KindChoice:
		return a.Choice
	case ai.KindScore:
		top := len(levels) - 1
		nearest := min(max(int(math.Round(a.Score)), 0), top)
		return fmt.Sprintf("%.2f of %d, %s", a.Score, top, levels[nearest].Label)
	case ai.KindBox:
		b := a.Bounds
		return fmt.Sprintf("top %.2f bottom %.2f left %.2f right %.2f", b.Top, b.Bottom, b.Left, b.Right)
	default:
		return ""
	}
}

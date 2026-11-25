package askquestion_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
)

// The approver of a host shows the questions of the call, then allows
// the call with the answers of the user in a new input. The tool
// returns these answers to the model.
func ExampleViaApprover() {
	call := ai.ToolCall{
		ID:    "call-1",
		Name:  askquestion.Name,
		Input: json.RawMessage(`{"questions":[{"header":"Area","question":"Which area?","options":[{"label":"Alfama"},{"label":"Baixa"}]}]}`),
	}

	// The approver gives the label that the user picked, by the text of
	// the question.
	input, err := askquestion.WithAnswers(call.Input, map[string]string{
		"Which area?": "Alfama",
	})
	if err != nil {
		panic(err)
	}
	call.Input = input

	res, err := askquestion.ViaApprover().Run(context.Background(), call)
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content[0].(ai.Text).Text)
	// Output: {"answers":[{"header":"Area","answer":"Alfama"}]}
}

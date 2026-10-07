// Command save-a-conversation writes a conversation to a file as JSON
// and reads it back. It does not call a model, so it needs no key.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/sonnes/gopi/pkg/ai"
)

func main() {
	call := ai.ToolCall{
		ID:    "call_1",
		Name:  "weather",
		Input: json.RawMessage(`{"city":"Lisbon"}`),
	}

	messages := []ai.Message{
		ai.UserMessage("What is the weather in Lisbon?"),
		ai.AssistantMessage(ai.Text{Text: "Let me check."}, call),
		ai.ToolResultMessage(call.ID, ai.Text{Text: `{"temp_c":21}`}),
		ai.AssistantMessage(ai.Text{Text: "It is 21 degrees."}),
	}

	path := filepath.Join(os.TempDir(), "conversation.json")

	// Each content block gets a type tag, so the file needs no schema.
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))

	// Read it back. An unknown type tag is an error, not a lost block.
	data, err = os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	var loaded []ai.Message
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Fatal(err)
	}

	for _, m := range loaded {
		fmt.Printf("%-11s blocks=%d\n", m.Role, len(m.Content))
	}
}

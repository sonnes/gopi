package ai

import "time"

// Options is one option or a joined set of options. The zero value is no
// options. A setter such as [MaxTokens], or a setter from [NewOption],
// makes one option. [JoinOptions] merges options.
//
// The design follows encoding/json/v2. An option is a typed value with
// its own key, so a provider package can define options that this
// package never sees. A provider reads a value back with [GetOption].
type Options interface {
	options()
}

// option is one key and its value. The key is the address that
// [NewOption] allocated, so two setters never collide.
type option struct {
	key   *byte
	value any
}

func (option) options() {}

// set is a flat list of options in the order that they were joined. The
// last value for a key wins.
type set []option

func (set) options() {}

// NewOption returns a setter for a new typed option. Call it once, at
// package level, and keep the setter. The setter is also the key that
// [GetOption] uses.
func NewOption[T any]() func(T) Options {
	key := new(byte)
	return func(v T) Options {
		return option{key: key, value: v}
	}
}

// JoinOptions merges options into one value. A later value for the same
// option wins. A nil entry is skipped.
func JoinOptions(opts ...Options) Options {
	var out set
	for _, o := range opts {
		switch v := o.(type) {
		case option:
			out = append(out, v)
		case set:
			out = append(out, v...)
		}
	}
	return out
}

// GetOption reads the value that setter stored in opts. If the option is
// unset, the bool is false. Unset is not the same as a zero value. A nil
// opts holds nothing.
//
// GetOption calls setter with a zero value to find the key, so it needs
// no registry and no reflection.
func GetOption[T any](opts Options, setter func(T) Options) (T, bool) {
	var zero T
	probe, ok := setter(zero).(option)
	if !ok {
		return zero, false
	}

	switch v := opts.(type) {
	case option:
		if v.key == probe.key {
			return v.value.(T), true
		}
	case set:
		for i := len(v) - 1; i >= 0; i-- {
			if v[i].key == probe.key {
				return v[i].value.(T), true
			}
		}
	}
	return zero, false
}

// ThinkingLevel is how much the model reasons before it answers. Each
// provider maps a level to its own request parameter.
type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
	ThinkingXHigh  ThinkingLevel = "xhigh"
	ThinkingMax    ThinkingLevel = "max"
)

// MaxTokens caps the output tokens of a call. If it is unset, a provider
// uses [Model.MaxTokens], then its own default.
var MaxTokens = NewOption[int]()

// Thinking sets the thinking level. If it is unset, a provider uses the
// default of the model.
var Thinking = NewOption[ThinkingLevel]()

// SystemPrompt sets the system prompt of a call to blocks, in order. A
// [Text] block is plain text. A [Section] block is a named part of the
// prompt. A provider skips every other block type.
//
// A provider sends the prompt as the system prompt of its API and marks
// it for the cache. It is an option and not an argument, because the
// messages are the only input that every call has. A [SystemMessage]
// adds to the prompt from its place in the messages.
var SystemPrompt = NewOption[[]Content]()

// Tools sets the tools that the model can call at the start of the
// messages. A system message changes them from its place, with
// [ToolAdded] and [ToolRemoved] blocks. A later value replaces the whole
// list, like every other option. A caller builds one list and passes
// it, so no rule merges two lists.
var Tools = NewOption[[]ToolInfo]()

// ImageSize sets the size of a generated image, for example "1024x1024".
// A provider that does not support the size ignores it.
var ImageSize = NewOption[string]()

// ImageCount sets the number of images to generate.
var ImageCount = NewOption[int]()

// MaxRetries sets how many times a provider sends a request again after
// an error that can pass, such as a rate limit or an overload. A
// provider retries only before the first event of the stream. If it is
// unset, a provider does not retry.
var MaxRetries = NewOption[int]()

// MaxRetryDelay caps the wait that a server can ask for before a retry.
// If the server asks for a longer wait, the provider returns the error
// at once. Zero removes the cap. If it is unset, a provider uses 60
// seconds.
var MaxRetryDelay = NewOption[time.Duration]()

// Temperature sets the sampling temperature. A provider does not send
// it to a model that rejects it.
var Temperature = NewOption[float64]()

// Headers adds HTTP headers to each request of a call. A header with the
// same name as one that the provider sets replaces it.
var Headers = NewOption[map[string]string]()

// ToolChoiceMode says if the model must call a tool. The setter has the
// name ToolChoice, so the type takes a suffix.
type ToolChoiceMode struct {
	// Type is "auto", "any", "none", or "tool".
	Type string `json:"type"`
	// Name is the tool that the model must call, for the type "tool".
	Name string `json:"name,omitempty"`
}

// The tool choices that name no tool.
var (
	// ToolChoiceAuto lets the model decide.
	ToolChoiceAuto = ToolChoiceMode{Type: "auto"}
	// ToolChoiceAny makes the model call one of the tools.
	ToolChoiceAny = ToolChoiceMode{Type: "any"}
	// ToolChoiceNone stops the model from a tool call.
	ToolChoiceNone = ToolChoiceMode{Type: "none"}
)

// ToolChoiceTool makes the model call the tool with name.
func ToolChoiceTool(name string) ToolChoiceMode {
	return ToolChoiceMode{
		Type: "tool",
		Name: name,
	}
}

// ToolChoice sets the tool choice of a call. If it is unset, the model
// decides. Some models reject a forced tool, and a provider does not
// check.
var ToolChoice = NewOption[ToolChoiceMode]()

// Retention is how long a provider keeps the cached prefix of a call.
type Retention string

const (
	// CacheNone sends no cache marker.
	CacheNone Retention = "none"
	// CacheShort keeps the prefix for the short time of the provider.
	CacheShort Retention = "short"
	// CacheLong keeps the prefix for the long time of the provider. A
	// long write costs more, and [Usage.CacheWriteLong] counts it.
	CacheLong Retention = "long"
)

// CacheRetention sets how long a provider keeps the cached prefix. If it
// is unset, a provider uses [CacheShort].
var CacheRetention = NewOption[Retention]()

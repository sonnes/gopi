package ai

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

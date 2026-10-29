module github.com/sonnes/gopi/pkg/ai/openrouter

go 1.27

require (
	github.com/google/jsonschema-go v0.4.3
	github.com/sonnes/gopi v0.0.1
	github.com/sonnes/gopi/pkg/ai/openai v0.0.1
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/openai/openai-go/v3 v3.66.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// A replace builds this module against the code in this repository.
// A program that requires this module ignores it.
replace (
	github.com/sonnes/gopi => ../../..
	github.com/sonnes/gopi/pkg/ai/openai => ../openai
)

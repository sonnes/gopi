module github.com/sonnes/gopi/pkg/catalog/env

go 1.27

require (
	github.com/sonnes/gopi v0.0.1
	github.com/sonnes/gopi/pkg/ai/anthropic v0.0.1
	github.com/sonnes/gopi/pkg/ai/fireworks v0.0.1
	github.com/sonnes/gopi/pkg/ai/openai v0.0.1
	github.com/sonnes/gopi/pkg/ai/openaichat v0.0.1
	github.com/sonnes/gopi/pkg/ai/openrouter v0.0.1
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/anthropics/anthropic-sdk-go v1.75.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.2 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/modelcontextprotocol/go-sdk v1.5.0 // indirect
	github.com/openai/openai-go/v3 v3.66.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// A replace builds this module against the code in this repository.
// A program that requires this module ignores it.
replace (
	github.com/sonnes/gopi => ../../..
	github.com/sonnes/gopi/pkg/ai/anthropic => ../../ai/anthropic
	github.com/sonnes/gopi/pkg/ai/fireworks => ../../ai/fireworks
	github.com/sonnes/gopi/pkg/ai/openai => ../../ai/openai
	github.com/sonnes/gopi/pkg/ai/openaichat => ../../ai/openaichat
	github.com/sonnes/gopi/pkg/ai/openrouter => ../../ai/openrouter
)

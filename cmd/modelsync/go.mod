module github.com/sonnes/gopi/cmd/modelsync

go 1.27

// The root module and the providers have no release yet, so the
// generator builds against the code in this repository.
replace (
	github.com/sonnes/gopi => ../..
	github.com/sonnes/gopi/pkg/ai/anthropic => ../../pkg/ai/anthropic
	github.com/sonnes/gopi/pkg/ai/fireworks => ../../pkg/ai/fireworks
	github.com/sonnes/gopi/pkg/ai/openai => ../../pkg/ai/openai
	github.com/sonnes/gopi/pkg/ai/openaichat => ../../pkg/ai/openaichat
	github.com/sonnes/gopi/pkg/ai/openrouter => ../../pkg/ai/openrouter
	github.com/sonnes/gopi/pkg/catalog/env => ../../pkg/catalog/env
)

require (
	github.com/sonnes/gopi v0.0.0
	github.com/sonnes/gopi/pkg/ai/openrouter v0.0.0
	github.com/sonnes/gopi/pkg/catalog/env v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/anthropics/anthropic-sdk-go v1.75.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/openai/openai-go/v3 v3.66.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sonnes/gopi/pkg/ai/anthropic v0.0.0 // indirect
	github.com/sonnes/gopi/pkg/ai/fireworks v0.0.0 // indirect
	github.com/sonnes/gopi/pkg/ai/openai v0.0.0 // indirect
	github.com/sonnes/gopi/pkg/ai/openaichat v0.0.0 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	golang.org/x/sync v0.22.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

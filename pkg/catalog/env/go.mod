module github.com/sonnes/gopi/pkg/catalog/env

go 1.27

require (
	github.com/sonnes/gopi v0.0.4
	github.com/sonnes/gopi/pkg/ai/anthropic v0.0.4
	github.com/sonnes/gopi/pkg/ai/fireworks v0.0.4
	github.com/sonnes/gopi/pkg/ai/gemini v0.0.4
	github.com/sonnes/gopi/pkg/ai/openai v0.0.4
	github.com/sonnes/gopi/pkg/ai/openaichat v0.0.4
	github.com/sonnes/gopi/pkg/ai/openrouter v0.0.4
	github.com/stretchr/testify v1.11.1
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.5.0 // indirect
	github.com/anthropics/anthropic-sdk-go v1.75.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.2 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
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
	go.opencensus.io v0.24.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genai v1.77.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/grpc v1.66.2 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// A replace builds this module against the code in this repository.
// A program that requires this module ignores it.
replace (
	github.com/sonnes/gopi => ../../..
	github.com/sonnes/gopi/pkg/ai/anthropic => ../../ai/anthropic
	github.com/sonnes/gopi/pkg/ai/fireworks => ../../ai/fireworks
	github.com/sonnes/gopi/pkg/ai/gemini => ../../ai/gemini
	github.com/sonnes/gopi/pkg/ai/openai => ../../ai/openai
	github.com/sonnes/gopi/pkg/ai/openaichat => ../../ai/openaichat
	github.com/sonnes/gopi/pkg/ai/openrouter => ../../ai/openrouter
)

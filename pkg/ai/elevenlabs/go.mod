module github.com/sonnes/gopi/pkg/ai/elevenlabs

go 1.27

require (
	github.com/sonnes/gopi v0.0.4
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// A replace builds this module against the code in this repository.
// A program that requires this module ignores it.
replace github.com/sonnes/gopi => ../../..

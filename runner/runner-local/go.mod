module github.com/marginlab/margin-eval/runner/runner-local

go 1.23

require github.com/marginlab/margin-eval/runner/runner-core v0.0.0

require (
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/marginlab/margin-eval/runner/runner-core => ../runner-core

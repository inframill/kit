module github.com/inframill/kit

go 1.27.0

replace github.com/sir-hassan/inframill => ./third_party/inframill

require (
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	github.com/sir-hassan/inframill v0.0.0
	gopkg.in/yaml.v3 v3.0.1
	oras.land/oras-go/v2 v2.6.2
)

require (
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

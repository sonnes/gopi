.PHONY: test check build tidy fmt

# Run the tests. Pass flags to go test with ARGS, for example
# `make test ARGS="-run TestStreamText -v"`. To test some packages only,
# set PKGS, for example `make test PKGS=./pkg/stream/...`.
PKGS ?= ./...

test:
	go test $(PKGS) $(ARGS)

# Tidy the module, vet the code, and fail on any file that gofmt would
# change.
check: tidy
	go vet ./...
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt: these files need formatting:"; \
		echo "$$files"; \
		exit 1; \
	fi

# Format every Go file in place.
fmt:
	gofmt -w .

# Build every binary under cmd/ into .bin/.
build:
	mkdir -p .bin
	go build -o .bin/ ./cmd/...

tidy:
	go mod tidy

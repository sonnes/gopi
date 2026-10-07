.PHONY: test check build tidy fmt record

# MODULES lists the directory of each module. go.work joins them, but
# ./... stops at the root of a nested module, so each target runs in
# each module.
MODULES := . pkg/ai/anthropic pkg/ai/openai pkg/ai/openaichat

# Run the tests. Pass flags to go test with ARGS, for example
# `make test ARGS="-run TestStreamText -v"`. To test some packages only,
# set PKGS, for example `make test PKGS=./pkg/ai/anthropic/...`.
PKGS ?=

test:
ifdef PKGS
	go test $(PKGS) $(ARGS)
else
	@for m in $(MODULES); do (cd $$m && go test ./... $(ARGS)) || exit 1; done
endif

# Tidy each module, vet the code, and fail on any file that gofmt would
# change.
check: tidy
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done
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
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

# Re-record the fixtures of one provider package. Set PKG to its
# directory under pkg/ai, for example `make record PKG=anthropic`. The
# target reads the keys from .envrc, if the file exists.
#
#   anthropic   needs ANTHROPIC_API_KEY, or OPENROUTER_API_KEY to record
#               from OpenRouter. ANTHROPIC_BASE_URL selects another
#               compatible API.
#
#   openai,
#   openaichat  needs OPENAI_API_KEY, or OPENROUTER_API_KEY to record
#               from OpenRouter. OPENAI_BASE_URL selects another
#               compatible API. The Fireworks test of openaichat
#               needs FIREWORKS_API_KEY.
#
# Set RUN to record some tests only, for example RUN=TestStreamText. A
# recording has the name of its test with each / as _, so the target
# changes / to _ in the pattern for -httprecord.
PKG ?= anthropic
RUN ?= .
record:
	@set -a; if [ -f .envrc ]; then . ./.envrc; fi; set +a; \
	cd pkg/ai/$(PKG) && go test . -httprecord='$(subst /,_,$(RUN))' -run='$(RUN)'

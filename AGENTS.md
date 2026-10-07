# gopi

gopi is a Go SDK for building AI agents. 

## Project Map

- `pkg/` - the SDK. One directory per package. Each provider under `pkg/ai/` is its own module, and `go.work` joins the modules.
- `cmd/gopi/` - the CLI, a test drive of the SDK. Build output goes to `.bin/`.
- `docs/` - the developer docs: the concept pages and the glossary. `docs/README.md` has the layout.
- `.claude/rules/` - path-scoped rules for Go code, tests, and `docs/`

<important if="you need to run commands to build, test, lint, or tidy the code">
Use the `make` targets. Do not call `go test`, `go build`, `go vet`, or `golangci-lint` directly.

If a target that you need does not exist, add it to the `Makefile` first. Then run it.

| Target       | Purpose                                                        |
| ------------ | -------------------------------------------------------------- |
| `make test`  | Run the tests. To give flags to `go test`, set `ARGS`.          |
| `make check` | Run `make tidy`, then the linter                                |
| `make build` | Build the binaries to `.bin/`                                   |
| `make tidy`  | Run `go mod tidy`                                               |
| `make fmt`   | Run `gofmt -w` on every Go file                                 |
| `make record` | Re-record the fixtures of one provider. Set `PKG` and `RUN`. |

Put all binaries in `.bin/`.
</important>

<important if="you are about to commit">
Run `make check` before you commit.
</important>

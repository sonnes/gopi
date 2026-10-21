# gopi

gopi is a Go SDK for building AI agents. 

## Project Map

- `pkg/` - the SDK. One directory per package. Each provider under `pkg/ai/` and `pkg/mcp` is its own module, and `go.work` joins the modules.
- `internal/` - the code that every provider shares: the retry loop, the message rules, and the test cases.
- `cmd/gopi/` - the CLI, a test drive of the SDK. Build output goes to `.bin/`.
- `docs/` - the developer docs: the concept pages and the glossary. `docs/README.md` has the layout.
- `web/` - the site for the users of gopi, with Astro and Starlight. `web/README.md` has the layout.
- `examples/` - the programs of the site guides. It is its own module, and `make test` builds it.
- `.claude/rules/` - path-scoped rules for Go code, tests, `docs/`, and the site pages

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
| `make record-cli` | Re-record the trace of one CLI agent. Set `AGENT`. |
| `make models` | Update `pkg/catalog/models/models.json`. Set `IDS` and `PRUNE`. |
| `make site-dev` | Serve the site with hot reload. Run `make site-install` first. |
| `make site-check` | Type-check the site                                       |
| `make site-test` | Run the tests of the site                                  |
| `make site-build` | Build the site to `web/dist`                              |
| `make docs-links` | Make sure that each link in `docs/` and the pages resolves |

Put all binaries in `.bin/`.
</important>

<important if="you are about to commit">
Run `make check` before you commit.
</important>

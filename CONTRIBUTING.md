# Contributing to TerraGraph

Thanks for contributing. By participating you agree to follow our
[Code of Conduct](CODE_OF_CONDUCT.md).

Security vulnerabilities: see [SECURITY.md](SECURITY.md) — please do not file a
public issue.

## Ways to contribute

- Bug reports and feature ideas (use the issue templates)
- Documentation improvements
- Code changes via pull request

## Branches and releases

`main` is the release branch. `develop` is the integration branch — open pull
requests against `develop` unless you are fixing something already broken in a
release.

**Every merge to `main` that touches code publishes a release.** The version is
computed automatically: the highest existing tag with its patch component
incremented, starting at `v0.0.1`. There is no version to bump by hand and no
changelog to edit — release notes are generated from the commits.

Documentation-only changes do not trigger a release. A README edit needs no new
binaries, and tagging one would make the release history useless for answering
what actually changed.

To cut a minor or major version instead of a patch, run the **Release** workflow
manually from the Actions tab and choose the bump.

## Develop from source

Requires Go 1.26 or newer — the version in `go.mod` is authoritative.

```bash
git clone https://github.com/dpalfery/TerraGraph && cd TerraGraph
go build ./...
go test ./...
```

Run either binary from source:

```bash
go run ./cmd/terragraph status --repo testdata/repo
go run ./cmd/terragraph-mcp --repo testdata/repo    # speaks JSON-RPC on stdout
```

## What CI enforces

Run these before pushing; CI runs exactly the same checks and nothing else:

```bash
gofmt -l ./cmd ./internal      # must print nothing
go mod tidy                    # must leave go.mod and go.sum unchanged
go vet ./...
go test -race ./...
shellcheck install.sh          # only if you touched the installer
```

CI additionally cross-compiles all five release targets, so a change that breaks
one platform is caught before a tag exists rather than after.

## Testing conventions

Tests are table-driven and live beside the code. Two conventions are worth
knowing because they are why the fixtures look the way they do:

**Fixtures are real, not hand-written.** `testdata/overlay/tfplan.json` and
`testdata/expanded/tfplan.json` are genuine `terraform show -json` output. The
plan schema has shapes that are easy to guess wrong — `index` is a number for
`count` and a string for `for_each`, and `module_address` embeds the module's own
expansion as `module.fleet["eu"]`. A fixture written from the documentation would
agree with a parser written from the same documentation while both were wrong.
They use `terraform_data`, a builtin, so regenerating them needs no provider
download and no credentials.

**Assert the claim, not the coverage.** The tests that have earned their keep are
the ones asserting one specific thing: that an address written in a comment
produces no edge, that a resource inside a `for_each`'d module resolves, that
every tool honours its character budget. Those found four real defects. A test
that merely executes a function found none.

If you change ranking or output shape, say what you measured. There is a
[token benchmark](docs/token-benchmark.md) with a reproducible method.

## Documentation

`docs/` is a governed corpus under
[Kyber-Weave's](https://github.com/dpalfery/kyber-weave) documentation ontology:
every document carries typed frontmatter, and `component` and `owner` come from
the closed vocabulary in [`docs/catalog.md`](docs/catalog.md).

If you add a document there, validate it:

```bash
kyber-weave docs validate .
```

A document naming a component with no catalog row fails `KW-DOC-SPEC-004`. Add
the row deliberately, or use an existing component.

Root-level files (this one, README, SECURITY) are outside the governed corpus and
need no frontmatter.

## Pull requests

One approving review is required, including from a code owner. The default merge
method is **squash**.

Checklist:

- [ ] The change is focused, and the description says why
- [ ] Tests added or updated when behaviour changes
- [ ] `go test ./...` and `go vet ./...` pass locally
- [ ] `gofmt -l ./cmd ./internal` prints nothing
- [ ] Docs updated when user-facing behaviour changes
- [ ] No secrets or credentials in the diff

## Licence

Contributions are accepted under the MIT licence. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).

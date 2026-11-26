# Contributing to gokue

Thanks for helping improve gokue. Bug reports, docs fixes, and pull requests are
all welcome.

## Finding work

Planned work is tracked as [GitHub issues](https://github.com/zt4ff/gokue/issues),
grouped by [milestone](https://github.com/zt4ff/gokue/milestones). Issues labelled
`good-first-issue` are a good place to start. Comment on an issue before starting
larger changes so work isn't duplicated.

## Public API

From v2.0.0 the packages below follow [semantic versioning](https://semver.org);
breaking changes to them need a new major version (and module path suffix, e.g.
`/v3`). Record every user-visible change under `Unreleased` in CHANGELOG.md:

- `gokue` (the `Queue` API, options, errors, `ShutdownMode`, `JobEvent`)
- `config`, `stats`, `job`
- `backend` and `backend/mongodb`

`internal/dispatcher` and `internal/logger` are implementation details and may
change in any release. New user-facing behaviour should be exposed through
`gokue`, re-exporting internal types with aliases where needed.

## Development setup

- Go 1.22 or newer (the minimum is set in `go.mod`).
- [staticcheck](https://staticcheck.dev) at the version pinned in
  `.github/workflows/ci.yml`:

  ```sh
  go install honnef.co/go/tools/cmd/staticcheck@v0.8.2
  ```

The MongoDB backend tests do not need a running MongoDB server.

## Before opening a pull request

Run the same checks as CI:

```sh
gofmt -l .            # must print nothing
go vet ./...
staticcheck ./...
go test -count=1 -race ./...
```

## Pull request guidelines

- Keep each PR focused on one issue or change, and reference it (`Closes #123`).
- Add a test that fails without your change for every bug fix.
- Update `README.md` when public behaviour or the API changes.
- Exported identifiers need doc comments.
- Avoid new dependencies in the core module; backend-specific dependencies
  belong in their backend package.

## Reporting bugs

Open an issue with the gokue and Go versions, a minimal reproduction, and the
expected versus actual behaviour.

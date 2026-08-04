# Contributing to busyctl

Thanks for helping improve busyctl.

## Development

The required Go version is declared in `go.mod`. To validate a change locally:

```sh
go test ./...
go vet ./...
go build -o bin/busyctl ./cmd/busyctl
```

Keep pull requests focused and include tests for new behavior. Run `gofmt` on
changed Go files before opening a pull request.

## Adding an app

Create a package under `internal/apps/<name>` with an exported application ID
and a `Run(context.Context, []string) error` entry point. Register it in
`cmd/busyctl/main.go`. Device operations shared by multiple apps belong in
`internal/busybar`; reusable image and animation helpers belong in
`internal/media`.

## Reporting bugs

Open a GitHub issue with the busyctl version, macOS version, BUSY Bar connection
type, steps to reproduce, and any relevant terminal output. Remove device API
tokens and other private information before posting logs.

By contributing, you agree that your contributions will be licensed under the
MIT License.

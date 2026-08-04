# Contributing

Thanks for helping improve busyctl and its BUSY Bar apps.

## Development

The required Go version is declared in `go.mod`. To validate a change locally:

```sh
go test ./...
go vet ./...
go build -trimpath -o bin/busyctl ./cmd/busyctl
```

Keep pull requests focused and include tests for new behavior. Run `gofmt` on
changed Go files before opening a pull request.

## Releases

Releases are generated from annotated semantic-version tags. After the release
commit is on `main`, create and push a tag such as `v0.2.0`; GitHub Actions will
test it and publish checksummed archives for the supported platforms.

## Reporting bugs

Open a GitHub issue with the macOS version, BUSY Bar connection
type, steps to reproduce, and any relevant terminal output. Remove device API
tokens and other private information before posting logs.

By contributing, you agree that your contributions will be licensed under the
MIT License.

# Contributing

Thanks for helping improve the Apple Music BUSY Bar display.

## Development

The required Go version is declared in `go.mod`. To validate a change locally:

```sh
go test ./...
go vet ./...
go build -o bin/applemusic ./cmd/applemusic
```

Keep pull requests focused and include tests for new behavior. Run `gofmt` on
changed Go files before opening a pull request.

## Reporting bugs

Open a GitHub issue with the macOS version, BUSY Bar connection
type, steps to reproduce, and any relevant terminal output. Remove device API
tokens and other private information before posting logs.

By contributing, you agree that your contributions will be licensed under the
MIT License.

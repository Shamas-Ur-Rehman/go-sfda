# Contributing

Thanks for helping improve `go-sfda`. Contributions should preserve
responsible request pacing and must not bypass SFDA access controls.

## Before opening a pull request

1. Open an issue for significant behavior or API changes.
2. Keep changes focused and preserve existing public APIs where practical.
3. Add deterministic tests using `httptest.Server`; unit tests must not depend
   on the live SFDA site.
4. Run from the repository root:

   ```shell
   go fmt ./...
   go vet ./...
   go test ./...
   go test -race ./...
   go build ./...
   ```

5. If changing the opt-in live example, test it explicitly and limit it to a
   small number of pages. Do not run full-registry scraping in CI.
6. Update the README and changelog for user-visible changes.

## Pull requests

Explain the user impact, implementation, and validation performed. Mention
known compatibility or data completeness limitations. Do not include
checkpoints, generated workbooks, credentials, or personal scrape data.

## License

No license is currently included. Contributions and redistribution terms
remain subject to the repository maintainer adding and approving a license.

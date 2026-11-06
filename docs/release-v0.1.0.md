# v0.1.0 release checklist

- [ ] Maintainer selects and approves an open-source license; add the approved
  license before publishing.
- [ ] Review the current SFDA page and confirm the documented request behavior.
- [ ] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and
  `go build ./...`.
- [ ] Run the opt-in one-page smoke test in `examples/smoke-test`.
- [ ] Review `go.mod`/`go.sum` and dependency advisories.
- [ ] Confirm CI is green and the worktree contains no generated workbooks,
  checkpoints, secrets, or scrape artifacts.
- [ ] Review the README limitations and MongoDB/PostgreSQL adapter guidance.
- [ ] Set repository topics: `golang`, `go`, `sfda`, `saudi-arabia`,
  `drug-registry`, `web-scraping`, `healthcare`, `pharmacy`, `excel-export`.
- [ ] After approval, create and publish the `v0.1.0` tag/release.

## Suggested release notes

`go-sfda v0.1.0` is an initial reusable Go client for the publicly visible SFDA
drug registry. It includes a bounded, paced scraper; context-aware retries;
filters; page-level resumable checkpointing; database-neutral sink adapters;
and streaming Excel output.

The SFDA frontend is not a versioned API. Scraping is at-least-once when
resuming after a partial page, so storage adapters should upsert by registration
number. This release is not affiliated with SFDA. Publish only after a license
has been selected and added by the maintainer.

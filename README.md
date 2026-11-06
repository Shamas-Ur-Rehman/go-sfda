# go-sfda

An unofficial, reusable Go client for reading publicly available drug
registration records from the Saudi Food and Drug Authority (SFDA) drugs list.
This project is not affiliated with, endorsed by, or sponsored by SFDA.

The client follows requests made by the current public website:

1. `GET /en/drugs-list?page=N` for server-rendered result pages.
2. Drupal AJAX `POST /en/details_data?...` requests for each detail dialog.

The website's markup and routes are not a versioned public API and may change.
Use this package responsibly, respect SFDA terms and access restrictions, and
stop if the service denies or limits requests. This package does not bypass
CAPTCHAs, authentication, rate limits, or other controls.

## Features

- Context-aware HTTP requests, configurable timeouts, retries with exponential
  backoff and jitter, and `Retry-After` handling for 429 and selected 5xx
  responses.
- A bounded detail worker pool, global request pacing, and per-page
  de-duplication of identical detail URLs.
- `FetchAll` for in-memory results and `Scrape` for page-by-page sink storage.
- Optional atomic JSON checkpoints that resume after the last fully stored
  page.
- Exact, contains, and prefix field filters.
- Excel output with a streaming row-source API, string-valued identifiers,
  export timestamp metadata, and Unicode/Arabic support.
- No MongoDB or PostgreSQL driver dependency in the core module.

## Requirements and installation

- Go 1.23 or newer
- Network access to the public SFDA site for live scraping

```shell
go get github.com/Shamas-Ur-Rehman/go-sfda
```

## Quick start

This fetches the complete registry and keeps results in memory. For a limited
live test, set `MaxPages: 1`; use `Scrape` with a sink for large datasets.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Shamas-Ur-Rehman/go-sfda/controller"
)

func main() {
	client, err := controller.NewScraper(controller.Options{})
	if err != nil {
		log.Fatal(err)
	}

	drugs, err := client.FetchAll(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Fetched %d records\n", len(drugs))
}
```

## Configuration and responsible usage

```go
client, err := controller.NewScraper(controller.Options{
	MaxWorkers:      3,                 // concurrent detail requests; default 3
	MaxPages:        1,                 // 0 means all pages
	RequestInterval: 500 * time.Millisecond,
	RequestTimeout:  30 * time.Second,
	MaxRetries:      4,                 // maximum attempts
	Backoff:         time.Second,
	MaxBackoff:      30 * time.Second,
	RetryJitter:     0.2,               // ±20%; 0 selects the default
	CheckpointPath:  "sfda.checkpoint.json",
})
```

`Options` also accepts a custom `HTTPClient` or `CheckpointStore`. A supplied
HTTP client's transport and other settings are retained; `RequestTimeout` is
applied if that client does not already have a timeout. Set either
`CheckpointStore` or `CheckpointPath`, not both.

The default worker count is three, and requests are paced across the client.
More concurrency is not necessarily faster and may burden the public service.
Keep the conservative defaults unless you have checked applicable usage
policies and observed safe behavior.

Retries are limited to transient network errors, HTTP 429, and HTTP 500, 502,
503, and 504. Other HTTP statuses are returned immediately. `Retry-After` delta
seconds and HTTP dates are observed. Request and response bodies are bounded;
unexpected HTML or Drupal response structures produce errors rather than
silently dropping records.

## Filtering

Filters are case-insensitive and use AND semantics. Fields accept Go names or
JSON names.

```go
drugs, err := client.FetchAll(ctx,
	model.Filter{
		Field: "TradeName",
		Value: "amox",
		Mode:  model.MatchContains,
	},
	model.Filter{
		Field: "country_of_manufacture",
		Value: "Saudi Arabia",
		Mode:  model.MatchExact,
	},
)
```

Modes are `model.MatchExact`, `model.MatchContains`, and `model.MatchPrefix`.
Unknown fields and modes return errors. `model.FilterDrugs` filters an already
fetched slice.

## Streaming to storage and resumable scraping

Implement `storage.Sink` for the database or output destination used by your
application. `Scrape` fetches, parses, filters, and stores one page at a time,
so callers do not need to hold the full registry in memory.

```go
err := client.Scrape(ctx, storage.SinkFunc(func(ctx context.Context, drug model.Drug) error {
	return json.NewEncoder(output).Encode(drug)
}))
```

Buffered sinks may additionally implement `storage.Flusher`. The scraper calls
`Flush(ctx)` at each page boundary and only marks a page complete after the
flush succeeds. A sink must return an error if it has not durably accepted the
record/page.

Set `CheckpointPath` to enable local JSON checkpointing, or provide a custom
`storage.CheckpointStore`. Checkpoints record the last completed page, the
in-progress page, failed pages, and a fingerprint of the endpoint, page limit,
and filters. Writes use a temporary file followed by atomic replacement.
Corrupt or mismatched checkpoints return explicit errors; they are not silently
discarded.

Pages are processed sequentially. On restart, the scraper re-fetches page zero
to discover current pagination, then resumes after `LastCompletedPage`. If a
sink fails partway through a page, that page is repeated: persistence is
therefore **at least once**, not a distributed transaction. Use an idempotent
sink keyed by `RegistrationNumber`, and keep the checkpoint with the same
destination and scrape configuration. A changed filter or page limit requires
a separate checkpoint or removal of the old one.

## Excel export

For modest in-memory results, use `view.WriteExcel`:

```go
file, err := os.Create("sfda_registered_drugs.xlsx")
if err != nil {
	return err
}
defer file.Close()
if err := view.WriteExcel(file, drugs); err != nil {
	return err
}
```

For larger or externally streamed inputs, `view.WriteExcelFrom` requests one
record at a time and uses Excelize's streaming writer:

```go
err := view.WriteExcelFrom(file, func() (model.Drug, bool, error) {
	drug, ok, err := nextDrugFromYourSource()
	return drug, ok, err
})
```

Both APIs write all fields represented by `model.Drug`, retain registration
numbers as strings, include an export timestamp on a metadata worksheet, and
support Unicode text including Arabic. `WriteExcelFrom` still creates the
workbook package on disk as needed by Excelize; available disk space is
required.

## Database adapter examples

The core module intentionally does not import database drivers. See
[`examples/database-adapters/README.md`](examples/database-adapters/README.md)
for MongoDB bulk upserts and PostgreSQL `ON CONFLICT` examples. Both examples
use `registration_number` as a stable unique key. MongoDB should create a
unique index; PostgreSQL should declare a primary key or unique constraint.
Use `storage.Flusher` when buffering writes so checkpoint progress is not
committed before the batch is persisted.

## Small live smoke test

The example is intentionally separate from unit tests. It contacts SFDA,
fetches only the first page, checks key fields, and writes
`sfda_smoke_test.xlsx`:

```powershell
cd examples\smoke-test
go run .
```

The example module uses a local replacement for this repository. Do not run
full-registry scraping as a connectivity check.

## Tests and benchmarks

Unit tests use `httptest.Server` and do not contact SFDA:

```shell
go test ./...
go test -race ./...
go vet ./...
go test -bench=. -benchmem ./controller ./model
```

The live smoke test is opt-in and is not run by CI. The CI workflow formats,
tests, vets, race-tests, and builds the library and smoke-test consumer without
calling SFDA.

## Troubleshooting and limitations

- **Unexpected page/Drupal structure:** SFDA may have changed its frontend.
  Inspect the public page and update parsing tests before changing selectors.
- **429 or 5xx errors:** the client retries selected responses and respects
  `Retry-After`; reduce concurrency or increase pacing if limits persist.
- **Checkpoint mismatch/corruption:** preserve a copy for diagnosis, then use a
  new checkpoint path or intentionally remove the old checkpoint to start
  over.
- **Repeated rows after restart:** checkpointing is at-least-once. Make the
  sink idempotent using the registration number.
- **Complete registry export:** `FetchAll` holds every result in memory; use
  `Scrape` or `WriteExcelFrom` when processing a large source incrementally.
- The SFDA page is not a versioned API. Completeness and data accuracy depend
  on the fields currently exposed by the page.

## Project layout

```text
controller/  HTTP workflow, pagination, retries, bounded detail workers
model/       Drug model and field filtering
storage/     Database-neutral sink and checkpoint contracts
view/        Excel export
examples/    Opt-in live smoke test and database adapter examples
```

## v0.1.0 release checklist

- [ ] Confirm and add an open-source license with maintainer approval.
- [ ] Review the current SFDA page and run `go test ./...`, `go vet ./...`,
  `go test -race ./...`, and the opt-in one-page smoke test.
- [ ] Review dependency advisories and the generated changelog notes.
- [ ] Confirm repository topics: `golang`, `go`, `sfda`, `saudi-arabia`,
  `drug-registry`, `web-scraping`, `healthcare`, `pharmacy`, `excel-export`.
- [ ] Tag and publish `v0.1.0` only after review; no release is created by CI.

See [`CHANGELOG.md`](CHANGELOG.md) for suggested initial release notes and
[`docs/release-v0.1.0.md`](docs/release-v0.1.0.md) for the release checklist.

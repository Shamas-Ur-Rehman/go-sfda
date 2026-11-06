# SFDA Drug Registry Go Package

A reusable Go client for fetching public drug registration records from the
Saudi Food and Drug Authority (SFDA) drugs list. It follows the requests made by
the current website:

1. `GET /en/drugs-list?page=N` to retrieve each server-rendered list page.
2. `POST /en/details_data?...&_wrapper_format=drupal_bootstrap4_modal` to retrieve
   each drug's details through the site's Drupal AJAX endpoint.

The package uses a bounded worker pool and conservative request pacing. It has
no database-driver dependency: callers can write records to any database by
implementing a small sink interface.

> The SFDA website can change its HTML, AJAX route, or rate limits. The client
> returns errors for unexpected page formats and does not bypass authentication
> or access controls.

## Requirements

- Go 1.23 or newer
- Internet access to the public SFDA website

## Install

```sh
go get github.com/Shamas-Ur-Rehman/go-sfda
```

## Fetch records

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Shamas-Ur-Rehman/go-sfda/controller"
)

func main() {
	client, err := controller.NewScraper(controller.Options{
		MaxWorkers: 3,
	})
	if err != nil {
		log.Fatal(err)
	}

	drugs, err := client.FetchAll(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Fetched %d drugs\n", len(drugs))
}
```

`FetchAll` returns records in the same order as the website's pages. It keeps
all returned records in memory; use `Scrape` with a sink for page-by-page
processing. Set `Options.MaxPages` to a positive number to limit a run; the
default (`0`) processes all pages.

## Try it with a small live test

The repository includes a small example application that uses the package,
fetches only the first page, checks that records were returned, and saves them
to `sfda_smoke_test.xlsx`. It makes real requests to the public SFDA site but
does not crawl the full registry:

```powershell
cd examples\smoke-test
go run .
```

The example's `go.mod` uses a local replacement for the parent repository so
you can test your current checkout before publishing a release. The standard
unit tests use a local HTTP server and do not contact SFDA:

```powershell
cd ..\..
go test ./...
```

## Filter records

Filters are case-insensitive and use AND semantics:

```go
import "github.com/Shamas-Ur-Rehman/go-sfda/model"

drugs, err := client.FetchAll(
	context.Background(),
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

Supported modes are `model.MatchExact`, `model.MatchContains`, and
`model.MatchPrefix`. `Field` accepts a Go field name (such as `TradeName`) or a
JSON field name (such as `trade_name`). Unknown fields and modes return errors.
`model.FilterDrugs` can also filter an already-fetched slice.

## Export an Excel workbook

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/Shamas-Ur-Rehman/go-sfda/controller"
	"github.com/Shamas-Ur-Rehman/go-sfda/view"
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
	file, err := os.Create("sfda_registered_drugs.xlsx")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	if err := view.WriteExcel(file, drugs); err != nil {
		log.Fatal(err)
	}
}
```

`view.WriteExcel` accepts any `io.Writer`, so it can also write directly to an
HTTP response for a browser download.

## Store in a database

Implement `storage.Sink` using the database driver already used by your
application. The module does not import MongoDB, PostgreSQL, or SQL drivers:

```go
type MongoSink struct {
	Collection *mongo.Collection
}

func (s MongoSink) Store(ctx context.Context, drug model.Drug) error {
	_, err := s.Collection.InsertOne(ctx, drug)
	return err
}

err := client.Scrape(ctx, MongoSink{Collection: collection})
```

For PostgreSQL or another SQL database, implement `Store` with your preferred
driver and an `INSERT`/upsert statement. The scraper calls `Store` serially;
this avoids requiring database-specific concurrency or transaction behavior.

## Scrape incrementally

`Scrape` writes one page of records at a time instead of retaining the entire
registry in memory:

```go
func stream(ctx context.Context, output io.Writer) error {
	client, err := controller.NewScraper(controller.Options{})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	return client.Scrape(ctx, storage.SinkFunc(func(ctx context.Context, drug model.Drug) error {
		return encoder.Encode(drug)
	}))
}
```

Import `context`, `encoding/json`, `io`, `controller`, `model`, and `storage` for
this example. The caller controls cancellation using `context.Context`. A sink
error stops the run and is returned to the caller.

## Concurrency, pacing, and retries

```go
client, err := controller.NewScraper(controller.Options{
	MaxWorkers:      3,                 // concurrent detail requests
	RequestInterval: 500 * time.Millisecond,
	MaxRetries:      4,
	Backoff:         time.Second,
	RequestTimeout:  30 * time.Second,
})
```

The default is three concurrent detail workers, with at least 500 ms between
requests across the client, exponential backoff for transient failures, and
`Retry-After` support for HTTP 429 responses. Increase concurrency only
responsibly; excessive requests may cause rate limiting or burden the public
service.

## MVC-style project layout

```text
go-sfda/
├── controller/   # SFDA HTTP workflow, pagination, concurrent detail requests
├── model/        # Drug model and reusable filtering
├── storage/      # Database-neutral sink interface
├── view/         # Excel workbook output
├── go.mod
└── README.md
```

This is a small MVC-inspired organization adapted for a Go library: models hold
the domain data, controllers coordinate the scrape, views format exports, and
storage defines an adapter boundary.

## Run tests

```sh
go test ./...
go test -race ./...
```

The tests use a local HTTP test server and do not crawl SFDA.
"# go-sfda" 

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScrapeUsesFrontendAJAXAndContinuesAcrossEmptyPage(t *testing.T) {
	var detailsCalls atomic.Int32
	var activeDetails atomic.Int32
	var maxActiveDetails atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/en/drugs-list":
			page := request.URL.Query().Get("page")
			switch page {
			case "0":
				rows := strings.Builder{}
				for id := 123; id < 127; id++ {
					fmt.Fprintf(&rows, `<tr><td>Example ingredient</td><td>Example drug %d</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=%d&page=0">Details</a></td></tr>`, id, id)
				}
				fmt.Fprint(writer, listHTML(1, rows.String()))
			case "1":
				fmt.Fprint(writer, `<html><script data-drupal-selector="drupal-settings-json">{"ajaxPageState":{"libraries":"test-state"}}</script><div>No Results Found</div></html>`)
			default:
				http.NotFound(writer, request)
			}
		case "/en/details_data":
			detailsCalls.Add(1)
			active := activeDetails.Add(1)
			defer activeDetails.Add(-1)
			for previous := maxActiveDetails.Load(); active > previous; previous = maxActiveDetails.Load() {
				if maxActiveDetails.CompareAndSwap(previous, active) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			if request.Method != http.MethodPost {
				http.Error(writer, "expected POST", http.StatusMethodNotAllowed)
				return
			}
			if request.URL.Query().Get("_wrapper_format") != "drupal_bootstrap4_modal" {
				http.Error(writer, "missing wrapper format", http.StatusBadRequest)
				return
			}
			if err := request.ParseForm(); err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}
			if request.Form.Get("ajax_page_state[libraries]") != "test-state" {
				http.Error(writer, "missing AJAX state", http.StatusBadRequest)
				return
			}
			id := request.URL.Query().Get("id")
			commands := []map[string]string{{
				"command": "openBootstrap4Dialog",
				"data":    fmt.Sprintf(`<table><tr><th>Register Number</th><td>%s</td></tr><tr><th>Trade Name</th><td>Example drug %s</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr><tr><th>Price</th><td>5.00</td></tr><tr><th>Legal Status</th><td>Prescription</td></tr></table>`, id, id),
			}}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(commands)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		MaxWorkers:      2,
		RequestInterval: time.Nanosecond,
		Backoff:         time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := scraper.FetchAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4", len(records))
	}
	if records[0].RegistrationNumber != "123" ||
		records[0].ManufacturingCompany != "Example Labs" ||
		records[0].CountryOfManufacture != "Saudi Arabia" {
		t.Fatalf("unexpected record: %#v", records[0])
	}
	if detailsCalls.Load() != 4 {
		t.Fatalf("detail requests = %d, want 4", detailsCalls.Load())
	}
	if got := maxActiveDetails.Load(); got < 2 || got > 2 {
		t.Fatalf("maximum concurrent detail requests = %d, want 2", got)
	}
}

func TestParseListPageRejectsUnexpectedHTML(t *testing.T) {
	_, err := parseListPage([]byte(`<html><body>Access denied</body></html>`), 4)
	if err == nil {
		t.Fatal("expected an error for an unexpected page")
	}
}

func TestScrapeMaxPagesLimitsLiveTestScope(t *testing.T) {
	var listCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/en/drugs-list":
			listCalls.Add(1)
			if request.URL.Query().Get("page") != "0" {
				http.Error(writer, "unexpected page request", http.StatusBadRequest)
				return
			}
			row := `<tr><td>Example ingredient</td><td>Example drug</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=123&page=0">Details</a></td></tr>`
			fmt.Fprint(writer, listHTML(459, row))
		case "/en/details_data":
			commands := []map[string]string{{
				"command": "openBootstrap4Dialog",
				"data":    `<table><tr><th>Register Number</th><td>123</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr></table>`,
			}}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(commands)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		MaxPages:        1,
		RequestInterval: time.Nanosecond,
		Backoff:         time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := scraper.FetchAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RegistrationNumber != "123" {
		t.Fatalf("got records %#v, want one record with registration number 123", records)
	}
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("list page requests = %d, want 1", got)
	}
}

func TestNewScraperRejectsNegativeMaxPages(t *testing.T) {
	_, err := NewScraper(Options{MaxPages: -1})
	if err == nil {
		t.Fatal("expected negative MaxPages to be rejected")
	}
}

func listHTML(lastPage int, row string) string {
	return fmt.Sprintf(`<html>
<script data-drupal-selector="drupal-settings-json">{"ajaxPageState":{"libraries":"test-state"}}</script>
<table class="table responsive-enabled"><tbody>%s</tbody></table>
<a href="?page=%d">Last</a>
</html>`, row, lastPage)
}

func TestDetailURLMustRemainOnConfiguredHost(t *testing.T) {
	scraper, err := NewScraper(Options{BaseURL: "https://www.sfda.gov.sa"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = scraper.fetchDetails(context.Background(), "https://example.com/en/details_data?id=1", "libraries")
	if err == nil || !strings.Contains(err.Error(), "does not match SFDA host") {
		t.Fatalf("expected host validation error, got %v", err)
	}
}

func TestPageIndexFromHref(t *testing.T) {
	index, ok := pageIndexFromHref("?page=459")
	if !ok || index != 459 {
		t.Fatalf("got (%d, %t), want (459, true)", index, ok)
	}
	_, ok = pageIndexFromHref("not a URL")
	if ok {
		t.Fatal("expected invalid page link to be rejected")
	}
}

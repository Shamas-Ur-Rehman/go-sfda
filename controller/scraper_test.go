package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
	"github.com/Shamas-Ur-Rehman/go-sfda/storage"
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

func TestParseListPageReportsMalformedDrupalState(t *testing.T) {
	body := []byte(`<html><script data-drupal-selector="drupal-settings-json">{bad json}</script><table class="responsive-enabled"><tbody><tr><td>Ingredient</td><td>Drug</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?id=1">Details</a></td></tr></tbody></table></html>`)
	_, err := parseListPage(body, 0)
	if err == nil || !strings.Contains(err.Error(), "invalid Drupal AJAX state") {
		t.Fatalf("expected a descriptive Drupal state error, got %v", err)
	}
}

func TestParseDetailTablePreservesArabicText(t *testing.T) {
	fields, err := parseDetailTable(`<table><tr><th>Trade Name</th><td>دواء تجريبي</td></tr></table>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := fields["Trade Name"]; got != "دواء تجريبي" {
		t.Fatalf("Arabic trade name = %q", got)
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

func TestRequestRetriesTransientStatusesAndHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			http.Error(writer, "temporary", http.StatusServiceUnavailable)
		case 2:
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, "rate limited", http.StatusTooManyRequests)
		default:
			_, _ = writer.Write([]byte("ok"))
		}
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		RequestInterval: time.Nanosecond,
		Backoff:         time.Millisecond,
		MaxBackoff:      time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := scraper.request(context.Background(), http.MethodGet, server.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" || calls.Load() != 3 {
		t.Fatalf("body=%q calls=%d, want ok and 3 calls", body, calls.Load())
	}
}

func TestRequestRetriesSupportedServerStatuses(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					http.Error(writer, "retry", status)
					return
				}
				_, _ = writer.Write([]byte("ok"))
			}))
			defer server.Close()
			scraper, err := NewScraper(Options{
				BaseURL:         server.URL,
				RequestInterval: time.Nanosecond,
				Backoff:         time.Nanosecond,
				MaxBackoff:      time.Nanosecond,
				MaxRetries:      2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := scraper.request(context.Background(), http.MethodGet, server.URL, nil, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatalf("requests = %d, want 2", calls.Load())
			}
		})
	}
}

func TestRetryAfterParsesDelayAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("3", now); got != 3*time.Second {
		t.Fatalf("delta Retry-After = %s, want 3s", got)
	}
	if got := parseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Fatalf("date Retry-After = %s, want 5s", got)
	}
	if got := parseRetryAfter("invalid", now); got != 0 {
		t.Fatalf("invalid Retry-After = %s, want 0", got)
	}
}

func TestConfiguredTimeoutAppliesToCustomHTTPClientWithoutTimeout(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		HTTPClient:      server.Client(),
		RequestTimeout:  25 * time.Millisecond,
		RequestInterval: time.Nanosecond,
		MaxRetries:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = scraper.request(context.Background(), http.MethodGet, server.URL, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected request timeout, got %v", err)
	}
	<-started
}

func TestRequestDoesNotRetryNonRetryableStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(writer, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{BaseURL: server.URL, RequestInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = scraper.request(context.Background(), http.MethodGet, server.URL, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d, want one non-retried 404", err, calls.Load())
	}
}

func TestRequestIntervalPacesConcurrentRequestStarts(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		RequestInterval: 100 * time.Millisecond,
		MaxRetries:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			_, requestErr := scraper.request(context.Background(), http.MethodGet, server.URL, nil, nil)
			errorsFound <- requestErr
		}()
	}
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("request start count = %d, want 2", len(starts))
	}
	if interval := starts[1].Sub(starts[0]); interval < 80*time.Millisecond {
		t.Fatalf("request starts were only %s apart, want at least 80ms", interval)
	}
}

func TestScrapeFetchesDuplicateDetailURLOnce(t *testing.T) {
	var detailCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/en/drugs-list":
			row := `<tr><td>Ingredient</td><td>Drug</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=100&page=0">Details</a></td></tr>`
			fmt.Fprint(writer, listHTML(0, row+row))
		case "/en/details_data":
			detailCalls.Add(1)
			commands := []map[string]string{{
				"command": "openBootstrap4Dialog",
				"data":    `<table><tr><th>Register Number</th><td>100</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr></table>`,
			}}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(commands)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{BaseURL: server.URL, RequestInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	records, err := scraper.FetchAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || detailCalls.Load() != 1 {
		t.Fatalf("records=%d detail calls=%d, want 2 records from one request", len(records), detailCalls.Load())
	}
}

func TestRequestContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()

	scraper, err := NewScraper(Options{BaseURL: server.URL, RequestInterval: time.Nanosecond, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, requestErr := scraper.request(ctx, http.MethodGet, server.URL, nil, nil)
		result <- requestErr
	}()
	<-started
	cancel()
	if err := <-result; err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestScrapeCheckpointResumesAfterLastCommittedPage(t *testing.T) {
	var detailsByID sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/en/drugs-list":
			page := request.URL.Query().Get("page")
			id := map[string]string{"0": "100", "1": "101", "2": "102"}[page]
			if id == "" {
				http.NotFound(writer, request)
				return
			}
			row := fmt.Sprintf(`<tr><td>Ingredient</td><td>Drug %s</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=%s&page=%s">Details</a></td></tr>`, id, id, page)
			fmt.Fprint(writer, listHTML(2, row))
		case "/en/details_data":
			id := request.URL.Query().Get("id")
			current, _ := detailsByID.LoadOrStore(id, &atomic.Int32{})
			current.(*atomic.Int32).Add(1)
			commands := []map[string]string{{
				"command": "openBootstrap4Dialog",
				"data":    fmt.Sprintf(`<table><tr><th>Register Number</th><td>%s</td></tr><tr><th>Trade Name</th><td>Drug %s</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr></table>`, id, id),
			}}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(commands)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	checkpointPath := filepath.Join(t.TempDir(), "resume.json")
	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		RequestInterval: time.Nanosecond,
		Backoff:         time.Nanosecond,
		CheckpointPath:  checkpointPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	storeErr := fmt.Errorf("simulated storage failure")
	err = scraper.Scrape(context.Background(), storage.SinkFunc(func(_ context.Context, drug model.Drug) error {
		if drug.RegistrationNumber == "101" {
			return storeErr
		}
		return nil
	}))
	if !errors.Is(err, storeErr) {
		t.Fatalf("expected storage failure, got %v", err)
	}
	checkpointStore, err := storage.NewJSONCheckpointStore(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := checkpointStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCompletedPage != 0 || checkpoint.InProgressPage == nil ||
		*checkpoint.InProgressPage != 1 || !reflect.DeepEqual(checkpoint.FailedPages, []int{1}) {
		t.Fatalf("unexpected failed checkpoint: %+v", checkpoint)
	}

	var stored []model.Drug
	if err := scraper.Scrape(context.Background(), storage.SinkFunc(func(_ context.Context, drug model.Drug) error {
		stored = append(stored, drug)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].RegistrationNumber != "101" || stored[1].RegistrationNumber != "102" {
		t.Fatalf("unexpected resumed records: %#v", stored)
	}
	for id, expected := range map[string]int32{"100": 1, "101": 2, "102": 1} {
		count, ok := detailsByID.Load(id)
		if !ok || count.(*atomic.Int32).Load() != expected {
			t.Fatalf("details calls for %s = %v, want %d", id, count, expected)
		}
	}
	checkpoint, err = checkpointStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCompletedPage != 2 || checkpoint.InProgressPage != nil || len(checkpoint.FailedPages) != 0 {
		t.Fatalf("unexpected completed checkpoint: %+v", checkpoint)
	}
}

func TestScrapeCheckpointRejectsDifferentFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/en/drugs-list":
			row := `<tr><td>Ingredient</td><td>Drug</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=100&page=0">Details</a></td></tr>`
			fmt.Fprint(writer, listHTML(0, row))
		case "/en/details_data":
			commands := []map[string]string{{
				"command": "openBootstrap4Dialog",
				"data":    `<table><tr><th>Register Number</th><td>100</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr></table>`,
			}}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(commands)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	checkpointPath := filepath.Join(t.TempDir(), "resume.json")
	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		RequestInterval: time.Nanosecond,
		CheckpointPath:  checkpointPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scraper.Scrape(context.Background(), storage.SinkFunc(func(context.Context, model.Drug) error { return nil })); err != nil {
		t.Fatal(err)
	}
	err = scraper.Scrape(context.Background(), storage.SinkFunc(func(context.Context, model.Drug) error { return nil }),
		model.Filter{Field: "TradeName", Value: "other"})
	if err == nil || !strings.Contains(err.Error(), "different scrape configuration") {
		t.Fatalf("expected configuration mismatch, got %v", err)
	}
}

type failOnceFlushSink struct {
	flushes int
}

func (s *failOnceFlushSink) Store(context.Context, model.Drug) error { return nil }

func (s *failOnceFlushSink) Flush(context.Context) error {
	s.flushes++
	if s.flushes == 1 {
		return fmt.Errorf("simulated batch flush failure")
	}
	return nil
}

func TestCheckpointDoesNotCommitPageUntilSinkFlushSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/en/drugs-list":
			row := `<tr><td>Ingredient</td><td>Drug</td><td>10</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=100&page=0">Details</a></td></tr>`
			fmt.Fprint(writer, listHTML(0, row))
		case "/en/details_data":
			commands := []map[string]string{{
				"command": "openBootstrap4Dialog",
				"data":    `<table><tr><th>Register Number</th><td>100</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr></table>`,
			}}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(commands)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	scraper, err := NewScraper(Options{
		BaseURL:         server.URL,
		RequestInterval: time.Nanosecond,
		CheckpointPath:  checkpointPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &failOnceFlushSink{}
	if err := scraper.Scrape(context.Background(), sink); err == nil {
		t.Fatal("expected the first flush to fail")
	}
	checkpointStore, err := storage.NewJSONCheckpointStore(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := checkpointStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCompletedPage != -1 || checkpoint.InProgressPage == nil || *checkpoint.InProgressPage != 0 {
		t.Fatalf("failed flush incorrectly committed page: %+v", checkpoint)
	}
	if err := scraper.Scrape(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	checkpoint, err = checkpointStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCompletedPage != 0 || checkpoint.InProgressPage != nil || len(checkpoint.FailedPages) != 0 {
		t.Fatalf("successful retry did not commit page: %+v", checkpoint)
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

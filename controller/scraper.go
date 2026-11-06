// Package controller contains the scraping use case and its HTTP client.
package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
	"github.com/Shamas-Ur-Rehman/go-sfda/storage"
	"golang.org/x/net/html"
)

const (
	defaultBaseURL        = "https://www.sfda.gov.sa"
	defaultListPath       = "/en/drugs-list"
	defaultWorkers        = 3
	defaultRequestGap     = 500 * time.Millisecond
	defaultRequestTimeout = 30 * time.Second
	defaultMaxRetries     = 4
	defaultBackoff        = time.Second
	defaultMaxBackoff     = 30 * time.Second
	defaultRetryJitter    = 0.2
	maxResponseBytes      = 8 << 20
)

// Options controls network behavior. Set HTTPClient and BaseURL to use custom
// transports or to test against an HTTP test server.
type Options struct {
	BaseURL         string
	ListPath        string
	HTTPClient      *http.Client
	MaxWorkers      int
	MaxPages        int
	RequestInterval time.Duration
	RequestTimeout  time.Duration
	MaxRetries      int
	Backoff         time.Duration
	MaxBackoff      time.Duration
	RetryJitter     float64
	CheckpointStore storage.CheckpointStore
	CheckpointPath  string
}

// Scraper fetches the public SFDA list and the detail pages used by its frontend.
type Scraper struct {
	baseURL         *url.URL
	listPath        string
	httpClient      *http.Client
	maxWorkers      int
	maxPages        int
	requestInterval time.Duration
	maxRetries      int
	backoff         time.Duration
	maxBackoff      time.Duration
	retryJitter     float64
	checkpointStore storage.CheckpointStore
	requestMu       sync.Mutex
	lastRequest     time.Time
}

// NewScraper creates a scraper with conservative concurrency and request pacing.
func NewScraper(options Options) (*Scraper, error) {
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("invalid SFDA base URL %q", baseURL)
	}

	listPath := options.ListPath
	if listPath == "" {
		listPath = defaultListPath
	}
	if !strings.HasPrefix(listPath, "/") {
		return nil, fmt.Errorf("list path must start with '/': %q", listPath)
	}

	workers := options.MaxWorkers
	if workers <= 0 {
		workers = defaultWorkers
	}
	if options.MaxPages < 0 {
		return nil, fmt.Errorf("maximum pages cannot be negative")
	}
	if options.CheckpointStore != nil && options.CheckpointPath != "" {
		return nil, fmt.Errorf("set either CheckpointStore or CheckpointPath, not both")
	}
	if options.RetryJitter < 0 || options.RetryJitter > 1 {
		return nil, fmt.Errorf("retry jitter must be between 0 and 1")
	}
	if options.MaxBackoff < 0 {
		return nil, fmt.Errorf("maximum backoff cannot be negative")
	}
	requestInterval := options.RequestInterval
	if requestInterval < 0 {
		return nil, fmt.Errorf("request interval cannot be negative")
	}
	if requestInterval == 0 {
		requestInterval = defaultRequestGap
	}
	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	retries := options.MaxRetries
	if retries <= 0 {
		retries = defaultMaxRetries
	}
	backoff := options.Backoff
	if backoff < 0 {
		return nil, fmt.Errorf("backoff cannot be negative")
	}
	if backoff == 0 {
		backoff = defaultBackoff
	}
	maxBackoff := options.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	if maxBackoff < backoff {
		maxBackoff = backoff
	}
	retryJitter := options.RetryJitter
	if retryJitter == 0 {
		retryJitter = defaultRetryJitter
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else {
		clientCopy := *client
		if clientCopy.Timeout <= 0 {
			clientCopy.Timeout = timeout
		}
		client = &clientCopy
	}
	checkpointStore := options.CheckpointStore
	if options.CheckpointPath != "" {
		checkpointStore, err = storage.NewJSONCheckpointStore(options.CheckpointPath)
		if err != nil {
			return nil, err
		}
	}

	return &Scraper{
		baseURL:         parsedBaseURL,
		listPath:        listPath,
		httpClient:      client,
		maxWorkers:      workers,
		maxPages:        options.MaxPages,
		requestInterval: requestInterval,
		maxRetries:      retries,
		backoff:         backoff,
		maxBackoff:      maxBackoff,
		retryJitter:     retryJitter,
		checkpointStore: checkpointStore,
	}, nil
}

// FetchAll scrapes the configured pages, applies optional filters, and returns
// matching records in the same order as the website. By default all pages are
// scraped; Options.MaxPages can limit a run.
func (s *Scraper) FetchAll(ctx context.Context, filters ...model.Filter) ([]model.Drug, error) {
	var records []model.Drug
	sink := storage.SinkFunc(func(_ context.Context, drug model.Drug) error {
		records = append(records, drug)
		return nil
	})
	if err := s.Scrape(ctx, sink, filters...); err != nil {
		return nil, err
	}
	return records, nil
}

// Scrape fetches records page by page and passes matching records to sink.
// The sink is called serially; database-specific concurrency can be implemented
// by the caller without requiring database drivers in this package.
func (s *Scraper) Scrape(ctx context.Context, sink storage.Sink, filters ...model.Filter) error {
	if sink == nil {
		return fmt.Errorf("scrape sink cannot be nil")
	}
	if _, err := model.FilterDrugs(nil, filters...); err != nil {
		return err
	}
	checkpoint := storage.NewCheckpoint()
	if s.checkpointStore != nil {
		var err error
		checkpoint, err = s.checkpointStore.Load()
		if err != nil {
			return fmt.Errorf("load scrape checkpoint: %w", err)
		}
		fingerprint, err := scrapeFingerprint(s.baseURL.String(), s.listPath, s.maxPages, filters)
		if err != nil {
			return fmt.Errorf("create scrape checkpoint fingerprint: %w", err)
		}
		if checkpoint.QueryFingerprint != "" && checkpoint.QueryFingerprint != fingerprint {
			return fmt.Errorf("checkpoint belongs to a different scrape configuration; use a separate checkpoint path or remove the old checkpoint")
		}
		if checkpoint.QueryFingerprint == "" {
			if checkpoint.LastCompletedPage >= 0 || checkpoint.InProgressPage != nil || len(checkpoint.FailedPages) > 0 {
				return fmt.Errorf("checkpoint has progress but no configuration fingerprint; remove it and start a new scrape")
			}
			checkpoint.QueryFingerprint = fingerprint
		}
	}
	firstPage, err := s.fetchListPage(ctx, 0)
	if err != nil {
		return fmt.Errorf("fetch SFDA list page 0: %w", err)
	}
	list, err := parseListPage(firstPage, 0)
	if err != nil {
		return fmt.Errorf("parse SFDA list page 0: %w", err)
	}
	totalPages := list.TotalPages
	if s.maxPages > 0 && s.maxPages < totalPages {
		totalPages = s.maxPages
	}

	startPage := checkpoint.LastCompletedPage + 1
	if startPage < 0 {
		startPage = 0
	}
	for page := startPage; page < totalPages; page++ {
		if err := ctx.Err(); err != nil {
			return s.failCheckpointPage(checkpoint, page, err)
		}
		if s.checkpointStore != nil {
			checkpoint.InProgressPage = intPointer(page)
			if err := s.checkpointStore.Save(checkpoint); err != nil {
				return fmt.Errorf("save checkpoint before page %d: %w", page, err)
			}
		}
		pageData := list
		if page != 0 {
			pageBody, fetchErr := s.fetchListPage(ctx, page)
			if fetchErr != nil {
				return s.failCheckpointPage(checkpoint, page, fmt.Errorf("fetch SFDA list page %d: %w", page, fetchErr))
			}
			pageData, err = parseListPage(pageBody, page)
			if err != nil {
				return s.failCheckpointPage(checkpoint, page, fmt.Errorf("parse SFDA list page %d: %w", page, err))
			}
		}

		drugs, fetchErr := s.fetchPageDetails(ctx, pageData)
		if fetchErr != nil {
			return s.failCheckpointPage(checkpoint, page, fmt.Errorf("fetch drug details on SFDA list page %d: %w", page, fetchErr))
		}
		filtered, filterErr := model.FilterDrugs(drugs, filters...)
		if filterErr != nil {
			return s.failCheckpointPage(checkpoint, page, filterErr)
		}
		for _, drug := range filtered {
			if err := ctx.Err(); err != nil {
				return s.failCheckpointPage(checkpoint, page, err)
			}
			if err := sink.Store(ctx, drug); err != nil {
				return s.failCheckpointPage(checkpoint, page, fmt.Errorf("store drug %q: %w", drug.RegistrationNumber, err))
			}
		}
		if flusher, ok := sink.(storage.Flusher); ok {
			if err := flusher.Flush(ctx); err != nil {
				return s.failCheckpointPage(checkpoint, page, fmt.Errorf("flush storage after SFDA list page %d: %w", page, err))
			}
		}
		if s.checkpointStore != nil {
			checkpoint.LastCompletedPage = page
			checkpoint.InProgressPage = nil
			checkpoint.FailedPages = removePage(checkpoint.FailedPages, page)
			if err := s.checkpointStore.Save(checkpoint); err != nil {
				return fmt.Errorf("save checkpoint after page %d: %w", page, err)
			}
		}
	}
	return nil
}

func (s *Scraper) failCheckpointPage(checkpoint storage.Checkpoint, page int, cause error) error {
	if s.checkpointStore == nil {
		return cause
	}
	checkpoint.InProgressPage = intPointer(page)
	checkpoint.FailedPages = addPage(checkpoint.FailedPages, page)
	if err := s.checkpointStore.Save(checkpoint); err != nil {
		return errors.Join(cause, fmt.Errorf("save failed-page checkpoint %d: %w", page, err))
	}
	return cause
}

func intPointer(value int) *int {
	return &value
}

func scrapeFingerprint(baseURL, listPath string, maxPages int, filters []model.Filter) (string, error) {
	configuration := struct {
		BaseURL  string
		ListPath string
		MaxPages int
		Filters  []model.Filter
	}{
		BaseURL:  baseURL,
		ListPath: listPath,
		MaxPages: maxPages,
		Filters:  filters,
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func addPage(pages []int, page int) []int {
	for _, existing := range pages {
		if existing == page {
			return pages
		}
	}
	return append(pages, page)
}

func removePage(pages []int, page int) []int {
	result := pages[:0]
	for _, existing := range pages {
		if existing != page {
			result = append(result, existing)
		}
	}
	return result
}

type detailResult struct {
	drug model.Drug
	err  error
}

func (s *Scraper) fetchPageDetails(ctx context.Context, page listPage) ([]model.Drug, error) {
	results := make([]detailResult, len(page.Drugs))
	type detailGroup struct {
		detailURL string
		indices   []int
	}
	groups := make([]detailGroup, 0, len(page.Drugs))
	groupByURL := make(map[string]int, len(page.Drugs))
	for index, summary := range page.Drugs {
		groupIndex, ok := groupByURL[summary.DetailURL]
		if !ok {
			groupIndex = len(groups)
			groupByURL[summary.DetailURL] = groupIndex
			groups = append(groups, detailGroup{detailURL: summary.DetailURL})
		}
		groups[groupIndex].indices = append(groups[groupIndex].indices, index)
	}
	jobs := make(chan int, s.maxWorkers)
	var waitGroup sync.WaitGroup
	workerCount := s.maxWorkers
	if workerCount > len(groups) {
		workerCount = len(groups)
	}
	for worker := 0; worker < workerCount; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for groupIndex := range jobs {
				group := groups[groupIndex]
				details, err := s.fetchDetails(ctx, group.detailURL, page.AjaxLibraries)
				for _, index := range group.indices {
					if err != nil {
						results[index].err = err
						continue
					}
					results[index].drug, results[index].err = mergeDrug(page.Drugs[index].Drug, details)
				}
			}
		}()
	}
	for groupIndex := range groups {
		select {
		case jobs <- groupIndex:
		case <-ctx.Done():
			for remaining := groupIndex; remaining < len(groups); remaining++ {
				for _, index := range groups[remaining].indices {
					results[index].err = ctx.Err()
				}
			}
			close(jobs)
			waitGroup.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	waitGroup.Wait()

	drugs := make([]model.Drug, 0, len(results))
	var failures []error
	for i, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("drug %q: %w", page.Drugs[i].Drug["registration_number"], result.err))
			continue
		}
		drugs = append(drugs, result.drug)
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return drugs, nil
}

func (s *Scraper) fetchListPage(ctx context.Context, page int) ([]byte, error) {
	requestURL := *s.baseURL
	requestURL.Path = strings.TrimRight(s.baseURL.Path, "/") + s.listPath
	query := requestURL.Query()
	query.Set("page", fmt.Sprint(page))
	requestURL.RawQuery = query.Encode()
	return s.request(ctx, http.MethodGet, requestURL.String(), nil, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, detailHref, libraries string) (map[string]string, error) {
	detailURL, err := url.Parse(detailHref)
	if err != nil {
		return nil, fmt.Errorf("parse details URL %q: %w", detailHref, err)
	}
	detailURL = s.baseURL.ResolveReference(detailURL)
	if detailURL.Host != s.baseURL.Host {
		return nil, fmt.Errorf("details URL host %q does not match SFDA host", detailURL.Host)
	}
	query := detailURL.Query()
	query.Set("_wrapper_format", "drupal_bootstrap4_modal")
	detailURL.RawQuery = query.Encode()

	payload := url.Values{
		"js":                           {"true"},
		"dialogOptions[dialogClasses]": {"modal-lg"},
		"_drupal_ajax":                 {"1"},
		"ajax_page_state[theme]":       {"sfda_main"},
		"ajax_page_state[theme_token]": {""},
		"ajax_page_state[libraries]":   {libraries},
	}
	body, err := s.request(ctx, http.MethodPost, detailURL.String(), strings.NewReader(payload.Encode()), map[string]string{
		"Accept":           "application/json, text/javascript, */*; q=0.01",
		"Content-Type":     "application/x-www-form-urlencoded; charset=UTF-8",
		"X-Requested-With": "XMLHttpRequest",
		"Referer":          s.baseURL.ResolveReference(&url.URL{Path: s.listPath}).String(),
	})
	if err != nil {
		return nil, err
	}
	var commands []struct {
		Command string `json:"command"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(body, &commands); err != nil {
		return nil, fmt.Errorf("decode Drupal details response: %w", err)
	}
	for _, command := range commands {
		if command.Command != "openBootstrap4Dialog" || command.Data == "" {
			continue
		}
		return parseDetailTable(command.Data)
	}
	return nil, fmt.Errorf("Drupal details response did not include an openBootstrap4Dialog command")
}

func (s *Scraper) request(
	ctx context.Context,
	method string,
	requestURL string,
	body io.Reader,
	headers map[string]string,
) ([]byte, error) {
	var requestBody []byte
	var err error
	if body != nil {
		requestBody, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}
	for attempt := 0; attempt < s.maxRetries; attempt++ {
		var requestReader io.Reader
		if body != nil {
			requestReader = bytes.NewReader(requestBody)
		}
		request, err := http.NewRequestWithContext(ctx, method, requestURL, requestReader)
		if err != nil {
			return nil, fmt.Errorf("create %s request: %w", method, err)
		}
		request.Header.Set("User-Agent", "sfda-go-scraper/1.0 (+public-data-client)")
		request.Header.Set("Accept-Language", "en-US,en;q=0.9")
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		if err := s.waitForRequestSlot(ctx); err != nil {
			return nil, err
		}

		response, requestErr := s.httpClient.Do(request)
		if requestErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt+1 == s.maxRetries {
				return nil, fmt.Errorf("%s %s failed after %d attempts: %w", method, requestURL, s.maxRetries, requestErr)
			}
			if err := sleepContext(ctx, s.retryDelay(attempt)); err != nil {
				return nil, err
			}
			continue
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt+1 == s.maxRetries {
				return nil, fmt.Errorf("read %s response after %d attempts: %w", method, s.maxRetries, readErr)
			}
			wait := s.retryDelay(attempt)
			if retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now()); retryAfter > wait {
				wait = retryAfter
			}
			if err := sleepContext(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close %s response: %w", method, closeErr)
		}
		if len(responseBody) > maxResponseBytes {
			return nil, fmt.Errorf("%s %s response exceeds %d bytes", method, requestURL, maxResponseBytes)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return responseBody, nil
		}
		if !retryableStatus(response.StatusCode) {
			return nil, fmt.Errorf("%s %s returned HTTP %d: %s", method, requestURL, response.StatusCode, strings.TrimSpace(string(responseBody)))
		}
		if attempt+1 == s.maxRetries {
			return nil, fmt.Errorf("%s %s returned HTTP %d after %d attempts", method, requestURL, response.StatusCode, s.maxRetries)
		}
		wait := s.retryDelay(attempt)
		if retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now()); retryAfter > wait {
			wait = retryAfter
		}
		if err := sleepContext(ctx, wait); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("request attempts exhausted")
}

func (s *Scraper) waitForRequestSlot(ctx context.Context) error {
	for {
		s.requestMu.Lock()
		wait := s.requestInterval - time.Since(s.lastRequest)
		if s.lastRequest.IsZero() || wait <= 0 {
			s.lastRequest = time.Now()
			s.requestMu.Unlock()
			return nil
		}
		s.requestMu.Unlock()
		if err := sleepContext(ctx, wait); err != nil {
			return err
		}
	}
}

func (s *Scraper) retryDelay(attempt int) time.Duration {
	delay := s.backoff
	for i := 0; i < attempt && delay < s.maxBackoff; i++ {
		if delay > s.maxBackoff/2 {
			delay = s.maxBackoff
			break
		}
		delay *= 2
	}
	if delay > s.maxBackoff {
		delay = s.maxBackoff
	}
	if s.retryJitter > 0 {
		factor := 1 - s.retryJitter + rand.Float64()*2*s.retryJitter
		delay = time.Duration(float64(delay) * factor)
	}
	if delay > s.maxBackoff {
		delay = s.maxBackoff
	}
	return delay
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		if seconds <= 0 {
			return 0
		}
		if int64(seconds) > int64((1<<63-1)/int64(time.Second)) {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type detailsParser struct {
	fields   map[string]string
	inRow    bool
	cell     string
	cells    []string
	cellText strings.Builder
}

func parseDetailTable(fragment string) (map[string]string, error) {
	root, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return nil, fmt.Errorf("parse drug detail HTML: %w", err)
	}
	parser := &detailsParser{fields: make(map[string]string)}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "tr" {
			parser.finishRow()
			parser.inRow = true
			parser.cells = nil
		} else if parser.inRow && node.Type == html.ElementNode && (node.Data == "th" || node.Data == "td") {
			parser.cell = node.Data
			parser.cellText.Reset()
		} else if parser.inRow && parser.cell == "td" && node.Type == html.ElementNode && node.Data == "br" {
			parser.cellText.WriteByte('\n')
		} else if parser.cell != "" && node.Type == html.TextNode {
			parser.cellText.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if node.Type == html.ElementNode && (node.Data == "th" || node.Data == "td") {
			parser.finishCell()
		}
		if node.Type == html.ElementNode && node.Data == "tr" {
			parser.finishRow()
		}
	}
	visit(root)
	parser.finishRow()
	if len(parser.fields) == 0 {
		return nil, fmt.Errorf("drug details contained no label/value rows")
	}
	return parser.fields, nil
}

func (p *detailsParser) finishCell() {
	if p.cell != "" {
		p.cells = append(p.cells, strings.TrimSpace(p.cellText.String()))
		p.cell = ""
		p.cellText.Reset()
	}
}

func (p *detailsParser) finishRow() {
	if p.inRow && len(p.cells) >= 2 {
		label := strings.Join(strings.Fields(p.cells[0]), " ")
		if label != "" {
			p.fields[label] = strings.TrimSpace(p.cells[1])
		}
	}
	p.inRow = false
	p.cells = nil
}

var manufacturerPattern = regexp.MustCompile(`(?m)Name:\s*(.*?)\s*\|\s*Country:\s*(.*)`)

func mergeDrug(summary, details map[string]string) (model.Drug, error) {
	get := func(label string) string { return strings.TrimSpace(details[label]) }
	manufacturers := manufacturerPattern.FindAllStringSubmatch(get("Manufacturers"), -1)
	if len(manufacturers) == 0 {
		return model.Drug{}, fmt.Errorf("drug details do not contain a recognized manufacturer entry")
	}
	manufacturerNames := make([]string, 0, len(manufacturers))
	manufacturerCountries := make([]string, 0, len(manufacturers))
	for _, item := range manufacturers {
		manufacturerNames = append(manufacturerNames, strings.TrimSpace(item[1]))
		manufacturerCountries = append(manufacturerCountries, strings.TrimSpace(item[2]))
	}

	drug := model.Drug{
		TradeName:                    firstNonEmpty(get("Trade Name"), summary["trade_name"]),
		ScientificName:               firstNonEmpty(get("Scientific Name"), summary["scientific_name"]),
		RegistrationNumber:           firstNonEmpty(get("Register Number"), summary["registration_number"]),
		ManufacturingCompany:         manufacturerNames[0],
		CountryOfManufacture:         manufacturerCountries[0],
		AdditionalManufacturers:      strings.Join(manufacturerNames[1:], "; "),
		AdditionalManufacturerPlaces: strings.Join(manufacturerCountries[1:], "; "),
		MarketingCompany:             get("Marketing Company"),
		MarketingCompanyCountry:      get("Country of marketing company"),
		Agents:                       get("Agents"),
		DrugDomain:                   firstNonEmpty(get("Drug Domain"), get("Domain")),
		DrugType:                     get("Drug Type"),
		DosageForm:                   firstNonEmpty(get("Dosage Form"), summary["dosage_form"]),
		RouteOfAdministration:        get("Route of Administration"),
		Strength:                     firstNonEmpty(get("Strength"), summary["strength"]),
		StrengthUnit:                 get("Strength Unit"),
		PackageType:                  get("Package Type"),
		PackageSize:                  get("Package Size"),
		Size:                         get("Size"),
		SizeUnit:                     get("Size Unit"),
		Price:                        firstNonEmpty(get("Price"), summary["price"]),
		MarketingStatus:              get("Marketing Status"),
		AuthorizationStatus:          get("Authorization Status"),
		LegalStatus:                  get("Legal Status"),
		RegistrationYear:             get("Registration Year"),
		ProductControl:               get("Product Control"),
		DistributionSite:             get("Distribution Site"),
		StorageConditions:            get("Storage Conditions"),
		ShelfLife:                    get("Shelf Life"),
		ATCCode1:                     get("ATC Code 1"),
		ATCCode2:                     get("ATC Code 2"),
		DescriptionCode:              get("Description Code"),
	}
	return drug, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// Package controller contains the scraping use case and its HTTP client.
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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
	requestSlots    chan struct{}
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
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
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
		requestSlots:    make(chan struct{}, 1),
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

	for page := 0; page < totalPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		pageData := list
		if page != 0 {
			pageBody, fetchErr := s.fetchListPage(ctx, page)
			if fetchErr != nil {
				return fmt.Errorf("fetch SFDA list page %d: %w", page, fetchErr)
			}
			pageData, err = parseListPage(pageBody, page)
			if err != nil {
				return fmt.Errorf("parse SFDA list page %d: %w", page, err)
			}
		}

		drugs, fetchErr := s.fetchPageDetails(ctx, pageData)
		if fetchErr != nil {
			return fmt.Errorf("fetch drug details on SFDA list page %d: %w", page, fetchErr)
		}
		filtered, filterErr := model.FilterDrugs(drugs, filters...)
		if filterErr != nil {
			return filterErr
		}
		for _, drug := range filtered {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := sink.Store(ctx, drug); err != nil {
				return fmt.Errorf("store drug %q: %w", drug.RegistrationNumber, err)
			}
		}
	}
	return nil
}

type detailResult struct {
	drug model.Drug
	err  error
}

func (s *Scraper) fetchPageDetails(ctx context.Context, page listPage) ([]model.Drug, error) {
	results := make([]detailResult, len(page.Drugs))
	semaphore := make(chan struct{}, s.maxWorkers)
	var waitGroup sync.WaitGroup
	for i, summary := range page.Drugs {
		waitGroup.Add(1)
		go func(index int, item drugSummary) {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			details, err := s.fetchDetails(ctx, item.DetailURL, page.AjaxLibraries)
			if err != nil {
				results[index].err = err
				return
			}
			results[index].drug, results[index].err = mergeDrug(item.Drug, details)
		}(i, summary)
	}
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
		if err := s.waitForRequestSlot(ctx); err != nil {
			return nil, err
		}
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

		response, requestErr := s.httpClient.Do(request)
		if requestErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt+1 == s.maxRetries {
				return nil, fmt.Errorf("%s %s failed after %d attempts: %w", method, requestURL, s.maxRetries, requestErr)
			}
			if err := sleepContext(ctx, s.backoff*time.Duration(1<<attempt)); err != nil {
				return nil, err
			}
			continue
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read %s response: %w", method, readErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return responseBody, nil
		}
		if response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500 {
			return nil, fmt.Errorf("%s %s returned HTTP %d: %s", method, requestURL, response.StatusCode, strings.TrimSpace(string(responseBody)))
		}
		if attempt+1 == s.maxRetries {
			return nil, fmt.Errorf("%s %s returned HTTP %d after %d attempts", method, requestURL, response.StatusCode, s.maxRetries)
		}
		wait := s.backoff * time.Duration(1<<attempt)
		if response.StatusCode == http.StatusTooManyRequests {
			if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, parseErr := time.ParseDuration(retryAfter + "s"); parseErr == nil {
					wait = seconds
				} else if retryAt, dateErr := http.ParseTime(retryAfter); dateErr == nil && time.Until(retryAt) > 0 {
					wait = time.Until(retryAt)
				}
			}
		}
		if err := sleepContext(ctx, wait); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("request attempts exhausted")
}

func (s *Scraper) waitForRequestSlot(ctx context.Context) error {
	select {
	case s.requestSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.requestSlots }()
	if err := sleepContext(ctx, s.requestInterval); err != nil {
		return err
	}
	return nil
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

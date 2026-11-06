package controller

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type drugSummary struct {
	Drug      map[string]string
	DetailURL string
}

type listPage struct {
	Drugs         []drugSummary
	TotalPages    int
	AjaxLibraries string
}

func parseListPage(body []byte, currentPage int) (listPage, error) {
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return listPage{}, fmt.Errorf("parse list page: %w", err)
	}

	result := listPage{TotalPages: currentPage + 1}
	var listTable *html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "table" && hasClass(node, "responsive-enabled") && listTable == nil {
			listTable = node
		}
		if node.Type == html.ElementNode && node.Data == "a" {
			href := attribute(node, "href")
			if pageIndex, ok := pageIndexFromHref(href); ok && pageIndex+1 > result.TotalPages {
				result.TotalPages = pageIndex + 1
			}
		}
		if node.Type == html.ElementNode && node.Data == "script" &&
			attribute(node, "data-drupal-selector") == "drupal-settings-json" {
			settings, settingsErr := parseDrupalSettings(textContent(node))
			if settingsErr == nil {
				result.AjaxLibraries = settings.AjaxPageState.Libraries
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)

	if listTable == nil {
		if strings.Contains(strings.ToLower(textContent(root)), "no results found") {
			if result.AjaxLibraries == "" {
				return listPage{}, fmt.Errorf("SFDA empty-results page is missing Drupal AJAX state")
			}
			return result, nil
		}
		return listPage{}, fmt.Errorf("SFDA response did not contain the expected drugs table or an empty-results message")
	}

	for row := listTable.FirstChild; row != nil; row = row.NextSibling {
		if row.Type != html.ElementNode || row.Data != "tbody" {
			continue
		}
		for tr := row.FirstChild; tr != nil; tr = tr.NextSibling {
			if tr.Type != html.ElementNode || tr.Data != "tr" {
				continue
			}
			cells := childCells(tr)
			detailURL := ""
			var findLink func(*html.Node)
			findLink = func(node *html.Node) {
				if node.Type == html.ElementNode && node.Data == "a" {
					href := attribute(node, "href")
					if strings.Contains(href, "/en/details_data?") {
						detailURL = href
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					findLink(child)
				}
			}
			findLink(tr)
			if len(cells) < 5 || detailURL == "" {
				continue
			}

			parsedURL, parseErr := url.Parse(detailURL)
			if parseErr != nil {
				return listPage{}, fmt.Errorf("parse drug details link %q: %w", detailURL, parseErr)
			}
			registrationNumber := parsedURL.Query().Get("id")
			result.Drugs = append(result.Drugs, drugSummary{
				Drug: map[string]string{
					"scientific_name":     cells[0],
					"trade_name":          cells[1],
					"strength":            cells[2],
					"dosage_form":         cells[3],
					"price":               cells[4],
					"registration_number": registrationNumber,
				},
				DetailURL: detailURL,
			})
		}
	}
	if len(result.Drugs) == 0 {
		return listPage{}, fmt.Errorf("SFDA drugs table contained no rows with details links")
	}
	if result.AjaxLibraries == "" {
		return listPage{}, fmt.Errorf("SFDA page did not include Drupal AJAX library state")
	}
	return result, nil
}

type drupalSettings struct {
	AjaxPageState struct {
		Libraries string `json:"libraries"`
	} `json:"ajaxPageState"`
}

func parseDrupalSettings(raw string) (drupalSettings, error) {
	var settings drupalSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return settings, fmt.Errorf("decode Drupal settings: %w", err)
	}
	if settings.AjaxPageState.Libraries == "" {
		return settings, fmt.Errorf("Drupal settings do not contain AJAX libraries")
	}
	return settings, nil
}

func pageIndexFromHref(href string) (int, bool) {
	parsed, err := url.Parse(href)
	if err != nil {
		return 0, false
	}
	raw := parsed.Query().Get("page")
	if raw == "" {
		return 0, false
	}
	index, err := strconv.Atoi(raw)
	return index, err == nil && index >= 0
}

func childCells(row *html.Node) []string {
	var cells []string
	for node := row.FirstChild; node != nil; node = node.NextSibling {
		if node.Type == html.ElementNode && (node.Data == "th" || node.Data == "td") {
			cells = append(cells, strings.Join(strings.Fields(textContent(node)), " "))
		}
	}
	return cells
}

func textContent(node *html.Node) string {
	var builder strings.Builder
	var visit func(*html.Node)
	visit = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
		}
		if current.Type == html.ElementNode && current.Data == "br" {
			builder.WriteByte('\n')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return builder.String()
}

func hasClass(node *html.Node, class string) bool {
	for _, item := range strings.Fields(attribute(node, "class")) {
		if item == class {
			return true
		}
	}
	return false
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

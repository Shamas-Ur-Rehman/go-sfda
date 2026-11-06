package controller

import (
	"fmt"
	"testing"
)

func BenchmarkParseListPage(b *testing.B) {
	var rows string
	for id := 100; id < 120; id++ {
		rows += fmt.Sprintf(`<tr><td>Ingredient %d</td><td>Drug %d</td><td>10 mg</td><td>Tablet</td><td>5.00</td><td><a href="/en/details_data?nid=1&id=%d&page=0">Details</a></td></tr>`, id, id, id)
	}
	page := []byte(listHTML(459, rows))
	b.ReportAllocs()
	b.SetBytes(int64(len(page)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := parseListPage(page, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseDetailTable(b *testing.B) {
	details := `<table><tr><th>Trade Name</th><td>Example medicine</td></tr><tr><th>Manufacturers</th><td>Name: Example Labs | Country: Saudi Arabia</td></tr><tr><th>Storage</th><td>Store below 25°C<br>Protect from light</td></tr></table>`
	b.ReportAllocs()
	b.SetBytes(int64(len(details)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := parseDetailTable(details); err != nil {
			b.Fatal(err)
		}
	}
}

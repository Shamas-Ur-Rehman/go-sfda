package model

import "testing"

func BenchmarkFilterDrugs(b *testing.B) {
	drugs := make([]Drug, 1000)
	for i := range drugs {
		drugs[i] = Drug{
			TradeName:            "Example drug",
			CountryOfManufacture: "Saudi Arabia",
			RegistrationNumber:   "100",
		}
	}
	filters := []Filter{
		{Field: "trade_name", Value: "example", Mode: MatchContains},
		{Field: "country_of_manufacture", Value: "Saudi Arabia", Mode: MatchExact},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FilterDrugs(drugs, filters...); err != nil {
			b.Fatal(err)
		}
	}
}

package model

import "testing"

func TestFilterDrugs(t *testing.T) {
	drugs := []Drug{
		{TradeName: "Amoxil 500 mg", CountryOfManufacture: "Saudi Arabia"},
		{TradeName: "Amoxicillin 250 mg", CountryOfManufacture: "India"},
		{TradeName: "Paracetamol", CountryOfManufacture: "India"},
	}

	got, err := FilterDrugs(drugs,
		Filter{Field: "trade_name", Value: "amox", Mode: MatchPrefix},
		Filter{Field: "Country Of Manufacture", Value: "India", Mode: MatchExact},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TradeName != "Amoxicillin 250 mg" {
		t.Fatalf("unexpected filtered results: %#v", got)
	}
}

func TestFilterDrugsRejectsUnknownField(t *testing.T) {
	_, err := FilterDrugs(nil, Filter{Field: "not_a_drug_field", Value: "x"})
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

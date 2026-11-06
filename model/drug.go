package model

import (
	"fmt"
	"reflect"
	"strings"
)

// Drug represents a registered SFDA drug and the fields exposed by its details
// dialog. Fields not currently returned by the website remain empty.
type Drug struct {
	TradeName                    string `json:"trade_name"`
	ScientificName               string `json:"scientific_name"`
	RegistrationNumber           string `json:"registration_number"`
	ManufacturingCompany         string `json:"manufacturing_company"`
	CountryOfManufacture         string `json:"country_of_manufacture"`
	AdditionalManufacturers      string `json:"additional_manufacturers"`
	AdditionalManufacturerPlaces string `json:"additional_manufacturer_countries"`
	MarketingCompany             string `json:"marketing_company"`
	MarketingCompanyCountry      string `json:"marketing_company_country"`
	Agents                       string `json:"agents"`
	DrugDomain                   string `json:"drug_domain"`
	DrugType                     string `json:"drug_type"`
	DosageForm                   string `json:"dosage_form"`
	RouteOfAdministration        string `json:"route_of_administration"`
	Strength                     string `json:"strength"`
	StrengthUnit                 string `json:"strength_unit"`
	PackageType                  string `json:"package_type"`
	PackageSize                  string `json:"package_size"`
	Size                         string `json:"size"`
	SizeUnit                     string `json:"size_unit"`
	Price                        string `json:"price"`
	MarketingStatus              string `json:"marketing_status"`
	AuthorizationStatus          string `json:"authorization_status"`
	LegalStatus                  string `json:"legal_status"`
	ProductControl               string `json:"product_control"`
	DistributionSite             string `json:"distribution_site"`
	StorageConditions            string `json:"storage_conditions"`
	ShelfLife                    string `json:"shelf_life"`
	RegistrationYear             string `json:"registration_year"`
	ATCCode1                     string `json:"atc_code_1"`
	ATCCode2                     string `json:"atc_code_2"`
	DescriptionCode              string `json:"description_code"`
}

// MatchMode selects how a field filter compares values.
type MatchMode string

const (
	// MatchExact requires a case-insensitive exact match.
	MatchExact MatchMode = "exact"
	// MatchContains performs a case-insensitive substring match.
	MatchContains MatchMode = "contains"
	// MatchPrefix performs a case-insensitive prefix match.
	MatchPrefix MatchMode = "prefix"
)

// Filter describes a case-insensitive filter against one Drug field.
// Field accepts a Go field name (for example TradeName) or its JSON name
// (for example trade_name).
type Filter struct {
	Field string
	Value string
	Mode  MatchMode
}

// FilterDrugs applies all supplied filters (AND semantics) and returns a new
// slice. An unknown field or match mode is reported rather than silently ignored.
func FilterDrugs(drugs []Drug, filters ...Filter) ([]Drug, error) {
	for _, filter := range filters {
		if _, ok := drugFieldIndex(filter.Field); !ok {
			return nil, fmt.Errorf("unknown drug filter field %q", filter.Field)
		}
		switch filter.Mode {
		case "", MatchExact, MatchContains, MatchPrefix:
		default:
			return nil, fmt.Errorf("unsupported match mode %q", filter.Mode)
		}
	}

	matches := make([]Drug, 0, len(drugs))
	for _, drug := range drugs {
		matched := true
		value := reflect.ValueOf(drug)
		for _, filter := range filters {
			index, _ := drugFieldIndex(filter.Field)
			fieldValue := value.Field(index).String()
			got := strings.ToLower(fieldValue)
			want := strings.ToLower(filter.Value)

			switch filter.Mode {
			case MatchContains:
				matched = strings.Contains(got, want)
			case MatchPrefix:
				matched = strings.HasPrefix(got, want)
			default:
				matched = got == want
			}
			if !matched {
				break
			}
		}
		if matched {
			matches = append(matches, drug)
		}
	}
	return matches, nil
}

func drugFieldIndex(name string) (int, bool) {
	normalized := normalizeFieldName(name)
	typeOfDrug := reflect.TypeOf(Drug{})
	for i := 0; i < typeOfDrug.NumField(); i++ {
		field := typeOfDrug.Field(i)
		if normalizeFieldName(field.Name) == normalized ||
			normalizeFieldName(strings.Split(field.Tag.Get("json"), ",")[0]) == normalized {
			return i, true
		}
	}
	return 0, false
}

func normalizeFieldName(name string) string {
	name = strings.ToLower(name)
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(name)
}

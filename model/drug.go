package model

import (
	"fmt"
	"reflect"
	"strings"
)

// Drug represents a registered SFDA drug and the fields exposed by its details
// dialog. Fields not currently returned by the website remain empty.
type Drug struct {
	TradeName                    string `json:"trade_name" bson:"trade_name"`
	ScientificName               string `json:"scientific_name" bson:"scientific_name"`
	RegistrationNumber           string `json:"registration_number" bson:"registration_number"`
	ManufacturingCompany         string `json:"manufacturing_company" bson:"manufacturing_company"`
	CountryOfManufacture         string `json:"country_of_manufacture" bson:"country_of_manufacture"`
	AdditionalManufacturers      string `json:"additional_manufacturers" bson:"additional_manufacturers"`
	AdditionalManufacturerPlaces string `json:"additional_manufacturer_countries" bson:"additional_manufacturer_countries"`
	MarketingCompany             string `json:"marketing_company" bson:"marketing_company"`
	MarketingCompanyCountry      string `json:"marketing_company_country" bson:"marketing_company_country"`
	Agents                       string `json:"agents" bson:"agents"`
	DrugDomain                   string `json:"drug_domain" bson:"drug_domain"`
	DrugType                     string `json:"drug_type" bson:"drug_type"`
	DosageForm                   string `json:"dosage_form" bson:"dosage_form"`
	RouteOfAdministration        string `json:"route_of_administration" bson:"route_of_administration"`
	Strength                     string `json:"strength" bson:"strength"`
	StrengthUnit                 string `json:"strength_unit" bson:"strength_unit"`
	PackageType                  string `json:"package_type" bson:"package_type"`
	PackageSize                  string `json:"package_size" bson:"package_size"`
	Size                         string `json:"size" bson:"size"`
	SizeUnit                     string `json:"size_unit" bson:"size_unit"`
	Price                        string `json:"price" bson:"price"`
	MarketingStatus              string `json:"marketing_status" bson:"marketing_status"`
	AuthorizationStatus          string `json:"authorization_status" bson:"authorization_status"`
	LegalStatus                  string `json:"legal_status" bson:"legal_status"`
	ProductControl               string `json:"product_control" bson:"product_control"`
	DistributionSite             string `json:"distribution_site" bson:"distribution_site"`
	StorageConditions            string `json:"storage_conditions" bson:"storage_conditions"`
	ShelfLife                    string `json:"shelf_life" bson:"shelf_life"`
	RegistrationYear             string `json:"registration_year" bson:"registration_year"`
	ATCCode1                     string `json:"atc_code_1" bson:"atc_code_1"`
	ATCCode2                     string `json:"atc_code_2" bson:"atc_code_2"`
	DescriptionCode              string `json:"description_code" bson:"description_code"`
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
	type compiledFilter struct {
		index int
		value string
		mode  MatchMode
	}
	compiled := make([]compiledFilter, len(filters))
	for filterIndex, filter := range filters {
		index, ok := drugFieldIndex(filter.Field)
		if !ok {
			return nil, fmt.Errorf("unknown drug filter field %q", filter.Field)
		}
		switch filter.Mode {
		case "", MatchExact, MatchContains, MatchPrefix:
		default:
			return nil, fmt.Errorf("unsupported match mode %q", filter.Mode)
		}
		compiled[filterIndex] = compiledFilter{
			index: index,
			value: strings.ToLower(filter.Value),
			mode:  filter.Mode,
		}
	}

	matches := make([]Drug, 0, len(drugs))
	for _, drug := range drugs {
		matched := true
		value := reflect.ValueOf(drug)
		for _, filter := range compiled {
			fieldValue := value.Field(filter.index).String()
			switch filter.mode {
			case MatchContains:
				matched = strings.Contains(strings.ToLower(fieldValue), filter.value)
			case MatchPrefix:
				matched = strings.HasPrefix(strings.ToLower(fieldValue), filter.value)
			default:
				matched = strings.EqualFold(fieldValue, filter.value)
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

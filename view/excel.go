// Package view contains output adapters for scraped SFDA records.
package view

import (
	"fmt"
	"io"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
	"github.com/xuri/excelize/v2"
)

var columns = []struct {
	header string
	value  func(model.Drug) string
}{
	{"Drug Name", func(d model.Drug) string { return d.TradeName }},
	{"Manufacturing Company", func(d model.Drug) string { return d.ManufacturingCompany }},
	{"Country of Manufacture", func(d model.Drug) string { return d.CountryOfManufacture }},
	{"Marketing Company / Saudi Agent", func(d model.Drug) string { return d.Agents }},
	{"Registration Number", func(d model.Drug) string { return d.RegistrationNumber }},
	{"Scientific Name", func(d model.Drug) string { return d.ScientificName }},
	{"Drug Domain", func(d model.Drug) string { return d.DrugDomain }},
	{"Drug Type", func(d model.Drug) string { return d.DrugType }},
	{"Dosage Form", func(d model.Drug) string { return d.DosageForm }},
	{"Administration Route", func(d model.Drug) string { return d.RouteOfAdministration }},
	{"Strength", func(d model.Drug) string { return d.Strength }},
	{"Strength Unit", func(d model.Drug) string { return d.StrengthUnit }},
	{"Package Type", func(d model.Drug) string { return d.PackageType }},
	{"Package Size", func(d model.Drug) string { return d.PackageSize }},
	{"Volume / Size", func(d model.Drug) string { return d.Size }},
	{"Size Unit", func(d model.Drug) string { return d.SizeUnit }},
	{"Price (SAR)", func(d model.Drug) string { return d.Price }},
	{"Marketing Status", func(d model.Drug) string { return d.MarketingStatus }},
	{"Authorization Status", func(d model.Drug) string { return d.AuthorizationStatus }},
	{"Legal Status", func(d model.Drug) string { return d.LegalStatus }},
	{"Product Control", func(d model.Drug) string { return d.ProductControl }},
	{"Distribution Area", func(d model.Drug) string { return d.DistributionSite }},
	{"Storage Conditions", func(d model.Drug) string { return d.StorageConditions }},
	{"Shelf Life (months)", func(d model.Drug) string { return d.ShelfLife }},
	{"Registration Year", func(d model.Drug) string { return d.RegistrationYear }},
	{"ATC Code 1", func(d model.Drug) string { return d.ATCCode1 }},
	{"ATC Code 2", func(d model.Drug) string { return d.ATCCode2 }},
	{"Company Name", func(d model.Drug) string { return d.MarketingCompany }},
	{"Company Country", func(d model.Drug) string { return d.MarketingCompanyCountry }},
	{"Additional Manufacturer", func(d model.Drug) string { return d.AdditionalManufacturers }},
	{"Additional Manufacturer Country", func(d model.Drug) string { return d.AdditionalManufacturerPlaces }},
}

// WriteExcel writes drug records to an XLSX workbook. Pass an os.File to create
// a file or an http.ResponseWriter to serve a browser download.
func WriteExcel(writer io.Writer, drugs []model.Drug) error {
	file := excelize.NewFile()
	defer file.Close()

	const sheet = "SFDA Registered Drugs"
	if err := file.SetSheetName("Sheet1", sheet); err != nil {
		return fmt.Errorf("rename Excel sheet: %w", err)
	}
	for columnIndex, column := range columns {
		cell, err := excelize.CoordinatesToCellName(columnIndex+1, 1)
		if err != nil {
			return fmt.Errorf("create Excel header cell: %w", err)
		}
		if err := file.SetCellValue(sheet, cell, column.header); err != nil {
			return fmt.Errorf("write Excel header: %w", err)
		}
	}
	for rowIndex, drug := range drugs {
		for columnIndex, column := range columns {
			cell, err := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+2)
			if err != nil {
				return fmt.Errorf("create Excel data cell: %w", err)
			}
			if err := file.SetCellValue(sheet, cell, column.value(drug)); err != nil {
				return fmt.Errorf("write Excel data: %w", err)
			}
		}
	}

	headerStyle, err := file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"1F4E79"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	if err != nil {
		return fmt.Errorf("create Excel header style: %w", err)
	}
	lastHeader, err := excelize.CoordinatesToCellName(len(columns), 1)
	if err != nil {
		return fmt.Errorf("create Excel header range: %w", err)
	}
	if err := file.SetCellStyle(sheet, "A1", lastHeader, headerStyle); err != nil {
		return fmt.Errorf("style Excel headers: %w", err)
	}
	lastColumn, err := excelize.ColumnNumberToName(len(columns))
	if err != nil {
		return fmt.Errorf("create Excel filter range: %w", err)
	}
	if err := file.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastColumn, len(drugs)+1), []excelize.AutoFilterOptions{}); err != nil {
		return fmt.Errorf("set Excel autofilter: %w", err)
	}
	if err := file.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return fmt.Errorf("freeze Excel header: %w", err)
	}
	if err := file.Write(writer); err != nil {
		return fmt.Errorf("write Excel workbook: %w", err)
	}
	return nil
}

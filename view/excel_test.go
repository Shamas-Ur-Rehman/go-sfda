package view

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
	"github.com/xuri/excelize/v2"
)

func TestWriteExcel(t *testing.T) {
	var workbook bytes.Buffer
	drug := model.Drug{
		TradeName:          "دواء تجريبي",
		ScientificName:     "عنصر نشط",
		RegistrationNumber: "00123",
		DescriptionCode:    "description-1",
	}
	err := WriteExcel(&workbook, []model.Drug{drug})
	if err != nil {
		t.Fatal(err)
	}

	file, err := excelize.OpenReader(bytes.NewReader(workbook.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	name := "SFDA Registered Drugs"
	if got, err := file.GetCellValue(name, "A1"); err != nil || got != "Drug Name" {
		t.Fatalf("header = %q, err = %v", got, err)
	}
	if got, err := file.GetCellValue(name, "A2"); err != nil || got != drug.TradeName {
		t.Fatalf("drug name = %q, err = %v", got, err)
	}
	if got, err := file.GetCellValue(name, "E2"); err != nil || got != "00123" {
		t.Fatalf("registration = %q, err = %v", got, err)
	}
	if got, err := file.GetCellValue(name, "AF2"); err != nil || got != drug.DescriptionCode {
		t.Fatalf("description code = %q, err = %v", got, err)
	}
	if got, err := file.GetCellValue("Metadata", "A1"); err != nil || got != "Exported At (UTC)" {
		t.Fatalf("metadata label = %q, err = %v", got, err)
	}
	if got, err := file.GetCellValue("Metadata", "B1"); err != nil || got == "" {
		t.Fatalf("metadata timestamp = %q, err = %v", got, err)
	}
}

func TestWriteExcelFromStreamsRowsAndReturnsSourceErrors(t *testing.T) {
	rows := []model.Drug{{TradeName: "one"}, {TradeName: "اثنان"}}
	index := 0
	var workbook bytes.Buffer
	if err := WriteExcelFrom(&workbook, func() (model.Drug, bool, error) {
		if index == len(rows) {
			return model.Drug{}, false, nil
		}
		drug := rows[index]
		index++
		return drug, true, nil
	}); err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenReader(bytes.NewReader(workbook.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for row, want := range []string{"one", "اثنان"} {
		cell := fmt.Sprintf("A%d", row+2)
		if got, err := file.GetCellValue("SFDA Registered Drugs", cell); err != nil || got != want {
			t.Fatalf("row %s = %q, err = %v; want %q", cell, got, err, want)
		}
	}

	sourceErr := errors.New("source failed")
	if err := WriteExcelFrom(&bytes.Buffer{}, func() (model.Drug, bool, error) {
		return model.Drug{}, false, sourceErr
	}); !errors.Is(err, sourceErr) || !strings.Contains(err.Error(), "read next drug") {
		t.Fatalf("expected source error to be returned, got %v", err)
	}
}

func TestWriteExcelFromRejectsNilSource(t *testing.T) {
	if err := WriteExcelFrom(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("expected nil row source to be rejected")
	}
}

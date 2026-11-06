package view

import (
	"bytes"
	"testing"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
	"github.com/xuri/excelize/v2"
)

func TestWriteExcel(t *testing.T) {
	var workbook bytes.Buffer
	err := WriteExcel(&workbook, []model.Drug{{TradeName: "Example", RegistrationNumber: "123"}})
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
	if got, err := file.GetCellValue(name, "A2"); err != nil || got != "Example" {
		t.Fatalf("drug name = %q, err = %v", got, err)
	}
	if got, err := file.GetCellValue(name, "E2"); err != nil || got != "123" {
		t.Fatalf("registration = %q, err = %v", got, err)
	}
}

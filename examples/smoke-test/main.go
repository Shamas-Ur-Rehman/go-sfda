package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Shamas-Ur-Rehman/go-sfda/controller"
	"github.com/Shamas-Ur-Rehman/go-sfda/view"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client, err := controller.NewScraper(controller.Options{
		MaxPages: 1,
	})
	if err != nil {
		return fmt.Errorf("create SFDA scraper: %w", err)
	}

	drugs, err := client.FetchAll(ctx)
	if err != nil {
		return fmt.Errorf("fetch first SFDA page: %w", err)
	}
	if len(drugs) == 0 {
		return fmt.Errorf("first SFDA page returned no drug records")
	}
	for index, drug := range drugs {
		if strings.TrimSpace(drug.TradeName) == "" || strings.TrimSpace(drug.RegistrationNumber) == "" {
			return fmt.Errorf("record %d is missing its trade name or registration number: %+v", index+1, drug)
		}
	}

	const outputPath = "sfda_smoke_test.xlsx"
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create Excel output: %w", err)
	}
	if err := view.WriteExcel(file, drugs); err != nil {
		_ = file.Close()
		return fmt.Errorf("write Excel output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Excel output: %w", err)
	}

	fmt.Printf("Smoke test passed: fetched %d records from one page and wrote %s\n", len(drugs), outputPath)
	return nil
}

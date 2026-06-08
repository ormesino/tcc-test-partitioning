// Command gendata exports synthetic fixtures as JSON files,
// producing the same format that the data collection pipeline
// (cmd/analyze) would generate from real projects.
//
// This allows testing the full CLI pipeline without access to
// the real Go projects or a Go test environment.
//
// Usage:
//
//	go run cmd/gendata/main.go -profile moderate -output data/synthetic/moderate.json
//	go run cmd/gendata/main.go -profile heavytail -output data/synthetic/heavytail.json
//	go run cmd/gendata/main.go -profile mixed -output data/synthetic/mixed.json
//	go run cmd/gendata/main.go -profile all -output-dir data/synthetic/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"tcc-test-partitioning/data/synthetic"
	"tcc-test-partitioning/internal/model"
)

func main() {
	profile := flag.String("profile", "all",
		"Profile to export: moderate, heavytail, mixed, all")
	output := flag.String("output", "",
		"Output file path (for single profile)")
	outputDir := flag.String("output-dir", "",
		"Output directory (for -profile all; generates one file per profile)")

	flag.Parse()

	if *profile == "all" {
		if *outputDir == "" {
			*outputDir = "data/synthetic"
		}
		exportAll(*outputDir)
	} else {
		if *output == "" {
			*output = fmt.Sprintf("data/synthetic/%s.json", *profile)
		}
		exportSingle(*profile, *output)
	}
}

func exportAll(dir string) {
	profiles := map[string][]model.PackageInfo{
		"moderate":  synthetic.ProfileModerate(),
		"heavytail": synthetic.ProfileHeavyTail(),
		"mixed":     synthetic.ProfileMixed(),
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}

	for name, pkgs := range profiles {
		path := filepath.Join(dir, name+".json")
		if err := writeJSON(path, pkgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("Wrote %s (%d packages)\n", path, len(pkgs))
	}
}

func exportSingle(profile, output string) {
	var pkgs []model.PackageInfo
	switch profile {
	case "moderate":
		pkgs = synthetic.ProfileModerate()
	case "heavytail":
		pkgs = synthetic.ProfileHeavyTail()
	case "mixed":
		pkgs = synthetic.ProfileMixed()
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown profile %q (valid: moderate, heavytail, mixed, all)\n", profile)
		os.Exit(1)
	}

	dir := filepath.Dir(output)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}

	if err := writeJSON(output, pkgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d packages)\n", output, len(pkgs))
}

func writeJSON(path string, pkgs []model.PackageInfo) error {
	data, err := json.MarshalIndent(pkgs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Package synthetic provides deterministic test fixtures that mimic
// the statistical profiles of the real Go projects selected for the
// empirical evaluation (ADR-009).
//
// All durations are hardcoded to guarantee full reproducibility.
// No randomness is involved.
package synthetic

import (
	"fmt"
	"time"

	"tcc-test-partitioning/internal/model"
)

// ProfileModerate returns 150 packages with moderate-to-high variance.
// Current duration distribution: CV ≈ 2.0, Max/Med ≈ 42.
// It is a synthetic stress case inspired by cli/cli and grpc-go, not
// a calibrated replica of their exact statistics.
func ProfileModerate() []model.PackageInfo {
	// Distribution strategy:
	//   - Bulk (~120 pkgs): 50ms – 500ms  (fast unit tests)
	//   - Medium (~25 pkgs): 500ms – 3s   (integration-ish)
	//   - Heavy (~5 pkgs):   5s – 10s     (slow tests)
	//   Median ≈ 237ms, Max = 10s → Max/Med ≈ 42
	//   Duration CV ≈ 2.0

	pkgs := make([]model.PackageInfo, 0, 150)

	// --- Bulk: 120 fast packages (50ms – 500ms) ---
	bulkDurations := []time.Duration{
		50, 55, 60, 62, 65, 68, 70, 72, 75, 78,
		80, 82, 85, 88, 90, 92, 95, 98, 100, 105,
		108, 110, 112, 115, 118, 120, 122, 125, 128, 130,
		132, 135, 138, 140, 142, 145, 148, 150, 152, 155,
		158, 160, 162, 165, 168, 170, 172, 175, 178, 180,
		185, 190, 195, 200, 205, 210, 215, 220, 225, 230,
		235, 240, 245, 250, 255, 260, 265, 270, 280, 290,
		300, 310, 320, 330, 340, 350, 355, 360, 365, 370,
		375, 380, 385, 390, 395, 400, 405, 410, 415, 420,
		425, 430, 435, 440, 445, 450, 455, 460, 465, 470,
		475, 480, 485, 490, 495, 500, 60, 70, 80, 90,
		100, 110, 120, 130, 140, 150, 160, 170, 180, 190,
	}
	for i, d := range bulkDurations {
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/moderate/fast/pkg%03d", i+1),
			Duration: d * time.Millisecond,
		})
	}

	// --- Medium: 25 packages (500ms – 3s) ---
	mediumDurations := []time.Duration{
		520, 580, 650, 720, 800, 880, 950, 1050, 1150, 1250,
		1350, 1500, 1600, 1750, 1900, 2000, 2100, 2250, 2400, 2500,
		2600, 2700, 2800, 2900, 3000,
	}
	for i, d := range mediumDurations {
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/moderate/mid/pkg%03d", i+1),
			Duration: d * time.Millisecond,
		})
	}

	// --- Heavy: 5 packages (5s – 10s) ---
	heavyDurations := []time.Duration{5000, 6200, 7500, 8800, 10000}
	for i, d := range heavyDurations {
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/moderate/heavy/pkg%03d", i+1),
			Duration: d * time.Millisecond,
		})
	}

	return pkgs
}

// ProfileHeavyTail returns 120 packages with an intentionally extreme
// heavy-tailed distribution.
// Current duration distribution: CV ≈ 4.2, Max/Med ≈ 698.
// It stresses the algorithms more aggressively than hugo and
// goreleaser, whose observed Max/Med ratios are lower.
func ProfileHeavyTail() []model.PackageInfo {
	// Distribution strategy:
	//   - Bulk (~95 pkgs):  10ms – 200ms   (very fast)
	//   - Medium (~15 pkgs): 200ms – 2s    (moderate)
	//   - Heavy (~8 pkgs):   5s – 30s      (slow)
	//   - Extreme (~2 pkgs): 60s – 90s     (dominant outliers)
	//   Median ≈ 129ms, Max = 90s → Max/Med ≈ 698
	//   Duration CV ≈ 4.2

	pkgs := make([]model.PackageInfo, 0, 120)

	// --- Bulk: 95 very fast packages (10ms – 200ms) ---
	for i := 0; i < 95; i++ {
		d := time.Duration(10+i*2) * time.Millisecond
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/heavytail/fast/pkg%03d", i+1),
			Duration: d,
		})
	}

	// --- Medium: 15 packages (200ms – 2s) ---
	mediumDurations := []time.Duration{
		200, 300, 400, 500, 650, 800, 950, 1100,
		1250, 1400, 1550, 1700, 1850, 1950, 2000,
	}
	for i, d := range mediumDurations {
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/heavytail/mid/pkg%03d", i+1),
			Duration: d * time.Millisecond,
		})
	}

	// --- Heavy: 8 packages (5s – 30s) ---
	heavyDurations := []time.Duration{5000, 8000, 11000, 14000, 17000, 20000, 25000, 30000}
	for i, d := range heavyDurations {
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/heavytail/heavy/pkg%03d", i+1),
			Duration: d * time.Millisecond,
		})
	}

	// --- Extreme: 2 dominant outliers (60s, 90s) ---
	extremeDurations := []time.Duration{60000, 90000}
	for i, d := range extremeDurations {
		pkgs = append(pkgs, model.PackageInfo{
			Name:     fmt.Sprintf("example.com/heavytail/extreme/pkg%03d", i+1),
			Duration: d * time.Millisecond,
		})
	}

	return pkgs
}

// ProfileMixed returns 200 packages combining fast and moderate
// sub-populations from the other fixtures. Used for robustness
// testing with a less extreme heterogeneous workload.
func ProfileMixed() []model.PackageInfo {
	// Strategy: take subsets of both profiles and merge them.
	// This creates a bimodal distribution that stresses the
	// algorithms differently from either profile alone.

	moderate := ProfileModerate()
	heavy := ProfileHeavyTail()

	pkgs := make([]model.PackageInfo, 0, 200)

	// Take first 110 from moderate profile.
	for i := 0; i < 110 && i < len(moderate); i++ {
		p := moderate[i]
		p.Name = fmt.Sprintf("example.com/mixed/modpart/pkg%03d", i+1)
		pkgs = append(pkgs, p)
	}

	// Take the first 90 fast packages from heavy-tail (relabelled
	// to avoid name collision) to reach our 200-package target.
	for i := 0; i < 90 && i < len(heavy); i++ {
		p := heavy[i]
		p.Name = fmt.Sprintf("example.com/mixed/heavypart/pkg%03d", i+1)
		pkgs = append(pkgs, p)
	}

	return pkgs
}

// Command preflight verifies the canonical child-process runtime policy.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tcc-test-partitioning/internal/executor"
)

func main() {
	effective, err := executor.VerifyCanonicalGOMAXPROCS(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report := struct {
		Configured int    `json:"gomaxprocs_configured"`
		Effective  int    `json:"gomaxprocs_effective"`
		Expected   int    `json:"gomaxprocs_expected"`
		Policy     string `json:"gomaxprocs_policy"`
		Applied    bool   `json:"child_environment_applied"`
	}{executor.CanonicalGOMAXPROCS, effective, executor.CanonicalGOMAXPROCS, executor.GOMAXPROCSPolicy, true}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

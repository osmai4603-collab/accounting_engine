package contractparity

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScenarioResult records the outcome of one contract test on a specific adapter.
type ScenarioResult struct {
	ScenarioName          string
	AdapterName           string
	EngineVersion         string
	Passed                bool
	Skipped               bool
	CapabilityDifferences []string
	SemanticDifferences   []string
	UnexpectedDifferences []string
	ExecutionDuration     time.Duration
}

// EngineVersion identifies the engine build recorded alongside acceptance
// results. Local test builds commonly report "(devel)".
func EngineVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	version := info.Main.Version
	if version == "" {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return fmt.Sprintf("%s+%s", version, setting.Value[:min(len(setting.Value), 12)])
		}
	}
	return version
}

// ParityReporter aggregates contract execution telemetry and generates an automated report.
type ParityReporter struct {
	mu      sync.Mutex
	results []ScenarioResult
}

// GlobalReporter is the package-level reporter instance.
var GlobalReporter = &ParityReporter{}

// Record adds a scenario result to the reporter.
func (r *ParityReporter) Record(res ScenarioResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res.CapabilityDifferences = append([]string(nil), res.CapabilityDifferences...)
	res.SemanticDifferences = append([]string(nil), res.SemanticDifferences...)
	res.UnexpectedDifferences = append([]string(nil), res.UnexpectedDifferences...)
	r.results = append(r.results, res)
}

// GenerateMarkdownReport produces a report from observed test outcomes.
func (r *ParityReporter) GenerateMarkdownReport() string {
	r.mu.Lock()
	results := append([]ScenarioResult(nil), r.results...)
	r.mu.Unlock()

	sort.Slice(results, func(i, j int) bool {
		if results[i].ScenarioName == results[j].ScenarioName {
			return results[i].AdapterName < results[j].AdapterName
		}
		return results[i].ScenarioName < results[j].ScenarioName
	})

	var out string
	out += "# Contract Parity Report — P3-07 / P7-01\n\n"
	out += fmt.Sprintf("- **Generated (UTC):** %s\n", time.Now().UTC().Format(time.RFC3339))
	out += fmt.Sprintf("- **Engine version:** %s\n", EngineVersion())
	out += "- **Adapters:** Memory, External SPI adapter, PostgreSQL (when `POSTGRES_URL` is configured).\n\n"
	out += "| Engine | Scenario | Adapter | Result | Semantic differences | Duration |\n"
	out += "| :--- | :--- | :--- | :---: | :--- | ---: |\n"

	passed, failed, skipped := 0, 0, 0
	for _, result := range results {
		status := "PASS"
		switch {
		case result.Skipped:
			status = "SKIP"
			skipped++
		case !result.Passed:
			status = "FAIL"
			failed++
		default:
			passed++
		}
		version := result.EngineVersion
		if version == "" {
			version = EngineVersion()
		}
		differences := result.SemanticDifferences
		if len(differences) == 0 {
			differences = []string{"none recorded"}
		}
		out += fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			version, result.ScenarioName, result.AdapterName, status,
			escapeCell(joinDifferences(differences)), result.ExecutionDuration.Round(time.Millisecond))
	}

	out += fmt.Sprintf("\n- **Totals:** %d passed, %d failed, %d skipped.\n", passed, failed, skipped)

	out += "\n## Capability differences\n\n"
	capabilities := make(map[string]struct{})
	for _, result := range results {
		for _, difference := range result.CapabilityDifferences {
			capabilities[result.AdapterName+": "+difference] = struct{}{}
		}
	}
	if len(capabilities) == 0 {
		out += "No capability declarations were recorded.\n"
	} else {
		items := make([]string, 0, len(capabilities))
		for item := range capabilities {
			items = append(items, item)
		}
		sort.Strings(items)
		for _, item := range items {
			out += "- " + item + "\n"
		}
	}

	out += "\n## Unexpected differences\n\n"
	var unexpected []string
	for _, result := range results {
		for _, difference := range result.UnexpectedDifferences {
			unexpected = append(unexpected, fmt.Sprintf("%s / %s: %s", result.ScenarioName, result.AdapterName, difference))
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) == 0 {
		out += "None recorded.\n"
	} else {
		for _, difference := range unexpected {
			out += "- " + difference + "\n"
		}
	}
	return out
}

func joinDifferences(differences []string) string {
	return strings.Join(differences, "; ")
}

func escapeCell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}

// WriteReportToFile outputs the report to the specified file path.
func (r *ParityReporter) WriteReportToFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(r.GenerateMarkdownReport()), 0644); err != nil {
		return fmt.Errorf("write parity report: %w", err)
	}
	return nil
}

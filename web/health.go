package web

import "fmt"

// HealthLine renders the design's cluster/database health rollup label and its
// CSS color class (defined in verticals.css) from open-check severity counts.
func HealthLine(crit, warn, info int) (text, cssClass string) {
	switch {
	case crit > 0:
		return fmt.Sprintf("[DEGRADED] %d CRIT · %d WARN", crit, warn), "hl-crit"
	case warn > 0:
		return fmt.Sprintf("[WARNING] %d WARN", warn), "hl-warn"
	case info > 0:
		return fmt.Sprintf("[HEALTHY] %d INFO", info), "hl-info"
	default:
		return "[HEALTHY] 0 OPEN", "hl-ok"
	}
}

// SevRank orders rows for the HEALTH sort (crit-first).
func SevRank(crit, warn, info int) int {
	switch {
	case crit > 0:
		return 2
	case warn > 0:
		return 1
	default:
		return 0
	}
}

// nextSort toggles between the two sort keys. Anything not "name" is treated
// as "health", so the default flips to "name".
func nextSort(cur string) string {
	if cur == "name" {
		return "health"
	}
	return "name"
}

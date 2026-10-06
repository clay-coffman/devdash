package collect

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseBytes parses human sizes as printed by docker ("1.274GiB", "40.78MiB",
// "3.779GB", "98.97MB (2%)", "0B") and /proc ("451692 kB"). Returns 0 for
// unparseable input rather than failing: these values decorate, they do not
// decide.
func ParseBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" {
		return 0
	}
	// Split number and unit.
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(strings.TrimSpace(s[i:]))
	var mult float64
	switch unit {
	case "", "b":
		mult = 1
	case "kb":
		mult = 1000
	case "kib", "k":
		mult = 1024
	case "mb":
		mult = 1000 * 1000
	case "mib", "m":
		mult = 1024 * 1024
	case "gb":
		mult = 1000 * 1000 * 1000
	case "gib", "g":
		mult = 1024 * 1024 * 1024
	case "tb":
		mult = 1e12
	case "tib", "t":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0
	}
	return int64(num * mult)
}

// ParsePercent parses "55.82%" into 55.82.
func ParsePercent(s string) float64 {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// FormatBytes renders bytes in binary units, one decimal.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

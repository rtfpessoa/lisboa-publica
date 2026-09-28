package patterns

import (
	"strconv"
	"strings"
)

// Only bin widths can be coarsened without changing the sampling/evidence profile.
func resolutionProfile(value string) string {
	i := strings.LastIndex(value, "-b")
	if i < 0 {
		return value
	}
	if n, err := strconv.Atoi(value[i+2:]); err == nil && n > 0 {
		return value[:i]
	}
	return value
}
func samplingProfile(value string) string {
	value = resolutionProfile(value)
	i := strings.LastIndex(value, "-s")
	if i >= 0 {
		if n, err := strconv.Atoi(value[i+2:]); err == nil && n > 0 {
			return value[i:]
		}
	}
	// Unknown sampling identities cannot certify cross-profile compatibility.
	return value
}
func commonResolution(a, b int) int {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	x, y := a, b
	for y != 0 {
		x, y = y, x%y
	}
	// A bounded effective precision is preferable to integer overflow or a
	// misleading LCM after many independent configuration changes.
	if a/x > 3600/b {
		return 0
	}
	return a / x * b
}

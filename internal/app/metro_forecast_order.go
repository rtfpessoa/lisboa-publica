package app

import "strings"

// A unique monotone assignment is meaningful only after cohort completeness,
// common path and original-clock continuity have independently been admitted.
// The current top-three Metro boards do not themselves supply that admission.
func metroOrderForecastMatch(cohort, slots []string, complete bool) ([]string, bool) {
	if !complete || !metroOrderCohortValid(cohort, len(slots)) {
		return nil, false
	}
	ways := metroOrderWays(cohort, slots)
	if ways[0][0] != 1 {
		return nil, false
	}
	return metroOrderAssignment(cohort, slots, ways), true
}

func metroOrderCohortValid(cohort []string, slots int) bool {
	if len(cohort) == 0 || len(cohort) > maxMetroPopupVisits || slots > len(cohort) {
		return false
	}
	seen := map[string]bool{}
	for _, ref := range cohort {
		if !metroReference(ref) || seen[ref] {
			return false
		}
		seen[ref] = true
	}
	return true
}

func metroOrderSlotMatches(slot, reference string) bool {
	return slot == "" || strings.TrimSpace(slot) == reference
}

func metroOrderWays(cohort, slots []string) [][]int {
	ways := make([][]int, len(slots)+1)
	for i := range ways {
		ways[i] = make([]int, len(cohort)+1)
	}
	for j := range ways[len(slots)] {
		ways[len(slots)][j] = 1
	}
	for i := len(slots) - 1; i >= 0; i-- {
		for j := len(cohort) - 1; j >= 0; j-- {
			ways[i][j] = ways[i][j+1]
			if metroOrderSlotMatches(slots[i], cohort[j]) {
				ways[i][j] = min(2, ways[i][j]+ways[i+1][j+1])
			}
		}
	}
	return ways
}

func metroOrderAssignment(cohort, slots []string, ways [][]int) []string {
	result := []string{}
	start := 0
	for i, slot := range slots {
		for j := start; j < len(cohort); j++ {
			if metroOrderSlotMatches(slot, cohort[j]) && ways[i+1][j+1] > 0 {
				result = append(result, cohort[j])
				start = j + 1
				break
			}
		}
	}
	return result
}

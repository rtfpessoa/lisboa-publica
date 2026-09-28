package patterns

import (
	"encoding/json"
	"time"
)

// MetroOfficialPoint uses the same strict source clock and published-slot
// eligibility as historical emission. It performs no training or evaluation.
func MetroOfficialPoint(row Row, now time.Time) (string, *time.Time, *time.Time) {
	slot := firstFutureSlot(row, now)
	if slot < 0 {
		return "", nil, nil
	}
	clock, _ := sourceClock(row.Clock)
	ids := []string{row.Train, row.Train2, row.Train3}
	seconds, _ := eta([]json.RawMessage{row.ETA, row.ETA2, row.ETA3}[slot])
	point := clock.Add(time.Duration(seconds * float64(time.Second)))
	return ids[slot], &point, &clock
}

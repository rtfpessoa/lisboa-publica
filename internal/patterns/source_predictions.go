package patterns

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"
)

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sourceClock(value string) (time.Time, bool) {
	at, err := time.ParseInLocation("20060102150405", value, lisbon)
	if err != nil || at.Format("20060102150405") != value {
		return time.Time{}, false
	}
	// A source clock without an offset cannot resolve the repeated DST hour.
	for _, delta := range []time.Duration{-time.Hour, time.Hour} {
		if at.Add(delta).In(lisbon).Format("20060102150405") == value {
			return time.Time{}, false
		}
	}
	return at.UTC(), true
}

func eta(value json.RawMessage) (float64, bool) {
	var number json.Number
	if len(value) == 0 || json.Unmarshal(value, &number) != nil {
		return 0, false
	}
	n, err := number.Float64()
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 7200
}

func validTrain(s string) bool { return s != "" && s != "0" && s != "--" && len(s) <= 128 }

func firstFutureSlot(r Row, now time.Time) int {
	clock, ok := sourceClock(r.Clock)
	if !ok || now.Before(clock) || now.Sub(clock) > 90*time.Second {
		return -1
	}
	ids := []string{r.Train, r.Train2, r.Train3}
	values := []json.RawMessage{r.ETA, r.ETA2, r.ETA3}
	for i, value := range ids {
		id := strings.TrimSpace(value)
		if !validTrain(id) {
			continue
		}
		count := 0
		for _, other := range ids {
			if strings.TrimSpace(other) == id {
				count++
			}
		}
		seconds, valid := eta(values[i])
		if count == 1 && valid && clock.Add(time.Duration(seconds*float64(time.Second))).After(now) {
			return i
		}
	}
	return -1
}

type officialSelection struct {
	stop, train, direction string
	now                    time.Time
}

func officialFor(rows []Row, stop, train, direction string, now time.Time) *official {
	selection := officialSelection{stop, train, direction, now}
	var found *official
	for _, row := range rows {
		points := selection.rowPoints(row)
		for _, point := range points {
			if found != nil {
				return nil
			}
			found = point
		}
	}
	return found
}

func (q officialSelection) rowPoints(r Row) []*official {
	points := []*official{}
	if r.Stop != q.stop || r.Destination != q.direction {
		return points
	}
	clock, ok := sourceClock(r.Clock)
	if !ok || q.now.Before(clock) || q.now.Sub(clock) > 90*time.Second {
		return points
	}
	values := []json.RawMessage{r.ETA, r.ETA2, r.ETA3}
	for i, id := range []string{r.Train, r.Train2, r.Train3} {
		if strings.TrimSpace(id) != q.train {
			continue
		}
		seconds, valid := eta(values[i])
		at := clock.Add(time.Duration(seconds * float64(time.Second)))
		if valid && at.After(q.now) {
			points = append(points, &official{at, clock, r.Platform, i == firstFutureSlot(r, q.now)})
		}
	}
	return points
}

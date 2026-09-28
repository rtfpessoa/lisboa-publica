package patterns

import "time"

// Rules verified 2026-09-27 against Labour Code art.234 and Lisbon's municipal calendar.
// This is a civil-date grouping, not an assertion about any operator's service calendar.
const calendarVersion = "pt-national-lisbon-2026-v1"

func easter(year int) time.Time {
	a, b, c := year%19, year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 12, 0, 0, 0, lisbon)
}
func dayType(at time.Time) string {
	local := at.In(lisbon)
	if local.Year() < 2017 || local.Year() > 2199 {
		return "calendar_unknown"
	}
	if lisbonCivilHoliday(local) {
		return "holiday"
	}
	days := map[time.Weekday]string{time.Saturday: "saturday", time.Sunday: "sunday"}
	value := days[local.Weekday()]
	if value == "" {
		value = "weekday"
	}
	return value
}

func lisbonCivilHoliday(local time.Time) bool {
	fixed := map[string]bool{"01-01": true, "04-25": true, "05-01": true, "06-10": true, "06-13": true, "08-15": true, "10-05": true, "11-01": true, "12-01": true, "12-08": true, "12-25": true}
	if fixed[local.Format("01-02")] {
		return true
	}
	date, sunday := local.Format("2006-01-02"), easter(local.Year())
	for _, offset := range []int{-2, 0, 60} {
		if sunday.AddDate(0, 0, offset).Format("2006-01-02") == date {
			return true
		}
	}
	return false
}

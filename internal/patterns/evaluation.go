package patterns

type EvaluationReport struct {
	Cohort               string   `json:"cohort"`
	Support              string   `json:"support"`
	Function             string   `json:"function"`
	Direction            string   `json:"direction"`
	Route                string   `json:"route"`
	Mode                 string   `json:"mode"`
	Profile              string   `json:"profile"`
	Condition            string   `json:"condition"`
	Horizon              int32    `json:"horizon"`
	Cases                int64    `json:"cases"`
	OfficialAvailable    int64    `json:"official_available"`
	OwnAvailable         int64    `json:"own_available"`
	Paired               int64    `json:"paired"`
	Evaluated            int64    `json:"evaluated"`
	Journeys             int64    `json:"journeys"`
	Days                 int      `json:"days"`
	MAEOwnLower          *float64 `json:"mae_own_lower"`
	MAEOwnUpper          *float64 `json:"mae_own_upper"`
	MAEOfficialLower     *float64 `json:"mae_official_lower"`
	MAEOfficialUpper     *float64 `json:"mae_official_upper"`
	P90OwnLower          *float64 `json:"p90_own_lower"`
	P90OwnUpper          *float64 `json:"p90_own_upper"`
	P90OfficialLower     *float64 `json:"p90_official_lower"`
	P90OfficialUpper     *float64 `json:"p90_official_upper"`
	BandCases            int64    `json:"band_cases"`
	BandCertain          int64    `json:"band_certain"`
	BandPossible         int64    `json:"band_possible"`
	JourneyCountComplete bool     `json:"journey_count_complete"`
}

func reportBase(f Forecast, c Config) Aggregate {
	a := baseAggregate(aggregateRequest{f.IssuedAt, f.Route, f.Direction, f.Stop, f.Platform, f.Profile, f.Condition, "report-issued:" + f.Function}, c)
	a.ErrorHistogram = true
	a.Mode = f.Mode
	a.Cohort = forecastCohort(f)
	a.Horizon = -1
	if f.OfficialAt != nil {
		a.Horizon = horizon(f.OfficialAt.Sub(f.IssuedAt).Seconds())
	}
	a.Count = 1
	a.KnownAt = f.IssuedAt.UnixNano()
	return a
}
func (e *engine) reportIssued(f Forecast, c Config) {
	a := reportBase(f, c)
	e.add(a)
	for _, v := range []struct {
		kind string
		yes  bool
	}{{"report-official:", f.OfficialAt != nil}, {"report-own:", f.OwnAt != nil}, {"report-paired:", f.OwnAt != nil && f.OfficialAt != nil}} {
		if v.yes {
			b := a
			b.Kind = v.kind + f.Function
			e.add(b)
		}
	}
	e.reportJourney(f, a)
}

func (e *engine) reportJourney(f Forecast, a Aggregate) {
	if f.Episode == "" {
		a.Kind = "report-unassociated:" + f.Function
		e.add(a)
		return
	}
	if e.ReportSeen == nil {
		e.ReportSeen = map[string]int64{}
	}
	a.Kind = "report-journeys:" + f.Function
	a.EpisodeHash = digest(f.Episode)
	key := digest([]any{a.Date, a.Route, a.Direction, a.Stop, a.Mode, a.Profile, a.Condition, a.Horizon, a.Cohort, f.Function, f.Episode})
	if _, ok := e.ReportSeen[key]; ok {
		return
	}
	if len(e.ReportSeen) >= aggregateCapacity(e) {
		a.Kind = "report-distinct-incomplete:" + f.Function
		e.add(a)
		return
	}
	e.ReportSeen[key] = f.IssuedAt.UnixNano()
	e.add(a)
}

func forecastCohort(f Forecast) string {
	if f.OwnAt != nil && f.OfficialAt != nil {
		return "paired"
	}
	if f.OwnAt != nil {
		return "own_only"
	}
	if f.OfficialAt != nil {
		return "official_only"
	}
	return "neither"
}

package patterns

import (
	"strconv"
	"strings"
	"time"
)

const providerReference = "published-stop-status-triple-v1"
const providerMode = "official+published-stop-components/v1"
const maxProviderAggregates = 5000
const maxProviderTracks = 2000
const maxProviderPaths = 256
const maxProviderCalls = 2000
const maxProviderComponentRefs = 20000

type ProviderVisit struct {
	Stop, Name string
	Sequence   int
	Lat, Lon   float64
}

// ProviderJourney is a verified published order, not proof that it operated.
type ProviderJourney struct {
	Route, Direction, Plan, Pattern, Headsign string
	Visits                                    []ProviderVisit
}
type ProviderPrediction struct {
	ID, Stop, Route, Trip, Plan, ServiceDate, Vehicle string
	Sequence                                          *int
	ExpectedAt, ReceivedAt, ValidUntil                time.Time
	SourceAt                                          *time.Time
}
type providerTrack struct {
	Context, Path string
	Previous      Observation
	Group         *group
	Index         int
}
type providerState struct {
	Engine                  *engine
	Paths                   map[string]ProviderJourney
	Tracks                  map[string]*providerTrack
	Predictions             map[string]ProviderPrediction
	PredictionSamples       map[string]time.Time
	LastSample, LastReceipt time.Time
	LastInputAt             time.Time
	FullError               string
	PendingCuts             map[string]bool
	PendingGap              bool
}

func newProvider(operator string) *providerState {
	e := newEngine()
	e.Operator = operator
	e.Capacity = maxProviderAggregates
	e.ReportSeen = map[string]int64{}
	return &providerState{Engine: e, Paths: map[string]ProviderJourney{}, Tracks: map[string]*providerTrack{}, Predictions: map[string]ProviderPrediction{}, PredictionSamples: map[string]time.Time{}, PendingCuts: map[string]bool{}}
}
func aggregateCapacity(e *engine) int {
	if e.Capacity > 0 && e.Capacity <= maxEngineAggregates {
		return e.Capacity
	}
	return maxEngineAggregates
}
func routeOperator(route string) string {
	operator, _, ok := strings.Cut(route, ":")
	if ok && knownOperator(operator) {
		return operator
	}
	return "metro"
}
func routeReference(route string) string {
	if routeOperator(route) != "metro" {
		return providerReference
	}
	return referenceProfile
}
func engineReference(e *engine) string {
	if e.Operator != "" && e.Operator != "metro" {
		return providerReference
	}
	return referenceProfile
}
func ProviderJourneyID(p ProviderJourney) string { return digest(p) }
func providerProfile(p ProviderJourney, c Config) string {
	return "reported-stop-v1-" + ProviderJourneyID(p) + "-s" + strconv.Itoa(int(c.SampleInterval/time.Second)) + "-b" + strconv.Itoa(c.BinSeconds)
}

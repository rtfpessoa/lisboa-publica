package app

import (
	"lisboapublica/internal/api"
	"time"
)

type stopEventSelection struct {
	selected  map[string]reportedStopEvent
	conflicts map[string]bool
}

func applyStoredEvents(call *api.StopCall, events []reportedStopEvent, now time.Time) {
	selection := stopEventSelection{selected: map[string]reportedStopEvent{}, conflicts: map[string]bool{}}
	for _, event := range events {
		if matchingStopEvent(call, event, now) {
			selection.accept(event)
		}
	}
	call.Arrival = selection.choose("arrival", call.Arrival, call.Phase == "previous", now)
	call.Departure = selection.choose("departure", call.Departure, call.Phase == "previous", now)
}
func matchingStopEvent(call *api.StopCall, event reportedStopEvent, now time.Time) bool {
	return call.JourneyId != nil && event.Journey == *call.JourneyId && event.Sequence == call.StopSequence && event.valid(now)
}
func (s stopEventSelection) accept(event reportedStopEvent) {
	old, exists := s.selected[event.Kind]
	if !exists {
		s.selected[event.Kind] = event
		return
	}
	if sameStopEvent(old, event) {
		return
	}
	if event.Revision > old.Revision && event.Correction {
		s.selected[event.Kind] = event
		s.conflicts[event.Kind] = false
	} else {
		s.conflicts[event.Kind] = true
	}
}
func (s stopEventSelection) choose(kind string, previous api.CallTime, past bool, now time.Time) api.CallTime {
	if s.conflicts[kind] {
		return missingCallTime("Registos reais contraditórios na fonte")
	}
	var actual *api.CallTimeEvidence
	if event, ok := s.selected[kind]; ok {
		actual = &event.Evidence
	}
	return selectCallTime(actual, previous.Prediction, previous.Schedule, past, now)
}

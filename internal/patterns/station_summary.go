package patterns

import (
	"fmt"
	"sort"
	"strings"
)

type hourPatternSummary struct {
	value HourPattern
	days  map[string]bool
	sum   float64
}

type stationPatternSummary struct {
	view        View
	stop        string
	groups      map[string]*hourPatternSummary
	collected   map[string]bool
	reports     reportBuilder
	dateEntries int
}

func (s *stationPatternSummary) add(a Aggregate) {
	if a.Stop == s.stop {
		s.addStationCounters(a)
	}
	if a.Stop != s.stop || a.Count <= 0 {
		return
	}
	if a.Kind != "signals" && a.Kind != "component" {
		return
	}
	g := s.hourGroup(a)
	s.collected[a.Date] = true
	s.addHourDate(g, a.Date)
	if g.value.ResolutionSeconds > 0 {
		g.value.ResolutionSeconds = commonResolution(g.value.ResolutionSeconds, int(a.Resolution))
	}
	if a.Kind == "signals" {
		g.value.Signals += a.Count
	} else {
		g.value.ComponentSamples += a.Count
		g.sum += a.Sum
	}
}

func (s *stationPatternSummary) addStationCounters(a Aggregate) {
	s.reports.add(a)
	if strings.HasPrefix(a.Kind, "evaluated:") {
		s.view.Evaluated += int(a.Count)
	}
	if strings.HasPrefix(a.Kind, "lost_reference:") {
		s.view.LostReference += int(a.Count)
	}
	if a.Kind == "receipts" {
		s.collected[a.Date] = true
	}
}

func (s *stationPatternSummary) hourGroup(a Aggregate) *hourPatternSummary {
	profile := resolutionProfile(a.Profile)
	if a.Kind == "component" && a.Compatibility != "" {
		profile = "compatible:" + a.Compatibility
	}
	// Incompatible profiles, offsets, calendars, references and conditions stay separate.
	key := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%d|%s|%s|%s", a.Route, a.Direction, a.Platform, a.Hour, a.DayType, profile, a.Offset, a.Condition, a.Target, a.TargetPlatform) + "|" + a.Calendar + "|" + a.Reference
	g := s.groups[key]
	if g == nil {
		g = &hourPatternSummary{value: HourPattern{Hour: int(a.Hour), Direction: a.Direction, Route: a.Route, Platform: a.Platform, DayType: a.DayType, ComponentTarget: a.Target, Profile: profile, OffsetSeconds: int(a.Offset), ResolutionSeconds: int(a.Resolution), Condition: a.Condition}, days: map[string]bool{}}
		s.groups[key] = g
	}
	return g
}

func (s *stationPatternSummary) addHourDate(g *hourPatternSummary, date string) {
	if g.days[date] {
		return
	}
	if s.dateEntries >= maxEngineAggregates {
		s.view.Status = "degraded"
		s.view.Message = "Resumo limitado; o número de dias de suporte pode ser inferior ao total retido."
		return
	}
	g.days[date] = true
	s.dateEntries++
}

func (s *stationPatternSummary) finish() (View, error) {
	for _, g := range s.groups {
		g.value.Days = len(g.days)
		if g.value.ComponentSamples > 0 {
			mean := g.sum / float64(g.value.ComponentSamples)
			g.value.MeanComponentSeconds = &mean
		}
		s.view.Patterns = append(s.view.Patterns, g.value)
	}
	sort.Slice(s.view.Patterns, func(i, j int) bool {
		a, b := s.view.Patterns[i], s.view.Patterns[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		return digest(a) < digest(b)
	})
	if len(s.view.Patterns) > 5000 || len(s.reports.groups) > 5000 {
		return s.view, fmt.Errorf("station pattern result limit")
	}
	s.view.Evaluation = s.reports.finish()
	s.view.CollectedDays = len(s.collected)
	return s.view, nil
}

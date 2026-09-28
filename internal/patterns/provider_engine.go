package patterns

import (
	"math"
	"strconv"
	"time"
)

func visitPlatform(v ProviderVisit) string { return "visit:" + strconv.Itoa(v.Sequence) }

type positionPoint struct{ lat, lon float64 }

func positionDistance(from, to positionPoint) float64 {
	return math.Hypot((from.lon-to.lon)*math.Cos(from.lat*math.Pi/180), from.lat-to.lat) * 111320
}
func providerContext(v Observation, at time.Time) string {
	day := v.OperationalDate
	if day == "" {
		day = "receipt-day:" + at.In(lisbon).Format("2006-01-02")
	}
	return digest([]string{v.ID, v.SourceID, v.SourceURL, v.Trip, v.Route, v.Plan, v.Pattern, v.Journey, day})
}
func (p *providerState) reset() {
	p.Tracks = map[string]*providerTrack{}
	p.PendingCuts = map[string]bool{}
	p.Engine.resetContinuity()
	p.PendingGap = true
	p.Engine.Gaps++
}

// Intermediate cuts are retained individually; a true value also revokes
// training contradicted by duplicate published identity.
func (p *providerState) cut(id string, withdraw bool, c Config) {
	if track := p.Tracks[id]; track != nil {
		if withdraw {
			p.Engine.Profile = providerProfile(p.Paths[track.Path], c)
			p.Engine.withdraw(track.Group)
		} else {
			p.Engine.Gaps++
		}
		delete(p.Tracks, id)
		delete(p.Engine.Groups, id)
	}
	if p.PendingCuts == nil {
		p.PendingCuts = map[string]bool{}
	}
	if len(p.PendingCuts) >= maxProviderTracks {
		p.reset()
		return
	}
	p.PendingCuts[id] = p.PendingCuts[id] || withdraw
}
func (p *providerState) retireCases(now time.Time, c Config) {
	active := map[string]bool{}
	for _, g := range p.Engine.Groups {
		if g.Active {
			active[g.ID] = true
		}
	}
	kept := p.Engine.Cases[:0]
	for _, f := range p.Engine.Cases {
		if f.Evaluated {
			continue
		}
		if active[f.Episode] {
			kept = append(kept, f)
			continue
		}
		f.Result = "lost_support"
		p.Engine.Outcomes = append(p.Engine.Outcomes, f)
		a := baseAggregate(aggregateRequest{f.IssuedAt, f.Route, f.Direction, f.Stop, f.Platform, f.Profile, f.Condition, "lost_reference:" + f.Function}, c)
		a.Count = 1
		a.KnownAt = now.UnixNano()
		p.Engine.add(a)
	}
	p.Engine.Cases = kept
}

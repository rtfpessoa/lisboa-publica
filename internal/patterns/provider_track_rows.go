package patterns

import (
	"strings"
	"time"
)

type providerSample struct {
	receipt    ProviderReceipt
	config     Config
	sampled    bool
	duplicates map[string]bool
}

type providerSampleRow struct {
	observation Observation
	path        ProviderJourney
	track       *providerTrack
	index       int
}

func (p *providerState) observeProviderRow(v Observation, context providerSample) {
	row, valid := p.providerRowInput(v, context)
	if !valid || !context.sampled {
		return
	}
	p.recordProviderCoverage(row, context)
	if row.track == nil {
		p.beginProviderTrack(row, context)
		return
	}
	p.advanceProviderTrack(row, context)
}

func (p *providerState) providerRowInput(v Observation, context providerSample) (providerSampleRow, bool) {
	path, known := p.Paths[v.Journey]
	valid := context.admits(v, path, known)
	track := p.Tracks[v.ID]
	if providerTrackContradicted(track, v, context.receipt.ReceivedAt, valid) {
		p.cut(v.ID, false, context.config)
		track = nil
	}
	row := providerSampleRow{observation: v, path: path, track: track, index: -1}
	if !valid {
		return row, false
	}
	row.index = observationIndex(path, v, track)
	if row.index < 0 {
		if track != nil {
			p.cut(v.ID, false, context.config)
		}
		return row, false
	}
	return row, true
}

func (q providerSample) admits(v Observation, path ProviderJourney, known bool) bool {
	if q.receipt.Error != "" || q.duplicates[v.ID] || !known {
		return false
	}
	if !strings.HasPrefix(v.ID, q.receipt.Operator+":") || !freshObservation(v, q.receipt.ReceivedAt) {
		return false
	}
	return v.Route == path.Route && admissibleProviderStatus(v.Status)
}

func admissibleProviderStatus(status string) bool {
	return status == "STOPPED_AT" || status == "INCOMING_AT" || status == "IN_TRANSIT_TO"
}

func providerTrackContradicted(track *providerTrack, v Observation, received time.Time, valid bool) bool {
	if track == nil {
		return false
	}
	if !valid || track.Context != providerContext(v, received) {
		return true
	}
	prior := track.Previous
	if v.ObservedAt.Before(prior.ObservedAt) || v.ObservedAt.Sub(prior.ObservedAt) > 60*time.Second {
		return true
	}
	return v.ObservedAt.Equal(prior.ObservedAt) && digest(v) != digest(prior)
}

func (p *providerState) recordProviderCoverage(row providerSampleRow, q providerSample) {
	profile := providerProfile(row.path, q.config)
	p.Engine.Profile = profile
	v := row.observation
	a := baseAggregate(aggregateRequest{v.ObservedAt, row.path.Route, row.path.Direction, v.Stop, visitPlatform(row.path.Visits[row.index]), profile, "unknown", "receipts"}, q.config)
	a.Count, a.KnownAt = 1, q.receipt.ReceivedAt.UnixNano()
	p.Engine.add(a)
}

func (p *providerState) beginProviderTrack(row providerSampleRow, q providerSample) {
	if len(p.Tracks) >= maxProviderTracks {
		p.Engine.Limited = true
		return
	}
	v, path := row.observation, row.path
	context := providerContext(v, q.receipt.ReceivedAt)
	g := &group{ID: digest([]any{context, q.receipt.ReceivedAt})[:24], Train: v.ID, Route: path.Route, Direction: path.Direction, Pairs: map[string]bool{}}
	p.Tracks[v.ID] = &providerTrack{Context: context, Path: v.Journey, Previous: v, Group: g, Index: row.index}
	p.Engine.Groups[v.ID] = g
}

func (p *providerState) advanceProviderTrack(row providerSampleRow, q providerSample) {
	v, prior := row.observation, row.track.Previous
	if v.ObservedAt.Equal(prior.ObservedAt) {
		return
	}
	if publishedArrivalTransition(v, prior, row.path.Visits[row.index]) {
		if !p.appendProviderSignal(row, q) {
			return
		}
	}
	row.track.Previous, row.track.Index = v, row.index
}

func publishedArrivalTransition(v, prior Observation, visit ProviderVisit) bool {
	if v.Status != "STOPPED_AT" || prior.Status == "STOPPED_AT" || prior.Stop != v.Stop {
		return false
	}
	return positionDistance(positionPoint{v.Lat, v.Lon}, positionPoint{visit.Lat, visit.Lon}) <= 150
}

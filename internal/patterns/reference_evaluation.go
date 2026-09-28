package patterns

import (
	"math"
	"time"
)

type referenceEvaluation struct {
	signal signal
	now    time.Time
	config Config
}

func (e *engine) evaluateReference(g *group, s signal, now time.Time, c Config) {
	context := referenceEvaluation{s, now, c}
	for i := range e.Cases {
		f := &e.Cases[i]
		if context.eligible(*f, g) {
			e.evaluateReferenceCase(f, context)
		}
	}
}

func (q referenceEvaluation) eligible(f Forecast, g *group) bool {
	if f.Evaluated || f.Episode != g.ID || f.Stop != q.signal.Stop {
		return false
	}
	if f.Platform != "" && f.Platform != q.signal.Platform {
		return false
	}
	if !q.signal.L.After(f.IssuedAt) || !q.now.After(f.IssuedAt) {
		return false
	}
	return uniqueCaseReference(f, g.Signals)
}

func uniqueCaseReference(f Forecast, signals []signal) bool {
	hits := 0
	for _, candidate := range signals {
		if candidate.Stop != f.Stop || !candidate.L.After(f.IssuedAt) {
			continue
		}
		if f.Platform == "" || candidate.Platform == f.Platform {
			hits++
		}
	}
	return hits == 1
}

func (e *engine) evaluateReferenceCase(f *Forecast, q referenceEvaluation) {
	f.Evaluated, f.Result = true, "evaluated_proxy"
	f.ReferenceLower, f.ReferenceUpper = &q.signal.L, &q.signal.U
	count := baseAggregate(aggregateRequest{f.IssuedAt, f.Route, f.Direction, f.Stop, f.Platform, f.Profile, f.Condition, "evaluated:" + f.Function}, q.config)
	count.Count, count.KnownAt = 1, q.now.UnixNano()
	e.add(count)
	if f.OwnAt != nil {
		e.evaluateOwnReference(f, q)
	} else {
		e.evaluateOfficialReference(*f, q)
	}
	e.Outcomes = append(e.Outcomes, *f)
}

func (q referenceEvaluation) errorHistogram(f Forecast, point time.Time, kind string) Aggregate {
	low, high := errorBounds(point, q.signal.L, q.signal.U)
	a := reportBase(f, q.config)
	a.Kind, a.Sum, a.LowerSum, a.UpperSum = kind+f.Function, high, low, high
	a.LowerBucket = int32(math.Floor(low / float64(q.config.BinSeconds)))
	a.Bucket = int32(math.Floor(high / float64(q.config.BinSeconds)))
	a.KnownAt = q.now.UnixNano()
	return a
}

func (e *engine) evaluateOfficialReference(f Forecast, q referenceEvaluation) {
	if f.OfficialAt != nil {
		e.add(q.errorHistogram(f, *f.OfficialAt, "evaluation-official:"))
	}
}

func (e *engine) evaluateOwnReference(f *Forecast, q referenceEvaluation) {
	low, high := errorBounds(*f.OwnAt, q.signal.L, q.signal.U)
	f.ErrorLower, f.ErrorUpper = &low, &high
	a := q.errorHistogram(*f, *f.OwnAt, "evaluation-own:")
	e.add(a)
	e.evaluateOfficialReference(*f, q)
	e.evaluateReferenceBand(*f, q)
	if f.Selected {
		a.Kind, a.Horizon = "calibration:"+f.Function, horizon(f.OwnAt.Sub(f.IssuedAt).Seconds())
		e.add(a)
	}
}

func (e *engine) evaluateReferenceBand(f Forecast, q referenceEvaluation) {
	if f.LowerAt == nil || f.UpperAt == nil {
		return
	}
	band := reportBase(f, q.config)
	band.KnownAt, band.Kind = q.now.UnixNano(), "report-band:"+f.Function
	e.add(band)
	if !q.signal.L.Before(*f.LowerAt) && !q.signal.U.After(*f.UpperAt) {
		band.Kind = "report-band-certain:" + f.Function
		e.add(band)
	}
	if !q.signal.U.Before(*f.LowerAt) && !q.signal.L.After(*f.UpperAt) {
		band.Kind = "report-band-possible:" + f.Function
		e.add(band)
	}
}

func errorBounds(point, L, U time.Time) (float64, float64) {
	return math.Max(0, math.Max(L.Sub(point).Seconds(), point.Sub(U).Seconds())), math.Max(math.Abs(L.Sub(point).Seconds()), math.Abs(U.Sub(point).Seconds()))
}

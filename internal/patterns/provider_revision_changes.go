package patterns

import "fmt"

func (j *providerRevision) applyChanges() error {
	for _, change := range j.changes {
		if err := j.applyChange(change); err != nil {
			return err
		}
	}
	if len(j.frames) == 0 {
		return fmt.Errorf("provider detail unavailable")
	}
	return nil
}

func (j *providerRevision) applyChange(change ProviderCorrection) error {
	if change.Evidence == "" || len(change.Evidence) > 2048 || len(change.ExpectedHash) != 64 || change.ReceivedAt.After(j.now) {
		return fmt.Errorf("provider correction needs source evidence and expected hash")
	}
	hits := 0
	for i := range j.frames {
		r := &j.frames[i]
		if !r.ReceivedAt.Equal(change.ReceivedAt) {
			continue
		}
		count, err := j.correctRows(r, change)
		if err != nil {
			return err
		}
		hits += count
	}
	if hits != 1 {
		return fmt.Errorf("provider correction input expired, ambiguous or changed")
	}
	return nil
}

func (j *providerRevision) correctRows(r *ProviderReceipt, change ProviderCorrection) (int, error) {
	hits := 0
	for i, row := range r.Rows {
		if digest(row) != change.ExpectedHash {
			continue
		}
		path, err := j.correctionPath(row, change.Row, r)
		if err != nil {
			return 0, err
		}
		j.affected[path.Route+"|"+path.Direction] = true
		j.addObservationDay(row)
		r.Rows[i] = change.Row
		hits++
	}
	return hits, nil
}

func (j *providerRevision) correctionPath(row, replacement Observation, r *ProviderReceipt) (ProviderJourney, error) {
	if providerContext(row, r.ReceivedAt) != providerContext(replacement, r.ReceivedAt) || row.Stop != replacement.Stop {
		return ProviderJourney{}, fmt.Errorf("provider correction changes unverified context")
	}
	path, ok := j.paths[row.Journey]
	if !ok {
		return path, fmt.Errorf("provider correction topology missing")
	}
	return path, nil
}

func (j *providerRevision) addObservationDay(row Observation) {
	if !row.ObservedAt.IsZero() {
		j.days[row.ObservedAt.In(lisbon).Format("2006-01-02")] = true
	}
}

func (j *providerRevision) expandDays() error {
	for _, r := range j.frames {
		j.expandFrameDays(r)
	}
	return nil
}

func (j *providerRevision) expandFrameDays(r ProviderReceipt) {
	for _, row := range r.Rows {
		path := j.paths[row.Journey]
		if j.affected[path.Route+"|"+path.Direction] {
			j.days[r.ReceivedAt.In(lisbon).Format("2006-01-02")] = true
			j.addObservationDay(row)
		}
	}
	for _, f := range r.Forecasts {
		if j.affected[f.Route+"|"+f.Direction] {
			j.days[f.IssuedAt.In(lisbon).Format("2006-01-02")] = true
		}
	}
}

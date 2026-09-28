package patterns

import "fmt"

func (j *metroRevision) applyChanges() error {
	all := append(j.existing, j.changes...)
	for i, change := range all {
		fresh := i >= len(j.existing)
		if fresh && !validCorrectionEvidence(change.Evidence, change.ExpectedHash) {
			return fmt.Errorf("correction requires source evidence and expected hash")
		}
		if err := j.applyChange(change, fresh); err != nil {
			return err
		}
	}
	j.expandAffectedDays()
	return nil
}

func validCorrectionEvidence(evidence, hash string) bool {
	return evidence != "" && len(evidence) <= 2048 && len(hash) == 64
}

func (j *metroRevision) applyChange(change Correction, fresh bool) error {
	found := false
	for i := range j.frames {
		frame := &j.frames[i]
		if !frame.ReceivedAt.Equal(change.ReceivedAt) {
			continue
		}
		changed, err := j.correctFrame(frame, change, fresh)
		if err != nil {
			return err
		}
		found = found || changed
	}
	if !found && fresh {
		return fmt.Errorf("correction input unavailable or changed")
	}
	return nil
}

func (j *metroRevision) correctFrame(frame *Receipt, change Correction, fresh bool) (bool, error) {
	if frame.Topology == nil {
		return false, fmt.Errorf("retained topology unavailable; cannot reconstruct")
	}
	found := false
	for i, row := range frame.Rows {
		if digest(row) != change.ExpectedHash {
			continue
		}
		route, err := metroCorrectionRoute(frame, row, change)
		if err != nil {
			return false, err
		}
		frame.Rows[i], found = change.Row, true
		if fresh {
			j.affected[route+"|"+row.Destination] = true
			j.days[frame.ReceivedAt.In(lisbon).Format("2006-01-02")] = true
		}
	}
	return found, nil
}

func (j *metroRevision) expandAffectedDays() {
	for _, frame := range j.frames {
		if frame.Topology == nil {
			continue
		}
		for _, row := range frame.Rows {
			route := frame.Topology.route(row.Stop, row.Destination)
			if !j.affected[route+"|"+row.Destination] {
				continue
			}
			j.days[frame.ReceivedAt.In(lisbon).Format("2006-01-02")] = true
			if at, valid := sourceClock(row.Clock); valid {
				j.days[at.In(lisbon).Format("2006-01-02")] = true
			}
		}
	}
}

func metroCorrectionRoute(frame *Receipt, row Row, change Correction) (string, error) {
	if contextKey(row) != contextKey(change.Row) {
		return "", fmt.Errorf("context changes require separate evidence validation")
	}
	route := frame.Topology.route(row.Stop, row.Destination)
	if route == "" {
		return "", fmt.Errorf("ambiguous correction route")
	}
	return route, nil
}

package patterns

import (
	"encoding/json"
	"fmt"
)

func (j *providerRecording) prepareBackup() error {
	if !j.sampled {
		return nil
	}
	encoded, err := json.Marshal(j.state)
	if err != nil || len(encoded) > maxBlockBytes {
		return fmt.Errorf("provider state size limit")
	}
	j.backup = &providerState{}
	if err = json.Unmarshal(encoded, j.backup); err != nil {
		return err
	}
	j.admittedBefore = j.service.admittedProviderDays(j.receipt.Operator)
	return nil
}

func (s *Service) admittedProviderDays(operator string) map[string]bool {
	days := map[string]bool{}
	for _, b := range s.index.Blocks {
		if b.Operator == operator && b.Kind == "aggregate" {
			days[b.Date] = true
		}
	}
	return days
}

func (j *providerRecording) rollback(result error) {
	if result == nil || j.committed || j.backup == nil {
		return
	}
	// Failed publication cannot retain unarchived training/cadence. Already
	// committed FIFO retirement remains authoritative over the earlier snapshot.
	admitted := j.service.admittedProviderDays(j.receipt.Operator)
	for key, a := range j.backup.Engine.Aggregates {
		if j.admittedBefore[a.Date] && !admitted[a.Date] {
			delete(j.backup.Engine.Aggregates, key)
			delete(j.backup.Engine.DirtyDays, a.Date)
		}
	}
	j.backup.reset()
	j.service.providers[j.receipt.Operator] = j.backup
}

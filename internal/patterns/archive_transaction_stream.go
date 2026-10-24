package patterns

import (
	"context"
	"fmt"
	"math"
	"time"
)

// transactionItem regenerates a deterministic payload without retaining other
// generations in memory. The caller holds the archive lock throughout.
type transactionItem struct {
	block  block
	encode func() ([]byte, error)
}

type streamTransaction struct {
	service  *Service
	context  context.Context
	items    []transactionItem
	sources  map[string]bool
	validate func() error
	now      time.Time
}

// publish writes one bounded generation at a time, but admits all generations
// and their checkpoint together through one durable manifest.
func (j *streamTransaction) publish() error {
	prepared, reserve, err := j.describe()
	if err != nil {
		return err
	}
	guarded, err := j.service.transactionSources(prepared)
	if err != nil {
		return err
	}
	if err := j.checkState(); err != nil {
		return err
	}
	if err := j.service.admitTransaction(prepared, guarded, reserve, j.now); err != nil {
		return err
	}
	if err := j.checkState(); err != nil {
		return err
	}
	transaction, err := j.service.admittedTransaction(prepared, guarded, j.sources)
	if err != nil {
		return err
	}
	defer transaction.rollback()
	if err := j.write(transaction, prepared); err != nil {
		return err
	}
	if err := j.checkState(); err != nil {
		return err
	}
	return transaction.commit()
}

func (j *streamTransaction) checkState() error {
	if err := j.context.Err(); err != nil {
		return err
	}
	if j.validate != nil {
		return j.validate()
	}
	return nil
}

func (j *streamTransaction) describe() ([]preparedBlock, int64, error) {
	prepared := make([]preparedBlock, 0, len(j.items))
	reserve := int64(0)
	for _, item := range j.items {
		if err := j.context.Err(); err != nil {
			return nil, 0, err
		}
		blob, err := item.encode()
		if err != nil {
			return nil, 0, err
		}
		if len(blob) > maxBlockBytes {
			return nil, 0, fmt.Errorf("transaction generation exceeds block limit")
		}
		allocation := allocatedBytes(len(blob))
		if reserve > math.MaxInt64-allocation {
			return nil, 0, fmt.Errorf("transaction reservation overflow")
		}
		reserve += allocation
		prepared = append(prepared, preparedBlock{block: namedGeneration(item.block, blob)})
	}
	return prepared, reserve, nil
}

func (j *streamTransaction) write(t *archiveTransaction, expected []preparedBlock) error {
	for i, item := range j.items {
		if err := j.context.Err(); err != nil {
			return err
		}
		blob, err := item.encode()
		if err != nil {
			return err
		}
		if len(blob) > maxBlockBytes || namedGeneration(item.block, blob) != expected[i].block {
			return fmt.Errorf("transaction payload changed during admission; retry")
		}
		if err := t.write(j.context, []preparedBlock{{block: expected[i].block, blob: blob}}); err != nil {
			return err
		}
	}
	return nil
}

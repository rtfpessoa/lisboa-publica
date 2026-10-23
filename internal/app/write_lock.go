package app

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// writeLock is a zero-value-compatible exclusive lock. The weighted semaphore
// queues waiters in order and removes canceled waiters without leaking a lock.
type writeLock struct {
	once sync.Once
	gate *semaphore.Weighted
}

func (l *writeLock) semaphore() *semaphore.Weighted {
	l.once.Do(func() { l.gate = semaphore.NewWeighted(1) })
	return l.gate
}

func (l *writeLock) Lock() { _ = l.LockContext(context.Background()) }

func (l *writeLock) LockContext(ctx context.Context) error {
	return l.semaphore().Acquire(ctx, 1)
}

func (l *writeLock) TryLock() bool { return l.semaphore().TryAcquire(1) }

func (l *writeLock) Unlock() { l.semaphore().Release(1) }

const historyReadSlots int64 = 1 << 30

// historyLock allows concurrent readers while making cleanup's exclusive wait
// cancellable. Queued cleanup is not bypassed by newly arriving readers.
type historyLock struct {
	once sync.Once
	gate *semaphore.Weighted
}

func (l *historyLock) semaphore() *semaphore.Weighted {
	l.once.Do(func() { l.gate = semaphore.NewWeighted(historyReadSlots) })
	return l.gate
}

func (l *historyLock) RLock() { _ = l.semaphore().Acquire(context.Background(), 1) }

func (l *historyLock) RUnlock() { l.semaphore().Release(1) }

func (l *historyLock) LockContext(ctx context.Context) error {
	return l.semaphore().Acquire(ctx, historyReadSlots)
}

func (l *historyLock) Unlock() { l.semaphore().Release(historyReadSlots) }

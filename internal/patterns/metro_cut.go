package patterns

import (
	"crypto/rand"
	"sync/atomic"
)

// MetroCutSnapshot identifies one process-local history visibility boundary.
type MetroCutSnapshot struct {
	Nonce    [16]byte
	Revision uint64
	Pending  bool
	Valid    bool
}

// MetroCut is shared by the archive and its live readers; it never acquires the archive lock.
type MetroCut struct {
	nonce [16]byte
	state atomic.Uint64
}

const invalidMetroCut = ^uint64(0)
const maxMetroCutRevision = invalidMetroCut >> 1

func newMetroCut() *MetroCut { return newMetroCutWith(rand.Read) }

func newMetroCutWith(read func([]byte) (int, error)) *MetroCut {
	c := &MetroCut{}
	if n, err := read(c.nonce[:]); err != nil || n != len(c.nonce) {
		c.state.Store(invalidMetroCut)
	}
	return c
}

// Snapshot returns a point-in-time state; invalid cuts never authorize history.
func (c *MetroCut) Snapshot() MetroCutSnapshot {
	if c == nil {
		return MetroCutSnapshot{Pending: true}
	}
	return c.snapshotState(c.state.Load())
}

func (c *MetroCut) snapshotState(state uint64) MetroCutSnapshot {
	if state == invalidMetroCut {
		return MetroCutSnapshot{Nonce: c.nonce, Pending: true}
	}
	return MetroCutSnapshot{Nonce: c.nonce, Revision: state >> 1, Pending: state&1 != 0, Valid: true}
}

// MarkPending establishes a new boundary, failing permanently closed on overflow.
func (c *MetroCut) MarkPending() MetroCutSnapshot {
	if c == nil {
		return MetroCutSnapshot{Pending: true}
	}
	for {
		old := c.state.Load()
		if old == invalidMetroCut {
			return c.Snapshot()
		}
		next := invalidMetroCut
		if old>>1 < maxMetroCutRevision-1 {
			next = ((old>>1)+1)<<1 | 1
		}
		if c.state.CompareAndSwap(old, next) {
			return c.snapshotState(next)
		}
	}
}

func (c *MetroCut) expectedState(expected MetroCutSnapshot) (uint64, bool) {
	if c == nil || !expected.Valid || expected.Nonce != c.nonce || expected.Revision >= maxMetroCutRevision {
		return 0, false
	}
	state := expected.Revision << 1
	if expected.Pending {
		state |= 1
	}
	return state, true
}

// Claim atomically validates the snapshot as the logical publication point.
func (c *MetroCut) Claim(expected MetroCutSnapshot) bool {
	state, ok := c.expectedState(expected)
	return ok && c.state.CompareAndSwap(state, state)
}

// Acknowledge clears only the exact cut represented by an accepted barrier.
func (c *MetroCut) Acknowledge(expected MetroCutSnapshot) bool {
	state, ok := c.expectedState(expected)
	return ok && expected.Pending && c.state.CompareAndSwap(state, state&^1)
}

// MetroCut returns the archive's single shared visibility state without I/O.
func (s *Service) MetroCut() *MetroCut { return s.metroCut }

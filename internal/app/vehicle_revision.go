package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) vehicleState(f Filter, now time.Time) (*State, time.Time, string, error) {
	if !strings.HasPrefix(f.Revision, "v:") {
		return s.initialVehicleState(f.Revision)
	}
	id, asOf, err := parseVehicleRevision(f.Revision)
	if err == nil {
		err = validateVehicleClock(asOf, now)
	}
	var state *State
	if err == nil {
		state = s.Cache.vehicleRevisionState(id, now)
		err = validateVehicleState(state, asOf)
	}
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return state, asOf, f.Revision, nil
}

func (s *Server) initialVehicleState(revision string) (*State, time.Time, string, error) {
	state, err := s.Cache.state(revision)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	// Lookup precedes the clock: publication can advance Created concurrently.
	asOf := time.Now().UTC()
	if asOf.Before(state.Created) || revision != "" {
		asOf = state.Created
	}
	return state, asOf, "v:" + state.Revision + ":" + strconv.FormatInt(asOf.UnixNano(), 10), nil
}

func parseVehicleRevision(revision string) (string, time.Time, error) {
	parts := strings.Split(revision, ":")
	if len(parts) != 3 {
		return "", time.Time{}, fail(http.StatusBadRequest, "revision", "Revisão de veículos inválida.")
	}
	stamp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", time.Time{}, fail(http.StatusBadRequest, "revision", "Revisão de veículos inválida.")
	}
	return parts[1], time.Unix(0, stamp), nil
}

func validateVehicleClock(asOf, now time.Time) error {
	if asOf.After(now) {
		return fail(http.StatusBadRequest, "revision", "Hora de revisão inválida.")
	}
	if now.Sub(asOf) > staticRefreshInterval {
		return fail(http.StatusGone, "revision_expired", "A coleção expirou. Volte a carregar a primeira página.")
	}
	return nil
}

func (c *Cache) vehicleRevisionState(id string, now time.Time) *State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state := c.current
	if state.Revision != id {
		state = c.versions[id]
		if state != nil && now.Sub(state.Created) > staticRefreshInterval {
			state = nil
		}
	}
	return state
}

func validateVehicleState(state *State, asOf time.Time) error {
	if state == nil {
		return fail(http.StatusGone, "revision_expired", "A coleção mudou. Volte a carregar a primeira página.")
	}
	if asOf.Before(state.Created) {
		return fail(http.StatusBadRequest, "revision", "Hora de revisão inválida.")
	}
	return nil
}

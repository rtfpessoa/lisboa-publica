package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type popupJourneyRevision struct {
	cache      string
	generation int64
	asOf       time.Time
}

func (s *Server) journeyReadState(ctx context.Context, f Filter) (*State, int64, time.Time, string, error) {
	revision, err := s.resolvePopupJourneyRevision(ctx, f.Revision)
	var state *State
	var token string
	if err == nil {
		state, err = s.Cache.state(revision.cache)
	}
	if err == nil {
		token = fmt.Sprintf("j:%s:%d:%d", state.Revision, revision.generation, revision.asOf.UnixNano())
	}
	return state, revision.generation, revision.asOf, token, err
}
func (s *Server) resolvePopupJourneyRevision(ctx context.Context, raw string) (popupJourneyRevision, error) {
	r := popupJourneyRevision{asOf: time.Now().UTC()}
	var err error
	if raw != "" {
		r, err = parsePopupJourneyRevision(raw)
	} else if s.Store != nil && s.Store.DB != nil {
		r.generation, err = s.Store.generation(ctx)
	}
	return r, err
}
func parsePopupJourneyRevision(raw string) (popupJourneyRevision, error) {
	r := popupJourneyRevision{}
	parts := strings.Split(raw, ":")
	if len(parts) != 4 || parts[0] != "j" {
		return r, fail(400, "revision", "Revisão de percurso inválida.")
	}
	r.cache = parts[1]
	generation, e := strconv.ParseInt(parts[2], 10, 64)
	ns, ee := strconv.ParseInt(parts[3], 10, 64)
	r.generation = generation
	r.asOf = time.Unix(0, ns).UTC()
	var err error
	if e != nil || ee != nil || generation < 0 {
		err = fail(400, "revision", "Revisão inválida.")
	} else if !validPopupJourneyInstant(r.asOf) {
		err = fail(410, "revision_expired", "Atualize o percurso.")
	}
	return r, err
}
func validPopupJourneyInstant(at time.Time) bool {
	return !at.After(time.Now().Add(providerClockSkew)) && time.Since(at) <= staticRefreshInterval
}

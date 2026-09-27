package app

import (
	"context"
	"strings"
	"time"
)

func (s *Server) popupFilter(ctx context.Context) (Filter, *State, string, error) {
	f, err := s.filter(ctx, false)
	if err != nil {
		return f, nil, "", err
	}
	if f.Revision == "" && request(ctx).URL.Query().Get("to") == "" {
		f.To = f.From.Add(2 * time.Hour)
	}
	if parts := strings.Split(f.Revision, "|"); len(parts) == 3 && parts[0] == "b" {
		f.Revision = parts[1]
	}
	state, rev, err := s.scheduleState(ctx, &f)
	return f, state, rev, err
}

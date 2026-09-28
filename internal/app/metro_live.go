package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"lisboapublica/internal/api"
)

// MetroStreamLimits bound encoded payloads and connection admission, not exact heap use.
type MetroStreamLimits struct{ FrameBytes, Process, IP, Principal int }

func (l MetroStreamLimits) defaults() MetroStreamLimits {
	if l.FrameBytes <= 0 {
		l.FrameBytes = 256 << 10
	}
	if l.Process <= 0 {
		l.Process = 64
	}
	if l.IP <= 0 {
		l.IP = 16
	}
	if l.Principal <= 0 {
		l.Principal = 4
	}
	return l
}

type metroStreams struct {
	mu              sync.Mutex
	total           int
	ips, principals map[string]int
}

func (s *Server) admitMetroStream(ctx context.Context) (func(), error) {
	limits := s.Options.MetroStreamLimits.defaults()
	ip := s.clientIP(request(ctx))
	actor := principal(ctx)
	key := ""
	if actor != nil {
		key = actor.Email + ":" + actor.KeyID
	}
	m := &s.metroStreams
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ips == nil {
		m.ips = map[string]int{}
		m.principals = map[string]int{}
	}
	if m.total >= limits.Process || m.ips[ip] >= limits.IP || key != "" && m.principals[key] >= limits.Principal {
		return nil, fail(429, "stream_limit", "Limite de ligações em tempo real atingido.")
	}
	m.total++
	m.ips[ip]++
	if key != "" {
		m.principals[key]++
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.total--
		m.ips[ip]--
		if m.ips[ip] == 0 {
			delete(m.ips, ip)
		}
		if key != "" {
			m.principals[key]--
			if m.principals[key] == 0 {
				delete(m.principals, key)
			}
		}
	}, nil
}

type metroInterest struct{ Route, Vehicle, Journey, Stop string }

func readMetroInterest(ctx context.Context) (metroInterest, error) {
	q := request(ctx).URL.Query()
	i := metroInterest{q.Get("route_id"), q.Get("vehicle_id"), q.Get("journey_id"), q.Get("stop_id")}
	if i.Vehicle != "" && i.Stop != "" {
		return i, fail(400, "interest", "Selecione um comboio ou uma estação.")
	}
	for _, value := range []string{i.Route, i.Vehicle, i.Journey, i.Stop} {
		if value != "" && !strings.HasPrefix(value, "metro:") {
			return i, fail(400, "interest", "Esta subscrição suporta apenas Metro.")
		}
	}
	return i, nil
}
func sameMetroRoute(d *StaticData, a, b string) bool {
	if a == b {
		return true
	}
	if d == nil {
		return false
	}
	names := map[string]string{}
	for _, r := range d.Routes {
		names[r.Id] = r.ShortName
	}
	return names[a] != "" && names[a] == names[b]
}
func (s *Server) metroFrame(ctx context.Context, i metroInterest) (api.MetroLiveFrame, []byte, error) {
	release, err := s.admitRead()
	if err != nil {
		return api.MetroLiveFrame{}, nil, err
	}
	defer release()
	return s.projectMetroFrame(ctx, i)
}

// Initial reset admission can wait briefly within the existing bounded stream
// inventory. Ordinary reads remain fail-fast; established ticks coalesce busy.
func (s *Server) initialMetroFrame(ctx context.Context, i metroInterest) (api.MetroLiveFrame, []byte, error) {
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	select {
	case s.expensiveReads <- struct{}{}:
		defer func() { <-s.expensiveReads }()
		return s.projectMetroFrame(ctx, i)
	case <-wait.Done():
		if ctx.Err() != nil {
			return api.MetroLiveFrame{}, nil, ctx.Err()
		}
		return api.MetroLiveFrame{}, nil, fail(http.StatusServiceUnavailable, "busy", "Pedidos em curso; tente novamente dentro de um segundo.")
	}
}
func (s *Server) projectMetroFrame(ctx context.Context, i metroInterest) (api.MetroLiveFrame, []byte, error) {
	b, err := s.newMetroFrameBuilder(ctx, i)
	if err != nil {
		return api.MetroLiveFrame{}, nil, err
	}
	b.selectJourney()
	b.associateVehicles()
	b.scopeCalls()
	b.stationForecasts()
	b.stationDirections()
	b.scopeTrains()
	return encodeMetroFrame(b.frame, s.Options.MetroStreamLimits.defaults().FrameBytes)
}
func (s *Server) GetMetroLive(ctx context.Context, _ api.GetMetroLiveRequestObject) (api.GetMetroLiveResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, expensiveReadTimeout)
	defer cancel()
	i, err := readMetroInterest(ctx)
	if err != nil {
		return nil, err
	}
	f, _, err := s.metroFrame(ctx, i)
	if err != nil {
		return nil, err
	}
	w := writer(ctx)
	etag := `"` + f.Revision + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if matchesMetroETag(request(ctx).Header.Get("If-None-Match"), etag) {
		return api.GetMetroLive304Response{}, nil
	}
	return api.GetMetroLive200JSONResponse{Body: f, Headers: api.GetMetroLive200ResponseHeaders{ETag: &etag}}, nil
}
func matchesMetroETag(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == etag {
			return true
		}
	}
	return false
}

type metroStreamResponse struct {
	server   *Server
	ctx      context.Context
	interest metroInterest
	first    []byte
	revision string
	release  func()
}

func (s *Server) StreamMetroLive(ctx context.Context, _ api.StreamMetroLiveRequestObject) (api.StreamMetroLiveResponseObject, error) {
	i, err := readMetroInterest(ctx)
	if err != nil {
		return nil, err
	}
	release, err := s.admitMetroStream(ctx)
	if err != nil {
		return nil, err
	}
	f, raw, err := s.initialMetroFrame(ctx, i)
	if err != nil {
		release()
		return nil, err
	}
	return metroStreamResponse{s, ctx, i, raw, f.Revision, release}, nil
}
func (r metroStreamResponse) VisitStreamMetroLiveResponse(w http.ResponseWriter) error {
	defer r.release()
	stream := metroStreamWriter{writer: w, control: http.NewResponseController(w)}
	// JSON keeps ordinary deadlines; this response uses bounded stream writes.
	if stream.control.SetWriteDeadline(time.Time{}) != nil {
		return fmt.Errorf("stream deadlines unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	if stream.send("reset", r.first) {
		r.runStream(&stream)
	}
	return nil
}

type metroStreamWriter struct {
	writer  http.ResponseWriter
	control *http.ResponseController
	cursor  uint64
}

func (s *metroStreamWriter) send(event string, raw []byte) bool {
	if s.control.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
		return false
	}
	var err error
	if event == "" {
		_, err = fmt.Fprint(s.writer, ": heartbeat\n\n")
	} else {
		s.cursor++
		_, err = fmt.Fprintf(s.writer, "id: %d\nevent: %s\ndata: %s\n\n", s.cursor, event, raw)
	}
	if err != nil || s.control.Flush() != nil {
		return false
	}
	return s.control.SetWriteDeadline(time.Time{}) == nil
}
func (r metroStreamResponse) runStream(stream *metroStreamWriter) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	auth := time.NewTicker(30 * time.Second)
	defer auth.Stop()
	revision := r.revision
	for {
		keep := true
		select {
		case <-r.ctx.Done():
			keep = false
		case <-auth.C:
			keep = r.authorized()
		case <-heartbeat.C:
			keep = stream.send("", nil)
		case <-ticker.C:
			keep = r.publishFrame(stream, &revision)
		}
		if !keep {
			return
		}
	}
}
func (r metroStreamResponse) authorized() bool {
	actor := principal(r.ctx)
	if actor == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	next, err := r.server.authenticate(request(r.ctx).WithContext(ctx))
	return err == nil && sameMetroPrincipal(actor, next)
}
func (r metroStreamResponse) publishFrame(stream *metroStreamWriter, revision *string) bool {
	if actor := principal(r.ctx); actor != nil && !time.Now().Before(actor.Expires) {
		return false
	}
	f, raw, err := r.server.metroFrame(r.ctx, r.interest)
	if err != nil {
		return metroStreamProjectionError(stream, err)
	}
	if f.Revision != *revision {
		if !stream.send("frame", raw) {
			return false
		}
		*revision = f.Revision
	}
	return true
}
func metroStreamProjectionError(stream *metroStreamWriter, err error) bool {
	var busy *apiError
	if errors.As(err, &busy) && busy.Code == "busy" {
		return true
	} // Coalesce while finite read admission is occupied.
	stream.send("unavailable", []byte(`{"reason":"projection_unavailable"}`))
	return false
}

func sameMetroPrincipal(actor, next *identity) bool {
	return next != nil && next.Email == actor.Email && next.KeyID == actor.KeyID && contains(next.Scopes, "read:transit")
}

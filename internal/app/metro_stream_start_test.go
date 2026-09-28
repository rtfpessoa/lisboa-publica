package app

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetroStreamStartWaitsForTemporaryReadPressure(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	first, err := s.admitRead()
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := s.admitRead()
	if err != nil {
		t.Fatal(err)
	}
	handler, err := s.Handler()
	if err != nil {
		second()
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Both existing slots remain occupied until ordinary work completes.
	released := make(chan struct{})
	go func() { time.Sleep(100 * time.Millisecond); second(); close(released) }()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-released
	if response.StatusCode != http.StatusOK {
		t.Fatalf("temporary pressure rejected new stream: HTTP %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	found := false
	for scanner.Scan() {
		if scanner.Text() == "event: reset" {
			found = true
		}
		if strings.HasPrefix(scanner.Text(), "data: ") {
			break
		}
	}
	if !found {
		t.Fatal("admitted stream omitted its complete reset")
	}
	if cap(s.expensiveReads) != 2 || len(s.expensiveReads) != 1 {
		t.Fatal("stream raised or leaked the shared read limit")
	}
	cancel()
	response.Body.Close()
	waitMetroStreamsReleased(t, s)
}

func TestMetroStreamStartPressureHasFiniteWaitAndReleasesAdmission(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	first, _ := s.admitRead()
	defer first()
	second, _ := s.admitRead()
	defer second()
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	started := time.Now()
	response, err := server.Client().Get(server.URL + "/api/v1/metro/live/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	elapsed := time.Since(started)
	if response.StatusCode != http.StatusServiceUnavailable || elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("initial admission wait is not bounded", response.StatusCode, elapsed)
	}
	waitMetroStreamsReleased(t, s)
	if len(s.expensiveReads) != 2 {
		t.Fatal("failed initialization changed occupied read slots")
	}
}

func TestMetroStreamStartCancellationReleasesAdmission(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	first, _ := s.admitRead()
	defer first()
	second, _ := s.admitRead()
	defer second()
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, _ := server.Client().Do(request)
		if response != nil {
			response.Body.Close()
		}
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		s.metroStreams.mu.Lock()
		n := s.metroStreams.total
		s.metroStreams.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not retain bounded stream admission while waiting")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("initialization ignored cancellation")
	}
	waitMetroStreamsReleased(t, s)
	if len(s.expensiveReads) != 2 {
		t.Fatal("cancelled initialization consumed a read slot")
	}
}

func waitMetroStreamsReleased(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.metroStreams.mu.Lock()
		n := s.metroStreams.total
		s.metroStreams.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream admission leaked")
}

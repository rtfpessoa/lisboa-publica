package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUpstreamSourceLimitAndRetryAfter(t *testing.T) {
	transport := NewBudgetTransport(900)
	now := time.Now()
	transport.now = func() time.Time { return now }
	count := 0
	code := 200
	retry := ""
	transport.Base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		count++
		return &http.Response{StatusCode: code, Header: http.Header{"Retry-After": []string{retry}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	r, _ := http.NewRequest("GET", "https://api.carrismetropolitana.pt/v2/vehicles", nil)
	for i := 0; i < 40; i++ {
		res, err := transport.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if _, err := transport.RoundTrip(r); err == nil || count != 40 {
		t.Fatal("CM rolling-second cap missing")
	}
	now = now.Add(time.Second)
	code = 429
	retry = "600"
	res, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	now = now.Add(599 * time.Second)
	if _, err = transport.RoundTrip(r); err == nil {
		t.Fatal("provider Retry-After shortened")
	}
	now = now.Add(time.Second)
	code = 200
	res, err = transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	code = 503
	retry = now.Add(90 * time.Second).UTC().Format(http.TimeFormat)
	res, err = transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	now = now.Add(89 * time.Second)
	if _, err = transport.RoundTrip(r); err == nil {
		t.Fatal("HTTP-date cooldown ignored")
	}
	now = now.Add(2 * time.Second)
	retry = "invalid"
	res, err = transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if _, err = transport.RoundTrip(r); err == nil {
		t.Fatal("absent Retry-After did not back off")
	}
}

func TestTMLConservativeMinuteBudget(t *testing.T) {
	transport := NewBudgetTransport(900)
	now := time.Now()
	transport.now = func() time.Time { return now }
	transport.Base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	r, _ := http.NewRequest("GET", "https://go.tmlmobilidade.pt/hub/api/v1/vehicles/positions", nil)
	for i := 0; i < 120; i++ {
		res, err := transport.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if _, err := transport.RoundTrip(r); err == nil {
		t.Fatal("TML local budget missing")
	}
	now = now.Add(time.Minute)
	res, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
}

func TestInFlightSuccessCannotShortenProviderCooldown(t *testing.T) {
	transport := NewBudgetTransport(900)
	slowStarted, release := make(chan struct{}), make(chan struct{})
	transport.Base = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}
		if r.URL.Path == "/slow" {
			close(slowStarted)
			<-release
		} else {
			response.StatusCode = 429
			response.Header.Set("Retry-After", "600")
		}
		return response, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := http.NewRequest("GET", "https://provider.test/slow", nil)
		res, err := transport.RoundTrip(r)
		if err == nil {
			res.Body.Close()
		}
	}()
	<-slowStarted
	r, _ := http.NewRequest("GET", "https://provider.test/limited", nil)
	res, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	close(release)
	<-done
	if _, err = transport.RoundTrip(r); err == nil {
		t.Fatal("in-flight success erased provider cooldown")
	}
}

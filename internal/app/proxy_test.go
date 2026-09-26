package app

import (
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyClientBudgets(t *testing.T) {
	server, err := NewServer(nil, NewCache(), Options{Origin: "https://example.com", PublicReads: true, RateLimit: 1, TrustedProxyCIDRs: "172.19.0.0/16"}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	check := func(peer, claimed string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/operators", nil)
		request.RemoteAddr = peer
		request.Header.Set("X-Lisboa-Client-IP", claimed)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("peer %s: got %d want %d: %s", peer, response.Code, want, response.Body.String())
		}
	}
	check("172.19.0.2:1234", "192.0.2.1", http.StatusOK)
	check("172.19.0.2:1234", "192.0.2.2", http.StatusOK)
	check("172.19.0.2:1234", "192.0.2.1", http.StatusTooManyRequests)
	check("198.51.100.1:1234", "192.0.2.3", http.StatusOK)
	check("198.51.100.1:1234", "192.0.2.4", http.StatusTooManyRequests)
	check("172.19.0.2:1234", "garbage", http.StatusOK)
	check("172.19.0.2:1234", "192.0.2.5,192.0.2.6", http.StatusTooManyRequests)
}

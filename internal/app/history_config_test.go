package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func historyConfigHandler(t *testing.T, store *Store) http.Handler {
	t.Helper()
	server, err := NewServer(store, NewCache(), Options{Origin: "https://example.com", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHistoryConfigMemoryOnly(t *testing.T) {
	for _, store := range []*Store{nil, {}} {
		response := httptest.NewRecorder()
		historyConfigHandler(t, store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("config: %d %s", response.Code, response.Body.String())
		}
		if got := decode[api.Config](t, response.Body.Bytes()).HistoryAvailableFrom; got != nil {
			t.Fatalf("memory-only cutoff: %v", got)
		}
	}
}

func TestHistoryConfigCutoffAndDatabaseFailure(t *testing.T) {
	store := testStore(t)
	cutoff := time.Now().UTC().Add(-8 * 24 * time.Hour).Truncate(time.Microsecond)
	if _, err := store.DB.Exec(context.Background(), "UPDATE history_pressure SET cutoff=$1 WHERE id=1", cutoff); err != nil {
		t.Fatal(err)
	}
	handler := historyConfigHandler(t, store)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("config: %d %s", response.Code, response.Body.String())
	}
	got := decode[api.Config](t, response.Body.Bytes()).HistoryAvailableFrom
	if got == nil || !got.Equal(cutoff) {
		t.Fatalf("cutoff = %v", got)
	}
	store.DB.Close()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if response.Code == http.StatusOK {
		t.Fatalf("database failure presented as known config: %s", response.Body.String())
	}
}

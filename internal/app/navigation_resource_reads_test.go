package app

import (
	"context"
	"encoding/json"
	"fmt"
	"lisboapublica/internal/api"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

func concurrentNavigationReads(t *testing.T, cache *Cache) {
	t.Helper()
	_, handler := securityServer(t, &Store{}, cache)
	var group sync.WaitGroup
	failures := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() { defer group.Done(); failures <- resourceNavigationRead(handler) }()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, _ := cache.state("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := listedVehicles(ctx, state, Filter{}, time.Now()); err != context.Canceled {
		t.Fatal("saturated read ignored cancellation")
	}
	var identified api.Vehicle
	for _, v := range navigationVehicles(state, "cm", time.Now()) {
		if v.PatternId != nil {
			identified = v
			break
		}
	}
	if _, err := buildVehicleCalls(ctx, state, identified, time.Now(), true); err != context.Canceled {
		t.Fatal("CM calls ignored cancellation")
	}
	t.Log("concurrent_vehicle_pages=2 calls_from_frozen_refs=2 complete_CM_paths=2 pinned_CM_pages=2 geometry_opt_in=verified cancelled_calls=verified")
}

func resourceNavigationRead(handler http.Handler) error {
	response := securityRequest(handler, "GET", "/api/v1/vehicles?limit=500", "", nil)
	if response.Code != 200 {
		return fmt.Errorf("resource first page status%d", response.Code)
	}
	var page api.VehiclePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		return err
	}
	if len(page.Data) != 500 || !page.Page.HasMore || page.Data[0].VehicleRef == nil {
		return fmt.Errorf("resource read did not saturate page")
	}
	reference := page.Data[0].VehicleRef
	next := securityRequest(handler, "GET", "/api/v1/vehicles?limit=500&offset=500&revision="+url.QueryEscape(*page.Page.Revision), "", nil)
	if next.Code != 200 {
		return fmt.Errorf("resource next page status%d", next.Code)
	}
	calls := securityRequest(handler, "GET", "/api/v1/vehicles/"+url.PathEscape(reference.VehicleId)+"/calls?reference="+url.QueryEscape(reference.Reference), "", nil)
	if calls.Code != 200 {
		return fmt.Errorf("resource calls status%d", calls.Code)
	}
	return resourceCMCalls(handler)
}
func resourceCMReference(handler http.Handler) (*api.VehicleReference, error) {
	response := securityRequest(handler, "GET", "/api/v1/vehicles?operators=cm&limit=500", "", nil)
	var page api.VehiclePage
	if response.Code != 200 {
		return nil, fmt.Errorf("CM vehicles status%d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		return nil, err
	}
	var ref *api.VehicleReference
	for _, v := range page.Data {
		if v.PatternId != nil {
			ref = v.VehicleRef
			break
		}
	}
	if ref == nil {
		return nil, fmt.Errorf("CM identified source missing from fixture page")
	}
	return ref, nil
}
func resourceCMCalls(handler http.Handler) error {
	ref, err := resourceCMReference(handler)
	if err != nil {
		return err
	}
	path := "/api/v1/vehicles/" + url.PathEscape(ref.VehicleId) + "/calls?reference=" + url.QueryEscape(ref.Reference)
	first := securityRequest(handler, "GET", path+"&include_geometry=true&limit=20", "", nil)
	var calls api.VehicleCallsPage
	if err := json.Unmarshal(first.Body.Bytes(), &calls); err != nil {
		return err
	}
	if first.Code != 200 || calls.Coverage != "complete_published_route" || calls.Geometry == nil || calls.Page.Total <= 20 || len(calls.Data) != 20 || !calls.Page.HasMore {
		return fmt.Errorf("CM full-path workload not exercised: status%d coverage%s total%d geometry%v", first.Code, calls.Coverage, calls.Page.Total, calls.Geometry != nil)
	}
	return resourceCMPinnedCalls(handler, path, calls)
}
func resourceCMPinnedCalls(handler http.Handler, path string, calls api.VehicleCallsPage) error {
	next := securityRequest(handler, "GET", path+"&include_geometry=true&limit=20&offset=20&revision="+url.QueryEscape(*calls.Page.Revision), "", nil)
	var later api.VehicleCallsPage
	if err := json.Unmarshal(next.Body.Bytes(), &later); err != nil {
		return err
	}
	if next.Code != 200 || later.Geometry != nil || later.Page.Total != calls.Page.Total || len(later.Data) == 0 || later.Data[0].Id == calls.Data[0].Id || *later.Page.Revision != *calls.Page.Revision {
		return fmt.Errorf("CM pinned path pagination failed")
	}
	return nil
}

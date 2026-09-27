package app

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Measurement-only candidate. It is deliberately not published to production:
// the optional ordered CM index must pass the same complete resource envelope.
type cmPathCandidate struct {
	Trips map[string]*cmCandidatePath
	Paths []*cmCandidatePath
}
type cmCandidatePath struct {
	Route, Headsign, Shape string
	Visits                 []cmCandidateVisit
}
type cmCandidateVisit struct {
	Stop     string
	Sequence int
}

func measureCMPaths(t *testing.T, feeds []officialGeometryFeed) *cmPathCandidate {
	t.Helper()
	out := &cmPathCandidate{Trips: map[string]*cmCandidatePath{}}
	for _, feed := range feeds {
		if feed.providerID() != "cm" {
			continue
		}
		blob, err := os.ReadFile(feed.File)
		if err != nil {
			t.Fatal(err)
		}
		archive, err := openGTFS(blob)
		if err != nil {
			t.Fatal(err)
		}
		trips := map[string]*cmCandidatePath{}
		err = archive.read("trips.txt", func(r map[string]string) error {
			trips[r["trip_id"]] = &cmCandidatePath{Route: r["route_id"], Headsign: r["trip_headsign"], Shape: r["shape_id"]}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		stops := map[string]string{}
		err = archive.read("stop_times.txt", func(r map[string]string) error {
			trip := trips[r["trip_id"]]
			if trip == nil {
				return fmt.Errorf("unknown CM candidate trip")
			}
			seq, err := strconv.Atoi(r["stop_sequence"])
			if err != nil || seq < 0 {
				return fmt.Errorf("invalid CM sequence")
			}
			id := r["stop_id"]
			if stops[id] == "" {
				stops[id] = id
			}
			trip.Visits = append(trip.Visits, cmCandidateVisit{stops[id], seq})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		paths := map[string]*cmCandidatePath{}
		refs := 0
		for id, trip := range trips {
			sort.Slice(trip.Visits, func(i, j int) bool { return trip.Visits[i].Sequence < trip.Visits[j].Sequence })
			var key strings.Builder
			fmt.Fprintf(&key, "%q/%q/%q", trip.Route, trip.Headsign, trip.Shape)
			for _, v := range trip.Visits {
				fmt.Fprintf(&key, "/%d:%q", v.Sequence, v.Stop)
			}
			canonical := paths[key.String()]
			if canonical == nil {
				visits := make([]cmCandidateVisit, len(trip.Visits))
				copy(visits, trip.Visits)
				trip.Visits = visits
				canonical = trip
				paths[key.String()] = trip
				out.Paths = append(out.Paths, trip)
				refs += len(visits)
			}
			out.Trips["["+feed.Plan+"]["+feed.Agency+"]"+id] = canonical
		}
		t.Logf("cm_candidate_agency=%s exact_trips=%d variants=%d stop_refs=%d", feed.Agency, len(trips), len(paths), refs)
	}
	return out
}

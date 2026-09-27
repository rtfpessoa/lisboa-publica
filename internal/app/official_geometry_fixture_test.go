package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type officialGeometryFeed struct {
	Operator, Agency, Plan, File string
	From, Until                  int
}

func officialGeometryFeeds(t *testing.T) []officialGeometryFeed {
	t.Helper()
	manifest := os.Getenv("GTFS_GEOMETRY_FIXTURES")
	if manifest == "" {
		t.Skip("set GTFS_GEOMETRY_FIXTURES to a downloaded official manifest")
	}
	blob, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var feeds []officialGeometryFeed
	if err := json.Unmarshal(blob, &feeds); err != nil {
		t.Fatal(err)
	}
	return feeds
}

func (feed officialGeometryFeed) providerID() string {
	if strings.HasPrefix(feed.Operator, "cm-") {
		return "cm"
	}
	return feed.Operator
}

func loadOfficialGeometry(t *testing.T, feed officialGeometryFeed) *StaticData {
	t.Helper()
	blob, err := os.ReadFile(feed.File)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := providerByID(feed.providerID())
	if !ok {
		t.Fatalf("unknown fixture operator %s", feed.Operator)
	}
	var data *StaticData
	if p.ID == "cm" {
		data, err = readCMNetwork(blob, &hubPlan{ID: feed.Plan, Agency: feed.Agency}, p, hubBase, time.Now().UTC())
	} else {
		data, err = readGTFS(blob, p, feed.Plan, fmt.Sprint(feed.From), fmt.Sprint(feed.Until), hubBase, time.Now().UTC())
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mergeCMFixture(network, data *StaticData) {
	network.Shapes = append(network.Shapes, data.Shapes...)
	network.CMPaths = append(network.CMPaths, data.CMPaths...)
	for id, metadata := range data.Models {
		network.Models[id] = metadata
	}
}

package app

import "strings"

func predictionEntityOperator(id string) string {
	for _, p := range providers {
		if p.ID != "cp" && p.Agency != "" && strings.Contains(id, "["+p.Agency+"]") {
			return p.ID
		}
	}
	for _, agency := range []string{"LA77N", "BNA17", "YA15B", "A2L1N"} {
		if strings.Contains(id, "["+agency+"]") {
			return "cm"
		}
	}
	return ""
}
func retainProviderEntity(feed *cpFeed, entity cpEntity) error {
	op := predictionEntityOperator(entity.Update.Trip.ID)
	if op == "" {
		return nil
	}
	if feed.OtherUpdates == nil {
		feed.OtherUpdates = map[string][]cpUpdate{}
	}
	if !providerUpdateFits(feed.OtherUpdates[op], entity.Update) {
		if feed.PartialOperators == nil {
			feed.PartialOperators = map[string]bool{}
		}
		feed.PartialOperators[op] = true
	} else {
		feed.OtherUpdates[op] = append(feed.OtherUpdates[op], entity.Update)
	}
	return nil
}
func providerUpdateFits(kept []cpUpdate, u cpUpdate) bool {
	count := len(u.Stops)
	for _, row := range kept {
		count += len(row.Stops)
	}
	return len(kept) < cpMaxEntities && count <= cpMaxInputRows && boundedCPUpdate(u)
}

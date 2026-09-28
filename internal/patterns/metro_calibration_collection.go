package patterns

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type metroCalibrationVisit struct{ journey, visit string }
type metroCalibrationCollection struct {
	refs           map[metroCalibrationVisit]MetroCalibrationReference
	groups         map[metroCalibrationVisit][]MetroCalibrationSample
	splits         map[string]string
	train, holdout map[string]bool
	keys           []metroCalibrationVisit
}

func prepareMetroCalibration(in MetroCalibrationDataset) (*metroCalibrationCollection, error) {
	if err := validateMetroDataset(in); err != nil {
		return nil, err
	}
	c := &metroCalibrationCollection{refs: map[metroCalibrationVisit]MetroCalibrationReference{}, groups: map[metroCalibrationVisit][]MetroCalibrationSample{}, splits: map[string]string{}, train: map[string]bool{}, holdout: map[string]bool{}}
	err := c.addReferences(in)
	if err == nil {
		err = c.addSamples(in.Samples)
	}
	if err == nil {
		err = c.validateCoverage()
	}
	if err != nil {
		return nil, err
	}
	for k := range c.groups {
		c.keys = append(c.keys, k)
	}
	sort.Slice(c.keys, func(i, j int) bool {
		a, b := c.keys[i], c.keys[j]
		return a.journey+"\x00"+a.visit < b.journey+"\x00"+b.visit
	})
	return c, nil
}
func validateMetroDataset(in MetroCalibrationDataset) error {
	if in.Kind != "observed" && in.Kind != "synthetic" && in.Kind != "model_consistency" {
		return fmt.Errorf("kind must be observed, synthetic or model_consistency")
	}
	if in.Kind == "model_consistency" && strings.TrimSpace(in.ModelProvenance) == "" {
		return fmt.Errorf("model consistency requires frozen qualification provenance")
	}
	return validateMetroDatasetEvidence(in)
}
func validateMetroDatasetEvidence(in MetroCalibrationDataset) error {
	if !metroDatasetProvenance(in) {
		return fmt.Errorf("versioned geometry, transform, source and resolution provenance are required")
	}
	if !metroDatasetSize(in) {
		return fmt.Errorf("require 1..50000 samples and 1..5000 reference visits")
	}
	return nil
}

func metroDatasetProvenance(in MetroCalibrationDataset) bool {
	versioned := in.Version != "" && in.Geometry != "" && in.Transform != ""
	provenance := in.SourceProvenance != "" && in.ResolutionProvenance != ""
	return versioned && provenance && positiveMetroMetres(in.ResolutionMetres)
}
func metroDatasetSize(in MetroCalibrationDataset) bool {
	return len(in.Samples) > 0 && len(in.Samples) <= 50000 && len(in.References) > 0 && len(in.References) <= 5000
}
func finiteMetroMetres(v float64) bool   { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func positiveMetroMetres(v float64) bool { return finiteMetroMetres(v) && v > 0 }
func validMetroReference(r MetroCalibrationReference) bool {
	identity := r.Journey != "" && r.Visit != "" && r.Provenance != ""
	stop := !r.StoppedFrom.IsZero() && !r.StoppedThrough.Before(r.StoppedFrom)
	move := r.FirstMovementFrom.After(r.StoppedThrough) && !r.FirstMovementThrough.Before(r.FirstMovementFrom)
	return identity && stop && move
}
func metroReferenceKind(kind string, r MetroCalibrationReference) bool {
	return kind == "observed" && r.Kind == "independent" || kind == "synthetic" && r.Kind == "synthetic" || kind == "model_consistency" && r.Kind == "model_support"
}
func (c *metroCalibrationCollection) addReferences(in MetroCalibrationDataset) error {
	for _, r := range in.References {
		if err := c.addReference(in.Kind, r); err != nil {
			return err
		}
	}
	return nil
}
func (c *metroCalibrationCollection) addReference(kind string, r MetroCalibrationReference) error {
	if !validMetroReference(r) {
		return fmt.Errorf("invalid referenced stop/movement window")
	}
	if !metroReferenceKind(kind, r) {
		return fmt.Errorf("reference kind must match dataset: observed/independent, synthetic/synthetic or model_consistency/model_support")
	}
	k := metroCalibrationVisit{r.Journey, r.Visit}
	_, exists := c.refs[k]
	var err error
	if exists {
		err = fmt.Errorf("duplicate reference visit")
	} else {
		c.refs[k] = r
	}
	return err

}
func validMetroSample(s MetroCalibrationSample) bool {
	identity := validMetroSampleIdentity(s)
	split := s.Split == "train" || s.Split == "holdout"
	clock := !s.SourceAt.IsZero() && !s.ReceivedAt.Before(s.SourceAt)
	return identity && split && clock && finiteMetroMetres(s.ProgressMetres)
}
func validMetroSampleIdentity(s MetroCalibrationSample) bool {
	named := s.Journey != "" && s.Visit != "" && s.EvidenceRef != ""
	return named && !strings.ContainsRune(s.Journey, 0) && !strings.ContainsRune(s.Visit, 0)
}
func (c *metroCalibrationCollection) addSamples(samples []MetroCalibrationSample) error {
	for _, s := range samples {
		if err := c.addSample(s); err != nil {
			return err
		}
	}
	return nil
}
func (c *metroCalibrationCollection) addSample(s MetroCalibrationSample) error {
	if !validMetroSample(s) {
		return fmt.Errorf("invalid sample identity, split, clocks or progress")
	}
	if err := c.bindSample(s); err != nil {
		return err
	}
	k := metroCalibrationVisit{s.Journey, s.Visit}
	g := c.groups[k]
	var err error
	if len(g) > 0 && !s.SourceAt.After(g[len(g)-1].SourceAt) {
		err = fmt.Errorf("samples must have strictly increasing original clocks within each visit")
	} else {
		c.appendSample(k, s)
	}
	return err
}
func (c *metroCalibrationCollection) appendSample(k metroCalibrationVisit, s MetroCalibrationSample) {
	c.groups[k] = append(c.groups[k], s)
	if s.Split == "train" {
		c.train[s.Journey] = true
	} else {
		c.holdout[s.Journey] = true
	}

}
func (c *metroCalibrationCollection) bindSample(s MetroCalibrationSample) error {
	if prev := c.splits[s.Journey]; prev != "" && prev != s.Split {
		return fmt.Errorf("journey leaks across train/holdout splits")
	}
	c.splits[s.Journey] = s.Split
	if _, ok := c.refs[metroCalibrationVisit{s.Journey, s.Visit}]; !ok {
		return fmt.Errorf("sample lacks a reference visit")
	}
	return nil
}
func (c *metroCalibrationCollection) validateCoverage() error {
	for k := range c.refs {
		if len(c.groups[k]) == 0 {
			return fmt.Errorf("reference visit has no samples")
		}
	}
	if len(c.train) == 0 || len(c.holdout) == 0 {
		return fmt.Errorf("separate training and holdout journeys are required")
	}
	return nil
}
func admissibleMetroSample(s MetroCalibrationSample) bool {
	return s.Valid && !s.Corrected && !s.Fallback
}
func metroStoppedPair(a, b MetroCalibrationSample, r MetroCalibrationReference) bool {
	stopped := a.Stopped && b.Stopped && admissibleMetroSample(a) && admissibleMetroSample(b)
	window := !a.SourceAt.Before(r.StoppedFrom) && !b.SourceAt.After(r.StoppedThrough)
	return stopped && window && b.SourceAt.Sub(a.SourceAt) <= 60*time.Second
}

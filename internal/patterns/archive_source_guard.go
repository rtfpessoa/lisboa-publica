package patterns

import "fmt"

type publicationSource struct {
	key   string
	block *block
}

func protectedPublication(b block) bool {
	return b.Kind == "detail" && b.Operator == "metro" || b.Key == "metro:state"
}

func (s *Service) guardPublication(b block) (*publicationSource, error) {
	if !protectedPublication(b) {
		return nil, nil
	}
	if s.detailFence != nil {
		return nil, s.detailFence
	}
	source, err := s.publicationSource(b.Key)
	if err != nil {
		return nil, err
	}
	if err = s.verifyPublicationSource(source); err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *Service) verifyGuardedSource(source *publicationSource) error {
	if source == nil {
		return nil
	}
	return s.verifyPublicationSource(*source)
}

func (s *Service) publicationSource(key string) (publicationSource, error) {
	source := publicationSource{key: key}
	ambiguous := false
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Key != key {
			continue
		}
		if source.block != nil {
			ambiguous = true
			continue
		}
		copy := *b
		source.block = &copy
	}
	if ambiguous {
		for i := range s.index.Blocks {
			if s.index.Blocks[i].Key == key {
				s.fenceDetail(fmt.Errorf("ambiguous publication source"), &s.index.Blocks[i])
			}
		}
		return source, s.detailFence
	}
	return source, nil
}

func samePublicationSource(a, b block) bool {
	// Closed is recomputed by admission and does not identify the generation.
	a.Closed, b.Closed = false, false
	return a == b
}

func (s *Service) verifyPublicationSource(source publicationSource) error {
	current, err := s.publicationSource(source.key)
	if err != nil {
		return err
	}
	if (current.block == nil) != (source.block == nil) || current.block != nil && !samePublicationSource(*current.block, *source.block) {
		return s.fenceDetail(fmt.Errorf("publication source changed during admission"), source.block)
	}
	if source.block != nil {
		if _, err = s.readBlock(*source.block); err != nil {
			return s.fenceDetail(err, source.block)
		}
	}
	return nil
}

func (s *Service) verifyOldGeneration(source publicationSource) error {
	if source.block == nil {
		return nil
	}
	if _, err := s.readBlock(*source.block); err != nil {
		return s.fenceDetail(err, source.block)
	}
	return nil
}

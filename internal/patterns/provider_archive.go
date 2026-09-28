package patterns

import (
	"fmt"
	"time"
)

func (s *Service) operatorEngine(operator string) *engine {
	if operator == "metro" {
		return s.engine
	}
	if p := s.providers[operator]; p != nil {
		return p.Engine
	}
	p := newProvider(operator)
	s.providers[operator] = p
	return p.Engine
}

func durationNumber(d time.Duration) string { return fmt.Sprint(int(d / time.Second)) }

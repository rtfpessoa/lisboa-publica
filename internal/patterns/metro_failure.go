package patterns

import (
	"errors"
	"fmt"
)

type metroFailure struct{ error }

func (e metroFailure) Unwrap() error { return e.error }

// markMetroFailure marks one archive error once even when it crosses callers.
// The archive owner mutex must be held.
func (s *Service) markMetroFailure(err error) error {
	if err == nil {
		err = fmt.Errorf("Metro history unavailable")
	}
	var marked metroFailure
	if errors.As(err, &marked) {
		return err
	}
	s.metroCut.MarkPending()
	s.applyMetroCut()
	return metroFailure{err}
}

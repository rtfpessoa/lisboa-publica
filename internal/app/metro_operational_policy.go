package app

import "time"

// This experimental profile is qualified for model-consistency replay, not
// physical accuracy. Changing a bound changes the persisted model version.
const (
	metroOperationalPolicy       = "metro-operational-geometry-v1"
	metroGeometryEnvelopeMetres  = 150.0
	metroGeometryTieMetres       = 3.0
	metroGeometryAmbiguityMetres = 100.0
	metroDirectionStepMetres     = 3.0
	metroDepartureStepMetres     = 6.0
	metroStopEnvelopeMetres      = 25.0
	metroMaximumModelSpeed       = 50.0
	metroModelChainGap           = 60 * time.Second
	metroProjectionLifetime      = 30 * time.Second
)

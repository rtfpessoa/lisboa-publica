package app

import "time"

// Limits keep provider requests, retained data and browser input bounded.
const (
	historyRetentionDays      = 30
	maxRequestBytes           = 32 << 10
	upstreamRequestsPerMinute = 900
	providerRefreshInterval   = 30 * time.Second
	sourceFreshness           = 90 * time.Second
	staticRefreshInterval     = 5 * time.Minute
	staticCacheLifetime       = 6 * time.Hour
	maxGTFSCompressedBytes    = 64 << 20
	maxGTFSExpandedBytes      = 512 << 20
	maxGTFSRows               = 5_000_000
	numericBitSize            = 64
	cachePartBytes            = 512 << 10
	metroOAuthResponseBytes   = 64 << 10
	maxPredictionWaitSeconds  = 7200
	metroRefreshTimeout       = 20 * time.Second
	metroStationTolerance     = 0.005

	maxCachedVersions          = 24
	maxConditionalBodies       = 20
	maxRateEntries             = 4096
	randomSecretBytes          = 32
	minimumLoginNonceLength    = 16
	defaultReadRate            = 300
	maxPersonalKeys            = 20
	maximumScheduleWindow      = 48 * time.Hour
	maxSampledSpeedKmh         = 130
	earthRadiusKm              = 6371.0
	secondsPerHour             = 3600
	secondsPerMinute           = 60
	maxGTFSServiceHours        = 72
	serviceDayNoonHour         = 12
	hexColorDigits             = 6
	defaultPageSize            = 100
	databaseMaxConnections     = 8
	serializationAttempts      = 5
	serializationBackoffMillis = 10
	lisbonNorthLatitude        = 39.3
	lisbonSouthLatitude        = 38.3
	lisbonWestLongitude        = 9.7
	lisbonEastLongitude        = 8.4
	sessionLifetime            = 24 * time.Hour
	loginNonceLifetime         = 5 * time.Minute
	upstreamTimeout            = 45 * time.Second
	googleVerificationTimeout  = 10 * time.Second
	maximumTokenMargin         = 10 * time.Second
	tokenMarginFraction        = 4
	providerClockSkew          = 30 * time.Second
	providerJSONBytes          = 16 << 20
)

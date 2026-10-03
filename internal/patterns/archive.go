package patterns

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
)

const maxBlockBytes = 64 << 20

// The checkpoint keeps a bounded aggregate window so the marshaled payload fits a
// single block even after restoring several large days.
const checkpointAggregateBudget = 40 << 20
const maxDetailHourBytes = 16 << 20
const manifestReserve = 1 << 20

type block struct {
	AsOf     time.Time `json:"as_of,omitempty"`
	Samples  int64     `json:"samples,omitempty"`
	Key      string    `json:"key"`
	File     string    `json:"file"`
	Hash     string    `json:"hash"`
	Kind     string    `json:"kind"`
	Operator string    `json:"operator"`
	Date     string    `json:"date"`
	Age      time.Time `json:"age"`
	Closed   bool      `json:"closed"`
}
type manifest struct {
	Version int     `json:"version"`
	Blocks  []block `json:"blocks"`
}
type checkpoint struct {
	Providers     map[string]*providerState
	Engine        *engine
	Hour          time.Time
	Detail        []Receipt
	BinSeconds    int
	SampleSeconds int
	Topology      Topology
}

// Service owns one archive and inference engine. The lock bounds file reader lifetime:
// FIFO cannot unlink until reads are closed. No file descriptors escape a query.
type Service struct {
	mu     sync.Mutex
	config Config
	index  manifest
	archiveResources
	metroArchiveState
	providers map[string]*providerState
	operators map[string]bool
}

// archiveResources owns the codecs and exclusive file lock for one open archive.
type archiveResources struct {
	lock    *os.File
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

// metroArchiveState is the inference and publication cursor for Metro receipts.
type metroArchiveState struct {
	pendingMetroDeliveryGap    bool
	engine                     *engine
	topology                   Topology
	hour                       time.Time
	detail                     []Receipt
	detailBytes                int
	lastSample, lastCheckpoint time.Time
	status, message            string
}

// Close flushes retained evidence and releases exclusive archive ownership.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.flush(time.Now().UTC())
	if s.encoder != nil {
		s.encoder.Close()
	}
	if s.decoder != nil {
		s.decoder.Close()
	}
	_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	closeErr := s.lock.Close()
	s.lock = nil
	return errors.Join(err, closeErr)
}

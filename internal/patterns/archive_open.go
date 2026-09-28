package patterns

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Open acquires exclusive archive ownership and restores only verified evidence.
func Open(config Config) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.Directory, 0700); err != nil {
		return nil, err
	}
	lock, err := acquireArchiveLock(config.Directory)
	if err != nil {
		return nil, err
	}
	s := &Service{providers: map[string]*providerState{}, operators: map[string]bool{"metro": true}, config: config, index: manifest{Version: 1, Blocks: []block{}}, metroArchiveState: metroArchiveState{engine: newEngine(), status: "collecting"}, archiveResources: archiveResources{lock: lock}}
	return openOwnedArchive(s)
}

func acquireArchiveLock(directory string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("transport archive already owned: %w", err)
	}
	return lock, nil
}

func openOwnedArchive(s *Service) (_ *Service, err error) {
	defer func() {
		if err != nil {
			s.releaseArchiveResources()
		}
	}()
	if err = s.initializeCodecs(); err != nil {
		return nil, err
	}
	if err = s.restoreArchive(); err != nil {
		return nil, err
	}
	if err = s.ensureSpace(manifestReserve, time.Now().UTC()); err != nil {
		s.status, s.message = "paused", "Arquivo pausado pelo limite de espaço."
		err = nil
	}
	return s, nil
}

// Failed initialization releases ownership without publishing archive contents.
func (s *Service) releaseArchiveResources() {
	if s.encoder != nil {
		s.encoder.Close()
	}
	if s.decoder != nil {
		s.decoder.Close()
	}
	_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	_ = s.lock.Close()
}

func (s *Service) initializeCodecs() error {
	var err error
	s.encoder, err = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(9)))
	if err == nil {
		s.decoder, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxBlockBytes))
	}
	return err
}

func (s *Service) readManifest() error {
	data, err := os.ReadFile(filepath.Join(s.config.Directory, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > manifestReserve || json.Unmarshal(data, &s.index) != nil || s.index.Version != 1 {
		return fmt.Errorf("invalid transport archive manifest")
	}
	return validateArchiveBlocks(s.index.Blocks)
}

func validateArchiveBlocks(blocks []block) error {
	for _, b := range blocks {
		if !safeFilename(b.File) || !validArchiveOperator(b) || !validArchiveKind(b.Kind) {
			return fmt.Errorf("invalid archive block")
		}
	}
	return nil
}

func validArchiveOperator(b block) bool {
	if !knownOperator(b.Operator) {
		return false
	}
	if b.Operator == "metro" {
		return true
	}
	return b.Kind == "observations" || b.Kind == "aggregate" || b.Kind == "correction"
}

func validArchiveKind(kind string) bool {
	switch kind {
	case "detail", "aggregate", "state", "correction", "observations", "popup-events":
		return true
	default:
		return false
	}
}

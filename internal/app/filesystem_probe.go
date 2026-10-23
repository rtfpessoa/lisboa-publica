package app

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"syscall"
)

var errFilesystemHeadroom = errors.New("database filesystem headroom exhausted")

// StorageFilesystemConfig describes an optional local PGDATA filesystem probe.
// The path must be a dedicated, inaccessible read-only mount of PGDATA; the
// deployer must verify separately mounted WAL/tablespaces and mount identity.
type StorageFilesystemConfig struct {
	Path          string
	MinimumBytes  uint64
	MinimumInodes uint64
}

type filesystemCapacity struct{ bytes, inodes uint64 }

type filesystemProbe struct {
	minimumBytes, minimumInodes uint64
	measure                     func() (filesystemCapacity, error)
}

func newFilesystemProbe(config StorageFilesystemConfig) (*filesystemProbe, error) {
	if !filepath.IsAbs(config.Path) || config.MinimumBytes < 1<<20 || config.MinimumInodes == 0 {
		return nil, fmt.Errorf("invalid database filesystem probe configuration")
	}
	p := &filesystemProbe{minimumBytes: config.MinimumBytes, minimumInodes: config.MinimumInodes}
	p.measure = func() (filesystemCapacity, error) { return measureDatabaseFilesystem(config.Path) }
	return p, nil
}

func measureDatabaseFilesystem(path string) (filesystemCapacity, error) {
	if err := validatePrivateDatabaseDirectory(path); err != nil {
		return filesystemCapacity{}, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil || fs.Bsize <= 0 {
		return filesystemCapacity{}, fmt.Errorf("database filesystem measurement unavailable")
	}
	size := uint64(fs.Bsize)
	blocks := uint64(fs.Bavail)
	bytes := uint64(math.MaxUint64)
	if blocks <= math.MaxUint64/size {
		bytes = blocks * size
	}
	return filesystemCapacity{bytes: bytes, inodes: uint64(fs.Ffree)}, nil
}

func validatePrivateDatabaseDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || os.Geteuid() == 0 {
		return fmt.Errorf("database filesystem probe must be an inaccessible directory for a non-root process")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid == uint32(os.Geteuid()) {
		return fmt.Errorf("database filesystem probe must not be owned by the application user")
	}
	// A permissive ACL or an unexpectedly privileged process must not expose
	// PGDATA even if the ordinary mode bits appear private.
	file, err := os.Open(path)
	if err == nil {
		_ = file.Close()
		return fmt.Errorf("database filesystem probe exposes database contents")
	}
	if !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("database filesystem probe unavailable: %w", err)
	}
	return nil
}

func (p *filesystemProbe) check(required int64) error {
	if p == nil {
		return nil
	}
	capacity, err := p.measure()
	if err != nil {
		return fmt.Errorf("database filesystem measurement unavailable: %w", err)
	}
	if required < 0 || capacity.inodes < p.minimumInodes || capacity.bytes < p.minimumBytes || uint64(required) > capacity.bytes-p.minimumBytes {
		return errFilesystemHeadroom
	}
	return nil
}

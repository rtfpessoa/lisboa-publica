package patterns

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func safeFilename(value string) bool {
	return value != "" && filepath.Base(value) == value && !strings.HasPrefix(value, ".")
}

var errManifestLimit = errors.New("archive manifest limit")

func (s *Service) saveManifest(next manifest) error {
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	blob, err := encodeManifest(next)
	if err != nil {
		return err
	}
	sync := s.manifestSync
	if sync == nil {
		sync = syncDirectory
	}
	renamed, err := atomicFileWithSync(s.config.Directory, "manifest.json", blob, sync)
	if renamed {
		s.index = next
		s.trimRetentionClocks()
		if err != nil {
			s.manifestUncertain = true
		}
	}
	return err
}

func encodeManifest(next manifest) ([]byte, error) {
	blob, err := json.Marshal(next)
	if err == nil && len(blob) > maxManifestBytes {
		return nil, errManifestLimit
	}
	return blob, err
}

func (s *Service) readBlock(b block) ([]byte, error) {
	path := filepath.Join(s.config.Directory, b.File)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBlockBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBlockBytes || digestBytes(data) != b.Hash {
		return nil, fmt.Errorf("archive block corrupt")
	}
	return data, nil
}

func digestBytes(blob []byte) string { return fmt.Sprintf("%x", sha256.Sum256(blob)) }

func atomicFile(directory, name string, data []byte) error {
	_, err := atomicFileWithSync(directory, name, data, syncDirectory)
	return err
}

// The caller must retain both generations when rename succeeds but directory
// synchronization fails: either manifest may survive a crash.
func atomicFileWithSync(directory, name string, data []byte, sync func(string) error) (bool, error) {
	temporary := "metro-" + name + ".tmp"
	if name == "manifest.json" {
		temporary = ".manifest.tmp"
	}
	path := filepath.Join(directory, temporary)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(path)
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, err
	}
	if err = os.Rename(path, filepath.Join(directory, name)); err != nil {
		return false, err
	}
	renamed = true
	return true, sync(directory)
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

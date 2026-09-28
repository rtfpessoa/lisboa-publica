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

func (s *Service) saveManifest(next manifest) error {
	blob, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(blob) > manifestReserve {
		return fmt.Errorf("archive manifest limit")
	}
	if err = atomicFile(s.config.Directory, "manifest.json", blob); err != nil {
		return err
	}
	s.index = next
	return nil
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
	temporary := "metro-" + name + ".tmp"
	if name == "manifest.json" {
		temporary = ".manifest.tmp"
	}
	path := filepath.Join(directory, temporary)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(path, filepath.Join(directory, name)); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

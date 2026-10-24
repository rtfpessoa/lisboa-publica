package patterns

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMetroTailRecoveryLockAndSourceSafety(t *testing.T) {
	t.Run("exclusive owner", func(t *testing.T) {
		f := makeTailFixture(t, nil)
		owner, err := openMetroRecoveryOwner(f.directory, f.request)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.close()
		before := inventory(t, f.directory)
		if _, err = RecoverMetroTail(f.directory, f.request); err == nil {
			t.Fatal("second owner succeeded")
		}
		if !reflect.DeepEqual(before, inventory(t, f.directory)) {
			t.Fatal("locked preflight changed archive")
		}
	})
	t.Run("corrupt source", func(t *testing.T) {
		f := makeTailFixture(t, nil)
		if err := os.WriteFile(filepath.Join(f.directory, f.detail.File), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		before := inventory(t, f.directory)
		if _, err := RecoverMetroTail(f.directory, f.request); err == nil {
			t.Fatal("corrupt detail accepted")
		}
		if !reflect.DeepEqual(before, inventory(t, f.directory)) {
			t.Fatal("corrupt preflight changed archive")
		}
	})
	t.Run("symlink source", func(t *testing.T) {
		f := makeTailFixture(t, nil)
		path := filepath.Join(f.directory, f.detail.File)
		if err := os.Rename(path, path+".saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path+".saved", path); err != nil {
			t.Fatal(err)
		}
		before := inventory(t, f.directory)
		if _, err := RecoverMetroTail(f.directory, f.request); err == nil {
			t.Fatal("symlink source accepted")
		}
		if !reflect.DeepEqual(before, inventory(t, f.directory)) {
			t.Fatal("symlink preflight changed archive")
		}
	})
	t.Run("rename failure", func(t *testing.T) {
		f := makeTailFixture(t, nil)
		owner, err := openMetroRecoveryOwner(f.directory, f.request)
		if err != nil {
			t.Fatal(err)
		}
		owner.rename = func(string, string) error { return errors.New("injected rename failure") }
		_, err = owner.recover()
		owner.close()
		if err == nil {
			t.Fatal("expected rename failure")
		}
		m, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
		if err != nil || digestBytes(m) != f.request.ManifestSHA256 {
			t.Fatal("failed rename changed manifest")
		}
		result, err := RecoverMetroTail(f.directory, f.request)
		if err != nil || result.Status != "repaired" {
			t.Fatalf("retry: %+v %v", result, err)
		}
	})
	t.Run("source changes before manifest rename", func(t *testing.T) {
		f := makeTailFixture(t, nil)
		owner, err := openMetroRecoveryOwner(f.directory, f.request)
		if err != nil {
			t.Fatal(err)
		}
		owner.syncDir = func(path string) error {
			if err := os.WriteFile(filepath.Join(path, f.day.File), []byte("changed"), 0600); err != nil {
				return err
			}
			return syncDirectory(path)
		}
		_, err = owner.recover()
		owner.close()
		if err == nil || !strings.Contains(err.Error(), "changed") {
			t.Fatalf("expected source CAS failure: %v", err)
		}
		m, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
		if err != nil || digestBytes(m) != f.request.ManifestSHA256 {
			t.Fatal("CAS failure changed manifest")
		}
	})
}

func TestMetroTailRecoveryCandidateCAS(t *testing.T) {
	f := makeTailFixture(t, nil)
	owner, err := openMetroRecoveryOwner(f.directory, f.request)
	if err != nil {
		t.Fatal(err)
	}
	owner.syncDir = func(path string) error {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "metro-state-") && entry.Name() != f.state.File {
				if err = os.WriteFile(filepath.Join(path, entry.Name()), []byte("changed"), 0600); err != nil {
					return err
				}
			}
		}
		return syncDirectory(path)
	}
	_, err = owner.recover()
	owner.close()
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected candidate CAS failure: %v", err)
	}
	m, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
	if err != nil || digestBytes(m) != f.request.ManifestSHA256 {
		t.Fatal("candidate CAS failure changed manifest")
	}
}

func TestMetroTailRecoveryGenerationRenameNeverReplaces(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("exclusive rename is unavailable on this platform")
	}
	directory := t.TempDir()
	source, target := filepath.Join(directory, "source"), filepath.Join(directory, "target")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := renameRecoveryNoReplace(source, target); err == nil {
		t.Fatal("existing generation was replaced")
	}
	for path, want := range map[string]string{source: "source", target: "target"} {
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != want {
			t.Fatalf("%s changed", path)
		}
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := renameRecoveryNoReplace(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source was not atomically moved")
	}
	if f, err := openRecoveryFile(target, false); err != nil {
		t.Fatalf("candidate has invalid links: %v", err)
	} else {
		f.Close()
	}
}

func TestMetroTailRecoverySyncsExistingCandidateBeforePublication(t *testing.T) {
	f := makeTailFixture(t, nil)
	for attempt := 0; attempt < 2; attempt++ {
		owner, err := openMetroRecoveryOwner(f.directory, f.request)
		if err != nil {
			t.Fatal(err)
		}
		owner.syncDir = func(string) error { return errors.New("injected directory sync failure") }
		_, err = owner.recover()
		owner.close()
		if err == nil {
			t.Fatal("unsynced candidate was published")
		}
		m, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
		if err != nil || digestBytes(m) != f.request.ManifestSHA256 {
			t.Fatal("manifest changed before candidate sync")
		}
	}
	if result, err := RecoverMetroTail(f.directory, f.request); err != nil || result.Status != "repaired" {
		t.Fatalf("retry: %+v %v", result, err)
	}
}

func TestMetroTailRecoveryRetryWithOnlyManifestHeadroom(t *testing.T) {
	f := makeTailFixture(t, nil)
	owner, err := openMetroRecoveryOwner(f.directory, f.request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := owner.stateBlock()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := owner.decodeBlock(state, maxBlockBytes)
	if err != nil {
		t.Fatal(err)
	}
	var cp checkpoint
	if err = knownJSON(raw, &cp); err != nil {
		t.Fatal(err)
	}
	detail, day, err := owner.recoverySources()
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.validateTail(&cp, state, detail, day); err != nil {
		t.Fatal(err)
	}
	candidate, err := owner.recoveredCheckpoint(&cp, state)
	if err != nil {
		t.Fatal(err)
	}
	blob := owner.encoder.EncodeAll(candidate, nil)
	nextState := namedGeneration(block{Key: state.Key, Kind: state.Kind, Operator: state.Operator, Age: state.Age}, blob)
	next, _, _ := replacementManifest(owner.manifest, nextState)
	nextRaw, err := encodeManifest(next)
	if err != nil {
		t.Fatal(err)
	}
	used, err := (&Service{config: Config{Directory: f.directory}}).allocated()
	if err != nil {
		t.Fatal(err)
	}
	f.request.ArchiveLimitBytes = used + allocatedBytes(len(blob)) + allocatedBytes(len(nextRaw))
	owner.close()
	owner, err = openMetroRecoveryOwner(f.directory, f.request)
	if err != nil {
		t.Fatal(err)
	}
	owner.syncDir = func(string) error { return errors.New("injected directory sync failure") }
	_, err = owner.recover()
	owner.close()
	if err == nil || !strings.Contains(err.Error(), "injected directory sync failure") {
		t.Fatalf("expected sync failure: %v", err)
	}
	if result, err := RecoverMetroTail(f.directory, f.request); err != nil || result.Status != "repaired" {
		t.Fatalf("retry with manifest headroom: %+v %v", result, err)
	}
}

func TestMetroTailRecoveryMissingReservedCandidateFailsClosed(t *testing.T) {
	f := makeTailFixture(t, nil)
	owner, err := openMetroRecoveryOwner(f.directory, f.request)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	blob := []byte("candidate")
	candidate := namedGeneration(block{Key: "metro:state", Kind: "state", Operator: "metro"}, blob)
	before := inventory(t, f.directory)
	if err := owner.writeGeneration(candidate, blob, true); err == nil {
		t.Fatal("missing reserved candidate was recreated")
	}
	if !reflect.DeepEqual(before, inventory(t, f.directory)) {
		t.Fatal("missing candidate changed archive")
	}
}

func TestMetroTailRecoveryFencesMissingOrCorruptOriginal(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := makeTailFixture(t, nil)
			if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.directory, f.state.File)
			switch mode {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".saved", path); err != nil {
					t.Fatal(err)
				}
			}
			// A fenced, undecodable reference must not cause other valid state
			// orphans to be treated as disposable during reconciliation.
			orphanBlob := []byte("independent state")
			orphan := namedGeneration(block{Key: "metro:state:other", Kind: "state"}, orphanBlob)
			if err := os.WriteFile(filepath.Join(f.directory, orphan.File), orphanBlob, 0600); err != nil {
				t.Fatal(err)
			}
			manifest, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				s, err := Open(DefaultConfig(f.directory))
				if err != nil {
					t.Fatal(err)
				}
				if s.detailFence == nil || s.tailRecovery == nil {
					t.Fatal("missing or corrupt recovery source did not fence")
				}
				if err = s.Close(); err == nil {
					t.Fatal("fenced close unexpectedly succeeded")
				}
				current, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
				if err != nil || !reflect.DeepEqual(current, manifest) {
					t.Fatal("fenced archive manifest changed")
				}
				if _, err = os.Lstat(filepath.Join(f.directory, orphan.File)); err != nil {
					t.Fatal("fenced state orphan was deleted")
				}
				if mode != "missing" {
					if _, err = os.Lstat(path); err != nil {
						t.Fatal("unverified original was deleted")
					}
				}
			}
		})
	}
}

func TestMetroTailRecoveryFencedUnknownCheckpointPreservesPriorState(t *testing.T) {
	f := makeTailFixture(t, nil)
	if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
		t.Fatal(err)
	}
	m := readFixtureManifest(t, f.directory)
	for _, b := range m.Blocks {
		if b.Key == "metro:state" {
			if err := os.WriteFile(filepath.Join(f.directory, b.File), []byte("corrupt checkpoint"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	s, err := Open(DefaultConfig(f.directory))
	if err != nil {
		t.Fatal(err)
	}
	if s.detailFence == nil {
		t.Fatal("corrupt checkpoint did not fence")
	}
	if err = s.Close(); err == nil {
		t.Fatal("fenced close unexpectedly succeeded")
	}
	original, err := os.ReadFile(filepath.Join(f.directory, f.state.File))
	if err != nil || digestBytes(original) != f.state.Hash {
		t.Fatal("original checkpoint was deleted")
	}
}

// A subprocess exits at the indicated directory-sync boundary. This models a
// process crash; storage power-loss durability still depends on the filesystem.
func TestMetroTailRecoveryCrashWorker(t *testing.T) {
	if os.Getenv("LISBOA_TAIL_CRASH_CHILD") == "" {
		return
	}
	directory := os.Getenv("LISBOA_TAIL_CRASH_DIRECTORY")
	raw, err := os.ReadFile(os.Getenv("LISBOA_TAIL_CRASH_REQUEST"))
	if err != nil {
		t.Fatal(err)
	}
	var request MetroTailRecoveryRequest
	if err = json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	owner, err := openMetroRecoveryOwner(directory, request)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	stage := os.Getenv("LISBOA_TAIL_CRASH_CHILD")
	owner.renameGeneration = func(source, target string) error {
		if stage == "before_generation_rename" {
			os.Exit(93)
		}
		err := renameRecoveryNoReplace(source, target)
		if err == nil && stage == "after_generation_rename" {
			os.Exit(93)
		}
		return err
	}
	calls := 0
	owner.syncDir = func(path string) error {
		calls++
		if (stage == "generation" && calls == 1) || (stage == "manifest" && calls == 2) {
			os.Exit(93)
		}
		return syncDirectory(path)
	}
	_, _ = owner.recover()
	t.Fatal("crash point was not reached")
}

func TestMetroTailRecoveryProcessCrashAndRetry(t *testing.T) {
	for _, stage := range []string{"before_generation_rename", "after_generation_rename", "generation", "manifest"} {
		t.Run(stage, func(t *testing.T) {
			f := makeTailFixture(t, nil)
			requestPath := filepath.Join(t.TempDir(), "request.json")
			raw, err := json.Marshal(f.request)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(requestPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestMetroTailRecoveryCrashWorker$")
			cmd.Env = append(os.Environ(), "LISBOA_TAIL_CRASH_CHILD="+stage, "LISBOA_TAIL_CRASH_DIRECTORY="+f.directory, "LISBOA_TAIL_CRASH_REQUEST="+requestPath)
			err = cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 93 {
				t.Fatalf("child did not crash as expected: %v", err)
			}
			result, err := RecoverMetroTail(f.directory, f.request)
			if err != nil {
				t.Fatal(err)
			}
			want := "repaired"
			if stage == "manifest" {
				want = "already_applied"
			}
			if result.Status != want {
				t.Fatalf("got %s want %s", result.Status, want)
			}
		})
	}
}

func TestMetroTailRecoveryMarkerSurvivesCheckpointWriters(t *testing.T) {
	f := makeTailFixture(t, nil)
	if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
		t.Fatal(err)
	}
	s, err := Open(DefaultConfig(f.directory))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.tailRecovery == nil {
		t.Fatal("missing recovered marker")
	}
	check := func(data []byte) {
		var cp checkpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cp.TailRecovery, s.tailRecovery) {
			t.Fatal("checkpoint writer lost recovery marker")
		}
	}
	raw, err := s.marshalCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	check(raw)
	now := time.Now().UTC()
	provider := providerRevision{service: s, operator: "cm", replacement: newProvider("cm"), now: now}
	if err = provider.prepareCheckpoint(); err != nil {
		t.Fatal(err)
	}
	raw, err = s.decoder.DecodeAll(provider.prepared[len(provider.prepared)-1].blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	check(raw)
	metro := metroRevision{service: s, replacement: s.engine, now: now, days: map[string]bool{}}
	if err = metro.prepareBlocks(); err != nil {
		t.Fatal(err)
	}
	raw, err = s.decoder.DecodeAll(metro.prepared[len(metro.prepared)-1].blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	check(raw)
}

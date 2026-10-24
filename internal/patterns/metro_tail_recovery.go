package patterns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"
)

// RecoverMetroTail performs a narrowly verified, offline repair. It never opens
// the normal archive service, runs cleanup, contacts a provider, or replays the tail.
func RecoverMetroTail(directory string, request MetroTailRecoveryRequest) (MetroTailRecoveryResult, error) {
	var result MetroTailRecoveryResult
	if err := request.validate(); err != nil {
		return result, err
	}
	owner, err := openMetroRecoveryOwner(directory, request)
	if err != nil {
		return result, err
	}
	defer owner.close()
	return owner.recover()
}

type metroRecoveryOwner struct {
	directory        string
	request          MetroTailRecoveryRequest
	lock             *os.File
	decoder          *zstd.Decoder
	encoder          *zstd.Encoder
	manifest         manifest
	manifestRaw      []byte
	syncDir          func(string) error
	rename           func(string, string) error
	renameGeneration func(string, string) error
}

func openMetroRecoveryOwner(directory string, request MetroTailRecoveryRequest) (*metroRecoveryOwner, error) {
	lock, err := lockRecoveryDirectory(directory)
	if err != nil {
		return nil, err
	}
	owner := &metroRecoveryOwner{directory: directory, request: request, lock: lock, syncDir: syncDirectory, rename: os.Rename, renameGeneration: renameRecoveryNoReplace}
	if err = owner.initialize(); err != nil {
		owner.close()
		return nil, err
	}
	return owner, nil
}

func lockRecoveryDirectory(directory string) (*os.File, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, fmt.Errorf("archive path must be absolute and clean")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return nil, fmt.Errorf("archive path is unavailable or contains a symlink")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("archive directory is unavailable")
	}
	lock, err := openRecoveryFile(filepath.Join(directory, ".lock"), true)
	if err != nil {
		return nil, fmt.Errorf("archive lock unavailable: %w", err)
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("archive already owned: %w", err)
	}
	return lock, nil
}

func (o *metroRecoveryOwner) initialize() error {
	var err error
	o.decoder, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxBlockBytes))
	if err != nil {
		return err
	}
	o.encoder, err = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(9)))
	if err != nil {
		return err
	}
	return o.loadManifest()
}

func (o *metroRecoveryOwner) loadManifest() error {
	var err error
	o.manifestRaw, err = readRecoveryFile(filepath.Join(o.directory, "manifest.json"), maxManifestBytes)
	if err != nil {
		return err
	}
	if err = knownJSON(o.manifestRaw, &o.manifest); err != nil || o.manifest.Version != 1 {
		return fmt.Errorf("invalid archive manifest")
	}
	canonical, err := encodeManifest(o.manifest)
	if err != nil || !bytes.Equal(canonical, o.manifestRaw) {
		return fmt.Errorf("noncanonical archive manifest")
	}
	if err = validateArchiveBlocks(o.manifest.Blocks); err != nil {
		return err
	}
	return validateRecoveryIdentities(o.manifest)
}

func (o *metroRecoveryOwner) close() {
	if o.encoder != nil {
		o.encoder.Close()
	}
	if o.decoder != nil {
		o.decoder.Close()
	}
	if o.lock != nil {
		_ = syscall.Flock(int(o.lock.Fd()), syscall.LOCK_UN)
		_ = o.lock.Close()
	}
}

// Follow neither symlinks nor hard-linked files, including the archive lock.
func openRecoveryFile(path string, writable bool) (*os.File, error) {
	flags := syscall.O_RDONLY
	if writable {
		flags = syscall.O_RDWR
	}
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("recovery source is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("recovery source has multiple links")
	}
	return f, nil
}

func readRecoveryFile(path string, limit int64) ([]byte, error) {
	f, err := openRecoveryFile(path, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, fmt.Errorf("recovery source is unreadable or exceeds its bound")
	}
	return raw, nil
}

func validateRecoveryIdentities(m manifest) error {
	keys, files := map[string]bool{}, map[string]bool{}
	for _, b := range m.Blocks {
		if keys[b.Key] || files[b.File] {
			return fmt.Errorf("duplicate archive identity")
		}
		keys[b.Key], files[b.File] = true, true
		if (b.Kind == "detail" && b.Operator == "metro") || bytes.HasPrefix([]byte(b.Key), []byte("metro:detail:")) {
			if !validRecoveryDetailIdentity(b) {
				return fmt.Errorf("invalid Metro detail identity")
			}
		}
	}
	return nil
}

func (o *metroRecoveryOwner) block(expected block) (block, error) {
	for _, b := range o.manifest.Blocks {
		if b.Key == expected.Key {
			if b.Kind != expected.Kind || b.Operator != "metro" || b.Hash != expected.Hash {
				return block{}, fmt.Errorf("recovery source identity changed")
			}
			return b, nil
		}
	}
	return block{}, fmt.Errorf("recovery source missing")
}

func (o *metroRecoveryOwner) readBlock(b block) ([]byte, error) {
	if !safeFilename(b.File) || !validSHA256(b.Hash) {
		return nil, fmt.Errorf("invalid recovery source identity")
	}
	raw, err := readRecoveryFile(filepath.Join(o.directory, b.File), maxBlockBytes)
	if err != nil || digestBytes(raw) != b.Hash {
		return nil, fmt.Errorf("recovery source missing or checksum mismatch")
	}
	return raw, nil
}

func (o *metroRecoveryOwner) decodeBlock(b block, maxBytes int) ([]byte, error) {
	blob, err := o.readBlock(b)
	if err != nil {
		return nil, err
	}
	raw, err := o.decoder.DecodeAll(blob, nil)
	if err != nil || len(raw) > maxBytes {
		return nil, fmt.Errorf("invalid or oversized recovery source")
	}
	return raw, nil
}

func (o *metroRecoveryOwner) recover() (MetroTailRecoveryResult, error) {
	var result MetroTailRecoveryResult
	r := o.request
	cp, state, recoveredHash, err := o.readRecoveryCheckpoint()
	if err != nil {
		return result, err
	}
	if cp.TailRecovery != nil {
		marker := cp.TailRecovery
		cp = checkpoint{}
		return o.alreadyRecovered(marker, state, recoveredHash)
	}
	if digestBytes(o.manifestRaw) != r.ManifestSHA256 || state.Hash != r.CheckpointSHA256 {
		return result, fmt.Errorf("recovery manifest or checkpoint changed")
	}
	detail, day, err := o.recoverySources()
	if err != nil {
		return result, err
	}
	if err = o.validateTail(&cp, state, detail, day); err != nil {
		return result, err
	}
	return o.publishRecovery(&cp, state, detail, day)
}

func (o *metroRecoveryOwner) readRecoveryCheckpoint() (checkpoint, block, string, error) {
	var cp checkpoint
	state, err := o.stateBlock()
	if err != nil {
		return cp, state, "", err
	}
	raw, err := o.decodeBlock(state, maxBlockBytes)
	if err != nil {
		return cp, state, "", err
	}
	if err = knownJSON(raw, &cp); err != nil || !validMetroCheckpoint(cp) {
		return cp, state, "", fmt.Errorf("invalid recovery checkpoint")
	}
	return cp, state, digestBytes(raw), nil
}

type recoveryPublication struct {
	state          block
	blob, manifest []byte
}

// Publish the verified checkpoint only after reserving and syncing its generation.
func (o *metroRecoveryOwner) publishRecovery(cp *checkpoint, state, detail, day block) (MetroTailRecoveryResult, error) {
	var result MetroTailRecoveryResult
	publication, err := o.prepareRecoveryPublication(cp, state)
	if err != nil {
		return result, err
	}
	if err = o.stageRecoveryGeneration(publication); err != nil {
		return result, err
	}
	if err = o.verifySourceCAS(state, detail, day, publication.state); err != nil {
		return result, err
	}
	if err = o.commitManifest(publication.manifest, state, detail, day, publication.state); err != nil {
		return result, err
	}
	return o.result("repaired", publication.manifest, publication.state), nil
}

func (o *metroRecoveryOwner) prepareRecoveryPublication(cp *checkpoint, state block) (recoveryPublication, error) {
	var publication recoveryPublication
	candidate, err := o.recoveredCheckpoint(cp, state)
	if err != nil {
		return publication, err
	}
	blob := o.encoder.EncodeAll(candidate, nil)
	decoded, err := o.decoder.DecodeAll(blob, nil)
	if err != nil || !bytes.Equal(candidate, decoded) {
		return publication, fmt.Errorf("checkpoint codec verification failed")
	}
	newState := namedGeneration(block{Key: "metro:state", Kind: "state", Operator: "metro", Age: state.Age}, blob)
	next, _, changed := replacementManifest(o.manifest, newState)
	if !changed {
		return publication, fmt.Errorf("recovery checkpoint did not change")
	}
	nextRaw, err := encodeManifest(next)
	if err != nil {
		return publication, err
	}
	return recoveryPublication{state: newState, blob: blob, manifest: nextRaw}, nil
}

func (o *metroRecoveryOwner) stageRecoveryGeneration(p recoveryPublication) error {
	existing, err := o.existingGeneration(p.state, p.blob)
	if err != nil {
		return err
	}
	additionalBytes, additionalFiles := len(p.blob), uint64(1)
	if existing {
		additionalBytes, additionalFiles = 0, 0
	}
	if err = o.reserve(additionalBytes, additionalFiles, p.manifest); err != nil {
		return err
	}
	if err = o.writeGeneration(p.state, p.blob, existing); err != nil {
		return err
	}
	return o.reserve(0, 0, p.manifest)
}

func (o *metroRecoveryOwner) recoveredCheckpoint(cp *checkpoint, state block) ([]byte, error) {
	cp.TailRecovery = o.request.marker(state)
	if !validMetroTailRecovery(cp.TailRecovery) || cp.Engine.Gaps == math.MaxInt64 {
		return nil, fmt.Errorf("invalid recovery boundary")
	}
	cp.Engine.Gaps++
	cp.Engine.resetContinuity()
	candidate, err := json.Marshal(cp)
	if err != nil || len(candidate) > maxBlockBytes {
		return nil, fmt.Errorf("recovered checkpoint exceeds its bound")
	}
	return candidate, nil
}

func (o *metroRecoveryOwner) stateBlock() (block, error) {
	var state block
	found := false
	for _, b := range o.manifest.Blocks {
		if b.Key == "metro:state" || b.Kind == "state" {
			if found || b.Key != "metro:state" || b.Kind != "state" || b.Operator != "metro" {
				return block{}, fmt.Errorf("ambiguous Metro checkpoint identity")
			}
			state, found = b, true
		}
	}
	if !found {
		return block{}, fmt.Errorf("Metro checkpoint missing")
	}
	return state, nil
}

func (o *metroRecoveryOwner) recoverySources() (block, block, error) {
	r := o.request
	hour := r.DetailHour.UTC()
	for _, b := range o.manifest.Blocks {
		if b.Kind == "detail" && b.Operator == "metro" && b.Age.After(hour) {
			return block{}, block{}, fmt.Errorf("later Metro detail requires separate recovery")
		}
	}
	detail, err := o.block(block{Key: "metro:detail:" + hour.Format(time.RFC3339), Kind: "detail", Hash: r.DetailSHA256})
	if err != nil || !detail.Age.Equal(hour) {
		return block{}, block{}, fmt.Errorf("Metro detail identity changed")
	}
	day, err := o.block(block{Key: "metro:aggregate:" + r.AffectedDay, Kind: "aggregate", Hash: r.AggregateSHA256})
	if err != nil || day.Date != r.AffectedDay {
		return block{}, block{}, fmt.Errorf("Metro daily aggregate identity changed")
	}
	return detail, day, nil
}

func (o *metroRecoveryOwner) validateTail(cp *checkpoint, state, detail, day block) error {
	r := o.request
	if err := validateRecoveryCheckpoint(cp, state, r); err != nil {
		return err
	}
	rows, err := o.recoveryTailRows(cp, detail)
	if err != nil {
		return err
	}
	tail := rows[len(rows)-1]
	if err = validateTailAffectedDay(cp.Engine, tail, r.AffectedDay); err != nil {
		return err
	}
	if err = o.validateDay(cp.Engine.Aggregates, day); err != nil {
		return err
	}
	if err = retireTailOutcomes(cp.Engine, tail.Outcomes, r.AffectedDay); err != nil {
		return err
	}
	cp.Detail = rows
	return nil
}

func validateRecoveryCheckpoint(cp *checkpoint, state block, r MetroTailRecoveryRequest) error {
	if !cp.Hour.Equal(r.DetailHour) || len(cp.Detail) != r.CheckpointRows ||
		!state.Age.Equal(r.CheckpointAt) || !cp.Engine.LastReceipt.Equal(r.CheckpointAt) {
		return fmt.Errorf("checkpoint does not match recovery request")
	}
	if cp.SampleSeconds != r.SampleSeconds || cp.BinSeconds != r.BinSeconds ||
		!cp.Detail[len(cp.Detail)-1].ReceivedAt.Equal(r.CheckpointAt) {
		return fmt.Errorf("checkpoint does not match recovery request")
	}
	if err := validateDetailReceipts(cp.Detail, cp.Hour); err != nil {
		return fmt.Errorf("invalid checkpoint detail")
	}
	return nil
}

func (o *metroRecoveryOwner) recoveryTailRows(cp *checkpoint, detail block) ([]Receipt, error) {
	r := o.request
	raw, err := o.decodeBlock(detail, maxDetailHourBytes)
	if err != nil {
		return nil, err
	}
	rows, err := decodeDetailRecords(raw, detail)
	if err != nil || len(rows) != r.CheckpointRows+1 || !receiptPrefix(cp.Detail, rows) {
		return nil, fmt.Errorf("recovery detail is not one complete matching tail")
	}
	if !validRecoveryTailReceipt(rows[len(rows)-1], r) {
		return nil, fmt.Errorf("tail does not match recovery request")
	}
	return rows, nil
}

func validRecoveryTailReceipt(tail Receipt, r MetroTailRecoveryRequest) bool {
	return tail.ReceivedAt.Equal(r.TailAt) && tail.ReceivedAt.After(r.CheckpointAt) && !tail.DeliveryGap &&
		tail.Error == "" && len(tail.Forecasts) == 0 && len(tail.Outcomes) == r.TailOutcomes
}

// The tail is not replayed. Bound every date it could have affected, including
// withdrawals of existing associations, before accepting one verified day.
func validateTailAffectedDay(e *engine, tail Receipt, day string) error {
	if err := validateRecoveryEngineDays(e, day); err != nil {
		return err
	}
	if err := validateRecoveryGroupDays(e, day); err != nil {
		return err
	}
	return validateRecoverySourceDays(e, tail, day)
}

func recoveryDay(clock time.Time, day string) bool {
	return !clock.IsZero() && clock.In(lisbon).Format("2006-01-02") == day
}

func validateRecoveryEngineDays(e *engine, day string) error {
	for date, dirty := range e.DirtyDays {
		if dirty && date != day {
			return fmt.Errorf("checkpoint has another dirty aggregate day")
		}
	}
	for _, c := range e.Cases {
		if !recoveryDay(c.IssuedAt, day) {
			return fmt.Errorf("pending case affects another aggregate day")
		}
	}
	return nil
}

func validateRecoveryGroupDays(e *engine, day string) error {
	for _, group := range e.Groups {
		if group == nil {
			return fmt.Errorf("invalid checkpoint association")
		}
		for _, signal := range group.Signals {
			if !recoveryDay(signal.L, day) || !recoveryDay(signal.U, day) {
				return fmt.Errorf("association affects another aggregate day")
			}
		}
	}
	return nil
}

func validateRecoverySourceDays(e *engine, tail Receipt, day string) error {
	for _, prior := range e.Previous {
		if !recoveryDay(prior.Clock, day) {
			return fmt.Errorf("prior clock affects another aggregate day")
		}
	}
	for _, row := range tail.Rows {
		clock, valid := sourceClock(row.Clock)
		if validMetroSampleClock(clock, valid, tail.ReceivedAt) && !recoveryDay(clock, day) {
			return fmt.Errorf("tail source clock affects another aggregate day")
		}
	}
	return nil
}

func (o *metroRecoveryOwner) validateDay(aggregates map[string]Aggregate, day block) error {
	rows, err := o.readRecoveryAggregate(day)
	if err != nil {
		return err
	}
	checkpointRows, err := recoveryCheckpointAggregates(aggregates, day.Date)
	if err != nil {
		return err
	}
	dailyRows, err := recoveryDailyAggregates(rows, day.Date)
	if err != nil {
		return err
	}
	if len(checkpointRows) == 0 || !reflect.DeepEqual(checkpointRows, dailyRows) {
		return fmt.Errorf("daily aggregate differs from checkpoint")
	}
	return nil
}

func (o *metroRecoveryOwner) readRecoveryAggregate(day block) ([]Aggregate, error) {
	blob, err := o.readBlock(day)
	if err != nil {
		return nil, err
	}
	file, err := parquet.OpenFile(bytes.NewReader(blob), int64(len(blob)))
	if err != nil || file.NumRows() > maxEngineAggregates {
		return nil, fmt.Errorf("invalid aggregate generation")
	}
	rows, err := parquet.Read[Aggregate](bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, fmt.Errorf("invalid aggregate rows")
	}
	return rows, nil
}

func recoveryCheckpointAggregates(aggregates map[string]Aggregate, day string) (map[string]Aggregate, error) {
	checkpointRows := map[string]Aggregate{}
	for _, a := range aggregates {
		if a.Date != day {
			continue
		}
		key := aggregateKey(a)
		if _, exists := checkpointRows[key]; exists {
			return nil, fmt.Errorf("duplicate checkpoint aggregate")
		}
		checkpointRows[key] = a
	}
	return checkpointRows, nil
}

func recoveryDailyAggregates(rows []Aggregate, day string) (map[string]Aggregate, error) {
	dailyRows := map[string]Aggregate{}
	for _, a := range rows {
		if a.Date != day {
			return nil, fmt.Errorf("daily aggregate date mismatch")
		}
		key := aggregateKey(a)
		if _, exists := dailyRows[key]; exists {
			return nil, fmt.Errorf("duplicate daily aggregate")
		}
		dailyRows[key] = a
	}
	return dailyRows, nil
}

func retireTailOutcomes(e *engine, outcomes []Forecast, day string) error {
	positions := map[string][]int{}
	for i, c := range e.Cases {
		positions[c.ID] = append(positions[c.ID], i)
	}
	seen, retire := map[string]bool{}, map[int]bool{}
	for _, out := range outcomes {
		i, err := matchingTailOutcome(e.Cases, out, positions[out.ID], seen[out.ID], day)
		if err != nil {
			return err
		}
		seen[out.ID] = true
		retire[i] = true
	}
	kept := make([]Forecast, 0, len(e.Cases)-len(retire))
	for i, f := range e.Cases {
		if !retire[i] {
			kept = append(kept, f)
		}
	}
	e.Cases = kept
	return nil
}

func matchingTailOutcome(cases []Forecast, out Forecast, positions []int, seen bool, day string) (int, error) {
	if out.ID == "" || seen || len(positions) != 1 || !validTailOutcome(out, day) {
		return 0, fmt.Errorf("tail outcome is not uniquely attributable")
	}
	i := positions[0]
	prior := cases[i]
	if prior.Evaluated || prior.Result != "pending" || !sameForecastInput(prior, out) {
		return 0, fmt.Errorf("tail outcome differs from pending case")
	}
	return i, nil
}

func validTailOutcome(out Forecast, day string) bool {
	if out.Result != "evaluated_proxy" || !out.Evaluated || out.IssuedAt.In(lisbon).Format("2006-01-02") != day {
		return false
	}
	return out.ReferenceLower != nil && out.ReferenceUpper != nil && !out.ReferenceUpper.Before(*out.ReferenceLower)
}

func sameForecastInput(a, b Forecast) bool {
	clear := func(f *Forecast) {
		f.Result, f.Evaluated = "", false
		f.ReferenceLower, f.ReferenceUpper, f.ErrorLower, f.ErrorUpper = nil, nil, nil, nil
	}
	clear(&a)
	clear(&b)
	return reflect.DeepEqual(a, b)
}

func (o *metroRecoveryOwner) reserve(candidateBytes int, candidateFiles uint64, nextManifest []byte) error {
	service := &Service{config: Config{Directory: o.directory}}
	used, err := service.allocated()
	if err != nil || used < 0 {
		return fmt.Errorf("archive allocation unavailable")
	}
	// The old generation and manifest remain allocated during publication.
	need := allocatedBytes(candidateBytes) + allocatedBytes(len(nextManifest))
	if need < 0 || used > o.request.ArchiveLimitBytes || need > o.request.ArchiveLimitBytes-used {
		return fmt.Errorf("archive recovery exceeds configured byte limit")
	}
	space, err := filesystemArchiveSpace(o.directory)
	files := candidateFiles + 1 // The temporary manifest is always created.
	if err != nil || !o.recoveryFilesystemHasSpace(space, uint64(need), files) {
		return fmt.Errorf("archive recovery filesystem reserve unavailable")
	}
	return nil
}

func (o *metroRecoveryOwner) recoveryFilesystemHasSpace(space archiveSpace, bytes, files uint64) bool {
	return bytes <= space.bytes && uint64(o.request.MinFreeBytes) <= space.bytes-bytes &&
		files <= space.inodes && uint64(o.request.MinFreeInodes) <= space.inodes-files
}

// Generation staging uses a unique temporary file. A crash leaves only an
// unreferenced file; recovery never deletes or rewrites a source generation.
func (o *metroRecoveryOwner) existingGeneration(b block, blob []byte) (bool, error) {
	path := filepath.Join(o.directory, b.File)
	if _, err := os.Lstat(path); err == nil {
		existing, err := o.readBlock(b)
		if err != nil || !bytes.Equal(existing, blob) {
			return false, fmt.Errorf("candidate generation conflicts with existing file")
		}
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func (o *metroRecoveryOwner) writeGeneration(b block, blob []byte, expectedExisting bool) error {
	exists, err := o.existingGeneration(b, blob)
	if err != nil {
		return err
	}
	if exists {
		// The prior attempt may have crashed after rename and before sync.
		return o.syncDir(o.directory)
	}
	if expectedExisting {
		return fmt.Errorf("candidate generation disappeared after reservation")
	}
	path := filepath.Join(o.directory, b.File)
	temp, err := writeRecoveryTemporary(o.directory, blob)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	// Atomic no-replace rename cannot leave two hard links after a crash.
	if err = o.renameGeneration(temp, path); err != nil {
		return fmt.Errorf("create candidate generation: %w", err)
	}
	return o.syncDir(o.directory)
}

func writeRecoveryTemporary(directory string, data []byte) (string, error) {
	f, err := os.CreateTemp(directory, "metro-recovery-*.tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func (o *metroRecoveryOwner) verifySourceCAS(sources ...block) error {
	current, err := readRecoveryFile(filepath.Join(o.directory, "manifest.json"), maxManifestBytes)
	if err != nil || !bytes.Equal(current, o.manifestRaw) {
		return fmt.Errorf("archive manifest changed during recovery")
	}
	for _, source := range sources {
		if _, err = o.readBlock(source); err != nil {
			return fmt.Errorf("archive source changed during recovery")
		}
	}
	return nil
}

func (o *metroRecoveryOwner) commitManifest(raw []byte, sources ...block) error {
	temp, err := writeRecoveryTemporary(o.directory, raw)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if err = o.verifySourceCAS(sources...); err != nil {
		return err
	}
	if err = o.rename(temp, filepath.Join(o.directory, "manifest.json")); err != nil {
		return err
	}
	if err = o.syncDir(o.directory); err != nil {
		return fmt.Errorf("manifest outcome uncertain after rename; reopen and verify before starting: %w", err)
	}
	return nil
}

func (o *metroRecoveryOwner) alreadyRecovered(marker *MetroTailRecovery, state block, recoveredHash string) (MetroTailRecoveryResult, error) {
	var empty MetroTailRecoveryResult
	if err := o.verifyRecoveryMarker(marker); err != nil {
		return empty, err
	}
	candidate, err := o.rebuildRecoveryCheckpoint(marker)
	if err != nil {
		return empty, err
	}
	if digestBytes(candidate) != recoveredHash {
		return empty, fmt.Errorf("recovered checkpoint differs from verified repair")
	}
	// A preceding attempt may have renamed the manifest before its directory
	// sync failed. Establish durability before reporting idempotent success.
	if err = o.syncDir(o.directory); err != nil {
		return empty, fmt.Errorf("recovered manifest durability remains uncertain: %w", err)
	}
	return o.result("already_applied", o.manifestRaw, state), nil
}

func (o *metroRecoveryOwner) verifyRecoveryMarker(marker *MetroTailRecovery) error {
	if !reflect.DeepEqual(marker, o.request.marker(marker.PriorState)) {
		return fmt.Errorf("existing recovery does not match request")
	}
	previous, _, changed := replacementManifest(o.manifest, marker.PriorState)
	original, err := encodeManifest(previous)
	if err != nil || !changed || digestBytes(original) != o.request.ManifestSHA256 {
		return fmt.Errorf("archive changed after recovery")
	}
	return nil
}

func (o *metroRecoveryOwner) rebuildRecoveryCheckpoint(marker *MetroTailRecovery) ([]byte, error) {
	originalState, err := o.decodeBlock(marker.PriorState, maxBlockBytes)
	if err != nil {
		return nil, fmt.Errorf("original checkpoint unavailable for repeat verification")
	}
	var originalCP checkpoint
	if err = knownJSON(originalState, &originalCP); err != nil || !validMetroCheckpoint(originalCP) || originalCP.TailRecovery != nil {
		return nil, fmt.Errorf("invalid original recovery checkpoint")
	}
	detail, day, err := o.recoverySources()
	if err != nil {
		return nil, err
	}
	if err = o.validateTail(&originalCP, marker.PriorState, detail, day); err != nil {
		return nil, err
	}
	return o.recoveredCheckpoint(&originalCP, marker.PriorState)
}

func (o *metroRecoveryOwner) result(status string, manifestRaw []byte, state block) MetroTailRecoveryResult {
	r := o.request
	return MetroTailRecoveryResult{Status: status, OldManifestSHA256: r.ManifestSHA256, NewManifestSHA256: digestBytes(manifestRaw),
		OldStateSHA256: r.CheckpointSHA256, NewStateSHA256: state.Hash, DetailSHA256: r.DetailSHA256,
		AggregateSHA256: r.AggregateSHA256, TailOutcomes: r.TailOutcomes}
}

package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
)

const (
	eventsFile       = "events.ndjson"
	lockFile         = ".lock"
	statusProjection = "projections/status.json"
	gateProjection   = "projections/gate.json"
	gateReport       = "reports/gate.json"
)

// WithLock serializes access to a run directory. Writers use an exclusive
// lock; readers can use a shared lock while projections are being read.
func WithLock(dir string, exclusive bool, fn func() error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open run lock: %w", err)
	}
	defer file.Close()
	lock := syscall.LOCK_SH
	if exclusive {
		lock = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), lock); err != nil {
		return fmt.Errorf("lock run directory: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}

func LoadRun(dir string) (model.Run, error) {
	var result model.Run
	if err := decodeFile(filepath.Join(dir, "run.json"), &result); err != nil {
		return result, fmt.Errorf("load run: %w", err)
	}
	if result.SchemaVersion != model.SchemaVersion || strings.TrimSpace(result.RunID) == "" || strings.TrimSpace(result.ProfileID) == "" || strings.TrimSpace(result.ProfileDigest) == "" {
		return result, fmt.Errorf("run.json is invalid")
	}
	return result, nil
}

func CreateRun(dir string, runValue model.Run) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "run.json")); err == nil {
		return fmt.Errorf("run already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "run.json"), runValue); err != nil {
		return fmt.Errorf("write run: %w", err)
	}
	return appendAndProject(dir, runValue, "run.created", map[string]string{
		"profileID":     runValue.ProfileID,
		"profileDigest": runValue.ProfileDigest,
	})
}

func AppendGate(dir string, runValue model.Run, report model.GateReport) error {
	if report.SchemaVersion != model.SchemaVersion || report.Kind != "gate-report" || report.RunID != runValue.RunID || report.ProfileID != runValue.ProfileID || report.ProfileDigest != runValue.ProfileDigest {
		return fmt.Errorf("gate report does not match run binding")
	}
	if err := appendAndProject(dir, runValue, "gate.evaluated", report); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, gateProjection), report); err != nil {
		return fmt.Errorf("write gate projection: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, gateReport), report); err != nil {
		return fmt.Errorf("write gate report: %w", err)
	}
	return nil
}

func LoadStatus(dir string) (model.StatusProjection, error) {
	var result model.StatusProjection
	if err := decodeFile(filepath.Join(dir, statusProjection), &result); err != nil {
		return result, fmt.Errorf("load status projection: %w", err)
	}
	return result, nil
}

func LoadReport(dir string) (model.GateReport, error) {
	var result model.GateReport
	if err := decodeFile(filepath.Join(dir, gateReport), &result); err != nil {
		return result, fmt.Errorf("load gate report: %w", err)
	}
	return result, nil
}

func appendAndProject(dir string, runValue model.Run, eventType string, value any) error {
	data, err := model.CanonicalJSON(value)
	if err != nil {
		return fmt.Errorf("encode event data: %w", err)
	}
	sequence, err := nextSequence(dir)
	if err != nil {
		return err
	}
	event := model.Event{
		SchemaVersion: model.SchemaVersion,
		Sequence:      sequence,
		RunID:         runValue.RunID,
		Type:          eventType,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Data:          json.RawMessage(data),
	}
	encoded, err := model.CanonicalJSON(event)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, eventsFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open event journal: %w", err)
	}
	if _, writeErr := file.Write(encoded); writeErr != nil {
		file.Close()
		return fmt.Errorf("append event: %w", writeErr)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync event journal: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close event journal: %w", err)
	}
	status := model.StatusProjection{
		SchemaVersion: model.SchemaVersion,
		RunID:         runValue.RunID,
		ProfileID:     runValue.ProfileID,
		ProfileDigest: runValue.ProfileDigest,
		LastSequence:  sequence,
		LastEventType: eventType,
		EventCount:    sequence,
	}
	if eventType == "gate.evaluated" {
		var report model.GateReport
		if err := json.Unmarshal(data, &report); err != nil {
			return fmt.Errorf("decode gate event: %w", err)
		}
		status.GateDecision = report.Decision
		status.GateEligible = report.Eligible
	}
	return writeAtomic(filepath.Join(dir, statusProjection), status)
}

func nextSequence(dir string) (uint64, error) {
	file, err := os.Open(filepath.Join(dir, eventsFile))
	if os.IsNotExist(err) {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open event journal: %w", err)
	}
	defer file.Close()
	var last uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event model.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return 0, fmt.Errorf("invalid event journal: %w", err)
		}
		if event.Sequence != last+1 {
			return 0, fmt.Errorf("event journal sequence gap at %d", event.Sequence)
		}
		last = event.Sequence
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read event journal: %w", err)
	}
	return last + 1, nil
}

func decodeFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeAtomic(path string, value any) error {
	encoded, err := model.CanonicalJSON(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

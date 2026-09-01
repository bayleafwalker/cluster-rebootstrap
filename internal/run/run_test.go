package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
)

func TestAppendBeforeAtomicProjectionAndCanonicalReport(t *testing.T) {
	dir := t.TempDir()
	runValue := model.Run{SchemaVersion: model.SchemaVersion, RunID: "run", ProfileID: "profile", ProfileDigest: "sha256:digest", CreatedAt: "2026-01-01T00:00:00Z"}
	if err := WithLock(dir, true, func() error { return CreateRun(dir, runValue) }); err != nil {
		t.Fatal(err)
	}
	report := model.GateReport{
		SchemaVersion:    model.SchemaVersion,
		Kind:             "gate-report",
		RunID:            runValue.RunID,
		ProfileID:        runValue.ProfileID,
		ProfileDigest:    runValue.ProfileDigest,
		Predicates:       map[string]model.PredicateObservation{"G1": {Status: model.Pass, Evidence: []string{"ok"}}},
		AllMandatoryPass: true,
		Eligible:         true,
		Decision:         "NO-GO",
	}
	if err := WithLock(dir, true, func() error { return AppendGate(dir, runValue, report) }); err != nil {
		t.Fatal(err)
	}
	status, err := LoadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastSequence != 2 || status.GateDecision != "NO-GO" || !status.GateEligible {
		t.Fatalf("unexpected status: %+v", status)
	}
	contents, err := os.ReadFile(filepath.Join(dir, gateReport))
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.GateReport
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	canonical, err := model.CanonicalJSON(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(canonical) {
		t.Fatalf("report is not canonical JSON: %q", contents)
	}
}

func TestSingleWriterLockSerializesWriters(t *testing.T) {
	dir := t.TempDir()
	firstLocked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		done <- WithLock(dir, true, func() error {
			close(firstLocked)
			<-release
			return nil
		})
	}()
	<-firstLocked
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		done <- WithLock(dir, true, func() error { return nil })
	}()
	<-secondStarted
	select {
	case err := <-done:
		t.Fatalf("second writer acquired lock early: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

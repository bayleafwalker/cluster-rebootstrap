package run

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
)

func TestAppendBeforeAtomicProjectionAndCanonicalReport(t *testing.T) {
	dir := t.TempDir()
	runValue := testRunValue()
	if err := CreateRun(dir, runValue); err != nil {
		t.Fatal(err)
	}
	report := model.GateReport{
		SchemaVersion:    model.SchemaVersion,
		Kind:             "gate-report",
		RunID:            runValue.RunID,
		ProfileID:        runValue.ProfileID,
		ProfileDigest:    runValue.ProfileDigest,
		RecoveryCommit:   runValue.RecoveryCommit,
		CheckpointDigest: runValue.CheckpointDigest,
		PlanDigest:       runValue.PlanDigest,
		Predicates:       map[string]model.PredicateObservation{"G1": {Status: model.Pass, Evidence: []string{"ok"}}},
		EvidenceDigest:   evidenceDigest(map[string]model.PredicateObservation{"G1": {Status: model.Pass, Evidence: []string{"ok"}}}),
		AllMandatoryPass: true,
		Eligible:         true,
		Decision:         "NO-GO",
	}
	if err := AppendGate(dir, runValue, report); err != nil {
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

func testRunValue() model.Run {
	return model.Run{SchemaVersion: model.SchemaVersion, RunID: "run", ProfileID: "profile", ProfileDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", RecoveryCommit: "0123456789abcdef0123456789abcdef01234567", CheckpointDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111", PlanDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222", CreatedAt: "2026-01-01T00:00:00Z"}
}

func evidenceDigest(predicates map[string]model.PredicateObservation) string {
	digest, err := model.EvidenceDigest(model.GateInput{SchemaVersion: model.SchemaVersion, Predicates: predicates})
	if err != nil {
		panic(err)
	}
	return digest
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

func TestReconcileRepairsAllProjectionsAfterInterruptedAppend(t *testing.T) {
	dir := t.TempDir()
	runValue := testRunValue()
	if err := CreateRun(dir, runValue); err != nil {
		t.Fatal(err)
	}
	predicates := map[string]model.PredicateObservation{"G1": {Status: model.Pass, Evidence: []string{"reconciled"}}}
	report := model.GateReport{
		SchemaVersion: model.SchemaVersion, Kind: "gate-report", RunID: runValue.RunID,
		ProfileID: runValue.ProfileID, ProfileDigest: runValue.ProfileDigest,
		RecoveryCommit: runValue.RecoveryCommit, CheckpointDigest: runValue.CheckpointDigest,
		PlanDigest: runValue.PlanDigest, Predicates: predicates,
		EvidenceDigest: evidenceDigest(predicates), AllMandatoryPass: true, Eligible: true, Decision: "NO-GO",
	}
	if err := AppendGate(dir, runValue, report); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{statusProjection, gateProjection, gateReport} {
		if err := os.Remove(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Reconcile(dir); err != nil {
		t.Fatal(err)
	}
	status, err := LoadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastSequence != 2 || status.GateDecision != "NO-GO" || !status.GateEligible {
		t.Fatalf("reconciled status mismatch: %+v", status)
	}
	restored, err := LoadReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.EvidenceDigest != report.EvidenceDigest || restored.PlanDigest != runValue.PlanDigest {
		t.Fatalf("reconciled report mismatch: %+v", restored)
	}
}

func TestReconcileRejectsForeignEventBinding(t *testing.T) {
	dir := t.TempDir()
	runValue := testRunValue()
	if err := CreateRun(dir, runValue); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, eventsFile)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, []byte(`{"schemaVersion":1,"sequence":2,"runID":"other","type":"run.created","occurredAt":"2026-01-01T00:00:00Z","data":{}}`+"\n")...)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(dir); err == nil {
		t.Fatal("foreign event was accepted")
	}
}

func TestAppendGateRejectsUnsafeEvidenceBeforeJournalWrite(t *testing.T) {
	dir := t.TempDir()
	runValue := testRunValue()
	if err := CreateRun(dir, runValue); err != nil {
		t.Fatal(err)
	}
	predicates := map[string]model.PredicateObservation{"G1": {Status: model.Pass, Evidence: []string{"password=not-a-secret"}}}
	report := model.GateReport{
		SchemaVersion: model.SchemaVersion, Kind: "gate-report", RunID: runValue.RunID,
		ProfileID: runValue.ProfileID, ProfileDigest: runValue.ProfileDigest,
		RecoveryCommit: runValue.RecoveryCommit, CheckpointDigest: runValue.CheckpointDigest,
		PlanDigest: runValue.PlanDigest, Predicates: predicates,
		EvidenceDigest: evidenceDigest(predicates), AllMandatoryPass: true, Eligible: true, Decision: "NO-GO",
	}
	if err := AppendGate(dir, runValue, report); err == nil {
		t.Fatal("unsafe evidence was journaled")
	}
	contents, err := os.ReadFile(filepath.Join(dir, eventsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) == "" || string(contents) == "\n" {
		t.Fatal("run-created event unexpectedly missing")
	}
	if bytes.Contains(contents, []byte("password=")) {
		t.Fatal("unsafe evidence entered journal")
	}
}

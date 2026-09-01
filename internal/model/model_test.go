package model

import (
	"strings"
	"testing"
)

func testProfile() Profile {
	return Profile{
		SchemaVersion: SchemaVersion,
		ID:            "test",
		MandatoryPredicates: []PredicateSpec{
			{ID: "G1", Description: "one"},
			{ID: "G2", Description: "two"},
		},
	}
}

func TestValidateGateInputRejectsUnknownAndMissingPredicates(t *testing.T) {
	profile := testProfile()
	input := GateInput{SchemaVersion: SchemaVersion, Predicates: map[string]PredicateObservation{
		"G1": {Status: Pass, Evidence: []string{"ok"}},
		"G3": {Status: Pass, Evidence: []string{"wrong"}},
	}}
	if err := ValidateGateInput(input, profile); err == nil {
		t.Fatal("expected missing/unknown predicate rejection")
	}
}

func TestProfileDigestIsCanonical(t *testing.T) {
	first, err := ProfileDigest(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProfileDigest(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest changed for same profile: %s != %s", first, second)
	}
}

func TestValidateSafeEvidenceRejectsMultilineAndCredentialShapedValues(t *testing.T) {
	for _, value := range []string{"command output\nsecond line", "Authorization: Bearer abc", "password=abc", "AGE-SECRET-KEY-1TEST"} {
		if err := ValidateSafeEvidence(value); err == nil {
			t.Errorf("unsafe evidence accepted: %q", value)
		}
	}
	if err := ValidateSafeEvidence("restore receipt passed"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGateInputEnforcesEvidenceBoundary(t *testing.T) {
	profile := testProfile()
	input := GateInput{SchemaVersion: SchemaVersion, Predicates: map[string]PredicateObservation{
		"G1": {Status: Pass, Evidence: []string{"ok"}},
		"G2": {Status: Pass, Evidence: []string{"raw\noutput"}},
	}}
	if err := ValidateGateInput(input, profile); err == nil || !strings.Contains(err.Error(), "single line") {
		t.Fatalf("expected evidence boundary error, got %v", err)
	}
}

func TestValidateAuthorizationIsBoundToRunAndEvidence(t *testing.T) {
	run := Run{SchemaVersion: SchemaVersion, RunID: "run-1", ProfileID: "profile", ProfileDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", RecoveryCommit: "0123456789abcdef0123456789abcdef01234567", CheckpointDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111", PlanDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222", CreatedAt: "2026-01-01T00:00:00Z"}
	evidence := "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	auth := OperatorAuthorization{Identity: "operator", AuthorizedAt: "2026-01-01T00:00:00Z", PointOfNoReturnAcknowledged: true, RunID: run.RunID, ProfileDigest: run.ProfileDigest, RecoveryCommit: run.RecoveryCommit, CheckpointDigest: run.CheckpointDigest, EvidenceDigest: evidence, PlanDigest: run.PlanDigest}
	if err := ValidateAuthorization(auth, run, evidence); err != nil {
		t.Fatal(err)
	}
	auth.RunID = "run-2"
	if err := ValidateAuthorization(auth, run, evidence); err == nil {
		t.Fatal("cross-run authorization was accepted")
	}
	auth.RunID = run.RunID
	auth.EvidenceDigest = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	if err := ValidateAuthorization(auth, run, evidence); err == nil {
		t.Fatal("stale evidence authorization was accepted")
	}
}

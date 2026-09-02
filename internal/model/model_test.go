package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
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

// Golden vector for ProfileDigest. It is sha256 over CanonicalDigestBytes and
// nothing else; if this value moves, every recorded profileDigest moves with it.
func TestProfileDigestGoldenVector(t *testing.T) {
	digest, err := ProfileDigest(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:aefb9c8f8d0970289a0101e6b7ec19e6275f2829aac23e90e4c6145710db0b10"; digest != want {
		t.Fatalf("profile digest golden vector drifted: %s != %s", digest, want)
	}
}

// Golden vector for EvidenceDigest.
func TestEvidenceDigestGoldenVector(t *testing.T) {
	digest, err := EvidenceDigest(GateInput{SchemaVersion: SchemaVersion, Predicates: map[string]PredicateObservation{
		"G1": {Status: Pass, Evidence: []string{"one"}},
		"G2": {Status: Fail, Evidence: []string{"two"}, Note: "note"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:16b20edf347b7c8756c2e0f1ecc77eb28fc61548f8f8a59d66122ecc3a5915ad"; digest != want {
		t.Fatalf("evidence digest golden vector drifted: %s != %s", digest, want)
	}
}

// One rule: sha256 over encoding/json of the typed value, with no trailing
// newline. CanonicalJSON adds a framing newline for the journal and receipts
// and must never be a digest input.
func TestDigestRuleExcludesTheFramingNewline(t *testing.T) {
	value := testProfile()
	digest, err := Digest(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	bare := sha256.Sum256(encoded)
	if want := "sha256:" + hex.EncodeToString(bare[:]); digest != want {
		t.Fatalf("digest rule drifted: %s != %s", digest, want)
	}
	framed, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(framed) != len(encoded)+1 || framed[len(framed)-1] != '\n' {
		t.Fatalf("CanonicalJSON is not the digest bytes plus one newline: %q", framed)
	}
	withNewline := sha256.Sum256(framed)
	if digest == "sha256:"+hex.EncodeToString(withNewline[:]) {
		t.Fatal("digest includes the framing newline")
	}
}

// The synthetic authorization is only usable if its recorded digests still
// match the fixtures the README quick start feeds the gate.
func TestSyntheticAuthorizationMatchesTheExampleFixtures(t *testing.T) {
	var profile Profile
	decodeFixture(t, "../../examples/synthetic/profile.json", &profile)
	var evidence GateInput
	decodeFixture(t, "../../examples/synthetic/evidence.json", &evidence)
	var authorization OperatorAuthorization
	decodeFixture(t, "../../examples/synthetic/authorization.json", &authorization)

	profileDigest, err := ProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.ProfileDigest != profileDigest {
		t.Errorf("authorization profileDigest is stale: %s != %s", authorization.ProfileDigest, profileDigest)
	}
	evidenceDigest, err := EvidenceDigest(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.EvidenceDigest != evidenceDigest {
		t.Errorf("authorization evidenceDigest is stale: %s != %s", authorization.EvidenceDigest, evidenceDigest)
	}
}

func decodeFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
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

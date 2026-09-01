package gate

import (
	"testing"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
)

func TestAllPassWithoutAuthorizationIsEligibleButNotGo(t *testing.T) {
	profile := model.Profile{
		SchemaVersion: model.SchemaVersion,
		ID:            "test",
		MandatoryPredicates: []model.PredicateSpec{
			{ID: "G1", Description: "one"},
		},
	}
	run := testRun(profile.ID)
	input := model.GateInput{SchemaVersion: model.SchemaVersion, Predicates: map[string]model.PredicateObservation{
		"G1": {Status: model.Pass, Evidence: []string{"evidence"}},
	}}
	report := Evaluate(run, profile, input, nil)
	if !report.AllMandatoryPass || !report.Eligible || report.Decision != "NO-GO" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAuthorizationCanProduceGoOnlyAfterAllPass(t *testing.T) {
	profile := model.Profile{
		SchemaVersion: model.SchemaVersion,
		ID:            "test",
		MandatoryPredicates: []model.PredicateSpec{
			{ID: "G1", Description: "one"},
		},
	}
	run := testRun(profile.ID)
	failInput := model.GateInput{SchemaVersion: model.SchemaVersion, Predicates: map[string]model.PredicateObservation{
		"G1": {Status: model.Fail, Evidence: []string{"failed"}},
	}}
	if report := Evaluate(run, profile, failInput, nil); report.Decision != "NO-GO" {
		t.Fatalf("failed predicate produced GO: %+v", report)
	}
	passInput := model.GateInput{SchemaVersion: model.SchemaVersion, Predicates: map[string]model.PredicateObservation{
		"G1": {Status: model.Pass, Evidence: []string{"passed"}},
	}}
	evidenceDigest, err := model.EvidenceDigest(passInput)
	if err != nil {
		t.Fatal(err)
	}
	auth := &model.OperatorAuthorization{
		Identity: "operator", AuthorizedAt: "2026-01-01T00:00:00Z", PointOfNoReturnAcknowledged: true,
		RunID: run.RunID, ProfileDigest: run.ProfileDigest, RecoveryCommit: run.RecoveryCommit,
		CheckpointDigest: run.CheckpointDigest, EvidenceDigest: evidenceDigest, PlanDigest: run.PlanDigest,
	}
	if report := Evaluate(run, profile, passInput, auth); report.Decision != "GO" {
		t.Fatalf("authorized all-pass input did not produce GO: %+v", report)
	}
}

func testRun(profileID string) model.Run {
	return model.Run{
		SchemaVersion: model.SchemaVersion, RunID: "run", ProfileID: profileID,
		ProfileDigest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		RecoveryCommit:   "0123456789abcdef0123456789abcdef01234567",
		CheckpointDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		PlanDigest:       "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		CreatedAt:        "2026-01-01T00:00:00Z",
	}
}

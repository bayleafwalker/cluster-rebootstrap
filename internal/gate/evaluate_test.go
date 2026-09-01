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
	run := model.Run{RunID: "run", ProfileID: profile.ID, ProfileDigest: "sha256:test"}
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
	run := model.Run{RunID: "run", ProfileID: profile.ID, ProfileDigest: "sha256:test"}
	auth := &model.OperatorAuthorization{Identity: "operator", AuthorizedAt: "2026-01-01T00:00:00Z", PointOfNoReturnAcknowledged: true}
	failInput := model.GateInput{SchemaVersion: model.SchemaVersion, Predicates: map[string]model.PredicateObservation{
		"G1": {Status: model.Fail, Evidence: []string{"failed"}},
	}}
	if report := Evaluate(run, profile, failInput, auth); report.Decision != "NO-GO" {
		t.Fatalf("failed predicate produced GO: %+v", report)
	}
	passInput := model.GateInput{SchemaVersion: model.SchemaVersion, Predicates: map[string]model.PredicateObservation{
		"G1": {Status: model.Pass, Evidence: []string{"passed"}},
	}}
	if report := Evaluate(run, profile, passInput, auth); report.Decision != "GO" {
		t.Fatalf("authorized all-pass input did not produce GO: %+v", report)
	}
}

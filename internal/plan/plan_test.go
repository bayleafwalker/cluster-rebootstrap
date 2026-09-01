package plan

import (
	"os"
	"strings"
	"testing"
)

func syntheticPlan(t *testing.T) Plan {
	t.Helper()
	data, err := os.ReadFile("../../examples/synthetic/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func bindDigest(t *testing.T, p *Plan) {
	t.Helper()
	digest, err := p.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	p.PlanDigest = digest
}

func TestSyntheticPlanValid(t *testing.T) {
	p := syntheticPlan(t)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	phases := map[string]bool{}
	for _, step := range p.Steps {
		phases[step.Phase] = true
	}
	for _, phase := range []string{"decommission", "bootstrap", "restore", "recommission"} {
		if !phases[phase] {
			t.Errorf("fixture does not cover phase %s", phase)
		}
	}
}

func TestDigestIsStableAndExcludesStoredDigest(t *testing.T) {
	p := syntheticPlan(t)
	digest, err := p.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	p.PlanDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	other, err := p.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != other || digest != "sha256:1c16856fc064f5052f41b7b33ad206e9af396e563091ce747e0e3e2360e44559" {
		t.Fatalf("unexpected digest stability: %s vs %s", digest, other)
	}
}

func TestUnknownDependencyRejected(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[0].DependsOn = []string{"does-not-exist"}
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("expected unknown dependency error, got %v", err)
	}
}

func TestCycleRejected(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[0].DependsOn = []string{"retire-old-disks"}
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestDelegatedShellSyntaxRejected(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[1].Argv = []string{"kubectl", "apply", "-f", "x;echo unsafe"}
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "shell syntax") {
		t.Fatalf("expected shell syntax error, got %v", err)
	}
}

func TestDelegatedInterpreterWrapperRejected(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[1].Argv = []string{"env", "bash", "-c", "unsafe"}
	p.Steps[1].Executable = "env"
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "invoke a shell") {
		t.Fatalf("expected interpreter wrapper error, got %v", err)
	}
}

func TestDestructiveStepRequiresConfirmation(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[len(p.Steps)-1].Confirmation = nil
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "operator confirmation") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

func TestConfirmationIsBoundToPlanAndAcknowledgesDestructiveSteps(t *testing.T) {
	p := syntheticPlan(t)
	confirmation := OperatorConfirmation{Operator: "operator@example.invalid", AuthorizedAt: "2026-09-01T18:00:00Z", PlanDigest: p.PlanDigest, CheckpointDigest: p.CheckpointDigest, Acknowledged: map[string]string{"retire-old-disks": p.Steps[len(p.Steps)-1].Confirmation.AcknowledgeWith}}
	if err := confirmation.Validate(p); err != nil {
		t.Fatal(err)
	}
	confirmation.PlanDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := confirmation.Validate(p); err == nil || !strings.Contains(err.Error(), "different plan digest") {
		t.Fatalf("expected digest binding error, got %v", err)
	}
}

func TestConfirmationRejectsUnknownAndMismatchedAcknowledgement(t *testing.T) {
	p := syntheticPlan(t)
	confirmation := OperatorConfirmation{Operator: "operator", AuthorizedAt: "2026-09-01T18:00:00Z", PlanDigest: p.PlanDigest, CheckpointDigest: p.CheckpointDigest, Acknowledged: map[string]string{"quiesce-writers": "anything"}}
	if err := confirmation.Validate(p); err == nil || !strings.Contains(err.Error(), "not a destructive step") {
		t.Fatalf("expected unknown/non-destructive acknowledgement error, got %v", err)
	}
	confirmation.Acknowledged = map[string]string{"retire-old-disks": "wrong text"}
	if err := confirmation.Validate(p); err == nil || !strings.Contains(err.Error(), "exact operator acknowledgement") {
		t.Fatalf("expected exact acknowledgement error, got %v", err)
	}
}

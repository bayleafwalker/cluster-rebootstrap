package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func destructiveStep(t *testing.T, p Plan) int {
	t.Helper()
	for index, step := range p.Steps {
		if step.Destructive {
			return index
		}
	}
	t.Fatal("fixture has no destructive step")
	return -1
}

// The synthetic fixture is a resume plan: the phase decision-gate.md requires
// and the validator previously could not express.
func TestSyntheticResumePlanValid(t *testing.T) {
	p := syntheticPlan(t)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Phase != Resume {
		t.Fatalf("fixture phase is %q, want resume", p.Phase)
	}
	kinds := map[StepKind]bool{}
	for _, step := range p.Steps {
		if step.Phase != Resume {
			t.Errorf("step %s declares phase %q in a resume plan", step.ID, step.Phase)
		}
		kinds[step.Kind] = true
	}
	for _, kind := range []StepKind{Automatic, Delegated, AgentAssisted, OperatorOnly} {
		if !kinds[kind] {
			t.Errorf("fixture does not cover kind %s", kind)
		}
	}
}

func TestEveryPhaseIsExpressibleAsAPlan(t *testing.T) {
	if len(Phases) != 5 {
		t.Fatalf("expected five phases, got %d", len(Phases))
	}
	for _, phase := range Phases {
		p := syntheticPlan(t)
		p.Phase = phase
		for index := range p.Steps {
			p.Steps[index].Phase = phase
		}
		bindDigest(t, &p)
		if err := p.Validate(); err != nil {
			t.Errorf("phase %s is not expressible as a plan: %v", phase, err)
		}
	}
}

func TestUnknownPhaseRejected(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[0].Phase = "teleport"
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "phase must be") {
		t.Fatalf("expected unknown step phase error, got %v", err)
	}

	p = syntheticPlan(t)
	p.Phase = "teleport"
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "plan phase must be") {
		t.Fatalf("expected unknown plan phase error, got %v", err)
	}
}

func TestPlansArePhaseHomogeneous(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[0].Phase = Restore
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), `does not match plan phase "resume"`) {
		t.Fatalf("expected phase homogeneity error, got %v", err)
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
	// Golden vector for plan.CanonicalDigest: sha256 over encoding/json of the
	// typed plan with planDigest blank and no trailing newline. If this value
	// moves, examples/synthetic/authorization.json and the README quick start
	// move with it.
	if digest != other || digest != "sha256:18a86974ad0ca0e204a589e8336d7261c0c10ddf8a848b0d5c3f59dfaba86765" {
		t.Fatalf("unexpected digest stability: %s vs %s", digest, other)
	}
}

// The digest is sha256 over model.CanonicalDigestBytes and nothing else: no
// trailing newline, no re-indentation, no second rule.
func TestCanonicalDigestFollowsTheRepositoryDigestRule(t *testing.T) {
	p := syntheticPlan(t)
	digest, err := p.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	bare := p
	bare.PlanDigest = ""
	encoded, err := json.Marshal(bare)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if want := "sha256:" + hex.EncodeToString(sum[:]); digest != want {
		t.Fatalf("digest rule drifted: %s != %s", digest, want)
	}
	if withNewline := sha256.Sum256(append(encoded, '\n')); digest == "sha256:"+hex.EncodeToString(withNewline[:]) {
		t.Fatal("digest includes a trailing newline")
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
	p.Steps[0].DependsOn = []string{p.Steps[len(p.Steps)-1].ID}
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
	p.Steps[destructiveStep(t, p)].Confirmation = nil
	bindDigest(t, &p)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "operator confirmation") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

func TestConfirmationIsBoundToPlanAndAcknowledgesDestructiveSteps(t *testing.T) {
	p := syntheticPlan(t)
	destructive := p.Steps[destructiveStep(t, p)]
	confirmation := OperatorConfirmation{Operator: "operator@example.invalid", AuthorizedAt: "2026-09-01T18:00:00Z", PlanDigest: p.PlanDigest, CheckpointDigest: p.CheckpointDigest, Acknowledged: map[string]string{destructive.ID: destructive.Confirmation.AcknowledgeWith}}
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
	destructive := p.Steps[destructiveStep(t, p)]
	confirmation := OperatorConfirmation{Operator: "operator", AuthorizedAt: "2026-09-01T18:00:00Z", PlanDigest: p.PlanDigest, CheckpointDigest: p.CheckpointDigest, Acknowledged: map[string]string{p.Steps[0].ID: "anything"}}
	if err := confirmation.Validate(p); err == nil || !strings.Contains(err.Error(), "not a destructive step") {
		t.Fatalf("expected unknown/non-destructive acknowledgement error, got %v", err)
	}
	confirmation.Acknowledged = map[string]string{destructive.ID: "wrong text"}
	if err := confirmation.Validate(p); err == nil || !strings.Contains(err.Error(), "exact operator acknowledgement") {
		t.Fatalf("expected exact acknowledgement error, got %v", err)
	}
}

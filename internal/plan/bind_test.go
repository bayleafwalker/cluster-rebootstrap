package plan

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// The digest the synthetic fixture carries as authored. Binding real anchors
// must move it: a plan that kept its digest across a rebind would be a plan
// whose digest no longer covers its anchors.
const syntheticPlanDigest = "sha256:18a86974ad0ca0e204a589e8336d7261c0c10ddf8a848b0d5c3f59dfaba86765"

const (
	boundProfileDigest    = "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"
	boundRecoveryCommit   = "5f0c1f9a3b2d4e6a8c0b1d3f5a7c9e1b2d4f6a80"
	boundCheckpointDigest = "sha256:fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9"
)

func TestBindRebindsAnchorsAndRedigests(t *testing.T) {
	authored := syntheticPlan(t)
	if authored.PlanDigest != syntheticPlanDigest {
		t.Fatalf("fixture digest moved: %s", authored.PlanDigest)
	}

	bound, err := Bind(authored, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := bound.Validate(); err != nil {
		t.Fatalf("bound plan does not validate: %v", err)
	}
	if bound.ProfileDigest != boundProfileDigest || bound.RecoveryCommit != boundRecoveryCommit || bound.CheckpointDigest != boundCheckpointDigest {
		t.Fatalf("anchors not bound: %+v", bound)
	}
	if bound.PlanDigest == syntheticPlanDigest {
		t.Fatal("plan digest did not move when the anchors did")
	}
	canonical, err := bound.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if bound.PlanDigest != canonical {
		t.Fatalf("recorded digest %s is not the canonical digest %s", bound.PlanDigest, canonical)
	}
}

// Binding is a pure rewrite of the anchors: the argument keeps its own anchors,
// its own digest, and its own steps.
func TestBindDoesNotMutateItsArgument(t *testing.T) {
	authored := syntheticPlan(t)
	before := syntheticPlan(t)

	bound, err := Bind(authored, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authored, before) {
		t.Fatal("Bind mutated the plan it was given")
	}

	destructive := destructiveStep(t, bound)
	bound.Steps[destructive].Confirmation.Prompt = "rewritten through the returned plan"
	bound.Steps[destructive].Preconditions[0] = "rewritten through the returned plan"
	if !reflect.DeepEqual(authored, before) {
		t.Fatal("the bound plan aliases the steps of the plan it was built from")
	}
}

// Rebinding must not become a plan editor: steps keep their order, their
// identities, and their confirmation requirements.
func TestBindPreservesStepsAndConfirmations(t *testing.T) {
	authored := syntheticPlan(t)
	bound, err := Bind(authored, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authored.Steps, bound.Steps) {
		t.Fatal("Bind altered the steps")
	}
	destructive := destructiveStep(t, bound)
	if bound.Steps[destructive].Confirmation == nil {
		t.Fatal("Bind cleared a confirmation requirement")
	}
	if bound.Steps[destructive].Confirmation == authored.Steps[destructive].Confirmation {
		t.Fatal("bound confirmation record is shared with the argument")
	}
	if bound.ID != authored.ID || bound.Phase != authored.Phase || bound.SchemaVersion != authored.SchemaVersion {
		t.Fatal("Bind altered the plan identity")
	}
}

func TestBindIsIdempotent(t *testing.T) {
	authored := syntheticPlan(t)
	once, err := Bind(authored, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Bind(once, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(once, twice) {
		t.Fatal("binding twice with identical anchors is not idempotent")
	}

	// Idempotence has to survive the wire form too, since a bound plan is
	// written out and read back before it is executed.
	var first, second bytes.Buffer
	if err := Encode(&first, once); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&second, twice); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("encoded forms differ:\n%s\n%s", first.String(), second.String())
	}
}

// A plan can be frozen before the quiesced window produces a checkpoint:
// checkpointDigest is omitempty and Validate only format-checks it when set.
func TestBindAcceptsAnEmptyCheckpointDigest(t *testing.T) {
	authored := syntheticPlan(t)
	bound, err := Bind(authored, boundProfileDigest, boundRecoveryCommit, "")
	if err != nil {
		t.Fatal(err)
	}
	if bound.CheckpointDigest != "" {
		t.Fatalf("checkpointDigest is %q, want empty", bound.CheckpointDigest)
	}
	if err := bound.Validate(); err != nil {
		t.Fatalf("unbound-checkpoint plan does not validate: %v", err)
	}
	var encoded bytes.Buffer
	if err := Encode(&encoded, bound); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded.String(), "checkpointDigest") {
		t.Fatal("an empty checkpoint digest was written into the plan")
	}

	// Binding the checkpoint later is the same operation, and it moves the
	// digest again.
	later, err := Bind(bound, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	if later.PlanDigest == bound.PlanDigest {
		t.Fatal("binding the checkpoint did not move the plan digest")
	}
	if err := later.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Binding must not launder an unsafe plan: a destructive step with no
// confirmation requirement is rejected rather than given a fresh valid digest.
func TestBindRejectsUnconfirmedDestructiveStep(t *testing.T) {
	cases := map[string]func(*ConfirmationRequirement) *ConfirmationRequirement{
		"missing":      func(*ConfirmationRequirement) *ConfirmationRequirement { return nil },
		"not required": func(c *ConfirmationRequirement) *ConfirmationRequirement { c.Required = false; return c },
		"blank prompt": func(c *ConfirmationRequirement) *ConfirmationRequirement { c.Prompt = "  "; return c },
		"blank acknowledgement": func(c *ConfirmationRequirement) *ConfirmationRequirement {
			c.AcknowledgeWith = ""
			return c
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := syntheticPlan(t)
			index := destructiveStep(t, p)
			requirement := *p.Steps[index].Confirmation
			p.Steps[index].Confirmation = mutate(&requirement)

			bound, err := Bind(p, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest)
			if err == nil {
				t.Fatal("Bind accepted a destructive step with no confirmation requirement")
			}
			if !strings.Contains(err.Error(), "confirmation") {
				t.Fatalf("unexpected error: %v", err)
			}
			if bound.PlanDigest != "" {
				t.Fatal("a rejected bind still returned a digested plan")
			}
		})
	}
}

func TestBindRejectsMalformedAnchors(t *testing.T) {
	cases := []struct {
		name             string
		profileDigest    string
		recoveryCommit   string
		checkpointDigest string
		want             string
	}{
		{"empty profile digest", "", boundRecoveryCommit, boundCheckpointDigest, "profileDigest"},
		{"unprefixed profile digest", strings.TrimPrefix(boundProfileDigest, "sha256:"), boundRecoveryCommit, boundCheckpointDigest, "profileDigest"},
		{"uppercase profile digest", "sha256:" + strings.ToUpper(strings.TrimPrefix(boundProfileDigest, "sha256:")), boundRecoveryCommit, boundCheckpointDigest, "profileDigest"},
		{"empty commit", boundProfileDigest, "", boundCheckpointDigest, "recoveryCommit"},
		{"abbreviated commit", boundProfileDigest, boundRecoveryCommit[:12], boundCheckpointDigest, "recoveryCommit"},
		{"uppercase commit", boundProfileDigest, strings.ToUpper(boundRecoveryCommit), boundCheckpointDigest, "recoveryCommit"},
		{"branch name as commit", boundProfileDigest, "rebuild/recovery", boundCheckpointDigest, "recoveryCommit"},
		{"malformed checkpoint digest", boundProfileDigest, boundRecoveryCommit, "sha256:not-a-digest", "checkpointDigest"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			bound, err := Bind(syntheticPlan(t), testCase.profileDigest, testCase.recoveryCommit, testCase.checkpointDigest)
			if err == nil {
				t.Fatal("Bind accepted a malformed anchor")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %v does not name %s", err, testCase.want)
			}
			if !reflect.DeepEqual(bound, Plan{}) {
				t.Fatal("a rejected bind returned a plan")
			}
		})
	}
}

// Bind refuses to hand back a plan that Validate would reject, so a caller
// cannot use it to mint a well-formed digest over an ill-formed plan.
func TestBindRejectsAnOtherwiseInvalidPlan(t *testing.T) {
	p := syntheticPlan(t)
	p.Steps[0].DependsOn = []string{"does-not-exist"}
	if _, err := Bind(p, boundProfileDigest, boundRecoveryCommit, boundCheckpointDigest); err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("expected the bound plan to be validated, got %v", err)
	}
}

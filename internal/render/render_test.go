package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/plan"
	"github.com/bayleafwalker/cluster-rebootstrap/internal/version"
)

const goldenPath = "../../examples/synthetic/runsheet.golden.md"

func loadSyntheticPlan(t *testing.T) plan.Plan {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "examples", "synthetic", "plan.json"))
	if err != nil {
		t.Fatalf("open synthetic plan: %v", err)
	}
	defer file.Close()
	p, err := plan.Decode(file)
	if err != nil {
		t.Fatalf("decode synthetic plan: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("synthetic plan does not validate: %v", err)
	}
	return p
}

func renderRunsheet(t *testing.T, p plan.Plan) string {
	t.Helper()
	var buffer bytes.Buffer
	// The golden file pins the dev sentinel because tests are never built
	// with the release linker flags. A stamped release binary renders its own
	// version in the header; that is the only line that differs.
	if version.Version != "dev" {
		t.Skipf("golden run sheet pins the dev sentinel, build reports %q", version.Version)
	}
	if err := Runsheet(&buffer, p, Meta{CLIVersion: version.Version}); err != nil {
		t.Fatalf("render run sheet: %v", err)
	}
	return buffer.String()
}

// TestRunsheetMatchesGolden is the drift guard: the run sheet is a document an
// operator reads under pressure, so a change to its wording or its ordering is
// a change that has to be looked at deliberately, not absorbed silently.
func TestRunsheetMatchesGolden(t *testing.T) {
	rendered := renderRunsheet(t, loadSyntheticPlan(t))
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden run sheet: %v", err)
	}
	if rendered != string(expected) {
		t.Errorf("run sheet drifted from %s\n--- rendered ---\n%s\n--- golden ---\n%s", goldenPath, rendered, expected)
	}
}

// TestRunsheetWarnsBeforeItActs is the property the format exists for: for
// every step, the STOP conditions and any operator confirmation appear above
// the action, never below it. A warning printed under a command is read after
// the command has already been run.
func TestRunsheetWarnsBeforeItActs(t *testing.T) {
	p := loadSyntheticPlan(t)
	rendered := renderRunsheet(t, p)
	for _, step := range p.Steps {
		block := stepBlock(t, rendered, step.ID)
		stop := strings.Index(block, "**STOP —")
		if stop < 0 {
			t.Fatalf("step %q: no STOP block", step.ID)
		}
		action := actionIndex(t, block, step)
		if stop > action {
			t.Errorf("step %q: STOP block appears after the action", step.ID)
		}
		if step.Confirmation != nil {
			confirmation := strings.Index(block, "**Operator confirmation —")
			if confirmation < 0 {
				t.Fatalf("step %q: confirmation requirement is not rendered", step.ID)
			}
			if confirmation > action {
				t.Errorf("step %q: confirmation prompt appears after the action", step.ID)
			}
			if !strings.Contains(block, step.Confirmation.AcknowledgeWith) {
				t.Errorf("step %q: acknowledgement text is not quoted verbatim", step.ID)
			}
		}
	}
}

func TestRunsheetBannersDestructiveSteps(t *testing.T) {
	p := loadSyntheticPlan(t)
	rendered := renderRunsheet(t, p)
	for _, step := range p.Steps {
		block := stepBlock(t, rendered, step.ID)
		banner := strings.Contains(block, "!! DESTRUCTIVE STEP !!")
		if banner != step.Destructive {
			t.Errorf("step %q: destructive=%v but banner present=%v", step.ID, step.Destructive, banner)
		}
		if !step.Destructive {
			continue
		}
		// The banner has to precede everything actionable in the block.
		if strings.Index(block, "!! DESTRUCTIVE STEP !!") > strings.Index(block, "**STOP —") {
			t.Errorf("step %q: destructive banner appears after the STOP block", step.ID)
		}
	}
}

// TestRunsheetIndexesOperatorStepsUpFront checks the index is complete and
// really is up front: learning at step 4 of 6 that step 5 needs you in person
// is how an outage stalls.
func TestRunsheetIndexesOperatorStepsUpFront(t *testing.T) {
	p := loadSyntheticPlan(t)
	rendered := renderRunsheet(t, p)
	index := strings.Index(rendered, "## Where you are personally needed")
	if index < 0 {
		t.Fatal("no operator index")
	}
	firstPhase := strings.Index(rendered, "## Phase: ")
	if firstPhase < index {
		t.Error("the operator index is printed after the first phase")
	}
	header := rendered[index:firstPhase]
	for _, step := range p.Steps {
		wanted := step.Kind == plan.OperatorOnly || step.Confirmation != nil
		listed := strings.Contains(header, "`"+step.ID+"`")
		if listed != wanted {
			t.Errorf("step %q: needs operator=%v but listed in index=%v", step.ID, wanted, listed)
		}
	}
}

func TestRunsheetHeaderBindsToAnchors(t *testing.T) {
	p := loadSyntheticPlan(t)
	rendered := renderRunsheet(t, p)
	header := rendered[:strings.Index(rendered, "## Where you are personally needed")]
	for name, value := range map[string]string{
		"plan digest":       p.PlanDigest,
		"recovery commit":   p.RecoveryCommit,
		"checkpoint digest": p.CheckpointDigest,
		"profile digest":    p.ProfileDigest,
		"CLI version":       version.Version,
	} {
		if !strings.Contains(header, value) {
			t.Errorf("header does not carry the %s (%q)", name, value)
		}
	}
}

// TestRunsheetMarksAnUnboundCheckpoint covers the plan shape Bind exists for:
// a plan frozen before the quiesced window produced a checkpoint. A blank cell
// there would read as an omission rather than as a fact.
func TestRunsheetMarksAnUnboundCheckpoint(t *testing.T) {
	p := loadSyntheticPlan(t)
	p.CheckpointDigest = ""
	var buffer bytes.Buffer
	if err := Runsheet(&buffer, p, Meta{CLIVersion: "v1.2.3"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buffer.String(), "*(not bound yet)*") {
		t.Error("an unbound checkpoint digest is not called out")
	}
	if !strings.Contains(buffer.String(), "rebootstrap v1.2.3") {
		t.Error("the header does not carry the supplied CLI version")
	}
}

func TestRunsheetNamesAnUnknownVersion(t *testing.T) {
	var buffer bytes.Buffer
	if err := Runsheet(&buffer, loadSyntheticPlan(t), Meta{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buffer.String(), "rebootstrap unknown") {
		t.Error("an empty CLI version should render as unknown, not as a blank")
	}
}

// TestTextRenderIsFrozen pins the legacy listing byte for byte. Callers parse
// it; extracting the renderer must not have changed a single character.
func TestTextRenderIsFrozen(t *testing.T) {
	var buffer bytes.Buffer
	if err := Text(&buffer, loadSyntheticPlan(t)); err != nil {
		t.Fatalf("render text: %v", err)
	}
	expected := "PLAN synthetic-resume\n" +
		"Digest: sha256:18a86974ad0ca0e204a589e8336d7261c0c10ddf8a848b0d5c3f59dfaba86765\n" +
		"Profile: sha256:0000000000000000000000000000000000000000000000000000000000000000\n" +
		"Recovery commit: 0123456789abcdef0123456789abcdef01234567\n"
	if !strings.HasPrefix(buffer.String(), expected) {
		t.Errorf("text header changed:\n%s", buffer.String())
	}
	for _, want := range []string{
		"\n02. [delegated] resume/verify-source-digests\n",
		"    depends on: classify-interrupted-steps\n",
		"    argv: \"restic\" \"snapshots\" \"--json\"\n",
		"\n04. [operator-only] resume/discard-partial-replicas mutating DESTRUCTIVE\n",
		"    operator confirmation: Discard only the listed partially written replicas; this is irreversible.\n",
		"    acknowledgement text: I confirm the listed partial replicas and understand the irreversible data-loss boundary.\n",
	} {
		if !strings.Contains(buffer.String(), want) {
			t.Errorf("text render lost %q", want)
		}
	}
}

// TestRenderersAreDeterministic guards the golden file's premise: rendering
// the same plan twice has to produce the same bytes, or the golden is noise.
func TestRenderersAreDeterministic(t *testing.T) {
	p := loadSyntheticPlan(t)
	for name, render := range map[string]func(*bytes.Buffer) error{
		"text":     func(b *bytes.Buffer) error { return Text(b, p) },
		"runsheet": func(b *bytes.Buffer) error { return Runsheet(b, p, Meta{CLIVersion: "v9.9.9"}) },
	} {
		var first, second bytes.Buffer
		if err := render(&first); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := render(&second); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if first.String() != second.String() {
			t.Errorf("%s render is not deterministic", name)
		}
	}
}

// TestRunsheetKeepsAnUnknownPhase makes sure no step can vanish from a sheet.
// A run sheet that silently drops a step is worse than one showing an odd
// phase name, so the renderer prints what it was handed.
func TestRunsheetKeepsAnUnknownPhase(t *testing.T) {
	p := loadSyntheticPlan(t)
	p.Steps = append(copySteps(p.Steps), plan.Step{
		ID:             "stray-step",
		Phase:          plan.Phase("not-a-phase"),
		Kind:           plan.OperatorOnly,
		Instruction:    "This step names a phase the renderer does not know.",
		Preconditions:  []string{"none"},
		StopConditions: []string{"none"},
		Observations:   []string{"none"},
	})
	var buffer bytes.Buffer
	if err := Runsheet(&buffer, p, Meta{CLIVersion: "dev"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buffer.String(), "## Phase: not-a-phase") {
		t.Error("an unrecognized phase was dropped from the sheet")
	}
	if !strings.Contains(buffer.String(), "07 · stray-step") {
		t.Error("a step in an unrecognized phase was dropped from the sheet")
	}
}

func copySteps(steps []plan.Step) []plan.Step {
	out := make([]plan.Step, len(steps))
	copy(out, steps)
	return out
}

// stepBlock returns the run sheet text belonging to one step: from its heading
// to the horizontal rule that closes it.
func stepBlock(t *testing.T, rendered, id string) string {
	t.Helper()
	start := strings.Index(rendered, " · "+id+"\n")
	if start < 0 {
		t.Fatalf("step %q has no block in the run sheet", id)
	}
	rest := rendered[start:]
	if end := strings.Index(rest, "\n---\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func actionIndex(t *testing.T, block string, step plan.Step) int {
	t.Helper()
	marker := "**" + instructionHeading(step) + "**"
	if len(step.Argv) > 0 {
		marker = "**Run this — argv, no shell"
	}
	index := strings.Index(block, marker)
	if index < 0 {
		t.Fatalf("step %q: no action block (looked for %q)", step.ID, marker)
	}
	return index
}

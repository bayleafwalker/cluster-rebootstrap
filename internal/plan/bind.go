package plan

import (
	"errors"
	"fmt"
	"strings"
)

// Bind rebinds a plan to the anchors that exist at execution-planning time and
// re-derives the plan digest over the result.
//
// A plan is digest-bound: Validate rejects a plan whose planDigest does not
// match the canonical form, so the anchors (profileDigest, recoveryCommit,
// checkpointDigest) cannot be filled in by hand after authoring without also
// recomputing the digest by hand. Bind is the one supported way to do it: it
// takes an authored plan whose anchors are placeholders, substitutes the real
// ones, and returns a plan that passes Validate.
//
// checkpointDigest may be empty. CheckpointDigest is omitempty and Validate
// only format-checks it when present, which is what lets a plan be frozen
// before the quiesced window has produced a checkpoint.
//
// Bind is deliberately not a plan editor. It copies the steps verbatim — same
// order, same confirmation requirements — and refuses to bind a plan carrying a
// destructive step without an operator confirmation requirement, so rebinding
// cannot launder an unsafe plan into an executable one.
//
// The argument is left untouched: the returned plan owns its own steps.
func Bind(p Plan, profileDigest, recoveryCommit, checkpointDigest string) (Plan, error) {
	if !sha256Pattern.MatchString(profileDigest) {
		return Plan{}, errors.New("bind: profileDigest must be a lowercase sha256 digest")
	}
	if !commitPattern.MatchString(recoveryCommit) {
		return Plan{}, errors.New("bind: recoveryCommit must be a lowercase immutable commit SHA")
	}
	if checkpointDigest != "" && !sha256Pattern.MatchString(checkpointDigest) {
		return Plan{}, errors.New("bind: checkpointDigest must be a lowercase sha256 digest when supplied")
	}
	for _, step := range p.Steps {
		if !step.Destructive {
			continue
		}
		if step.Confirmation == nil || !step.Confirmation.Required ||
			strings.TrimSpace(step.Confirmation.Prompt) == "" ||
			strings.TrimSpace(step.Confirmation.AcknowledgeWith) == "" {
			return Plan{}, fmt.Errorf("bind: destructive step %q lacks an operator confirmation requirement", step.ID)
		}
	}

	bound := p
	bound.Steps = copySteps(p.Steps)
	bound.ProfileDigest = profileDigest
	bound.RecoveryCommit = recoveryCommit
	bound.CheckpointDigest = checkpointDigest

	digest, err := bound.CanonicalDigest()
	if err != nil {
		return Plan{}, fmt.Errorf("bind: %w", err)
	}
	bound.PlanDigest = digest

	if err := bound.Validate(); err != nil {
		return Plan{}, fmt.Errorf("bind: %w", err)
	}
	return bound, nil
}

// copySteps deep-copies steps so the bound plan shares no state with its
// argument. Nil and empty slices are kept distinct: encoding/json renders them
// as null and [], and the plan digest is taken over that encoding.
func copySteps(steps []Step) []Step {
	if steps == nil {
		return nil
	}
	out := make([]Step, len(steps))
	for index, step := range steps {
		copied := step
		copied.Argv = copyStrings(step.Argv)
		copied.Preconditions = copyStrings(step.Preconditions)
		copied.StopConditions = copyStrings(step.StopConditions)
		copied.Observations = copyStrings(step.Observations)
		copied.DependsOn = copyStrings(step.DependsOn)
		if step.Confirmation != nil {
			confirmation := *step.Confirmation
			copied.Confirmation = &confirmation
		}
		out[index] = copied
	}
	return out
}

func copyStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

// Package render turns a typed execution plan into text an operator reads.
//
// Rendering is pure: it writes bytes derived from the plan and nothing else.
// It never executes a step's argv, never opens a network connection, and
// never touches a cluster. The CLI deliberately does not execute plan argv
// values, and this package is where that promise would be easiest to break,
// so it holds no exec, no os, and no net import at all.
//
// Two formats exist and they serve different readers:
//
//   - Text is the original flat listing. It is a digest check for a reviewer
//     who already knows the plan. Its bytes are frozen: anything downstream
//     that parses it keeps working.
//
//   - Runsheet is a document for one specific reader — an operator working an
//     outage at three in the morning, on the worst day this tool will ever be
//     used. Everything about its shape follows from that: the reader is tired,
//     reading top to bottom, and about to type a command that may be
//     irreversible. So every STOP condition and every confirmation prompt is
//     printed ABOVE the command it guards, never below it, because a warning
//     under a command is read after the command has already been run.
package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bayleafwalker/cluster-rebootstrap/internal/plan"
)

// Meta carries the identity of the tool that produced a run sheet, so a
// printed sheet can be traced back to the exact binary that rendered it.
type Meta struct {
	// CLIVersion is the semantic version of the rendering binary. An empty
	// value renders as "unknown" rather than as a blank the reader might
	// mistake for a real version.
	CLIVersion string
}

// Text writes the flat listing. Its output is byte-for-byte the original
// render, and is kept that way on purpose.
func Text(w io.Writer, p plan.Plan) error {
	out := &writer{w: w}
	out.printf("PLAN %s\nDigest: %s\nProfile: %s\nRecovery commit: %s\n", p.ID, p.PlanDigest, p.ProfileDigest, p.RecoveryCommit)
	for index, step := range p.Steps {
		out.printf("\n%02d. [%s] %s/%s", index+1, step.Kind, step.Phase, step.ID)
		if step.Mutating {
			out.print(" mutating")
		}
		if step.Destructive {
			out.print(" DESTRUCTIVE")
		}
		out.print("\n")
		if len(step.DependsOn) > 0 {
			out.printf("    depends on: %s\n", strings.Join(step.DependsOn, ", "))
		}
		if len(step.Argv) > 0 {
			out.printf("    argv: %s\n", quoteArgv(step.Argv))
		} else {
			out.printf("    instruction: %s\n", step.Instruction)
		}
		out.printf("    preconditions: %s\n    STOP: %s\n    observe: %s\n", strings.Join(step.Preconditions, " | "), strings.Join(step.StopConditions, " | "), strings.Join(step.Observations, " | "))
		if step.Confirmation != nil {
			out.printf("    operator confirmation: %s\n", step.Confirmation.Prompt)
			out.printf("    acknowledgement text: %s\n", step.Confirmation.AcknowledgeWith)
		}
	}
	return out.err
}

// Runsheet writes the outage run sheet: one Markdown document, worked top to
// bottom, grouped by phase.
func Runsheet(w io.Writer, p plan.Plan, meta Meta) error {
	out := &writer{w: w}
	writeHeader(out, p, meta)
	writeOperatorIndex(out, p)
	writePhases(out, p)
	writeCloseOut(out, p)
	return out.err
}

func writeHeader(out *writer, p plan.Plan, meta Meta) {
	out.printf("# Outage run sheet — %s\n\n", p.ID)
	out.print("Work this sheet from top to bottom. For every step the STOP conditions and\n")
	out.print("the operator confirmation are printed **above** the command they guard: if a\n")
	out.print("STOP condition holds, stop there and do not run the command below it.\n\n")
	out.print("Nothing in this document runs itself. Rendering a plan never executes it.\n\n")

	out.print("| Field | Value |\n")
	out.print("| --- | --- |\n")
	out.printf("| Plan ID | `%s` |\n", p.ID)
	out.printf("| Phase | `%s` |\n", p.Phase)
	out.printf("| Plan digest | `%s` |\n", p.PlanDigest)
	out.printf("| Profile digest | `%s` |\n", p.ProfileDigest)
	out.printf("| Recovery commit | `%s` |\n", p.RecoveryCommit)
	out.printf("| Checkpoint digest | %s |\n", optionalCode(p.CheckpointDigest))
	out.printf("| Rendered by | `rebootstrap %s` |\n", versionOf(meta))
	out.printf("| Steps | %s |\n\n", stepCensus(p))
}

// writeOperatorIndex prints, before any step, every place the operator is
// personally needed. Learning at step 4 of 6 that step 5 cannot proceed
// without you is how an outage stalls; this is the whole point of the index.
func writeOperatorIndex(out *writer, p plan.Plan) {
	out.print("## Where you are personally needed\n\n")
	needed := operatorSteps(p)
	if len(needed) == 0 {
		out.print("No step in this plan requires the operator by hand or by\n")
		out.print("acknowledgement. Read the STOP conditions anyway: any one of them\n")
		out.print("firing hands the plan back to you.\n\n")
		return
	}
	out.printf("%s below cannot proceed without you. Read this list before starting\n", countOf(len(needed), "step", "steps"))
	out.print("step 1, so no hand-off arrives as a surprise mid-outage.\n\n")
	out.print("| Step | ID | Why you | Destructive |\n")
	out.print("| --- | --- | --- | --- |\n")
	for _, item := range needed {
		out.printf("| %02d | `%s` | %s | %s |\n", item.number, item.step.ID, whyOperator(item.step), destructiveCell(item.step))
	}
	out.print("\n")
}

func writePhases(out *writer, p plan.Plan) {
	for _, phase := range orderedPhases(p) {
		out.printf("## Phase: %s\n\n", phase)
		for _, item := range stepsInPhase(p, phase) {
			writeStep(out, item)
		}
	}
}

func writeStep(out *writer, item numberedStep) {
	step := item.step
	out.printf("### %02d · %s\n\n", item.number, step.ID)
	out.printf("`%s` · %s\n\n", step.Kind, mutationWords(step))

	if step.Destructive {
		out.print("> **!! DESTRUCTIVE STEP !!**\n")
		out.print(">\n")
		out.print("> **This step destroys data and cannot be undone.** Do not run it until\n")
		out.print("> every STOP condition below is false, the preconditions are all checked\n")
		out.print("> off, and the acknowledgement is recorded word for word. If you are\n")
		out.print("> unsure about any of those, stop and escalate instead.\n\n")
	}

	out.print("**STOP — do not proceed if any of these is true:**\n\n")
	writeChecklist(out, step.StopConditions)

	out.print("**Preconditions — all must hold first:**\n\n")
	writeChecklist(out, step.Preconditions)

	out.printf("Depends on: %s\n\n", dependencyList(step))

	if step.Confirmation != nil {
		writeConfirmation(out, step)
	}

	writeAction(out, step)

	out.print("**Observe and record:**\n\n")
	writeChecklist(out, step.Observations)

	out.print("Outcome: `[ ] done`  `[ ] not-started`  `[ ] STOPPED — escalate`\n\n")
	out.print("Notes: ______________________________________________________________\n\n")
	out.print("---\n\n")
}

// writeConfirmation prints the operator prompt and the exact acknowledgement
// text ahead of the command. The acknowledgement is quoted verbatim because a
// confirmation record is only valid when it matches this string exactly.
func writeConfirmation(out *writer, step plan.Step) {
	out.print("**Operator confirmation — REQUIRED before the action below:**\n\n")
	out.printf("- Prompt: %s\n", step.Confirmation.Prompt)
	out.print("- [ ] Acknowledged, recorded word for word as:\n\n")
	out.printf("      %s\n\n", step.Confirmation.AcknowledgeWith)
}

func writeAction(out *writer, step plan.Step) {
	if len(step.Argv) == 0 {
		out.printf("**%s**\n\n", instructionHeading(step))
		out.printf("> %s\n\n", step.Instruction)
		return
	}
	out.printf("**Run this — argv, no shell%s:**\n\n", adapterNote(step))
	out.print("```\n")
	out.printf("%s\n", quoteArgv(step.Argv))
	out.print("```\n\n")
	out.print("Each quoted token above is one argument. There is no shell here: no\n")
	out.print("globbing, no pipes, no variable expansion. Type the tokens as written.\n\n")
}

func writeCloseOut(out *writer, p plan.Plan) {
	out.print("## Closing out\n\n")
	out.print("- [ ] Every step above is marked done, not-started, or STOPPED.\n")
	out.print("- [ ] Every STOP condition that fired is written down, with what was done about it.\n")
	out.print("- [ ] Every acknowledgement recorded matches its required text word for word.\n")
	out.print("- [ ] Observations are transcribed into the run's evidence, not left on paper.\n\n")
	out.print("This sheet was rendered from plan digest\n\n")
	out.printf("    %s\n\n", p.PlanDigest)
	out.print("If the plan is rebound or re-authored that digest changes, and this sheet is\n")
	out.print("stale. Render a new one rather than working from this copy.\n")
}

func writeChecklist(out *writer, items []string) {
	if len(items) == 0 {
		out.print("- [ ] (none recorded)\n\n")
		return
	}
	for _, item := range items {
		out.printf("- [ ] %s\n", item)
	}
	out.print("\n")
}

type numberedStep struct {
	number int
	step   plan.Step
}

// operatorSteps returns every step that needs the operator in person: an
// operator-only step, or any step gated behind a confirmation the operator
// alone can give.
func operatorSteps(p plan.Plan) []numberedStep {
	var needed []numberedStep
	for index, step := range p.Steps {
		if step.Kind == plan.OperatorOnly || step.Confirmation != nil {
			needed = append(needed, numberedStep{number: index + 1, step: step})
		}
	}
	return needed
}

// orderedPhases lists the phases present in the plan, in canonical execution
// order. A validated plan is phase-homogeneous, so this is normally a single
// phase; the loop keeps the renderer honest about what it was handed rather
// than assuming the invariant it does not enforce.
func orderedPhases(p plan.Plan) []plan.Phase {
	present := make(map[plan.Phase]bool, len(p.Steps))
	for _, step := range p.Steps {
		present[step.Phase] = true
	}
	var ordered []plan.Phase
	for _, phase := range plan.Phases {
		if present[phase] {
			ordered = append(ordered, phase)
			delete(present, phase)
		}
	}
	// An unrecognized phase would otherwise vanish from the sheet. Print it
	// in step order instead: a run sheet that silently drops a step is worse
	// than one that shows an odd phase name.
	for _, step := range p.Steps {
		if present[step.Phase] {
			ordered = append(ordered, step.Phase)
			delete(present, step.Phase)
		}
	}
	return ordered
}

func stepsInPhase(p plan.Plan, phase plan.Phase) []numberedStep {
	var items []numberedStep
	for index, step := range p.Steps {
		if step.Phase == phase {
			items = append(items, numberedStep{number: index + 1, step: step})
		}
	}
	return items
}

func stepCensus(p plan.Plan) string {
	destructive, operator := 0, 0
	for _, step := range p.Steps {
		if step.Destructive {
			destructive++
		}
		if step.Kind == plan.OperatorOnly {
			operator++
		}
	}
	return fmt.Sprintf("%d total · %d destructive · %d operator-only", len(p.Steps), destructive, operator)
}

func whyOperator(step plan.Step) string {
	switch {
	case step.Kind == plan.OperatorOnly && step.Confirmation != nil:
		return "operator-only, and confirmation required"
	case step.Kind == plan.OperatorOnly:
		return "operator-only"
	default:
		return "confirmation required"
	}
}

func destructiveCell(step plan.Step) string {
	if step.Destructive {
		return "**YES**"
	}
	return "no"
}

func mutationWords(step plan.Step) string {
	switch {
	case step.Destructive:
		return "mutating, **DESTRUCTIVE**"
	case step.Mutating:
		return "mutating"
	default:
		return "read-only"
	}
}

func dependencyList(step plan.Step) string {
	if len(step.DependsOn) == 0 {
		return "nothing — this step can start once its preconditions hold"
	}
	quoted := make([]string, len(step.DependsOn))
	for index, dependency := range step.DependsOn {
		quoted[index] = "`" + dependency + "`"
	}
	return strings.Join(quoted, ", ")
}

// instructionHeading names who acts on a step that carries no argv, so the
// sheet never tells an operator to do by hand something a tool does, or leaves
// them assuming a step is automatic when it is theirs.
func instructionHeading(step plan.Step) string {
	switch step.Kind {
	case plan.OperatorOnly:
		return "Do this by hand — this step is yours:"
	case plan.AgentAssisted:
		return "Agent-assisted — an agent proposes, you approve each effect:"
	case plan.Automatic:
		return "Automatic — no operator action; confirm it happened:"
	default:
		return "Do this:"
	}
}

func adapterNote(step plan.Step) string {
	if step.Adapter == "" {
		return ""
	}
	return fmt.Sprintf(" (adapter `%s`)", step.Adapter)
}

func quoteArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for index, arg := range argv {
		quoted[index] = strconv.Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func optionalCode(value string) string {
	if value == "" {
		return "*(not bound yet)*"
	}
	return "`" + value + "`"
}

func versionOf(meta Meta) string {
	if strings.TrimSpace(meta.CLIVersion) == "" {
		return "unknown"
	}
	return meta.CLIVersion
}

func countOf(count int, singular, plural string) string {
	if count == 1 {
		return "The 1 " + singular
	}
	return fmt.Sprintf("The %d %s", count, plural)
}

// writer keeps the first write error and stops caring after that, so the
// render functions read as a straight document instead of an error ladder.
type writer struct {
	w   io.Writer
	err error
}

func (x *writer) print(text string) {
	if x.err != nil {
		return
	}
	_, x.err = io.WriteString(x.w, text)
}

func (x *writer) printf(format string, args ...any) {
	if x.err != nil {
		return
	}
	_, x.err = fmt.Fprintf(x.w, format, args...)
}

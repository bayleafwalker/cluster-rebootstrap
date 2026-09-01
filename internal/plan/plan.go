package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

type StepKind string

const (
	Automatic     StepKind = "automatic"
	Delegated     StepKind = "delegated"
	AgentAssisted StepKind = "agent-assisted"
	OperatorOnly  StepKind = "operator-only"
)

var (
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,95}$`)
	sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type ConfirmationRequirement struct {
	Required        bool   `json:"required"`
	Prompt          string `json:"prompt"`
	AcknowledgeWith string `json:"acknowledgeWith"`
}

type Step struct {
	ID             string                   `json:"id"`
	Phase          string                   `json:"phase"`
	Kind           StepKind                 `json:"kind"`
	Argv           []string                 `json:"argv,omitempty"`
	Instruction    string                   `json:"instruction,omitempty"`
	Mutating       bool                     `json:"mutating"`
	Destructive    bool                     `json:"destructive"`
	Preconditions  []string                 `json:"preconditions"`
	StopConditions []string                 `json:"stopConditions"`
	Observations   []string                 `json:"observations"`
	DependsOn      []string                 `json:"dependsOn"`
	Confirmation   *ConfirmationRequirement `json:"confirmation,omitempty"`
}

type Plan struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ID             string `json:"id"`
	ProfileDigest  string `json:"profileDigest"`
	RecoveryCommit string `json:"recoveryCommit"`
	PlanDigest     string `json:"planDigest"`
	Steps          []Step `json:"steps"`
}

type OperatorConfirmation struct {
	Operator     string          `json:"operator"`
	AuthorizedAt string          `json:"authorizedAt"`
	PlanDigest   string          `json:"planDigest"`
	Acknowledged map[string]bool `json:"acknowledged"`
}

// Decode reads a plan while rejecting fields that would otherwise be silently dropped.
func Decode(r io.Reader) (Plan, error) {
	var p Plan
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("decode plan: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return p, errors.New("decode plan: multiple JSON values")
		}
		return p, fmt.Errorf("decode plan: trailing data: %w", err)
	}
	return p, nil
}

func Encode(w io.Writer, p Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(p)
}

// CanonicalDigest hashes the typed JSON representation with the digest field blank.
// Struct field order is fixed and encoding/json emits deterministic object keys.
func (p Plan) CanonicalDigest() (string, error) {
	copy := p
	copy.PlanDigest = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("canonical plan: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (p Plan) ValidatedDigest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return p.CanonicalDigest()
}

func (p Plan) Validate() error {
	if p.SchemaVersion != 1 {
		return errors.New("schemaVersion must be 1")
	}
	if !idPattern.MatchString(p.ID) {
		return errors.New("id must be a safe non-empty identifier")
	}
	if !sha256Pattern.MatchString(p.ProfileDigest) {
		return errors.New("profileDigest must be a lowercase sha256 digest")
	}
	if !commitPattern.MatchString(p.RecoveryCommit) {
		return errors.New("recoveryCommit must be a lowercase immutable commit SHA")
	}
	if !sha256Pattern.MatchString(p.PlanDigest) {
		return errors.New("planDigest must be a lowercase sha256 digest")
	}
	digest, err := p.CanonicalDigest()
	if err != nil {
		return err
	}
	if p.PlanDigest != digest {
		return fmt.Errorf("planDigest does not match canonical plan: expected %s", digest)
	}
	if len(p.Steps) == 0 {
		return errors.New("steps must not be empty")
	}
	steps := make(map[string]Step, len(p.Steps))
	for _, step := range p.Steps {
		if err := validateStep(step); err != nil {
			return fmt.Errorf("step %q: %w", step.ID, err)
		}
		if _, exists := steps[step.ID]; exists {
			return fmt.Errorf("step %q: duplicate id", step.ID)
		}
		steps[step.ID] = step
	}
	for _, step := range p.Steps {
		for _, dependency := range step.DependsOn {
			if _, exists := steps[dependency]; !exists {
				return fmt.Errorf("step %q depends on unknown step %q", step.ID, dependency)
			}
		}
	}
	if cycle := findCycle(p.Steps); len(cycle) > 0 {
		return fmt.Errorf("dependency cycle: %s", strings.Join(cycle, " -> "))
	}
	return nil
}

func validateStep(step Step) error {
	if !idPattern.MatchString(step.ID) {
		return errors.New("id must be a safe non-empty identifier")
	}
	switch step.Phase {
	case "decommission", "bootstrap", "restore", "recommission":
	default:
		return errors.New("phase must be decommission, bootstrap, restore, or recommission")
	}
	switch step.Kind {
	case Automatic, Delegated, AgentAssisted, OperatorOnly:
	default:
		return fmt.Errorf("unknown kind %q", step.Kind)
	}
	if len(step.Argv) == 0 && strings.TrimSpace(step.Instruction) == "" {
		return errors.New("argv or instruction is required")
	}
	if len(step.Argv) > 0 && strings.TrimSpace(step.Instruction) != "" {
		return errors.New("argv and instruction are mutually exclusive")
	}
	if step.Kind == Delegated {
		if len(step.Argv) == 0 {
			return errors.New("delegated steps require argv")
		}
		if err := validateArgv(step.Argv); err != nil {
			return err
		}
	}
	if step.Destructive && !step.Mutating {
		return errors.New("destructive steps must be mutating")
	}
	if len(step.Preconditions) == 0 || len(step.StopConditions) == 0 || len(step.Observations) == 0 {
		return errors.New("preconditions, stopConditions, and observations are required")
	}
	if step.Destructive {
		if step.Confirmation == nil || !step.Confirmation.Required || strings.TrimSpace(step.Confirmation.Prompt) == "" || strings.TrimSpace(step.Confirmation.AcknowledgeWith) == "" {
			return errors.New("destructive steps require explicit operator confirmation data")
		}
	}
	for _, dependency := range step.DependsOn {
		if dependency == step.ID {
			return errors.New("step cannot depend on itself")
		}
	}
	return nil
}

func validateArgv(argv []string) error {
	if argv[0] == "sh" || argv[0] == "bash" || argv[0] == "zsh" || strings.HasSuffix(argv[0], "/sh") || strings.HasSuffix(argv[0], "/bash") {
		return errors.New("delegated argv cannot invoke a shell")
	}
	for _, arg := range argv {
		if strings.ContainsAny(arg, ";|&><$`()\n\r") {
			return errors.New("delegated argv contains shell syntax")
		}
	}
	return nil
}

func findCycle(steps []Step) []string {
	graph := make(map[string][]string, len(steps))
	for _, step := range steps {
		graph[step.ID] = append([]string(nil), step.DependsOn...)
	}
	state := make(map[string]uint8, len(steps))
	stack := make([]string, 0, len(steps))
	var visit func(string) []string
	visit = func(id string) []string {
		switch state[id] {
		case 1:
			for i, item := range stack {
				if item == id {
					return append(append([]string(nil), stack[i:]...), id)
				}
			}
			return []string{id, id}
		case 2:
			return nil
		}
		state[id] = 1
		stack = append(stack, id)
		for _, dependency := range graph[id] {
			if cycle := visit(dependency); len(cycle) > 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return nil
	}
	ids := make([]string, 0, len(graph))
	for id := range graph {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if cycle := visit(id); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

func (c OperatorConfirmation) Validate(p Plan) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	if strings.TrimSpace(c.Operator) == "" || strings.TrimSpace(c.AuthorizedAt) == "" {
		return errors.New("operator and authorizedAt are required")
	}
	if c.PlanDigest != p.PlanDigest {
		return errors.New("operator confirmation is bound to a different plan digest")
	}
	for _, step := range p.Steps {
		if step.Destructive && !c.Acknowledged[step.ID] {
			return fmt.Errorf("destructive step %q lacks operator acknowledgement", step.ID)
		}
	}
	return nil
}

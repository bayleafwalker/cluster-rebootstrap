package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = 1

type Profile struct {
	SchemaVersion       int             `json:"schemaVersion"`
	ID                  string          `json:"id"`
	MandatoryPredicates []PredicateSpec `json:"mandatoryPredicates"`
}

type PredicateSpec struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type Status string

const (
	Pass    Status = "PASS"
	Fail    Status = "FAIL"
	Blocked Status = "BLOCKED"
	Stale   Status = "STALE"
	NotRun  Status = "NOT_RUN"
)

func (s Status) Valid() bool {
	switch s {
	case Pass, Fail, Blocked, Stale, NotRun:
		return true
	default:
		return false
	}
}

type PredicateObservation struct {
	Status   Status   `json:"status"`
	Evidence []string `json:"evidence"`
	Note     string   `json:"note,omitempty"`
}

var (
	sensitiveEvidence = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer|(?:password|passwd|token|secret|api[_-]?key)\s*[:=]|-----begin [^-]*private key-----|age-secret-key-|\b(?:gh[pousr]|github_pat)_[A-Za-z0-9_\-]+)`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ValidateSafeEvidence is the persistence boundary for operator evidence.
// Evidence is intentionally short, single-line, and descriptive; raw command
// output and credential-shaped values must never enter the journal.
func ValidateSafeEvidence(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("evidence must not be empty")
	}
	if len(value) > 2048 {
		return fmt.Errorf("evidence exceeds 2048 bytes")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("evidence must be a single line without control characters")
	}
	if sensitiveEvidence.MatchString(value) {
		return fmt.Errorf("evidence resembles credential material or raw secret output")
	}
	return nil
}

type GateInput struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Predicates    map[string]PredicateObservation `json:"predicates"`
}

type OperatorAuthorization struct {
	Identity                    string `json:"identity"`
	AuthorizedAt                string `json:"authorizedAt"`
	PointOfNoReturnAcknowledged bool   `json:"pointOfNoReturnAcknowledged"`
	RunID                       string `json:"runID"`
	ProfileDigest               string `json:"profileDigest"`
	RecoveryCommit              string `json:"recoveryCommit"`
	CheckpointDigest            string `json:"checkpointDigest"`
	EvidenceDigest              string `json:"evidenceDigest"`
	PlanDigest                  string `json:"planDigest"`
}

type Run struct {
	SchemaVersion    int    `json:"schemaVersion"`
	RunID            string `json:"runID"`
	ProfileID        string `json:"profileID"`
	ProfileDigest    string `json:"profileDigest"`
	RecoveryCommit   string `json:"recoveryCommit"`
	CheckpointDigest string `json:"checkpointDigest"`
	PlanDigest       string `json:"planDigest"`
	CreatedAt        string `json:"createdAt"`
}

type GateReport struct {
	SchemaVersion         int                             `json:"schemaVersion"`
	Kind                  string                          `json:"kind"`
	RunID                 string                          `json:"runID"`
	ProfileID             string                          `json:"profileID"`
	ProfileDigest         string                          `json:"profileDigest"`
	RecoveryCommit        string                          `json:"recoveryCommit"`
	CheckpointDigest      string                          `json:"checkpointDigest"`
	PlanDigest            string                          `json:"planDigest"`
	EvidenceDigest        string                          `json:"evidenceDigest"`
	Predicates            map[string]PredicateObservation `json:"predicates"`
	AllMandatoryPass      bool                            `json:"allMandatoryPass"`
	Eligible              bool                            `json:"eligible"`
	Decision              string                          `json:"decision"`
	OperatorAuthorization *OperatorAuthorization          `json:"operatorAuthorization"`
}

type Event struct {
	SchemaVersion int             `json:"schemaVersion"`
	Sequence      uint64          `json:"sequence"`
	RunID         string          `json:"runID"`
	Type          string          `json:"type"`
	OccurredAt    string          `json:"occurredAt"`
	Data          json.RawMessage `json:"data"`
}

type StatusProjection struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runID"`
	ProfileID     string `json:"profileID"`
	ProfileDigest string `json:"profileDigest"`
	LastSequence  uint64 `json:"lastSequence"`
	LastEventType string `json:"lastEventType"`
	EventCount    uint64 `json:"eventCount"`
	GateDecision  string `json:"gateDecision,omitempty"`
	GateEligible  bool   `json:"gateEligible"`
}

func ValidateProfile(p Profile) error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported profile schemaVersion %d", p.SchemaVersion)
	}
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("profile id is required")
	}
	if len(p.MandatoryPredicates) == 0 {
		return fmt.Errorf("profile must define at least one mandatory predicate")
	}
	seen := make(map[string]struct{}, len(p.MandatoryPredicates))
	for _, predicate := range p.MandatoryPredicates {
		if strings.TrimSpace(predicate.ID) == "" || strings.TrimSpace(predicate.Description) == "" {
			return fmt.Errorf("mandatory predicates require id and description")
		}
		if _, exists := seen[predicate.ID]; exists {
			return fmt.Errorf("duplicate mandatory predicate %q", predicate.ID)
		}
		seen[predicate.ID] = struct{}{}
	}
	return nil
}

func ValidateGateInput(input GateInput, profile Profile) error {
	if input.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported gate input schemaVersion %d", input.SchemaVersion)
	}
	if input.Predicates == nil {
		return fmt.Errorf("gate input predicates are required")
	}
	expected := make(map[string]struct{}, len(profile.MandatoryPredicates))
	for _, predicate := range profile.MandatoryPredicates {
		expected[predicate.ID] = struct{}{}
		observation, ok := input.Predicates[predicate.ID]
		if !ok {
			return fmt.Errorf("missing observation for mandatory predicate %q", predicate.ID)
		}
		if !observation.Status.Valid() {
			return fmt.Errorf("predicate %q has invalid status %q", predicate.ID, observation.Status)
		}
		if len(observation.Evidence) == 0 {
			return fmt.Errorf("predicate %q requires evidence", predicate.ID)
		}
		for _, evidence := range observation.Evidence {
			if err := ValidateSafeEvidence(evidence); err != nil {
				return fmt.Errorf("predicate %q: %w", predicate.ID, err)
			}
		}
		if observation.Note != "" {
			if err := ValidateSafeEvidence(observation.Note); err != nil {
				return fmt.Errorf("predicate %q note: %w", predicate.ID, err)
			}
		}
	}
	for id := range input.Predicates {
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("gate input contains unknown predicate %q", id)
		}
	}
	return nil
}

func ValidateAuthorization(auth OperatorAuthorization, run Run, evidenceDigest string) error {
	if strings.TrimSpace(auth.Identity) == "" {
		return fmt.Errorf("operator authorization identity is required")
	}
	if _, err := time.Parse(time.RFC3339, auth.AuthorizedAt); err != nil {
		return fmt.Errorf("operator authorization authorizedAt must be RFC3339: %w", err)
	}
	if !auth.PointOfNoReturnAcknowledged {
		return fmt.Errorf("operator authorization must acknowledge the point of no return")
	}
	if strings.ContainsAny(auth.Identity, "\x00\r\n") || sensitiveEvidence.MatchString(auth.Identity) {
		return fmt.Errorf("operator identity is not safe evidence")
	}
	if auth.RunID != run.RunID || auth.ProfileDigest != run.ProfileDigest || auth.RecoveryCommit != run.RecoveryCommit || auth.CheckpointDigest != run.CheckpointDigest || auth.PlanDigest != run.PlanDigest || auth.EvidenceDigest != evidenceDigest {
		return fmt.Errorf("operator authorization is bound to a different run, checkpoint, evidence, or plan")
	}
	return nil
}

func ValidateRun(run Run) error {
	if run.SchemaVersion != SchemaVersion || strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(run.ProfileID) == "" {
		return fmt.Errorf("run identity is required")
	}
	if !digestPattern.MatchString(run.ProfileDigest) {
		return fmt.Errorf("run profileDigest must be a sha256 digest")
	}
	if !commitPattern.MatchString(run.RecoveryCommit) {
		return fmt.Errorf("run recoveryCommit must be a lowercase immutable commit SHA")
	}
	if !digestPattern.MatchString(run.CheckpointDigest) || !digestPattern.MatchString(run.PlanDigest) {
		return fmt.Errorf("run checkpointDigest and planDigest must be sha256 digests")
	}
	if _, err := time.Parse(time.RFC3339, run.CreatedAt); err != nil {
		return fmt.Errorf("run createdAt must be RFC3339: %w", err)
	}
	return nil
}

func EvidenceDigest(input GateInput) (string, error) {
	return Digest(input)
}

func ValidateGateReport(report GateReport, run Run) error {
	if report.SchemaVersion != SchemaVersion || report.Kind != "gate-report" || report.RunID != run.RunID || report.ProfileID != run.ProfileID || report.ProfileDigest != run.ProfileDigest || report.RecoveryCommit != run.RecoveryCommit || report.CheckpointDigest != run.CheckpointDigest || report.PlanDigest != run.PlanDigest {
		return fmt.Errorf("gate report does not match run binding")
	}
	input := GateInput{SchemaVersion: SchemaVersion, Predicates: report.Predicates}
	digest, err := EvidenceDigest(input)
	if err != nil {
		return err
	}
	if report.EvidenceDigest != digest {
		return fmt.Errorf("gate report evidence digest mismatch")
	}
	for id, observation := range report.Predicates {
		if !observation.Status.Valid() || len(observation.Evidence) == 0 {
			return fmt.Errorf("predicate %q has invalid evidence", id)
		}
		for _, evidence := range observation.Evidence {
			if err := ValidateSafeEvidence(evidence); err != nil {
				return fmt.Errorf("predicate %q: %w", id, err)
			}
		}
		if observation.Note != "" {
			if err := ValidateSafeEvidence(observation.Note); err != nil {
				return fmt.Errorf("predicate %q note: %w", id, err)
			}
		}
	}
	if report.Decision != "GO" && report.Decision != "NO-GO" {
		return fmt.Errorf("gate report has invalid decision")
	}
	if report.OperatorAuthorization != nil {
		if err := ValidateAuthorization(*report.OperatorAuthorization, run, report.EvidenceDigest); err != nil {
			return fmt.Errorf("invalid operator authorization: %w", err)
		}
	}
	if report.Decision == "GO" {
		if !report.AllMandatoryPass || !report.Eligible || report.OperatorAuthorization == nil {
			return fmt.Errorf("GO report lacks valid all-pass operator authorization")
		}
	}
	return nil
}

func ProfileDigest(p Profile) (string, error) {
	return Digest(p)
}

// CanonicalDigestBytes is the single canonical encoding this repository digests:
// encoding/json of the typed value, with no trailing newline. Struct field order
// is fixed by declaration and encoding/json emits map keys in sorted order, so
// the encoding is deterministic for a given typed value.
//
// Every digest in this repository — model.ProfileDigest, model.EvidenceDigest,
// and plan.Plan.CanonicalDigest — is sha256 over these bytes and no other rule.
func CanonicalDigestBytes(value any) ([]byte, error) {
	return json.Marshal(value)
}

// Digest is the one digest rule: sha256 over CanonicalDigestBytes, rendered as
// "sha256:" followed by lowercase hex.
func Digest(value any) (string, error) {
	canonical, err := CanonicalDigestBytes(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// CanonicalJSON is CanonicalDigestBytes plus a single trailing newline. It is
// the line format for the NDJSON event journal, the atomically replaced
// projections, and stdout receipts. It is never a digest input: the newline is
// a framing byte, not part of the canonical value.
func CanonicalJSON(value any) ([]byte, error) {
	encoded, err := CanonicalDigestBytes(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

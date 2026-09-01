package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

type GateInput struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Predicates    map[string]PredicateObservation `json:"predicates"`
}

type OperatorAuthorization struct {
	Identity                    string `json:"identity"`
	AuthorizedAt                string `json:"authorizedAt"`
	PointOfNoReturnAcknowledged bool   `json:"pointOfNoReturnAcknowledged"`
}

type Run struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runID"`
	ProfileID     string `json:"profileID"`
	ProfileDigest string `json:"profileDigest"`
	CreatedAt     string `json:"createdAt"`
}

type GateReport struct {
	SchemaVersion         int                             `json:"schemaVersion"`
	Kind                  string                          `json:"kind"`
	RunID                 string                          `json:"runID"`
	ProfileID             string                          `json:"profileID"`
	ProfileDigest         string                          `json:"profileDigest"`
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
			if strings.TrimSpace(evidence) == "" {
				return fmt.Errorf("predicate %q contains empty evidence", predicate.ID)
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

func ValidateAuthorization(auth OperatorAuthorization) error {
	if strings.TrimSpace(auth.Identity) == "" {
		return fmt.Errorf("operator authorization identity is required")
	}
	if _, err := time.Parse(time.RFC3339, auth.AuthorizedAt); err != nil {
		return fmt.Errorf("operator authorization authorizedAt must be RFC3339: %w", err)
	}
	if !auth.PointOfNoReturnAcknowledged {
		return fmt.Errorf("operator authorization must acknowledge the point of no return")
	}
	return nil
}

func ProfileDigest(p Profile) (string, error) {
	canonical, err := CanonicalJSON(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func CanonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

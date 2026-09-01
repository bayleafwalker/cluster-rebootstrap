package gate

import (
	"github.com/bayleafwalker/cluster-rebootstrap/internal/model"
)

// Evaluate computes eligibility from predicate observations. Authorization is
// intentionally a separate input: all-PASS observations never imply that an
// operator elected to cross the point of no return.
func Evaluate(run model.Run, profile model.Profile, input model.GateInput, authorization *model.OperatorAuthorization) model.GateReport {
	allPass := true
	for _, predicate := range profile.MandatoryPredicates {
		if input.Predicates[predicate.ID].Status != model.Pass {
			allPass = false
		}
	}
	evidenceDigest, _ := model.EvidenceDigest(input)
	authorized := authorization != nil && model.ValidateAuthorization(*authorization, run, evidenceDigest) == nil
	return model.GateReport{
		SchemaVersion:         model.SchemaVersion,
		Kind:                  "gate-report",
		RunID:                 run.RunID,
		ProfileID:             run.ProfileID,
		ProfileDigest:         run.ProfileDigest,
		RecoveryCommit:        run.RecoveryCommit,
		CheckpointDigest:      run.CheckpointDigest,
		PlanDigest:            run.PlanDigest,
		EvidenceDigest:        evidenceDigest,
		Predicates:            input.Predicates,
		AllMandatoryPass:      allPass,
		Eligible:              allPass,
		Decision:              decision(allPass && authorized),
		OperatorAuthorization: authorization,
	}
}

func decision(goEligible bool) string {
	if goEligible {
		return "GO"
	}
	return "NO-GO"
}

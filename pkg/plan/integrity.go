package plan

import "fmt"

// LoadVerifiedPlan loads a saved plan after checking it against the checksum
// recorded when it was saved. A plan that was edited afterwards, or has no
// checksum, is refused: plans carry commands that get executed.
// LOCAL-032
func (s *PlanStore) LoadVerifiedPlan(clusterName, planID string) (*Plan, error) {
	verified, err := s.VerifyPlan(clusterName, planID)
	if err != nil {
		return nil, fmt.Errorf("cannot verify plan %s: %w", planID, err)
	}
	if !verified {
		return nil, fmt.Errorf("plan %s does not match its recorded checksum (it was modified after it was saved, or the checksum file is missing); generate a new plan", planID)
	}
	return s.LoadPlan(clusterName, planID)
}

// StructuralProblems reports defects that make a plan unusable regardless of
// the state of the machine: no phases, no operations, untyped operations or
// duplicate operation IDs. It returns nil for a well-formed plan.
// LOCAL-033
func (p *Plan) StructuralProblems() []string {
	var problems []string
	if p.PlanID == "" {
		problems = append(problems, "plan has no ID")
	}
	if p.ClusterName == "" {
		problems = append(problems, "plan has no cluster name")
	}
	if len(p.Phases) == 0 {
		problems = append(problems, "plan has no phases")
	}

	// A phase may be empty (a standalone has nothing to initialize), but a
	// plan with no operations at all does nothing.
	if len(p.Phases) > 0 && p.TotalOperations() == 0 {
		problems = append(problems, "plan has no operations")
	}

	seen := make(map[string]string)
	for _, phase := range p.Phases {
		for _, op := range phase.Operations {
			if op.Type == "" {
				problems = append(problems, fmt.Sprintf("operation %q in phase %q has no type", op.ID, phase.Name))
			}
			if firstPhase, dup := seen[op.ID]; dup {
				problems = append(problems, fmt.Sprintf("operation ID %q is used in both phase %q and phase %q", op.ID, firstPhase, phase.Name))
				continue
			}
			seen[op.ID] = phase.Name
		}
	}
	return problems
}

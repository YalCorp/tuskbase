package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

type assertionSpan struct {
	Text    string
	Stance  int
	Subject map[string]struct{}
	Scope   map[string]struct{}
	Object  map[string]struct{}
}

func (s *Service) classifyProposal(ctx context.Context, workspaceID, proposal string, decision domain.Decision) DecisionProposalRelation {
	relation := classifyDecisionRelation(proposal, decision)
	if s.validator == nil {
		return relation
	}
	validated, err := s.validator.ValidateRelationship(ctx, ports.RelationshipValidationRequest{
		WorkspaceID: workspaceID,
		Proposal:    proposal,
		Decision:    decision,
		Relation:    relation.Type,
		Evidence:    relation.Evidence,
	})
	if err != nil {
		relation.Evidence = append(relation.Evidence, ports.RelationshipSignal{Name: "validator_unavailable", Detail: err.Error(), Weight: 0})
		return relation
	}
	if validated.Relation != "" {
		relation.Type = validated.Relation
	}
	if validated.Confidence > 0 {
		relation.Confidence = clamp(validated.Confidence)
	}
	if strings.TrimSpace(validated.Reason) != "" {
		relation.Reason = strings.TrimSpace(validated.Reason)
	}
	relation.Evidence = append(relation.Evidence, ports.RelationshipSignal{Name: "validator", Detail: "Relationship validator confirmed or adjusted the deterministic classification.", Weight: relation.Confidence})
	return relation
}

func classifyDecisionRelation(proposal string, decision domain.Decision) DecisionProposalRelation {
	proposalAssertions := assertionSpans(proposal)
	decisionAssertions := assertionSpans(decisionAssertionText(decision))
	best := relationMatch{}
	for _, p := range proposalAssertions {
		for _, d := range decisionAssertions {
			match := compareAssertions(p, d)
			if match.Score > best.Score {
				best = match
			}
		}
	}
	if best.Score == 0 {
		best = relationMatch{Score: tokenOverlap(proposal, decisionAssertionText(decision)), Subject: sharedSubject(importantSubjectTokens(proposal), importantSubjectTokens(decisionAssertionText(decision)))}
	}
	evidence := []ports.RelationshipSignal{
		{Name: "subject_overlap", Detail: fmt.Sprintf("Shared subject terms: %s", strings.Join(best.Subject, ", ")), Weight: best.Score},
	}
	if best.OpposingStance {
		evidence = append(evidence, ports.RelationshipSignal{Name: "opposing_stance", Detail: "The proposal and active decision use opposing action cues for the same subject.", Weight: 0.35})
	}
	if best.ScopeDiverges {
		evidence = append(evidence, ports.RelationshipSignal{Name: "scope_distinction", Detail: "Shared terms appear in different scopes or different selected mechanisms, reducing conflict confidence.", Weight: -0.3})
	}
	base := DecisionProposalRelation{DecisionID: decision.ID, Subject: strings.Join(best.Subject, ", "), Evidence: evidence}
	proposalLower := strings.ToLower(proposal)
	switch {
	case reconcileCue(proposalLower) && best.Score >= 0.25:
		base.Type = domain.RelationshipReconciles
		base.Confidence = clamp(0.55 + best.Score*0.35)
		base.Reason = fmt.Sprintf("Proposal appears to reconcile prior direction around %s.", subjectPhrase(best.Subject, decision.Title))
	case supersedeCue(proposalLower) && best.Score >= 0.35:
		base.Type = domain.RelationshipSupersedes
		base.Confidence = clamp(0.58 + best.Score*0.35)
		base.Reason = fmt.Sprintf("Proposal appears to replace or retire active decision %q.", decision.Title)
	case best.OpposingStance && best.Score >= 0.5 && !best.ScopeDiverges:
		base.Type = domain.RelationshipConflicts
		base.Confidence = clamp(0.68 + best.Score*0.27)
		base.Reason = fmt.Sprintf("Proposal takes the opposite stance from active decision %q for %s.", decision.Title, subjectPhrase(best.Subject, "the same subject"))
	case best.Score >= 0.78 && best.SameStance:
		base.Type = domain.RelationshipDuplicates
		base.Confidence = clamp(0.55 + best.Score*0.4)
		base.Reason = "Proposal substantially restates an active decision."
	case best.Score >= 0.35:
		base.Type = domain.RelationshipExtends
		base.Confidence = clamp(0.35 + best.Score*0.45)
		base.Reason = "Proposal shares subject matter with an active decision without a deterministic conflict."
	default:
		base.Type = domain.RelationshipFollows
		base.Confidence = 0.35
		base.Reason = "No deterministic conflict found against this active decision."
	}
	base.Confidence = float64(int(base.Confidence*100+0.5)) / 100
	return base
}

type relationMatch struct {
	Score          float64
	Subject        []string
	OpposingStance bool
	SameStance     bool
	ScopeDiverges  bool
}

func compareAssertions(a, b assertionSpan) relationMatch {
	score := setSimilarity(a.Subject, b.Subject)
	shared := sharedSubject(a.Subject, b.Subject)
	if score == 0 {
		return relationMatch{}
	}
	opposing := a.Stance != 0 && b.Stance != 0 && a.Stance != b.Stance
	same := a.Stance != 0 && b.Stance != 0 && a.Stance == b.Stance
	objectDiverges := opposing && len(a.Object) > 0 && len(b.Object) > 0 && setSimilarity(a.Object, b.Object) == 0
	scopeDiverges := len(shared) <= 1 && len(a.Scope) > 0 && len(b.Scope) > 0 && setSimilarity(a.Scope, b.Scope) < 0.25
	return relationMatch{
		Score:          score,
		Subject:        shared,
		OpposingStance: opposing,
		SameStance:     same,
		ScopeDiverges:  scopeDiverges || objectDiverges,
	}
}

func assertionSpans(text string) []assertionSpan {
	parts := splitAssertions(text)
	out := make([]assertionSpan, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, assertionSpan{Text: part, Stance: stanceForText(part), Subject: importantSubjectTokens(part), Scope: scopeTokens(part), Object: objectTokens(part)})
	}
	if len(out) == 0 {
		out = append(out, assertionSpan{Text: text, Stance: stanceForText(text), Subject: importantSubjectTokens(text), Scope: scopeTokens(text), Object: objectTokens(text)})
	}
	return out
}

func splitAssertions(text string) []string {
	text = strings.NewReplacer("\n", ".", ";", ".", "•", ".").Replace(text)
	return strings.Split(text, ".")
}

func decisionAssertionText(decision domain.Decision) string {
	return strings.Join([]string{decision.Title, decision.Outcome, decision.Rationale, claimsText(decision.Claims)}, ". ")
}

func stanceForText(text string) int {
	lower := " " + strings.ToLower(text) + " "
	negative := []string{" avoid ", " do not ", " don't ", " never ", " without ", " remove ", " reject ", " forbid ", " disable ", " deprecate ", " no longer "}
	for _, cue := range negative {
		if strings.Contains(lower, cue) {
			return -1
		}
	}
	positive := []string{" use ", " adopt ", " keep ", " require ", " choose ", " allow ", " enable ", " store ", " prefer "}
	for _, cue := range positive {
		if strings.Contains(lower, cue) {
			return 1
		}
	}
	return 0
}

func importantSubjectTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range wordRE.FindAllString(strings.ToLower(text), -1) {
		token = strings.Trim(token, "-_./")
		if len(token) < 3 || relationshipStopWords[token] {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func objectTokens(text string) map[string]struct{} {
	tokens := wordRE.FindAllString(strings.ToLower(text), -1)
	out := map[string]struct{}{}
	capture := false
	for _, token := range tokens {
		if token == "for" || token == "when" || token == "during" || token == "inside" || token == "within" {
			break
		}
		if token == "use" || token == "adopt" || token == "keep" || token == "require" || token == "choose" || token == "prefer" || token == "avoid" || token == "remove" || token == "replace" || token == "reject" {
			capture = true
			continue
		}
		if !capture {
			continue
		}
		if len(token) >= 3 && !relationshipStopWords[token] {
			out[token] = struct{}{}
		}
	}
	return out
}

func scopeTokens(text string) map[string]struct{} {
	tokens := wordRE.FindAllString(strings.ToLower(text), -1)
	out := map[string]struct{}{}
	capture := false
	for _, token := range tokens {
		if token == "for" || token == "when" || token == "during" || token == "inside" || token == "within" {
			capture = true
			continue
		}
		if !capture {
			continue
		}
		if len(token) >= 3 && !relationshipStopWords[token] {
			out[token] = struct{}{}
		}
	}
	return out
}

func setSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var shared int
	for token := range a {
		if _, ok := b[token]; ok {
			shared++
		}
	}
	denom := max(len(a), len(b))
	return float64(shared) / float64(denom)
}

func tokenOverlap(a, b string) float64 {
	return setSimilarity(importantSubjectTokens(a), importantSubjectTokens(b))
}

func sharedSubject(a, b map[string]struct{}) []string {
	out := make([]string, 0, min(len(a), len(b)))
	for token := range a {
		if _, ok := b[token]; ok {
			out = append(out, token)
		}
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func subjectPhrase(tokens []string, fallback string) string {
	if len(tokens) == 0 {
		return fallback
	}
	return strings.Join(tokens, ", ")
}

func supersedeCue(text string) bool {
	cues := []string{"replace", "supersede", "retire", "migrate away", "instead of", "no longer use", "move from"}
	for _, cue := range cues {
		if strings.Contains(text, cue) {
			return true
		}
	}
	return false
}

func reconcileCue(text string) bool {
	cues := []string{"reconcile", "resolve conflict", "settle", "harmonize", "combine prior"}
	for _, cue := range cues {
		if strings.Contains(text, cue) {
			return true
		}
	}
	return false
}

var relationshipStopWords = map[string]bool{
	"about": true, "active": true, "after": true, "before": true, "choose": true,
	"decision": true, "decisions": true, "during": true, "elsewhere": true, "first": true,
	"from": true, "have": true, "into": true, "keep": true, "must": true,
	"need": true, "prior": true, "proposal": true, "require": true, "should": true,
	"storage": true, "store": true, "stores": true, "that": true, "their": true,
	"this": true, "when": true, "with": true, "without": true, "use": true,
	"using": true, "adopt": true, "avoid": true, "never": true, "remove": true,
	"replace": true, "reject": true, "prefer": true, "allow": true, "enable": true,
}

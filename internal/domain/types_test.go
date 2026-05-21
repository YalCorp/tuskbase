package domain

import "testing"

func TestEnumParsing(t *testing.T) {
	if kind, err := ParseActorKind("agent"); err != nil || kind != ActorAgent {
		t.Fatalf("ParseActorKind() = %q, %v", kind, err)
	}
	if _, err := ParseActorKind("bot"); err == nil {
		t.Fatal("ParseActorKind accepted invalid value")
	}
	if rel, err := ParseRelationshipType("conflicts"); err != nil || rel != RelationshipConflicts {
		t.Fatalf("ParseRelationshipType() = %q, %v", rel, err)
	}
	if sev, err := ParseConflictSeverity("critical"); err != nil || sev != SeverityCritical {
		t.Fatalf("ParseConflictSeverity() = %q, %v", sev, err)
	}
}

func TestDecisionValidation(t *testing.T) {
	d := Decision{
		ID:          "d1",
		WorkspaceID: "w1",
		Actor:       Actor{Kind: ActorAgent, Name: "codex"},
		Type:        "architecture",
		Title:       "Use SQLite first",
		Outcome:     "Store local decisions in SQLite.",
		Confidence:  0.9,
		Status:      DecisionActive,
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
	d.Confidence = 1.2
	if err := d.Validate(); err == nil {
		t.Fatal("invalid confidence accepted")
	}
}

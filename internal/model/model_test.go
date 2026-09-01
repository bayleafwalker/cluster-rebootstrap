package model

import "testing"

func testProfile() Profile {
	return Profile{
		SchemaVersion: SchemaVersion,
		ID:            "test",
		MandatoryPredicates: []PredicateSpec{
			{ID: "G1", Description: "one"},
			{ID: "G2", Description: "two"},
		},
	}
}

func TestValidateGateInputRejectsUnknownAndMissingPredicates(t *testing.T) {
	profile := testProfile()
	input := GateInput{SchemaVersion: SchemaVersion, Predicates: map[string]PredicateObservation{
		"G1": {Status: Pass, Evidence: []string{"ok"}},
		"G3": {Status: Pass, Evidence: []string{"wrong"}},
	}}
	if err := ValidateGateInput(input, profile); err == nil {
		t.Fatal("expected missing/unknown predicate rejection")
	}
}

func TestProfileDigestIsCanonical(t *testing.T) {
	first, err := ProfileDigest(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProfileDigest(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digest changed for same profile: %s != %s", first, second)
	}
}

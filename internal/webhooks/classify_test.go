package webhooks

import "testing"

func TestLooksLikeDeploy(t *testing.T) {
	yes := []string{
		"deploy", "Deploy to prod", "deployment", "release", "Releases",
		"publish", "production", "CD", "ship", "Prod",
		"deploy-production", "cd / release", // long kw present
	}
	for _, n := range yes {
		if !looksLikeDeploy(n) {
			t.Errorf("looksLikeDeploy(%q) = false, want true", n)
		}
	}
	no := []string{
		"load-cd-tests",   // "cd" only as a substring
		"shipping-label",  // "ship" only as a substring
		"product-catalog", // "prod" only as a substring
		"unit tests", "lint", "codeql", "build", "", "scd",
	}
	for _, n := range no {
		if looksLikeDeploy(n) {
			t.Errorf("looksLikeDeploy(%q) = true, want false", n)
		}
	}
}

func TestIsNonOutcomeConclusion(t *testing.T) {
	for _, s := range []string{"cancelled", "canceled", "skipped", "Cancelled", " skipped "} {
		if !isNonOutcomeConclusion(s) {
			t.Errorf("isNonOutcomeConclusion(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"success", "failure", "", "timed_out", "neutral"} {
		if isNonOutcomeConclusion(s) {
			t.Errorf("isNonOutcomeConclusion(%q) = true, want false", s)
		}
	}
}

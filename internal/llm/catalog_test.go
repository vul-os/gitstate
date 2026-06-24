package llm

import (
	"math"
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// TestCatalogOnlyThreeProviders asserts the curated/offline catalog (gw=nil)
// returns models exclusively for anthropic, openai, and google — and that all
// three providers are present.
func TestCatalogOnlyThreeProviders(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Markup = 1.05

	models := Models(cfg, nil)
	if len(models) == 0 {
		t.Fatal("expected a non-empty catalog")
	}

	seen := map[string]bool{}
	for _, m := range models {
		switch m.Provider {
		case "anthropic", "openai", "google":
			seen[m.Provider] = true
		default:
			t.Errorf("catalog contained disallowed provider %q (model %q)", m.Provider, m.ID)
		}
	}
	for _, p := range []string{"anthropic", "openai", "google"} {
		if !seen[p] {
			t.Errorf("expected provider %q in catalog", p)
		}
	}
}

// TestCatalogAppliesMarkup asserts our prices = base × markup, the markup is
// reported on each entry, and the default markup is 1.05 when unset.
func TestCatalogAppliesMarkup(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Markup = 1.05

	for _, m := range Models(cfg, nil) {
		if m.Markup != 1.05 {
			t.Errorf("%s: markup = %v, want 1.05", m.ID, m.Markup)
		}
		wantIn := round4(m.InputUSDPerMTok * 1.05)
		wantOut := round4(m.OutputUSDPerMTok * 1.05)
		if math.Abs(m.OurInputUSDPerMTok-wantIn) > 1e-9 {
			t.Errorf("%s: our input = %v, want %v (base %v ×1.05)", m.ID, m.OurInputUSDPerMTok, wantIn, m.InputUSDPerMTok)
		}
		if math.Abs(m.OurOutputUSDPerMTok-wantOut) > 1e-9 {
			t.Errorf("%s: our output = %v, want %v (base %v ×1.05)", m.ID, m.OurOutputUSDPerMTok, wantOut, m.OutputUSDPerMTok)
		}
	}
}

// TestCatalogDefaultMarkup asserts a zero/unset markup falls back to 1.05.
func TestCatalogDefaultMarkup(t *testing.T) {
	cfg := &config.Config{} // Markup left 0
	models := Models(cfg, nil)
	if len(models) == 0 {
		t.Fatal("expected a non-empty catalog")
	}
	for _, m := range models {
		if m.Markup != 1.05 {
			t.Errorf("%s: default markup = %v, want 1.05", m.ID, m.Markup)
		}
	}
}

// TestCatalogCustomMarkup asserts a custom markup flows through to our prices.
func TestCatalogCustomMarkup(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Markup = 1.20

	for _, m := range Models(cfg, nil) {
		if m.Markup != 1.20 {
			t.Errorf("%s: markup = %v, want 1.20", m.ID, m.Markup)
		}
		if want := round4(m.InputUSDPerMTok * 1.20); math.Abs(m.OurInputUSDPerMTok-want) > 1e-9 {
			t.Errorf("%s: our input = %v, want %v", m.ID, m.OurInputUSDPerMTok, want)
		}
	}
}

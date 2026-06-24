/**
 * Curated fallback model catalog for the public /models page.
 *
 * The live list comes from GET /api/models (see lib/api.js → fetchModels). This
 * curated set keeps the page rendering offline / when the endpoint 404s, and
 * documents the price shape every row must satisfy:
 *
 *   { provider, id, displayName, contextTokens,
 *     inputUsdPerMTok, outputUsdPerMTok,        // provider's published list price
 *     ourInputUsdPerMTok, ourOutputUsdPerMTok } // our managed rate = base × 1.05
 *
 * Base USD/1M-token rates below are indicative public flagship + mini prices for
 * each provider's current line-up. The our* fields are derived here so a hand-
 * edited base price can never drift from the +5% promise.
 */

export const MARKUP = 1.05

/** Round to a tidy 4 decimals so 0.15 × 1.05 = 0.1575 (not 0.15749999…). */
const our = (base) => Math.round(base * MARKUP * 10000) / 10000

/** Build a full row from base prices, deriving the our* fields. */
function model({ provider, id, displayName, contextTokens, input, output, tier }) {
  return {
    provider,
    id,
    displayName,
    contextTokens,
    inputUsdPerMTok: input,
    outputUsdPerMTok: output,
    ourInputUsdPerMTok: our(input),
    ourOutputUsdPerMTok: our(output),
    tier, // "flagship" | "balanced" | "mini" — display hint only (not in the API contract)
  }
}

// Indicative public list prices, USD per 1M tokens. Curated fallback only —
// the live endpoint is authoritative.
export const FALLBACK_MODELS = [
  // ── Anthropic ──
  model({ provider: 'anthropic', id: 'claude-opus-4-8',   displayName: 'Claude Opus 4.8',   contextTokens: 1_000_000, input: 5,    output: 25,  tier: 'flagship' }),
  model({ provider: 'anthropic', id: 'claude-sonnet-4-6', displayName: 'Claude Sonnet 4.6', contextTokens: 1_000_000, input: 3,    output: 15,  tier: 'balanced' }),
  model({ provider: 'anthropic', id: 'claude-haiku-4-5',  displayName: 'Claude Haiku 4.5',  contextTokens: 200_000,   input: 1,    output: 5,   tier: 'mini' }),

  // ── OpenAI ──
  model({ provider: 'openai',    id: 'gpt-5',             displayName: 'GPT-5',             contextTokens: 400_000,   input: 1.25, output: 10,  tier: 'flagship' }),
  model({ provider: 'openai',    id: 'gpt-5-mini',        displayName: 'GPT-5 mini',        contextTokens: 400_000,   input: 0.25, output: 2,   tier: 'balanced' }),
  model({ provider: 'openai',    id: 'gpt-5-nano',        displayName: 'GPT-5 nano',        contextTokens: 400_000,   input: 0.05, output: 0.4, tier: 'mini' }),

  // ── Google ──
  model({ provider: 'google',    id: 'gemini-2.5-pro',    displayName: 'Gemini 2.5 Pro',    contextTokens: 1_000_000, input: 1.25, output: 10,  tier: 'flagship' }),
  model({ provider: 'google',    id: 'gemini-2.5-flash',  displayName: 'Gemini 2.5 Flash',  contextTokens: 1_000_000, input: 0.30, output: 2.5, tier: 'balanced' }),
  model({ provider: 'google',    id: 'gemini-2.5-flash-lite', displayName: 'Gemini 2.5 Flash-Lite', contextTokens: 1_000_000, input: 0.10, output: 0.4, tier: 'mini' }),
]

/**
 * Normalize a raw /api/models row into the shape the page expects. The endpoint
 * is supposed to send our* already (base × 1.05); we recompute defensively if
 * they're missing so the +5% display is always correct and consistent.
 */
export function normalizeModel(m) {
  const input = Number(m.inputUsdPerMTok)
  const output = Number(m.outputUsdPerMTok)
  return {
    provider: m.provider,
    id: m.id,
    displayName: m.displayName ?? m.id,
    contextTokens: m.contextTokens ?? null,
    inputUsdPerMTok: input,
    outputUsdPerMTok: output,
    ourInputUsdPerMTok: m.ourInputUsdPerMTok != null ? Number(m.ourInputUsdPerMTok) : our(input),
    ourOutputUsdPerMTok: m.ourOutputUsdPerMTok != null ? Number(m.ourOutputUsdPerMTok) : our(output),
    tier: m.tier ?? null,
  }
}

export const PROVIDER_ORDER = ['anthropic', 'openai', 'google']

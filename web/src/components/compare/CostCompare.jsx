/**
 * CostCompare — Compare-page wrapper around the shared, HONEST
 * CompetitorCalculator.
 *
 * The real logic (slider inputs, sort by actual cost, managed/BYOK + AI
 * toggles, always-cheapest result with savings vs the next-cheapest and the
 * most-expensive) lives in ./CompetitorCalculator so /pricing and /compare
 * share one source of truth. This wrapper just loads the live plans (with
 * fallback) so gitstate's per-builder price is always current.
 */
import CompetitorCalculator from './CompetitorCalculator.jsx'
import { usePlans } from '../../lib/usePlans.js'

export default function CostCompare() {
  const { plans } = usePlans()
  return <CompetitorCalculator plans={plans} planKey="team" />
}

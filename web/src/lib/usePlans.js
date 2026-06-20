/**
 * usePlans — shared loader for GET /api/plans, used by Pricing + Compare.
 *
 * Backend shape (per plan):
 *   { key, name, perBuilderUsd, byokPerBuilderUsd, includedLlmUsd,
 *     overageMarkup, builders }
 * where builders=0 means unlimited; free caps at 2; perBuilderUsd=null →
 * Enterprise/custom. byokPerBuilderUsd is the discounted price when you bring
 * your own LLM key (free/enterprise → null).
 *
 * Returns { plans, loading, error } with a static fallback so the pages render
 * even if the API is unreachable.
 */
import { useState, useEffect } from 'react'
import { get } from './api.js'

export const FALLBACK_PLANS = [
  {
    key: 'free',
    name: 'Free',
    perBuilderUsd: 0,
    byokPerBuilderUsd: null,
    includedLlmUsd: 0,
    overageMarkup: 0,
    builders: 2,
  },
  {
    key: 'team',
    name: 'Team',
    perBuilderUsd: 6,
    byokPerBuilderUsd: 3,
    includedLlmUsd: 4,
    overageMarkup: 1.3,
    builders: 0,
  },
  {
    key: 'business',
    name: 'Business',
    perBuilderUsd: 14,
    byokPerBuilderUsd: 8,
    includedLlmUsd: 12,
    overageMarkup: 1.3,
    builders: 0,
  },
  {
    key: 'ent',
    name: 'Enterprise',
    perBuilderUsd: null,
    byokPerBuilderUsd: null,
    includedLlmUsd: null,
    overageMarkup: null,
    builders: 0,
  },
]

export function usePlans() {
  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    get('/api/plans')
      .then((data) => {
        if (cancelled) return
        setPlans(Array.isArray(data) && data.length > 0 ? data : FALLBACK_PLANS)
        setLoading(false)
      })
      .catch(() => {
        if (cancelled) return
        setPlans(FALLBACK_PLANS)
        setError('Using cached plan data — some prices may be stale.')
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return { plans, loading, error }
}

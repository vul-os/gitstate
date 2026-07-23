/**
 * useMeasure — track an element's content-box width via ResizeObserver.
 *
 * SVG charts need a real pixel width to lay out (a viewBox with
 * `preserveAspectRatio="none"` would stretch strokes and text). Returns
 * `[ref, width]`; width is 0 until the first observation, so callers should
 * skip rendering marks while it is falsy.
 */
import { useState, useEffect, useRef } from 'react'

export function useMeasure() {
  const ref = useRef(null)
  const [width, setWidth] = useState(0)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    // Seed synchronously so the first paint isn't an empty frame.
    setWidth(el.clientWidth)

    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const w = entry.contentRect?.width ?? entry.target.clientWidth
        setWidth((prev) => (Math.abs(prev - w) > 0.5 ? w : prev))
      }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  return [ref, width]
}

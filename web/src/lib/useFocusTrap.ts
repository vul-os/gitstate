/**
 * useFocusTrap — accessibility helper for modals & drawers.
 *
 * Given a ref to the dialog container, when `active` is true it:
 *   - remembers the element that was focused when the dialog opened,
 *   - moves focus into the dialog (first focusable element, or the container),
 *   - traps Tab / Shift+Tab so focus cycles within the dialog,
 *   - calls `onClose` when Escape is pressed,
 *   - restores focus to the original trigger element when it deactivates.
 *
 * Usage:
 *   const ref = useRef(null)
 *   useFocusTrap(ref, true, onClose)
 *   return <div ref={ref} role="dialog" aria-modal="true">…</div>
 */
import { useEffect, type RefObject } from 'react'

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'textarea:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusable(container: HTMLElement | null): HTMLElement[] {
  if (!container) return []
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => el.offsetParent !== null || el === document.activeElement,
  )
}

export function useFocusTrap(
  ref: RefObject<HTMLElement | null>,
  active: boolean,
  onClose?: () => void,
): void {
  useEffect(() => {
    if (!active) return
    const container = ref.current
    if (!container) return

    const previouslyFocused =
      document.activeElement instanceof HTMLElement ? document.activeElement : null

    // Move focus into the dialog. Prefer an element that opts in via
    // [data-autofocus], else the first focusable, else the container itself.
    const initial: HTMLElement =
      container.querySelector<HTMLElement>('[data-autofocus]') || focusable(container)[0] || container
    // The container needs a tabindex to be focusable as a last resort.
    if (initial === container && !container.hasAttribute('tabindex')) {
      container.setAttribute('tabindex', '-1')
    }
    // Defer to next frame so animated/portaled content is mounted & laid out.
    const raf = requestAnimationFrame(() => initial?.focus?.())

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose?.()
        return
      }
      if (e.key !== 'Tab') return
      const items = focusable(container)
      if (items.length === 0) {
        // Nothing focusable — keep focus on the container.
        e.preventDefault()
        container?.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      const activeEl = document.activeElement
      if (e.shiftKey) {
        if (activeEl === first || !container?.contains(activeEl)) {
          e.preventDefault()
          last.focus()
        }
      } else if (activeEl === last || !container?.contains(activeEl)) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      cancelAnimationFrame(raf)
      document.removeEventListener('keydown', onKeyDown, true)
      // Restore focus to the trigger if it's still in the document.
      if (previouslyFocused && document.contains(previouslyFocused)) {
        previouslyFocused.focus()
      }
    }
  }, [ref, active, onClose])
}

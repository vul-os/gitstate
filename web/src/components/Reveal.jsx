/**
 * Reveal — staggered fade-up animation using Motion (Framer Motion successor).
 *
 * Usage:
 *   // Wrap a parent to stagger its children:
 *   <Reveal stagger>
 *     <Card>...</Card>
 *     <Card>...</Card>
 *   </Reveal>
 *
 *   // Or animate a single element:
 *   <Reveal delay={0.2}>
 *     <Hero />
 *   </Reveal>
 *
 *   // Disable on mount (only animate once in view):
 *   <Reveal inView>...</Reveal>
 */
import { motion, useInView, useReducedMotion } from 'motion/react'
import { useRef, Children } from 'react'

const FADE_UP = {
  hidden:  { opacity: 0, y: 18, filter: 'blur(4px)' },
  visible: (i = 0) => ({
    opacity: 1,
    y: 0,
    filter: 'blur(0px)',
    transition: {
      duration: 0.5,
      delay: i * 0.08,
      ease: [0.22, 1, 0.36, 1],
    },
  }),
}

/** Single animated wrapper */
export function Reveal({ children, delay = 0, className = '', inView: triggerInView = false, as = 'div' }) {
  const ref = useRef(null)
  const isInView = useInView(ref, { once: true, margin: '-40px' })
  const reduce = useReducedMotion()
  // Reduced motion (and screenshots) → render visible immediately, never trapped at opacity:0.
  const shouldAnimate = reduce || !triggerInView || isInView

  const Tag = motion[as] ?? motion.div

  return (
    <Tag
      ref={ref}
      initial={reduce ? 'visible' : 'hidden'}
      animate={shouldAnimate ? 'visible' : 'hidden'}
      variants={{
        hidden: { opacity: 0, y: 18, filter: 'blur(4px)' },
        visible: {
          opacity: 1,
          y: 0,
          filter: 'blur(0px)',
          transition: { duration: 0.5, delay, ease: [0.22, 1, 0.36, 1] },
        },
      }}
      className={className}
    >
      {children}
    </Tag>
  )
}

/** Staggered container — wraps each child in a Reveal */
export function RevealList({ children, className = '', staggerDelay = 0.08, baseDelay = 0, inView: triggerInView = false }) {
  const ref = useRef(null)
  const isInView = useInView(ref, { once: true, margin: '-40px' })
  const reduce = useReducedMotion()
  const shouldAnimate = reduce || !triggerInView || isInView

  return (
    <div ref={ref} className={className}>
      {Children.map(children, (child, i) => (
        <motion.div
          key={i}
          initial={reduce ? 'visible' : 'hidden'}
          animate={shouldAnimate ? 'visible' : 'hidden'}
          custom={i}
          variants={{
            hidden: FADE_UP.hidden,
            visible: {
              ...FADE_UP.visible(i * staggerDelay + baseDelay),
              transition: {
                ...FADE_UP.visible(i * staggerDelay + baseDelay).transition,
              },
            },
          }}
        >
          {child}
        </motion.div>
      ))}
    </div>
  )
}

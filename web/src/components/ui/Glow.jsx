/**
 * Glow — decorative mesh-gradient background element.
 * Place it as a sibling or parent with `relative overflow-hidden`.
 *
 * Usage:
 *   <div className="relative overflow-hidden">
 *     <Glow variant="teal" className="top-0 left-1/4" />
 *     <YourContent />
 *   </div>
 */
export function Glow({
  variant = 'brand',
  size = 600,
  className = '',
  style = {},
}) {
  const colors = {
    teal:   'radial-gradient(circle, rgba(45,212,191,0.18) 0%, transparent 70%)',
    indigo: 'radial-gradient(circle, rgba(99,102,241,0.18) 0%, transparent 70%)',
    brand:  'radial-gradient(circle, rgba(45,212,191,0.12) 0%, rgba(99,102,241,0.10) 45%, transparent 70%)',
  }

  return (
    <div
      aria-hidden="true"
      className={['gs-glow absolute pointer-events-none', className].join(' ')}
      style={{
        width: size,
        height: size,
        background: colors[variant] ?? colors.brand,
        filter: 'blur(1px)',
        borderRadius: '50%',
        transform: 'translate(-50%, -50%)',
        ...style,
      }}
    />
  )
}

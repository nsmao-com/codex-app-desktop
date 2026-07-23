/** Shared motion-v transition presets for shell chrome and panels. */

export const springSnappy = {
  type: 'spring' as const,
  stiffness: 420,
  damping: 34,
  mass: 0.85,
}

export const springSoft = {
  type: 'spring' as const,
  stiffness: 320,
  damping: 32,
  mass: 0.9,
}

export const springPanel = {
  type: 'spring' as const,
  stiffness: 380,
  damping: 36,
  mass: 0.88,
}

export const easeOutQuick = {
  duration: 0.2,
  ease: [0.16, 1, 0.3, 1] as const,
}

export const pressable = {
  whileHover: { scale: 1.04 },
  whilePress: { scale: 0.94 },
  transition: springSnappy,
}

export const pageEnter = {
  initial: { opacity: 0, y: 10, scale: 0.985 },
  animate: { opacity: 1, y: 0, scale: 1 },
  exit: { opacity: 0, y: -8, scale: 0.99 },
  transition: easeOutQuick,
}

export const panelFromRight = {
  initial: { opacity: 0, x: 28 },
  animate: { opacity: 1, x: 0 },
  exit: { opacity: 0, x: 20 },
  transition: springPanel,
}

export const overlayFade = {
  initial: { opacity: 0 },
  animate: { opacity: 1 },
  exit: { opacity: 0 },
  transition: { duration: 0.18 },
}

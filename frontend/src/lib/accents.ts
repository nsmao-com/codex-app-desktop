/** Accent palette shared by settings UI and appearance runtime. */

export const APP_ACCENTS = [
  'codex',
  'amber',
  'gold',
  'rose',
  'coral',
  'emerald',
  'moss',
  'ocean',
  'sky',
  'slate',
  'graphite',
] as const

export type AppAccent = (typeof APP_ACCENTS)[number]

export type AccentOption = {
  value: AppAccent
  /** Swatch color shown in settings (dark-leaning mid tone). */
  color: string
}

export const ACCENT_OPTIONS: AccentOption[] = [
  { value: 'codex', color: '#339CFF' },
  { value: 'amber', color: '#d97757' },
  { value: 'gold', color: '#d4a017' },
  { value: 'rose', color: '#cf5f84' },
  { value: 'coral', color: '#e06b5c' },
  { value: 'emerald', color: '#2f9d78' },
  { value: 'moss', color: '#7f9a3d' },
  { value: 'ocean', color: '#2a9b96' },
  { value: 'sky', color: '#3d86c6' },
  { value: 'slate', color: '#5f738a' },
  { value: 'graphite', color: '#8a8680' },
]

export function isAppAccent(value: string): value is AppAccent {
  return (APP_ACCENTS as readonly string[]).includes(value)
}

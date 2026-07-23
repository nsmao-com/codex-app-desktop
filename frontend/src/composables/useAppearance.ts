import { computed, ref } from 'vue'

import { isAppAccent, type AppAccent } from '@/lib/accents'

export type AppTheme = 'light' | 'dark' | 'system'
export type { AppAccent }
export type BuiltinFont = 'manrope' | 'system' | 'mono'
export type AppFont = BuiltinFont | string

const THEME_ATTR = 'data-theme'
const ACCENT_ATTR = 'data-accent'
const FONT_ATTR = 'data-font'
const FONT_CUSTOM_VAR = '--font-custom'

const theme = ref<AppTheme>('light')
const accent = ref<AppAccent>('amber')
const font = ref<AppFont>('manrope')
const initialized = ref(false)

function readSystemTheme(): 'light' | 'dark' {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function isBuiltinFont(value: string): value is BuiltinFont {
  return value === 'manrope' || value === 'system' || value === 'mono'
}

function applyAttributes(): void {
  document.documentElement.setAttribute(THEME_ATTR, theme.value)
  document.documentElement.setAttribute(ACCENT_ATTR, accent.value)
  if (isBuiltinFont(font.value)) {
    document.documentElement.setAttribute(FONT_ATTR, font.value)
    document.documentElement.style.removeProperty(FONT_CUSTOM_VAR)
    return
  }
  document.documentElement.setAttribute(FONT_ATTR, 'custom')
  // Quote the family so names with spaces / CJK characters apply correctly.
  const escaped = font.value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')
  document.documentElement.style.setProperty(FONT_CUSTOM_VAR, `"${escaped}"`)
}

const resolvedTheme = computed<'light' | 'dark'>(() => {
  if (theme.value === 'system') return readSystemTheme()
  return theme.value
})

const isDark = computed(() => resolvedTheme.value === 'dark')

function initAppearance(initial: { theme?: AppTheme; accent?: AppAccent; font?: AppFont } = {}): void {
  if (initialized.value) return
  theme.value = initial.theme ?? 'light'
  accent.value = initial.accent && isAppAccent(initial.accent) ? initial.accent : 'amber'
  font.value = initial.font ?? 'manrope'

  applyAttributes()
  initialized.value = true

  const media = window.matchMedia('(prefers-color-scheme: dark)')
  media.addEventListener('change', () => {
    if (theme.value === 'system') applyAttributes()
  })
}

function setTheme(value: AppTheme): void {
  theme.value = value
  applyAttributes()
}

function setAccent(value: AppAccent | string): void {
  accent.value = isAppAccent(value) ? value : 'amber'
  applyAttributes()
}

function setFont(value: AppFont): void {
  font.value = value || 'manrope'
  applyAttributes()
}

export function useAppearance() {
  return {
    theme,
    accent,
    font,
    resolvedTheme,
    isDark,
    initialized,
    initAppearance,
    setTheme,
    setAccent,
    setFont,
  }
}

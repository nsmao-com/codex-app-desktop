import { computed, ref } from 'vue'

import { isAppAccent, type AppAccent } from '@/lib/accents'

export type AppTheme = 'light' | 'dark' | 'system'
export type { AppAccent }
export type BuiltinFont = 'manrope' | 'system' | 'mono'
export type AppFont = BuiltinFont | string
export type FontScale = 'sm' | 'md' | 'lg'

const THEME_ATTR = 'data-theme'
const ACCENT_ATTR = 'data-accent'
const FONT_ATTR = 'data-font'
const FONT_CUSTOM_VAR = '--font-custom'
const UI_SIZE_ATTR = 'data-ui-size'
const CODE_SIZE_ATTR = 'data-code-size'
const TRANSLUCENT_ATTR = 'data-translucent-sidebar'
const CONTRAST_ATTR = 'data-high-contrast'
const POINTER_ATTR = 'data-pointer-cursor'
const MOTION_ATTR = 'data-reduce-motion'

const theme = ref<AppTheme>('light')
const accent = ref<AppAccent>('codex')
const font = ref<AppFont>('system')
const uiFontSize = ref<FontScale>('md')
const codeFontSize = ref<FontScale>('md')
const translucentSidebar = ref(true)
const highContrast = ref(false)
const pointerCursor = ref(false)
const reduceMotion = ref(false)
const initialized = ref(false)

function readSystemTheme(): 'light' | 'dark' {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function isBuiltinFont(value: string): value is BuiltinFont {
  return value === 'manrope' || value === 'system' || value === 'mono'
}

function normalizeScale(value?: string): FontScale {
  if (value === 'sm' || value === 'lg') return value
  return 'md'
}

function applyAttributes(): void {
  const root = document.documentElement
  root.setAttribute(THEME_ATTR, theme.value)
  root.setAttribute(ACCENT_ATTR, accent.value)
  root.setAttribute(UI_SIZE_ATTR, uiFontSize.value)
  root.setAttribute(CODE_SIZE_ATTR, codeFontSize.value)
  root.setAttribute(TRANSLUCENT_ATTR, translucentSidebar.value ? 'true' : 'false')
  root.setAttribute(CONTRAST_ATTR, highContrast.value ? 'true' : 'false')
  root.setAttribute(POINTER_ATTR, pointerCursor.value ? 'true' : 'false')
  root.setAttribute(MOTION_ATTR, reduceMotion.value ? 'true' : 'false')
  if (isBuiltinFont(font.value)) {
    root.setAttribute(FONT_ATTR, font.value)
    root.style.removeProperty(FONT_CUSTOM_VAR)
    return
  }
  root.setAttribute(FONT_ATTR, 'custom')
  const escaped = font.value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')
  root.style.setProperty(FONT_CUSTOM_VAR, `"${escaped}"`)
}

const resolvedTheme = computed<'light' | 'dark'>(() => {
  if (theme.value === 'system') return readSystemTheme()
  return theme.value
})

const isDark = computed(() => resolvedTheme.value === 'dark')

export type AppearanceState = {
  theme?: AppTheme
  accent?: AppAccent | string
  font?: AppFont
  uiFontSize?: string
  codeFontSize?: string
  translucentSidebar?: boolean
  highContrast?: boolean
  pointerCursor?: boolean
  reduceMotion?: boolean
}

function initAppearance(initial: AppearanceState = {}): void {
  if (initialized.value) return
  theme.value = initial.theme ?? 'light'
  accent.value = initial.accent && isAppAccent(initial.accent) ? initial.accent : 'codex'
  font.value = initial.font ?? 'system'
  uiFontSize.value = normalizeScale(initial.uiFontSize)
  codeFontSize.value = normalizeScale(initial.codeFontSize)
  translucentSidebar.value = initial.translucentSidebar !== false
  highContrast.value = Boolean(initial.highContrast)
  pointerCursor.value = Boolean(initial.pointerCursor)
  reduceMotion.value = Boolean(initial.reduceMotion)

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
  accent.value = isAppAccent(value) ? value : 'codex'
  applyAttributes()
}

function setFont(value: AppFont): void {
  font.value = value || 'system'
  applyAttributes()
}

function setUiPrefs(prefs: AppearanceState): void {
  if (prefs.uiFontSize !== undefined) uiFontSize.value = normalizeScale(prefs.uiFontSize)
  if (prefs.codeFontSize !== undefined) codeFontSize.value = normalizeScale(prefs.codeFontSize)
  if (prefs.translucentSidebar !== undefined) translucentSidebar.value = prefs.translucentSidebar
  if (prefs.highContrast !== undefined) highContrast.value = prefs.highContrast
  if (prefs.pointerCursor !== undefined) pointerCursor.value = prefs.pointerCursor
  if (prefs.reduceMotion !== undefined) reduceMotion.value = prefs.reduceMotion
  applyAttributes()
}

export function useAppearance() {
  return {
    theme,
    accent,
    font,
    uiFontSize,
    codeFontSize,
    translucentSidebar,
    highContrast,
    pointerCursor,
    reduceMotion,
    resolvedTheme,
    isDark,
    initialized,
    initAppearance,
    setTheme,
    setAccent,
    setFont,
    setUiPrefs,
  }
}

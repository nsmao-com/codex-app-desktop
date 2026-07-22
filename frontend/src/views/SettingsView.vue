<script setup lang="ts">
import {
  ArrowLeft,
  Blocks,
  Check,
  Cpu,
  Download,
  Palette,
  Plus,
  RefreshCw,
  Shield,
  SlidersHorizontal,
  Sparkles,
  SquareTerminal,
  Trash2,
  Zap,
} from '@lucide/vue'
import { computed, onMounted, onUnmounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { supportedLocales } from '@/i18n'
import { useAppStore, useCodexStore } from '@/stores'
import type { SelectOption } from '@/types/codex'
import { modelsForRuntime } from '@/utils/runtimeProviders'

const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const codexStore = useCodexStore()
const { t } = useI18n()

const saving = shallowRef(false)
const saved = shallowRef(false)
const checkingUpdate = shallowRef(false)
const DEFAULT_MODEL_VALUE = '__nice_codex_default_model__'

const model = shallowRef(appStore.settings.model)
const customModels = shallowRef<string[]>([...(appStore.settings.customModels ?? [])])
const customModelDraft = shallowRef('')
const effort = shallowRef(appStore.settings.effort)
const serviceTier = shallowRef(appStore.settings.serviceTier)
const collaborationMode = shallowRef(appStore.settings.collaborationMode)
const personality = shallowRef(appStore.settings.personality)
const multiAgentMode = shallowRef(appStore.settings.multiAgentMode)
const sandbox = shallowRef(appStore.settings.sandbox)
const approvalPolicy = shallowRef(appStore.settings.approvalPolicy)
const theme = shallowRef(appStore.settings.theme)
const accentColor = shallowRef(appStore.settings.accentColor)
const fontFamily = shallowRef(appStore.settings.fontFamily)
const terminalProfile = shallowRef(appStore.settings.terminalProfile)
const language = shallowRef(appStore.settings.language)
const autoConnect = shallowRef(appStore.settings.autoConnect)
const activeSection = shallowRef<'agent' | 'safety' | 'appearance' | 'terminal'>('agent')
const settingsScroll = useTemplateRef<HTMLElement>('settingsScroll')

const modelSelection = computed({
  get: () => model.value || DEFAULT_MODEL_VALUE,
  set: (value: string) => { model.value = value === DEFAULT_MODEL_VALUE ? '' : value },
})

const selectedModel = computed(() => appStore.models.find((item) => item.model === model.value))
const codexStatus = computed(() => appStore.agentProviders.find((provider) => provider.kind === 'codex'))

const modelOptions = computed<SelectOption[]>(() => {
  const catalog = modelsForRuntime(appStore.models, customModels.value)
  return [
    { value: DEFAULT_MODEL_VALUE, label: t('settings.defaultModel'), description: t('settings.defaultModelDescription') },
    ...catalog.map((option) => ({
      value: option.model,
      label: option.displayName,
      description: '',
      badge: option.isDefault ? t('common.recommended') : '',
    })),
  ]
})

const effortOptions = computed(() => {
  const options = selectedModel.value?.supportedReasoningEfforts ?? []
  return options.length ? options : [
    { effort: 'low', description: 'Fast responses with lighter reasoning' },
    { effort: 'medium', description: 'Balanced speed and depth' },
    { effort: 'high', description: 'Deeper reasoning for complex work' },
    { effort: 'xhigh', description: 'Extra-high reasoning depth' },
    { effort: 'max', description: 'Maximum reasoning for hard problems' },
    { effort: 'ultra', description: 'Ultra reasoning depth' },
  ]
})

const fastTier = computed(() => selectedModel.value?.serviceTiers.find((tier) =>
  tier.id.toLocaleLowerCase() === 'fast' || tier.name.toLocaleLowerCase().includes('fast'),
))
const fastEnabled = computed(() => serviceTier.value !== '' && serviceTier.value === fastTier.value?.id)

const sandboxOptions = computed<SelectOption[]>(() => [
  { value: 'read-only', label: t('settings.readOnly') },
  { value: 'workspace-write', label: t('settings.workspaceWrite') },
  { value: 'danger-full-access', label: t('settings.fullAccess') },
])

const approvalOptions = computed<SelectOption[]>(() => [
  { value: 'untrusted', label: t('settings.untrusted') },
  { value: 'on-request', label: t('settings.onRequest') },
  { value: 'never', label: t('settings.never') },
])

const permissionPreset = computed(() => {
  if (sandbox.value === 'danger-full-access' && approvalPolicy.value === 'never') return 'auto'
  if (sandbox.value === 'workspace-write' && approvalPolicy.value === 'on-request') return 'ask'
  if (sandbox.value === 'read-only' && approvalPolicy.value === 'untrusted') return 'strict'
  return 'custom'
})

const languageOptions = computed<SelectOption[]>(() => supportedLocales.map((option) => ({
  value: option.value,
  label: option.label,
})))

const collaborationModeOptions = shallowRef<SelectOption[]>([
  { value: 'default', label: t('settings.defaultMode'), description: t('settings.defaultModeHint') },
  { value: 'plan', label: t('settings.planMode'), description: t('settings.planModeHint') },
])
const collaborationOptions = computed(() => collaborationModeOptions.value)

const personalityOptions = computed<SelectOption[]>(() => [
  { value: 'pragmatic', label: t('settings.pragmatic') },
  { value: 'friendly', label: t('settings.friendly') },
  { value: 'none', label: t('settings.noPersonality') },
])

const multiAgentOptions = computed<SelectOption[]>(() => [
  { value: 'explicitRequestOnly', label: t('settings.explicitAgents'), description: t('settings.explicitAgentsHint') },
  { value: 'proactive', label: t('settings.proactiveAgents'), description: t('settings.proactiveAgentsHint') },
])

const fontOptions = computed<SelectOption[]>(() => {
  const builtins: SelectOption[] = [
    { value: 'manrope', label: t('settings.fontManrope'), description: t('settings.fontManropeHint') },
    { value: 'system', label: t('settings.fontSystem'), description: t('settings.fontSystemHint') },
    { value: 'mono', label: t('settings.fontMono'), description: t('settings.fontMonoHint') },
  ]
  const local = appStore.systemFonts.map((font) => ({
    value: font.family,
    label: font.family,
    description: t('settings.fontLocalHint'),
  }))
  const seen = new Set(builtins.map((item) => item.value.toLowerCase()))
  for (const item of local) {
    if (seen.has(item.value.toLowerCase())) continue
    seen.add(item.value.toLowerCase())
    builtins.push(item)
  }
  return builtins
})

function fontOptionLabel(value: string): string {
  return fontOptions.value.find((item) => item.value === value)?.label || value
}

async function checkUpdatesNow(): Promise<void> {
  if (checkingUpdate.value) return
  checkingUpdate.value = true
  try {
    await appStore.checkForUpdates(false)
  } finally {
    checkingUpdate.value = false
  }
}

const terminalOptions = computed<SelectOption[]>(() => appStore.terminalProfiles.map((option) => ({
  value: option.id,
  label: option.name,
  description: terminalProfileDescription(option.id, option.description),
  badge: option.available ? '' : t('common.unavailable'),
  disabled: !option.available,
})))

const selectedTerminal = computed(() => appStore.terminalProfiles.find((option) => option.id === terminalProfile.value))
const selectedTerminalHint = computed(() => selectedTerminal.value?.available
  ? terminalProfileDescription(selectedTerminal.value.id, selectedTerminal.value.description)
  : t('settings.terminalUnavailable'))

const accentOptions = computed(() => [
  { value: 'amber', label: t('settings.accentAmber'), color: '#d97757' },
  { value: 'emerald', label: t('settings.accentEmerald'), color: '#879b70' },
  { value: 'coral', label: t('settings.accentCoral'), color: '#c8786a' },
  { value: 'graphite', label: t('settings.accentGraphite'), color: '#98958f' },
])

watch([theme, accentColor, fontFamily], () => {
  appStore.previewAppearance({
    theme: theme.value as 'light' | 'dark' | 'system',
    accentColor: accentColor.value as 'amber' | 'emerald' | 'coral' | 'graphite',
    fontFamily: fontFamily.value,
  })
})

onMounted(() => {
  syncFromStore()
  void loadCollaborationModes()
})

async function loadCollaborationModes(): Promise<void> {
  try {
    const response = await backend.ListCollaborationModes()
    const data = (response as { data?: unknown[] } | null)?.data
    if (!Array.isArray(data) || !data.length) return
    const options: SelectOption[] = []
    for (const item of data) {
      const record = item as Record<string, unknown>
      const mode = String(record.mode ?? record.id ?? record.name ?? '').trim()
      if (!mode) continue
      const label = String(record.displayName ?? record.label ?? mode)
      const description = String(record.description ?? '')
      options.push({ value: mode, label, description })
    }
    if (options.length) collaborationModeOptions.value = options
  } catch {
    // Keep default/plan fallback when app-server is offline.
  }
}

onUnmounted(() => {
  if (!saved.value) appStore.restoreAppearance()
})

function syncFromStore(): void {
  const settings = appStore.settings
  model.value = settings.model
  customModels.value = [...(settings.customModels ?? [])].filter((item) => !item.includes('·') && !/claude|gemini|grok/i.test(item))
  effort.value = settings.effort
  serviceTier.value = settings.serviceTier
  collaborationMode.value = settings.collaborationMode
  personality.value = settings.personality
  multiAgentMode.value = settings.multiAgentMode
  sandbox.value = settings.sandbox
  approvalPolicy.value = settings.approvalPolicy
  theme.value = settings.theme
  accentColor.value = settings.accentColor
  fontFamily.value = settings.fontFamily
  terminalProfile.value = settings.terminalProfile
  language.value = settings.language
  autoConnect.value = settings.autoConnect
}

function onModelChange(): void {
  const supported = effortOptions.value
  if (supported.length && !supported.some((option) => option.effort === effort.value)) {
    effort.value = selectedModel.value?.defaultReasoningEffort ?? supported[0]?.effort ?? 'high'
  }
  if (!selectedModel.value?.serviceTiers.some((tier) => tier.id === serviceTier.value)) {
    serviceTier.value = selectedModel.value?.defaultServiceTier ?? ''
  }
}

function addCustomModel(): void {
  const value = customModelDraft.value.trim()
  if (!value || value.length > 160 || customModels.value.some((item) => item.toLocaleLowerCase() === value.toLocaleLowerCase())) return
  customModels.value = [...customModels.value, value].slice(0, 24)
  model.value = value
  customModelDraft.value = ''
}

function removeCustomModel(value: string): void {
  customModels.value = customModels.value.filter((item) => item !== value)
  if (model.value === value) model.value = ''
}

function toggleFast(value?: boolean): void {
  if (!fastTier.value) return
  serviceTier.value = value === undefined
    ? (fastEnabled.value ? '' : fastTier.value.id)
    : (value ? fastTier.value.id : '')
}

function applyPermissionPreset(preset: 'auto' | 'ask' | 'strict'): void {
  if (preset === 'auto') {
    sandbox.value = 'danger-full-access'
    approvalPolicy.value = 'never'
  } else if (preset === 'ask') {
    sandbox.value = 'workspace-write'
    approvalPolicy.value = 'on-request'
  } else {
    sandbox.value = 'read-only'
    approvalPolicy.value = 'untrusted'
  }
}

function terminalProfileDescription(id: string, fallback: string): string {
  const keys: Record<string, string> = {
    powershell: 'settings.terminalPowerShellHint',
    'git-bash': 'settings.terminalGitBashHint',
    wsl: 'settings.terminalWSLHint',
  }
  return keys[id] ? t(keys[id]) : fallback
}

function selectedOptionLabel(options: SelectOption[], value: string): string {
  return options.find((option) => option.value === value)?.label ?? value
}

function scrollToSection(id: string, section: typeof activeSection.value): void {
  activeSection.value = section
  const container = settingsScroll.value
  const target = document.getElementById(id)
  if (!container || !target) return
  container.scrollTo({ top: Math.max(0, target.offsetTop - 16), behavior: 'smooth' })
}

function updateActiveSection(): void {
  const container = settingsScroll.value
  if (!container) return
  const top = container.getBoundingClientRect().top + 48
  const sections: Array<[typeof activeSection.value, string]> = [
    ['agent', 'settings-agent'],
    ['safety', 'settings-safety'],
    ['appearance', 'settings-appearance'],
    ['terminal', 'settings-terminal'],
  ]
  let next = sections[0]?.[0] ?? 'agent'
  for (const [section, id] of sections) {
    const element = document.getElementById(id)
    if (element && element.getBoundingClientRect().top <= top) next = section
  }
  activeSection.value = next
}

function closeSettings(): void {
  const from = typeof route.query.from === 'string' ? route.query.from : ''
  void router.replace(from === 'capabilities' ? { name: 'capabilities' } : { name: 'workbench' })
}

async function save(): Promise<void> {
  saving.value = true
  try {
    await appStore.savePreferences({
      ...appStore.settings,
      recentWorkspaces: appStore.settings.recentWorkspaces ?? [],
      model: model.value,
      modelProvider: '',
      customModels: customModels.value,
      effort: effort.value,
      serviceTier: serviceTier.value,
      collaborationMode: collaborationMode.value,
      personality: personality.value,
      multiAgentMode: multiAgentMode.value,
      sandbox: sandbox.value,
      approvalPolicy: approvalPolicy.value,
      theme: theme.value,
      accentColor: accentColor.value,
      fontFamily: fontFamily.value,
      terminalProfile: terminalProfile.value,
      language: language.value,
      autoConnect: autoConnect.value,
    })
    saved.value = true
    await codexStore.loadModels()
    closeSettings()
  } catch {
    saved.value = false
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="grid h-screen overflow-hidden bg-foreground/10 p-3 backdrop-blur-sm sm:p-6">
    <div
      v-motion
      :initial="{ opacity: 0, scale: 0.985, y: 8 }"
      :animate="{ opacity: 1, scale: 1, y: 0, transition: { duration: 0.2, ease: 'easeOut' } }"
      class="mx-auto flex h-full w-full max-w-6xl overflow-hidden rounded-xl border bg-card shadow-2xl"
    >
      <aside class="hidden w-60 shrink-0 flex-col border-r bg-sidebar p-3 md:flex">
        <Button variant="ghost" class="mb-4 h-8 justify-start px-2 text-xs" @click="closeSettings">
          <ArrowLeft :size="14" class="mr-2" /> Nice Codex
        </Button>
        <p class="px-2 pb-2 text-[10px] font-semibold uppercase text-muted-foreground">{{ t('settings.title') }}</p>
        <nav class="space-y-0.5">
          <Button :variant="activeSection === 'agent' ? 'secondary' : 'ghost'" class="h-8 w-full justify-start px-2 text-xs" @click="scrollToSection('settings-agent', 'agent')"><Cpu :size="14" class="mr-2" />{{ t('settings.agent') }}</Button>
          <Button :variant="activeSection === 'safety' ? 'secondary' : 'ghost'" class="h-8 w-full justify-start px-2 text-xs" @click="scrollToSection('settings-safety', 'safety')"><Shield :size="14" class="mr-2" />{{ t('settings.safety') }}</Button>
          <Button :variant="activeSection === 'appearance' ? 'secondary' : 'ghost'" class="h-8 w-full justify-start px-2 text-xs" @click="scrollToSection('settings-appearance', 'appearance')"><Palette :size="14" class="mr-2" />{{ t('settings.appearance') }}</Button>
          <Button :variant="activeSection === 'terminal' ? 'secondary' : 'ghost'" class="h-8 w-full justify-start px-2 text-xs" @click="scrollToSection('settings-terminal', 'terminal')"><SquareTerminal :size="14" class="mr-2" />{{ t('settings.terminal') }}</Button>
          <Button variant="ghost" class="h-8 w-full justify-start px-2 text-xs" @click="void router.push({ name: 'capabilities', query: { from: 'settings' } })"><Blocks :size="14" class="mr-2" />{{ t('capabilities.title') }}</Button>
        </nav>
        <div class="mt-auto rounded-md border bg-panel p-2.5 text-[10px] leading-4 text-muted-foreground">
          Codex {{ appStore.codexVersion || 'app-server' }}
        </div>
      </aside>

      <div class="flex min-w-0 flex-1 flex-col">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b bg-card px-4">
      <Button variant="ghost" size="icon-sm" :aria-label="t('settings.back')" @click="closeSettings">
        <ArrowLeft :size="17" />
      </Button>
      <div class="min-w-0 flex-1">
        <p class="text-[10px] font-semibold uppercase tracking-wider text-primary">{{ t('settings.kicker') }}</p>
        <h1 class="text-sm font-semibold">{{ t('settings.title') }}</h1>
      </div>
      <Button form="settings-form" type="submit" :disabled="saving">
        <SlidersHorizontal :size="15" class="mr-1.5" />
        {{ saving ? t('common.saving') : t('settings.save') }}
      </Button>
    </header>

    <main ref="settingsScroll" class="scrollbar-thin flex-1 overflow-y-auto px-4 py-6 sm:px-8" @scroll="updateActiveSection">
      <p class="mx-auto mb-6 max-w-3xl text-xs leading-6 text-muted-foreground">{{ t('settings.pageDescription') }}</p>

      <form id="settings-form" class="mx-auto max-w-3xl space-y-6" @submit.prevent="save">
        <Card id="settings-agent" class="scroll-mt-6 border-0 bg-transparent shadow-none">
          <CardHeader>
            <CardTitle class="flex items-center gap-2 text-sm">
              <Cpu :size="16" class="text-primary" />
              {{ t('settings.agent') }}
            </CardTitle>
            <CardDescription class="text-xs">{{ t('settings.agentDescription') }}</CardDescription>
          </CardHeader>
          <CardContent class="space-y-4">
            <div class="space-y-1">
              <Label class="text-xs">Codex</Label>
              <p class="text-[10px] text-muted-foreground">{{ t('settings.providerCodexHint') }}</p>
              <div v-if="codexStatus" class="mt-2 flex items-center gap-3 rounded-md border px-3 py-2">
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="truncate text-[11px] font-medium">{{ codexStatus.name }}</span>
                    <code v-if="codexStatus.version" class="truncate text-[9px] text-muted-foreground">{{ codexStatus.version }}</code>
                  </div>
                  <p class="mt-0.5 truncate text-[10px] text-muted-foreground">{{ codexStatus.message || 'Codex CLI / app-server' }}</p>
                </div>
                <Badge :variant="codexStatus.runtimeReady ? 'default' : 'outline'" class="shrink-0 text-[9px]">
                  {{ codexStatus.runtimeReady ? (appStore.settings.language === 'en-US' ? 'Ready' : '已就绪') : (appStore.settings.language === 'en-US' ? 'Not installed' : '未安装') }}
                </Badge>
              </div>
            </div>

            <div class="space-y-1">
              <Label class="text-xs">{{ t('settings.model') }}</Label>
              <Select v-model="modelSelection" @update:model-value="onModelChange">
                <SelectTrigger :aria-label="t('settings.model')">
                  <SelectValue :placeholder="t('settings.model')">{{ selectedOptionLabel(modelOptions, modelSelection) }}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="option in modelOptions" :key="option.value" :value="option.value">
                    <span class="flex items-center gap-2">
                      {{ option.label }}
                      <Badge v-if="option.badge" variant="secondary" class="text-[9px]">{{ option.badge }}</Badge>
                    </span>
                  </SelectItem>
                </SelectContent>
              </Select>
              <p class="text-[10px] text-muted-foreground">{{ selectedModel?.description || t('settings.defaultModelDescription') }}</p>
            </div>

            <div class="space-y-2">
              <Label for="custom-model-id" class="text-xs">{{ t('settings.customModel') }}</Label>
              <div class="flex gap-2">
                <Input id="custom-model-id" v-model="customModelDraft" :placeholder="t('settings.customModelPlaceholder')" class="h-9 text-xs" maxlength="160" @keydown.enter.prevent="addCustomModel" />
                <Button type="button" variant="outline" size="sm" class="h-9 shrink-0" :disabled="!customModelDraft.trim()" @click="addCustomModel">
                  <Plus :size="14" class="mr-1.5" />{{ t('common.add') }}
                </Button>
              </div>
              <div v-if="customModels.length" class="divide-y rounded-md border">
                <div v-for="customModel in customModels" :key="customModel" class="flex items-center gap-2 px-3 py-2">
                  <code class="min-w-0 flex-1 truncate text-[11px]">{{ customModel }}</code>
                  <Button type="button" variant="ghost" size="icon-xs" :aria-label="t('common.delete')" @click="removeCustomModel(customModel)"><Trash2 :size="12" /></Button>
                </div>
              </div>
              <p class="text-[10px] text-muted-foreground">{{ t('settings.customModelDescription') }}</p>
            </div>

            <div class="space-y-1">
              <Label class="text-xs">{{ t('settings.reasoning') }}</Label>
              <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <Button
                  v-for="option in effortOptions"
                  :key="option.effort"
                  type="button"
                  variant="outline"
                  size="sm"
                  class="h-auto min-w-0 flex-col items-start whitespace-normal px-3 py-2 text-left text-xs"
                  :class="effort === option.effort ? 'border-primary bg-primary/5' : ''"
                  @click="effort = option.effort"
                >
                  <span class="flex w-full items-center justify-between">
                    <strong>{{ 'displayName' in option ? option.displayName : option.effort }}</strong>
                    <Check v-if="effort === option.effort" :size="14" class="text-primary" />
                  </span>
                  <small class="line-clamp-2 w-full text-left text-[10px] leading-4 text-muted-foreground">{{ option.description }}</small>
                </Button>
              </div>
            </div>

            <div class="flex items-center justify-between rounded-lg border p-3">
              <div class="space-y-0.5">
                <Label class="flex items-center gap-2 text-xs">
                  <Zap :size="13" />
                  {{ t('settings.fastMode') }}
                </Label>
                <p class="text-[10px] text-muted-foreground">{{ fastTier?.description || t('settings.fastModeUnavailable') }}</p>
              </div>
              <Switch :checked="fastEnabled" :aria-label="t('settings.fastMode')" :disabled="!fastTier" @update:checked="toggleFast" />
            </div>

            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.collaborationMode') }}</Label>
                <Select v-model="collaborationMode">
                  <SelectTrigger :aria-label="t('settings.collaborationMode')">
                    <SelectValue>{{ selectedOptionLabel(collaborationOptions, collaborationMode) }}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in collaborationOptions" :key="option.value" :value="option.value">
                      <span class="block">{{ option.label }}</span>
                      <span class="block text-[10px] text-muted-foreground">{{ option.description }}</span>
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.personality') }}</Label>
                <Select v-model="personality">
                  <SelectTrigger :aria-label="t('settings.personality')">
                    <SelectValue>{{ selectedOptionLabel(personalityOptions, personality) }}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in personalityOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div class="space-y-1">
              <Label class="text-xs">{{ t('settings.multiAgent') }}</Label>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <Button
                  v-for="option in multiAgentOptions"
                  :key="option.value"
                  type="button"
                  variant="outline"
                  size="sm"
                  class="h-auto items-start px-3 py-2 text-left text-xs"
                  :class="multiAgentMode === option.value ? 'border-primary bg-primary/5' : ''"
                  @click="multiAgentMode = option.value"
                >
                  <span class="flex w-full items-center justify-between">
                    <strong>{{ option.label }}</strong>
                    <Check v-if="multiAgentMode === option.value" :size="14" class="text-primary" />
                  </span>
                  <small class="text-[10px] text-muted-foreground">{{ option.description }}</small>
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>

        <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Card id="settings-safety" class="scroll-mt-6 border-0 bg-transparent shadow-none">
            <CardHeader>
              <CardTitle class="flex items-center gap-2 text-sm">
                <Shield :size="16" class="text-primary" />
                {{ t('settings.safety') }}
              </CardTitle>
              <CardDescription class="text-xs">{{ t('settings.safetyDescription') }}</CardDescription>
            </CardHeader>
            <CardContent class="space-y-4">
              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.permissionMode') }}</Label>
                <div class="space-y-2">
                  <Button
                    v-for="preset in ['ask', 'auto', 'strict'] as const"
                    :key="preset"
                    type="button"
                    variant="outline"
                    size="sm"
                    class="h-auto w-full justify-between px-3 py-2 text-xs"
                    :class="permissionPreset === preset ? 'border-primary bg-primary/5' : ''"
                    @click="applyPermissionPreset(preset)"
                  >
                    <span class="text-left">
                      <strong>{{ t(`settings.permission${preset[0]?.toUpperCase()}${preset.slice(1)}`) }}</strong>
                      <small class="block text-[10px] text-muted-foreground">{{ t(`settings.permission${preset[0]?.toUpperCase()}${preset.slice(1)}Hint`) }}</small>
                    </span>
                    <Check v-if="permissionPreset === preset" :size="15" class="text-primary" />
                  </Button>
                </div>
              </div>

              <div v-if="permissionPreset === 'auto'" class="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-[11px] text-destructive">
                {{ t('settings.fullAccessWarning') }}
              </div>

              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.sandbox') }}</Label>
                <Select v-model="sandbox">
                  <SelectTrigger :aria-label="t('settings.sandbox')">
                    <SelectValue>{{ selectedOptionLabel(sandboxOptions, sandbox) }}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in sandboxOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.approvals') }}</Label>
                <Select v-model="approvalPolicy">
                  <SelectTrigger :aria-label="t('settings.approvals')">
                    <SelectValue>{{ selectedOptionLabel(approvalOptions, approvalPolicy) }}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in approvalOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                  </SelectContent>
                </Select>
                <p class="text-[10px] text-muted-foreground">{{ t('settings.safetyHint') }}</p>
              </div>
            </CardContent>
          </Card>

          <Card id="settings-appearance" class="scroll-mt-6 border-0 bg-transparent shadow-none">
            <CardHeader>
              <CardTitle class="flex items-center gap-2 text-sm">
                <Palette :size="16" class="text-primary" />
                {{ t('settings.appearance') }}
              </CardTitle>
              <CardDescription class="text-xs">{{ t('settings.appearanceDescription') }}</CardDescription>
            </CardHeader>
            <CardContent class="space-y-4">
              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.language') }}</Label>
                <Select v-model="language">
                  <SelectTrigger :aria-label="t('settings.language')">
                    <SelectValue>{{ selectedOptionLabel(languageOptions, language) }}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in languageOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.fontFamily') }}</Label>
                <Select v-model="fontFamily">
                  <SelectTrigger :aria-label="t('settings.fontFamily')">
                    <SelectValue>{{ fontOptionLabel(fontFamily) }}</SelectValue>
                  </SelectTrigger>
                  <SelectContent class="max-h-72">
                    <SelectItem v-for="option in fontOptions" :key="option.value" :value="option.value">
                      <span class="block" :style="option.value === 'manrope' || option.value === 'system' || option.value === 'mono' ? undefined : { fontFamily: `'${option.value}'` }">
                        {{ option.label }}
                      </span>
                      <span class="block text-[10px] text-muted-foreground">{{ option.description }}</span>
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.theme') }}</Label>
                <div class="grid grid-cols-3 gap-2">
                  <Button
                    v-for="option in ['light', 'dark', 'system'] as const"
                    :key="option"
                    type="button"
                    variant="outline"
                    size="sm"
                    class="text-xs"
                    :class="theme === option ? 'border-primary bg-primary/5' : ''"
                    @click="theme = option"
                  >
                    {{ t(`settings.${option}`) }}
                  </Button>
                </div>
              </div>

              <div class="space-y-1">
                <Label class="text-xs">{{ t('settings.accentColor') }}</Label>
                <div class="grid grid-cols-2 gap-2">
                  <Button
                    v-for="option in accentOptions"
                    :key="option.value"
                    type="button"
                    variant="outline"
                    size="sm"
                    class="h-auto justify-start gap-2 px-2.5 py-2 text-[11px]"
                    :class="accentColor === option.value ? 'border-primary bg-primary/5' : ''"
                    @click="accentColor = option.value"
                  >
                    <span class="size-3.5 shrink-0 rounded-full border shadow-sm" :style="{ backgroundColor: option.color }" />
                    <span class="truncate">{{ option.label }}</span>
                    <Check v-if="accentColor === option.value" :size="13" class="ml-auto shrink-0 text-primary" />
                  </Button>
                </div>
              </div>

              <div class="flex items-center justify-between rounded-lg border p-3">
                <div class="space-y-0.5">
                  <Label class="text-xs">{{ t('settings.reconnect') }}</Label>
                  <p class="text-[10px] text-muted-foreground">{{ t('settings.reconnectHint') }}</p>
                </div>
                <Switch :checked="autoConnect" :aria-label="t('settings.reconnect')" @update:checked="autoConnect = $event" />
              </div>

              <div class="space-y-3 rounded-lg border p-3">
                <div class="space-y-0.5">
                  <Label class="text-xs">{{ t('updates.about') }}</Label>
                  <p class="text-[10px] text-muted-foreground">{{ t('updates.aboutHint') }}</p>
                </div>
                <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                  <span>{{ t('updates.currentVersion') }}: <code class="font-mono text-foreground">v{{ appStore.appVersion }}</code></span>
                  <span v-if="appStore.updateInfo?.latestVersion">
                    {{ t('updates.latestVersion') }}: <code class="font-mono text-foreground">v{{ appStore.updateInfo.latestVersion }}</code>
                  </span>
                </div>
                <div class="flex flex-wrap gap-2">
                  <Button type="button" variant="outline" size="sm" class="text-xs" :disabled="checkingUpdate" @click="checkUpdatesNow">
                    <RefreshCw :size="13" class="mr-1.5" :class="checkingUpdate ? 'animate-spin' : ''" />
                    {{ checkingUpdate ? t('updates.checking') : t('updates.checkNow') }}
                  </Button>
                  <Button
                    v-if="appStore.updateInfo?.updateAvailable"
                    type="button"
                    size="sm"
                    class="text-xs"
                    @click="appStore.openUpdatePage"
                  >
                    <Download :size="13" class="mr-1.5" />
                    {{ t('updates.download') }}
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>
        </div>

        <Card id="settings-terminal" class="scroll-mt-6 border-0 bg-transparent shadow-none">
          <CardHeader>
            <CardTitle class="flex items-center gap-2 text-sm">
              <SquareTerminal :size="16" class="text-primary" />
              {{ t('settings.terminal') }}
            </CardTitle>
            <CardDescription class="text-xs">{{ t('settings.terminalDescription') }}</CardDescription>
          </CardHeader>
          <CardContent>
            <div class="space-y-1">
              <Label class="text-xs">{{ t('settings.terminalProfile') }}</Label>
              <Select v-model="terminalProfile">
                <SelectTrigger :aria-label="t('settings.terminalProfile')">
                  <SelectValue>{{ selectedOptionLabel(terminalOptions, terminalProfile) }}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="option in terminalOptions" :key="option.value" :value="option.value" :disabled="option.disabled">
                    <span class="flex items-center gap-2">
                      {{ option.label }}
                      <Badge v-if="option.badge" variant="outline" class="text-[9px]">{{ option.badge }}</Badge>
                    </span>
                  </SelectItem>
                </SelectContent>
              </Select>
              <p class="text-[10px] text-muted-foreground">{{ selectedTerminalHint }}</p>
            </div>
          </CardContent>
        </Card>

        <div class="flex justify-end gap-2 pb-6">
          <Button variant="outline" type="button" @click="closeSettings">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">
            <Sparkles :size="15" class="mr-1.5" />
            {{ saving ? t('common.saving') : t('settings.save') }}
          </Button>
        </div>
      </form>
    </main>
      </div>
    </div>
  </div>
</template>

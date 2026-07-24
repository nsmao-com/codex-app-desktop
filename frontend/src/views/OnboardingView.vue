<script setup lang="ts">
import {
  CheckCircle2,
  Download,
  FolderOpen,
  LoaderCircle,
  Monitor,
  Moon,
  RefreshCw,
  Sparkles,
  Sun,
  Terminal,
  AlertCircle,
} from '@lucide/vue'
import { computed, onMounted, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'
import type { AppTheme } from '@/composables/useAppearance'
import { notify } from '@/utils/notify'
import {
  checkCLITools,
  installCLITool,
  type CLIToolStatus,
  type CLIToolsReport,
} from '@/utils/cliTools'

const { t, locale } = useI18n()
const appStore = useAppStore()
const workspaceStore = useWorkspaceStore()
const codexStore = useCodexStore()

const step = shallowRef(0)
const stepDirection = shallowRef(1)
const finishing = shallowRef(false)

const theme = shallowRef<AppTheme>((appStore.settings.theme as AppTheme) || 'system')
const language = shallowRef(appStore.settings.language === 'en-US' ? 'en-US' : 'zh-CN')

const cliReport = shallowRef<CLIToolsReport | null>(null)
const cliLoading = shallowRef(false)
const cliInstalling = shallowRef<Record<string, boolean>>({})
const cliError = shallowRef('')

const steps = computed(() => [
  t('onboarding.stepWelcome'),
  t('onboarding.stepTheme'),
  t('onboarding.stepLanguage'),
  t('onboarding.stepRuntime'),
  t('onboarding.stepWorkspace'),
])

const STEP_RUNTIME = 3
const STEP_WORKSPACE = 4

const transitionName = computed(() => (stepDirection.value >= 0 ? 'onboard-forward' : 'onboard-back'))

const themeOptions = computed(() => ([
  { id: 'light' as const, icon: Sun, label: t('settings.light') },
  { id: 'dark' as const, icon: Moon, label: t('settings.dark') },
  { id: 'system' as const, icon: Monitor, label: t('settings.system') },
]))

const cliTools = computed(() => cliReport.value?.tools ?? [])

function selectTheme(next: AppTheme): void {
  theme.value = next
  appStore.previewAppearance({
    theme: next,
    accentColor: appStore.settings.accentColor,
    fontFamily: appStore.settings.fontFamily,
    uiFontSize: appStore.settings.uiFontSize,
    codeFontSize: appStore.settings.codeFontSize,
    translucentSidebar: appStore.settings.translucentSidebar,
    highContrast: appStore.settings.highContrast,
    pointerCursor: appStore.settings.pointerCursor,
    reduceMotion: appStore.settings.reduceMotion,
  })
}

function selectLanguage(next: 'zh-CN' | 'en-US'): void {
  language.value = next
  locale.value = next
}

async function chooseWorkspace(): Promise<void> {
  await workspaceStore.selectWorkspace()
}

async function refreshCLITools(): Promise<void> {
  cliLoading.value = true
  cliError.value = ''
  try {
    cliReport.value = await checkCLITools()
  } catch (error) {
    cliError.value = error instanceof Error ? error.message : String(error || 'check failed')
  } finally {
    cliLoading.value = false
  }
}

async function installTool(tool: CLIToolStatus): Promise<void> {
  if (!tool?.id || cliInstalling.value[tool.id]) return
  cliInstalling.value = { ...cliInstalling.value, [tool.id]: true }
  cliError.value = ''
  try {
    const result = await installCLITool(tool.id)
    if (result.tool) {
      const list = [...(cliReport.value?.tools ?? [])]
      const idx = list.findIndex((item) => item.id === tool.id)
      if (idx >= 0) list[idx] = result.tool
      else list.push(result.tool)
      cliReport.value = {
        ...(cliReport.value || {
          packageManager: result.tool.packageManager,
          nodeAvailable: result.tool.nodeAvailable,
          nodeVersion: '',
          checkedAt: Date.now() / 1000,
        }),
        tools: list,
      }
    }
    if (result.ok) {
      notify('success', tool.name, result.message || t('onboarding.cliInstallOk'))
      // Soft re-check so version / badges refresh without full app bootstrap.
      void refreshCLITools()
    } else {
      notify('error', tool.name, result.message || t('onboarding.cliInstallFailed'))
      cliError.value = result.message || result.output || ''
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error || 'install failed')
    cliError.value = message
    notify('error', tool.name, message)
  } finally {
    cliInstalling.value = { ...cliInstalling.value, [tool.id]: false }
  }
}

function toolStatusLabel(tool: CLIToolStatus): string {
  if (!tool.installed) return t('onboarding.cliMissing')
  if (tool.updateAvailable) return t('onboarding.cliUpdateAvailable')
  return t('onboarding.cliReady')
}

function next(): void {
  if (step.value < steps.value.length - 1) {
    stepDirection.value = 1
    step.value += 1
    if (step.value === STEP_RUNTIME) {
      void refreshCLITools()
    }
    return
  }
  void finish()
}

function back(): void {
  if (step.value > 0) {
    stepDirection.value = -1
    step.value -= 1
  }
}

async function finish(): Promise<void> {
  if (finishing.value) return
  finishing.value = true
  try {
    await appStore.completeOnboarding({
      theme: theme.value,
      language: language.value,
    })
    if (appStore.settings.workspace && appStore.settings.autoConnect && appStore.codexAvailable) {
      void codexStore.connect(appStore.settings.workspace)
    }
  } finally {
    finishing.value = false
  }
}

async function skipWorkspace(): Promise<void> {
  await finish()
}

onMounted(() => {
  // Warm detection early so the runtime step feels instant.
  void refreshCLITools()
})
</script>

<template>
  <div class="onboarding-stage relative flex h-full w-full flex-col overflow-hidden bg-background text-foreground">
    <div class="welcome-aurora pointer-events-none absolute inset-0" aria-hidden="true" />
    <div class="welcome-orb welcome-orb-a pointer-events-none absolute" aria-hidden="true" />
    <div class="welcome-orb welcome-orb-b pointer-events-none absolute" aria-hidden="true" />

    <div class="relative z-[1] mx-auto flex w-full max-w-2xl flex-1 flex-col px-6 py-10 sm:px-8">
      <div class="onboarding-chrome mb-8 flex items-center justify-between gap-3">
        <div>
          <p class="text-[11px] font-medium tracking-[0.18em] text-muted-foreground uppercase">Nice Codex</p>
          <p class="mt-1 text-[13px] text-muted-foreground">{{ t('onboarding.kicker') }}</p>
        </div>
        <div class="flex items-center gap-1.5">
          <span
            v-for="(label, index) in steps"
            :key="label"
            class="onboarding-dot h-1.5 rounded-full"
            :class="index === step ? 'is-active' : index < step ? 'is-done' : 'is-todo'"
            :title="label"
          />
        </div>
      </div>

      <div class="relative flex min-h-0 flex-1 flex-col justify-center overflow-hidden">
        <Transition :name="transitionName" mode="out-in">
          <!-- Welcome -->
          <div v-if="step === 0" key="welcome" class="onboarding-panel space-y-4">
            <div class="onboarding-icon inline-flex size-12 items-center justify-center rounded-2xl border bg-card/90 shadow-sm backdrop-blur-sm">
              <Sparkles :size="22" class="text-primary" />
            </div>
            <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">{{ t('onboarding.welcomeTitle') }}</h1>
            <p class="max-w-xl text-[14px] leading-6 text-muted-foreground">
              {{ t('onboarding.welcomeBody') }}
            </p>
          </div>

          <!-- Theme -->
          <div v-else-if="step === 1" key="theme" class="onboarding-panel space-y-5">
            <div>
              <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('onboarding.themeTitle') }}</h1>
              <p class="mt-2 text-[14px] text-muted-foreground">{{ t('onboarding.themeBody') }}</p>
            </div>
            <div class="grid gap-3 sm:grid-cols-3">
              <button
                v-for="(option, optionIndex) in themeOptions"
                :key="option.id"
                type="button"
                class="onboarding-choice flex flex-col items-start gap-3 rounded-2xl border px-4 py-4 text-left"
                :class="theme === option.id ? 'is-selected' : ''"
                :style="{ '--stagger': `${optionIndex * 60}ms` }"
                @click="selectTheme(option.id)"
              >
                <component :is="option.icon" :size="18" class="text-foreground/80" />
                <span class="text-[13px] font-medium">{{ option.label }}</span>
              </button>
            </div>
          </div>

          <!-- Language -->
          <div v-else-if="step === 2" key="language" class="onboarding-panel space-y-5">
            <div>
              <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('onboarding.languageTitle') }}</h1>
              <p class="mt-2 text-[14px] text-muted-foreground">{{ t('onboarding.languageBody') }}</p>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <button
                type="button"
                class="onboarding-choice rounded-2xl border px-4 py-4 text-left"
                :class="language === 'zh-CN' ? 'is-selected' : ''"
                style="--stagger: 0ms"
                @click="selectLanguage('zh-CN')"
              >
                <p class="text-[13px] font-medium">简体中文</p>
                <p class="mt-1 text-[11px] text-muted-foreground">Chinese</p>
              </button>
              <button
                type="button"
                class="onboarding-choice rounded-2xl border px-4 py-4 text-left"
                :class="language === 'en-US' ? 'is-selected' : ''"
                style="--stagger: 60ms"
                @click="selectLanguage('en-US')"
              >
                <p class="text-[13px] font-medium">English</p>
                <p class="mt-1 text-[11px] text-muted-foreground">English (US)</p>
              </button>
            </div>
          </div>

          <!-- Runtime / CLI tools -->
          <div v-else-if="step === STEP_RUNTIME" key="runtime" class="onboarding-panel space-y-5">
            <div class="flex items-start justify-between gap-3">
              <div>
                <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('onboarding.runtimeTitle') }}</h1>
                <p class="mt-2 text-[14px] text-muted-foreground">{{ t('onboarding.runtimeBody') }}</p>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                class="h-8 shrink-0 px-2 text-xs"
                :disabled="cliLoading"
                @click="refreshCLITools"
              >
                <RefreshCw :size="13" class="mr-1" :class="cliLoading ? 'animate-spin' : ''" />
                {{ t('onboarding.cliRecheck') }}
              </Button>
            </div>

            <div
              v-if="cliReport && !cliReport.nodeAvailable"
              class="flex items-start gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2.5 text-[12px] text-amber-900 dark:text-amber-100"
            >
              <AlertCircle :size="15" class="mt-0.5 shrink-0" />
              <div>
                <p class="font-medium">{{ t('onboarding.nodeMissingTitle') }}</p>
                <p class="mt-0.5 text-[11px] opacity-90">{{ t('onboarding.nodeMissingBody') }}</p>
              </div>
            </div>

            <div class="space-y-3">
              <div
                v-for="tool in cliTools"
                :key="tool.id"
                class="onboarding-card rounded-2xl border bg-card/90 px-4 py-3.5 backdrop-blur-sm"
              >
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-2">
                      <Terminal :size="14" class="shrink-0 text-muted-foreground" />
                      <p class="text-[13px] font-medium">{{ tool.name }}</p>
                      <Badge
                        :variant="tool.installed && !tool.updateAvailable ? 'default' : 'outline'"
                        class="text-[9px]"
                      >
                        {{ toolStatusLabel(tool) }}
                      </Badge>
                    </div>
                    <p class="mt-1.5 font-mono text-[11px] text-muted-foreground">
                      {{ tool.package }}
                    </p>
                    <p class="mt-1 text-[11px] text-muted-foreground">
                      <template v-if="tool.installed">
                        {{ t('onboarding.cliVersion', { version: tool.version || '—' }) }}
                        <span v-if="tool.latestVersion">
                          · {{ t('onboarding.cliLatest', { version: tool.latestVersion }) }}
                        </span>
                      </template>
                      <template v-else>
                        {{ t('onboarding.cliInstallHint', { command: tool.installCommand }) }}
                      </template>
                    </p>
                  </div>
                  <div class="flex shrink-0 flex-col items-end gap-1.5">
                    <CheckCircle2
                      v-if="tool.installed && !tool.updateAvailable"
                      :size="18"
                      class="text-emerald-500"
                    />
                    <Button
                      v-else
                      type="button"
                      size="sm"
                      class="h-8 text-xs"
                      :disabled="!tool.canInstall || Boolean(cliInstalling[tool.id])"
                      @click="installTool(tool)"
                    >
                      <LoaderCircle
                        v-if="cliInstalling[tool.id]"
                        :size="13"
                        class="mr-1.5 animate-spin"
                      />
                      <Download v-else :size="13" class="mr-1.5" />
                      {{
                        cliInstalling[tool.id]
                          ? t('onboarding.cliInstalling')
                          : tool.installed
                            ? t('onboarding.cliUpdate')
                            : t('onboarding.cliInstall')
                      }}
                    </Button>
                  </div>
                </div>
              </div>

              <p v-if="cliLoading && !cliTools.length" class="text-center text-[12px] text-muted-foreground">
                {{ t('onboarding.cliChecking') }}
              </p>
              <p v-if="cliError" class="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-[11px] text-destructive">
                {{ cliError }}
              </p>
            </div>

            <p
              v-if="cliReport?.codexHome || cliReport?.grokHome"
              class="text-[11px] leading-5 text-muted-foreground"
            >
              {{
                t('onboarding.cliHomesHint', {
                  codexHome: cliReport?.codexHome || '~/.codex',
                  grokHome: cliReport?.grokHome || '~/.grok',
                })
              }}
            </p>
            <p class="text-[12px] text-muted-foreground">
              {{ t('onboarding.runtimeSkipHint') }}
            </p>
          </div>

          <!-- Workspace -->
          <div v-else key="workspace" class="onboarding-panel space-y-5">
            <div>
              <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('onboarding.workspaceTitle') }}</h1>
              <p class="mt-2 text-[14px] text-muted-foreground">{{ t('onboarding.workspaceBody') }}</p>
            </div>
            <div class="onboarding-card rounded-2xl border bg-card/90 px-4 py-4 backdrop-blur-sm">
              <p class="text-[12px] text-muted-foreground">{{ t('onboarding.workspaceCurrent') }}</p>
              <p class="mt-1 truncate text-[13px] font-medium">
                {{ workspaceStore.workspace?.path || appStore.settings.workspace || t('onboarding.workspaceEmpty') }}
              </p>
              <Button type="button" class="mt-4 h-9" @click="chooseWorkspace">
                <FolderOpen :size="15" class="mr-1.5" />
                {{ t('onboarding.chooseFolder') }}
              </Button>
            </div>
            <p class="text-[12px] text-muted-foreground">{{ t('onboarding.workspaceHint') }}</p>
          </div>
        </Transition>
      </div>

      <div class="onboarding-chrome mt-8 flex items-center justify-between gap-3">
        <Button
          v-if="step > 0"
          type="button"
          variant="ghost"
          class="h-9"
          :disabled="finishing || Object.values(cliInstalling).some(Boolean)"
          @click="back"
        >
          {{ t('onboarding.back') }}
        </Button>
        <div v-else />
        <div class="flex items-center gap-2">
          <Button
            v-if="step === STEP_WORKSPACE"
            type="button"
            variant="ghost"
            class="h-9"
            :disabled="finishing"
            @click="skipWorkspace"
          >
            {{ t('onboarding.skipForNow') }}
          </Button>
          <Button
            type="button"
            class="h-9 min-w-[112px]"
            :disabled="finishing || Object.values(cliInstalling).some(Boolean)"
            @click="next"
          >
            {{ finishing ? t('common.saving') : (step === steps.length - 1 ? t('onboarding.finish') : t('onboarding.continue')) }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

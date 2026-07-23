<script setup lang="ts">
import { FolderOpen, Moon, Sparkles, Sun, Monitor } from '@lucide/vue'
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'
import type { AppTheme } from '@/composables/useAppearance'

const { t, locale } = useI18n()
const appStore = useAppStore()
const workspaceStore = useWorkspaceStore()
const codexStore = useCodexStore()

const step = shallowRef(0)
const stepDirection = shallowRef(1)
const finishing = shallowRef(false)

const theme = shallowRef<AppTheme>((appStore.settings.theme as AppTheme) || 'system')
const language = shallowRef(appStore.settings.language === 'en-US' ? 'en-US' : 'zh-CN')

const steps = computed(() => [
  t('onboarding.stepWelcome'),
  t('onboarding.stepTheme'),
  t('onboarding.stepLanguage'),
  t('onboarding.stepWorkspace'),
])

const transitionName = computed(() => (stepDirection.value >= 0 ? 'onboard-forward' : 'onboard-back'))

const themeOptions = computed(() => ([
  { id: 'light' as const, icon: Sun, label: t('settings.light') },
  { id: 'dark' as const, icon: Moon, label: t('settings.dark') },
  { id: 'system' as const, icon: Monitor, label: t('settings.system') },
]))

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

function next(): void {
  if (step.value < steps.value.length - 1) {
    stepDirection.value = 1
    step.value += 1
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
          :disabled="finishing"
          @click="back"
        >
          {{ t('onboarding.back') }}
        </Button>
        <div v-else />
        <div class="flex items-center gap-2">
          <Button
            v-if="step === 3"
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
            :disabled="finishing"
            @click="next"
          >
            {{ finishing ? t('common.saving') : (step === steps.length - 1 ? t('onboarding.finish') : t('onboarding.continue')) }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

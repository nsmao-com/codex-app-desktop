<script setup lang="ts">
import {
  ChevronLeft,
  ChevronRight,
  PanelLeft,
  Minus,
  Square,
  Copy,
  X,
} from '@lucide/vue'
import { Window } from '@wailsio/runtime'
import { computed, onMounted, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { SimpleTooltip } from '@/components/ui/tooltip'
import { useAppStore, useClaudeStore, useCodexStore, useGrokStore, useNavigationStore, useShellStore, useWorkspaceStore } from '@/stores'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const shellStore = useShellStore()
const navStore = useNavigationStore()

const maximised = shallowRef(false)

const backTitle = computed(() => {
  const entry = navStore.backEntry
  if (!entry) return t('window.back')
  return t('window.backTo', { label: entry.label })
})
const forwardTitle = computed(() => {
  const entry = navStore.forwardEntry
  if (!entry) return t('window.forward')
  return t('window.forwardTo', { label: entry.label })
})

onMounted(() => {
  void refreshMaximised()
})

async function refreshMaximised(): Promise<void> {
  try {
    maximised.value = await Window.IsMaximised()
  } catch {
    maximised.value = false
  }
}

async function minimise(): Promise<void> {
  await Window.Minimise()
}

async function toggleMaximise(): Promise<void> {
  await Window.ToggleMaximise()
  await refreshMaximised()
}

async function closeWindow(): Promise<void> {
  await Window.Close()
}

async function onTitlebarDblClick(event: MouseEvent): Promise<void> {
  const target = event.target as HTMLElement | null
  if (target?.closest('.titlebar-controls, .titlebar-menus, .window-controls')) return
  await toggleMaximise()
}

function goBack(): void {
  void navStore.goBack()
}

function goForward(): void {
  void navStore.goForward()
}

function openSettings(): void {
  void router.push({ name: 'settings' })
}

function openCapabilities(): void {
  void router.push({ name: 'capabilities' })
}

function openWorkbench(): void {
  void router.push({ name: 'workbench' })
}

function checkUpdates(): void {
  void appStore.openUpdateCheckDialog()
}

function openAbout(): void {
  void appStore.openUpdateCheckDialog()
}

function openReleases(): void {
  void appStore.openReleasesPage()
}

function openGitHub(): void {
  void appStore.openGitHubRepo()
}
</script>

<template>
  <header
    class="app-titlebar flex h-10 shrink-0 items-stretch select-none"
    @dblclick="onTitlebarDblClick"
  >
    <div class="titlebar-controls flex items-center gap-0.5 pl-2 pr-1">
      <SimpleTooltip :content="t('sidebar.toggle')">
        <button
          type="button"
          class="chrome-btn"
          :aria-label="t('sidebar.toggle')"
          @click.stop="shellStore.toggleSidebar()"
        >
          <PanelLeft :size="15" />
        </button>
      </SimpleTooltip>
      <SimpleTooltip :content="backTitle">
        <button
          type="button"
          class="chrome-btn"
          :class="{ 'is-disabled': !navStore.canGoBack }"
          :disabled="!navStore.canGoBack"
          :aria-label="backTitle"
          @click.stop="goBack"
        >
          <ChevronLeft :size="16" />
        </button>
      </SimpleTooltip>
      <SimpleTooltip :content="forwardTitle">
        <button
          type="button"
          class="chrome-btn"
          :class="{ 'is-disabled': !navStore.canGoForward }"
          :disabled="!navStore.canGoForward"
          :aria-label="forwardTitle"
          @click.stop="goForward"
        >
          <ChevronRight :size="16" />
        </button>
      </SimpleTooltip>
    </div>

    <nav class="titlebar-menus flex items-center gap-0.5 pr-2">
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <button type="button" class="menu-label" @dblclick.stop>
            {{ t('window.menuFile') }}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" class="min-w-44">
          <DropdownMenuItem
            :disabled="appStore.isGrokMode
              ? !grokStore.workspacePath
              : appStore.isClaudeMode
                ? !claudeStore.workspacePath
                : !codexStore.isReady"
            @click="appStore.isGrokMode
              ? grokStore.newSession()
              : appStore.isClaudeMode
                ? claudeStore.newSession()
                : void codexStore.newThread()"
          >
            {{ t('sidebar.newTask') }}
          </DropdownMenuItem>
          <DropdownMenuItem
            @click="appStore.isGrokMode
              ? void workspaceStore.selectWorkspace().then(() => void grokStore.loadSessions())
              : appStore.isClaudeMode
                ? void workspaceStore.selectWorkspace().then(() => void claudeStore.loadSessions())
                : void codexStore.selectProject()"
          >
            {{ t('welcome.chooseWorkspace') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            v-if="appStore.isCodexMode"
            :disabled="!codexStore.activeThread || Boolean(codexStore.threadMutation) || codexStore.activeThreadBusy"
            @click="void codexStore.archiveActiveThread()"
          >
            {{ t('threadActions.archive') }}
          </DropdownMenuItem>
          <DropdownMenuItem
            v-else-if="appStore.isClaudeMode"
            :disabled="!claudeStore.activeSessionId"
            @click="void claudeStore.archiveActiveSession()"
          >
            {{ t('threadActions.archive') }}
          </DropdownMenuItem>
          <DropdownMenuItem
            v-else-if="appStore.isGrokMode"
            :disabled="!grokStore.activeSessionId"
            @click="void grokStore.archiveActiveSession()"
          >
            {{ t('threadActions.archive') }}
          </DropdownMenuItem>
          <DropdownMenuItem
            class="text-destructive focus:text-destructive"
            :disabled="appStore.isGrokMode
              ? !grokStore.activeSessionId
              : appStore.isClaudeMode
                ? !claudeStore.activeSessionId
                : (!codexStore.activeThread || Boolean(codexStore.threadMutation) || codexStore.activeThreadBusy)"
            @click="appStore.isGrokMode
              ? void grokStore.deleteSession(grokStore.activeSessionId)
              : appStore.isClaudeMode
                ? void claudeStore.deleteActiveSession()
                : void codexStore.deleteActiveThread()"
          >
            {{ t('threadActions.delete') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem @click="openSettings">
            {{ t('sidebar.openSettings') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem @click="closeWindow">
            {{ t('window.close') }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <button type="button" class="menu-label" @dblclick.stop>
            {{ t('window.menuEdit') }}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" class="min-w-44">
          <DropdownMenuItem @click="openSettings">
            {{ t('settings.title') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="openCapabilities">
            {{ t('capabilities.title') }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <button type="button" class="menu-label" @dblclick.stop>
            {{ t('window.menuView') }}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" class="min-w-44">
          <DropdownMenuItem @click="shellStore.toggleSidebar()">
            {{ t('sidebar.toggle') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="appStore.toggleTheme()">
            {{ t('settings.toggleTheme') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="openCapabilities">
            {{ t('capabilities.title') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="openWorkbench">
            {{ t('app.workbench') }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <button type="button" class="menu-label" @dblclick.stop>
            {{ t('window.menuHelp') }}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" class="min-w-48">
          <DropdownMenuItem @click="checkUpdates">
            {{ t('updates.checkNow') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="openReleases">
            {{ t('updates.viewReleases') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="openGitHub">
            {{ t('updates.viewGitHub') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem @click="openAbout">
            {{ t('updates.aboutApp') }}
          </DropdownMenuItem>
          <DropdownMenuItem disabled>
            Nice Codex v{{ appStore.appVersion }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </nav>

    <div class="min-w-0 flex-1" />

    <!-- Windows-style caption buttons on the right -->
    <div class="window-controls flex h-full shrink-0 items-stretch">
      <SimpleTooltip :content="t('window.minimise')">
        <button
          type="button"
          class="caption-btn"
          :aria-label="t('window.minimise')"
          @click.stop="minimise"
        >
          <Minus :size="14" stroke-width="1.75" />
        </button>
      </SimpleTooltip>
      <SimpleTooltip :content="maximised ? t('window.restore') : t('window.maximise')">
        <button
          type="button"
          class="caption-btn"
          :aria-label="maximised ? t('window.restore') : t('window.maximise')"
          @click.stop="toggleMaximise"
        >
          <Copy v-if="maximised" :size="11" stroke-width="1.75" class="-scale-x-100" />
          <Square v-else :size="11" stroke-width="1.75" />
        </button>
      </SimpleTooltip>
      <SimpleTooltip :content="t('window.close')">
        <button
          type="button"
          class="caption-btn caption-close"
          :aria-label="t('window.close')"
          @click.stop="closeWindow"
        >
          <X :size="14" stroke-width="1.75" />
        </button>
      </SimpleTooltip>
    </div>
  </header>
</template>

<style scoped>
.app-titlebar {
  --wails-draggable: drag;
  background: transparent;
}

.titlebar-controls,
.titlebar-menus,
.window-controls,
.chrome-btn,
.menu-label,
.caption-btn {
  --wails-draggable: no-drag;
}

.chrome-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--muted-foreground);
  cursor: pointer;
}

.chrome-btn:hover:not(:disabled) {
  background: rgb(0 0 0 / 6%);
  color: var(--foreground);
}

.chrome-btn.is-disabled,
.chrome-btn:disabled {
  opacity: 0.35;
  cursor: default;
}

.menu-label {
  height: 26px;
  padding: 0 8px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--muted-foreground);
  font-size: 12px;
  line-height: 26px;
  cursor: default;
}

.menu-label:hover,
.menu-label[data-state='open'] {
  background: rgb(0 0 0 / 6%);
  color: var(--foreground);
}

.caption-btn {
  display: inline-flex;
  width: 46px;
  height: 100%;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--foreground);
  cursor: pointer;
}

.caption-btn:hover {
  background: rgb(0 0 0 / 6%);
}

.caption-close:hover {
  background: #e81123;
  color: #fff;
}

:root[data-theme='dark'] .chrome-btn:hover:not(:disabled),
:root[data-theme='dark'] .menu-label:hover,
:root[data-theme='dark'] .menu-label[data-state='open'],
:root[data-theme='dark'] .caption-btn:hover {
  background: rgb(255 255 255 / 8%);
}

:root[data-theme='dark'] .caption-close:hover {
  background: #e81123;
  color: #fff;
}
</style>

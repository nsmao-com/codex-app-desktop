<script setup lang="ts">
import {
  Archive,
  Blocks,
  ChevronDown,
  ChevronRight,
  Coins,
  Columns2,
  Columns3,
  Columns4,
  Copy,
  Folder,
  FolderOpen,
  GripVertical,
  LoaderCircle,
  LogIn,
  LogOut,
  MessageSquareText,
  MoreHorizontal,
  Pencil,
  Pin,
  Plus,
  RefreshCw,
  Search,
  Settings,
  SlidersHorizontal,
  Trash2,
  X,
} from '@lucide/vue'
import { Motion } from 'motion-v'
import { useRouter } from 'vue-router'
import { computed, nextTick, onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import ClaudeIcon from '@/components/icons/ClaudeIcon.vue'
import GrokIcon from '@/components/icons/GrokIcon.vue'
import GeminiIcon from '@/components/icons/GeminiIcon.vue'
import OpenCodeIcon from '@/components/icons/OpenCodeIcon.vue'
import OpenAIIcon from '@/components/icons/OpenAIIcon.vue'
import { springPanel, springSnappy } from '@/lib/motion'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Progress } from '@/components/ui/progress'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  SimpleTooltip,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import type { ThreadGroup, ThreadSummary } from '@/types/codex'
import {
  buildUsageRangeView,
  formatTokenCount,
  formatUsageDateLabel,
  type UsageRangeDays,
} from '@/utils/accountUsage'
import { notify } from '@/utils/notify'
import { sameWorkspacePath } from '@/utils/workspacePath'

const router = useRouter()
const appStore = useAppStore()
const arenaStore = useArenaStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const { locale, t } = useI18n()
let externalSearchTimer = 0
const sidebarActionRuntime = computed(() => arenaStore.isArenaMode
  ? (arenaStore.focusedPane?.runtime || appStore.activeRuntime)
  : appStore.activeRuntime)
const sidebarIsCodexMode = computed(() => sidebarActionRuntime.value === 'codex')
const sidebarIsClaudeMode = computed(() => sidebarActionRuntime.value === 'claude')
const sidebarIsGrokMode = computed(() => sidebarActionRuntime.value === 'grok')
const sidebarIsGeminiMode = computed(() => sidebarActionRuntime.value === 'gemini')
const sidebarIsOpenCodeMode = computed(() => sidebarActionRuntime.value === 'opencode')
const geminiRuntimeName = computed(() => appStore.runtimeDisplayName('gemini'))

onBeforeUnmount(() => {
  if (externalSearchTimer) window.clearTimeout(externalSearchTimer)
  endProjectDrag()
})

const props = defineProps<{
  collapsed?: boolean
  mobile?: boolean
}>()

const emit = defineEmits<{
  'toggle-sidebar': []
}>()

const usagePopoverOpen = shallowRef(false)
const usageRangeDays = shallowRef<UsageRangeDays>(7)
const usageLoading = shallowRef(false)

function openUsageFromCommand(event: Event): void {
  usagePopoverOpen.value = true
  const range = (event as CustomEvent<{ range?: string }>).detail?.range
  if (range === 'today') usageRangeDays.value = 1
  else if (range === 'week') usageRangeDays.value = 7
  else if (range === 'cumulative') usageRangeDays.value = 'cumulative'
}

onMounted(() => {
  window.addEventListener('nice-codex:open-usage', openUsageFromCommand)
})

onBeforeUnmount(() => {
  window.removeEventListener('nice-codex:open-usage', openUsageFromCommand)
})

const usageRanges = computed(() => ([
  { days: 1 as const, label: t('sidebar.usageToday') },
  { days: 7 as const, label: t('sidebar.usageWeek') },
  { days: 14 as const, label: t('sidebar.usageTwoWeeks') },
  { days: 30 as const, label: t('sidebar.usageMonth') },
  { days: 'cumulative' as const, label: t('sidebar.usageCumulative') },
]))

const usageRangeView = computed(() => buildUsageRangeView(appStore.accountUsage, usageRangeDays.value))
const usageRangeTotalLabel = computed(() => usageRangeDays.value === 'cumulative'
  ? formatTokenCount(appStore.accountUsage?.lifetimeTokens)
  : formatTokenCount(usageRangeView.value.totalTokens))
const usageRangeMeta = computed(() => usageRangeDays.value === 'cumulative'
  ? t('sidebar.usageAggregateMeta', { count: formatTokenCount(appStore.accountUsage?.lifetimeTokens) })
  : usageRangeView.value.dayCount
    ? t('sidebar.usageRangeMeta', {
      days: usageRangeView.value.days,
      avg: formatTokenCount(usageRangeView.value.averageTokens),
      count: usageRangeView.value.dayCount,
    })
    : t('sidebar.usageRangeMeta', { days: usageRangeView.value.days, avg: '—', count: 0 }))
const usageLocale = computed(() => (locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'))
const usageSubtitle = computed(() => {
  if (sidebarIsGrokMode.value) return t('sidebar.usageSubtitleGrok')
  if (sidebarIsClaudeMode.value) return t('sidebar.usageSubtitleClaude')
  if (sidebarIsGeminiMode.value) return t('sidebar.usageSubtitleGemini')
  if (sidebarIsOpenCodeMode.value) return t('sidebar.usageSubtitleOpenCode')
  return t('sidebar.usageSubtitle')
})

watch(usagePopoverOpen, (open) => {
  if (!open) return
  usageLoading.value = true
  // Grok/Claude: local usage.json only. Codex may also seed from cloud after auth.
  const localOnly = !sidebarIsCodexMode.value || !appStore.account.authenticated
  const refresh = localOnly
    ? appStore.loadLocalUsage()
    : appStore.refreshAccountData()
      .catch(() => undefined)
      .then(() => appStore.loadLocalUsage())
  void refresh.finally(() => {
    usageLoading.value = false
  })
})

// Keep runtime-scoped local totals warm when switching.
watch(
  sidebarActionRuntime,
  () => {
    void appStore.loadLocalUsage().catch(() => undefined)
  },
)

const sidebarMotion = computed(() => {
  if (props.mobile) {
    return {
      width: 276,
      x: props.collapsed ? -288 : 0,
      opacity: props.collapsed ? 0 : 1,
    }
  }
  return {
    width: props.collapsed ? 0 : 276,
    x: 0,
    opacity: props.collapsed ? 0 : 1,
  }
})

const search = computed({
  get: () => {
    if (sidebarIsGrokMode.value) return grokStore.search
    if (sidebarIsClaudeMode.value) return claudeStore.search
    return codexStore.threadSearch
  },
  set: (value: string) => {
    if (sidebarIsGrokMode.value) {
      grokStore.search = value
      if (externalSearchTimer) window.clearTimeout(externalSearchTimer)
      externalSearchTimer = window.setTimeout(() => {
        if (sidebarIsGrokMode.value) void grokStore.loadSessions()
      }, 250)
      return
    }
    if (sidebarIsClaudeMode.value) {
      claudeStore.search = value
      if (externalSearchTimer) window.clearTimeout(externalSearchTimer)
      externalSearchTimer = window.setTimeout(() => {
        if (sidebarIsClaudeMode.value) void claudeStore.loadSessions()
      }, 250)
      return
    }
    codexStore.setSearch(value)
  },
})

function sidebarRuntimeWorkspacePath(runtime: WorkspaceRuntime): string {
  if (runtime === 'gemini') return appStore.settings.geminiWorkspace || appStore.settings.workspace
  if (runtime === 'opencode') return appStore.settings.openCodeWorkspace || appStore.settings.workspace
  return appStore.settings.workspace
}

function sidebarRuntimeRecentWorkspacePaths(runtime: WorkspaceRuntime): string[] {
  if (runtime === 'gemini') {
    return appStore.settings.geminiRecentWorkspaces?.length
      ? appStore.settings.geminiRecentWorkspaces
      : (appStore.settings.recentWorkspaces ?? [])
  }
  if (runtime === 'opencode') {
    return appStore.settings.openCodeRecentWorkspaces?.length
      ? appStore.settings.openCodeRecentWorkspaces
      : (appStore.settings.recentWorkspaces ?? [])
  }
  return appStore.settings.recentWorkspaces ?? []
}

const sidebarNewTaskWorkspacePaths = computed(() => {
  const runtime = sidebarActionRuntime.value
  const current = sidebarRuntimeWorkspacePath(runtime)
  const recent = sidebarRuntimeRecentWorkspacePaths(runtime)
  return appStore.orderWorkspacePaths(runtime, [current, ...recent], recent)
    .filter((path, index, paths) =>
      Boolean(path && path !== '(unknown)')
      && paths.findIndex((candidate) => sameWorkspacePath(candidate, path)) === index,
    )
})

function sidebarWorkspaceName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '')
  return clean.split(/[\\/]/).filter(Boolean).at(-1) || path
}

const sidebarThreadGroups = computed<ThreadGroup[]>(() => {
  if (!arenaStore.isArenaMode) return codexStore.filteredThreadGroups
  const runtime = sidebarActionRuntime.value
  if (runtime !== 'codex' && runtime !== 'gemini' && runtime !== 'opencode') return []
  const workspace = sidebarRuntimeWorkspacePath(runtime)
  const recent = sidebarRuntimeRecentWorkspacePaths(runtime)
  const paths = appStore.orderWorkspacePaths(runtime, [workspace, ...recent], recent)
  const query = codexStore.threadSearch.trim().toLocaleLowerCase()
  return paths
    .map((path) => {
      const group = codexStore.arenaThreadGroups.find((item) => sameWorkspacePath(item.path, path))
      if (!group) return null
      const threads = group.threads.filter((thread) => codexStore.knownRuntimeIDForThread(thread.id) === runtime)
      const matchesGroup = `${group.name} ${group.path}`.toLocaleLowerCase().includes(query)
      return {
        ...group,
        active: sameWorkspacePath(group.path, workspace),
        threads: !query || matchesGroup
          ? threads
          : threads.filter((thread) =>
              `${thread.name} ${thread.preview}`.toLocaleLowerCase().includes(query),
            ),
      }
    })
    .filter((group): group is ThreadGroup => Boolean(group && (!query || group.threads.length > 0)))
})

function visibleCodexDraft(thread: ThreadSummary): boolean {
  if (!thread.id.startsWith('pending-thread-')) return true
  return Boolean(
    (codexStore.itemsByThread[thread.id] ?? []).length
    || (codexStore.queuedMessagesByThread[thread.id] ?? []).length
    || codexStore.threadHasActiveWork(thread.id),
  )
}

function visibleClaudeDraft(sessionId: string): boolean {
  if (!sessionId.startsWith('pending-claude-')) return true
  return Boolean(
    (claudeStore.itemsBySession[sessionId] ?? []).length
    || (claudeStore.queueBySession[sessionId] ?? []).length
    || claudeStore.isSessionBusy(sessionId),
  )
}

const groups = computed<ThreadGroup[]>(() => sidebarThreadGroups.value.map((group) => ({
  ...group,
  threads: group.threads.filter(visibleCodexDraft),
})))
const grokGroups = computed(() => grokStore.sessionGroups)
const claudeGroups = computed(() => claudeStore.sessionGroups.map((group) => ({
  ...group,
  sessions: group.sessions.filter((session) => visibleClaudeDraft(session.id)),
})))

const threadCount = computed(() => {
  if (sidebarIsGrokMode.value) {
    return grokGroups.value.reduce((total, group) => total + group.sessions.length, 0)
  }
  if (sidebarIsClaudeMode.value) {
    return claudeGroups.value.reduce((total, group) => total + group.sessions.length, 0)
  }
  return groups.value.reduce((total, group) => total + group.threads.length, 0)
})
const usesCodexTimeline = computed(() => sidebarIsCodexMode.value || sidebarIsGeminiMode.value || sidebarIsOpenCodeMode.value)
const sidebarNewSessionDisabled = computed(() => {
  if (workspaceStore.switchingWorkspace || codexStore.creatingThread) return true
  const runtime = sidebarActionRuntime.value
  if (runtime === 'codex') return !appStore.codexAvailable
  if (runtime === 'gemini' || runtime === 'opencode') {
    return !appStore.providerForRuntime(runtime)?.runtimeReady
  }
  return false
})
const activeSidebarSessionId = computed(() => {
  if (arenaStore.isArenaMode) {
    const pane = arenaStore.focusedPane
    return pane ? arenaStore.sessionForPane(pane.id) : ''
  }
  if (sidebarIsGrokMode.value) return grokStore.activeSessionId
  if (sidebarIsClaudeMode.value) return claudeStore.activeSessionId
  return codexStore.activeThreadId
})
const sidebarNewSessionActive = computed(() => {
  const sessionId = activeSidebarSessionId.value
  if (!sessionId) return true
  if (sidebarIsGrokMode.value) return sessionId.startsWith('pending-grok-')
  if (sidebarIsClaudeMode.value) return sessionId.startsWith('pending-claude-')
  return sessionId.startsWith('pending-thread-')
})

function isActiveSidebarSession(runtime: WorkspaceRuntime, sessionId: string): boolean {
  const active = activeSidebarSessionId.value
  if (!active) return false
  if (runtime === 'grok') return grokStore.sameSession(active, sessionId)
  if (runtime === 'claude') return claudeStore.sameSession(active, sessionId)
  return codexStore.sameThread(active, sessionId)
}

function isGrokSessionRunning(sessionId: string): boolean {
  return grokStore.runningSessionIds.some((id) => grokStore.sameSession(id, sessionId))
}

function isClaudeSessionRunning(sessionId: string): boolean {
  return claudeStore.runningSessionIds.some((id) => claudeStore.sameSession(id, sessionId))
}

function isCodexThreadRunning(threadId: string): boolean {
  return codexStore.threadHasActiveWork(threadId)
}
const creatingInProject = shallowRef('')
const renamingThreadId = shallowRef('')
const renamingRuntime = shallowRef<WorkspaceRuntime | ''>('')
const renameDraft = shallowRef('')

function formatUpdated(timestamp: number): string {
  if (!timestamp) return ''
  const difference = Date.now() - timestamp * 1000
  const minutes = Math.floor(difference / 60_000)
  if (minutes < 1) return t('sidebar.now')
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  const date = new Date(timestamp * 1000)
  // Compact numeric form avoids CJK “7月23日” wrapping vertically in the narrow column.
  return `${date.getMonth() + 1}/${date.getDate()}`
}

function openSettings(): void {
  router.push('/settings')
}

function openCapabilities(): void {
  router.push('/capabilities')
}

async function setWorkMode(mode: 'code' | 'cowork'): Promise<void> {
  if (!sidebarIsCodexMode.value) return
  if (appStore.settings.workMode === mode) return
  const previous = { ...appStore.settings }
  const next = {
    ...appStore.settings,
    workMode: mode,
    collaborationMode: mode === 'cowork' && appStore.settings.collaborationMode === 'default'
      ? 'plan'
      : appStore.settings.collaborationMode,
  }
  appStore.settings = next
  try {
    await appStore.savePreferences(next, { silent: true })
    await codexStore.switchWorkMode()
  } catch {
    appStore.settings = previous
    await appStore.savePreferences(previous, { silent: true }).catch(() => undefined)
  }
}

async function setActiveRuntime(runtime: WorkspaceRuntime): Promise<void> {
  if (arenaStore.isArenaMode) {
    const focused = arenaStore.focusedPane
    const target = focused?.runtime === runtime
      ? focused
      : arenaStore.panes.find((pane) => pane.runtime === runtime)
    if (target) {
      if (arenaStore.focusedPaneId === target.id) arenaStore.requestFocusedPaneActivation()
      else arenaStore.focusPane(target.id)
    }
    else if (focused) arenaStore.setPaneRuntime(focused.id, runtime)
    return
  }
  if (appStore.activeRuntime === runtime) return
  // Persist the runtime first; App.vue continues runtime-scoped hydration independently.
  await appStore.setActiveRuntime(runtime)
}

const arenaContextMenu = shallowRef<{ x: number; y: number; runtime: WorkspaceRuntime } | null>(null)

function openArenaContextMenu(event: MouseEvent, runtime: WorkspaceRuntime): void {
  arenaContextMenu.value = {
    x: event.clientX,
    y: event.clientY,
    runtime,
  }
}

function closeArenaContextMenu(): void {
  arenaContextMenu.value = null
}

function activeSessionForArenaSeed(runtime: WorkspaceRuntime): string {
  if (runtime !== appStore.activeRuntime) return ''
  if (runtime === 'grok') return grokStore.activeSessionId
  if (runtime === 'claude') return claudeStore.activeSessionId
  const threadId = codexStore.activeThreadId
  return threadId && codexStore.runtimeIDForThread(threadId) === runtime ? threadId : ''
}

function startArena(columns: 2 | 3 | 4 | 6 | 8): void {
  const seed = arenaContextMenu.value?.runtime || appStore.activeRuntime
  const sessionId = activeSessionForArenaSeed(seed)
  arenaStore.openArena(columns, seed)
  if (sessionId && arenaStore.panes[0]) {
    arenaStore.setPaneSession(arenaStore.panes[0].id, sessionId)
  }
  closeArenaContextMenu()
  void setActiveRuntime(seed)
}

function startArenaSameProvider(columns: 2 | 3 | 4): void {
  const seed = arenaContextMenu.value?.runtime || appStore.activeRuntime
  const sessionId = activeSessionForArenaSeed(seed)
  arenaStore.openArena(2, seed)
  // Force every pane to the same provider so multi-tab same-model works.
  for (const pane of arenaStore.panes) {
    arenaStore.setPaneRuntime(pane.id, seed)
  }
  while (arenaStore.panes.length < columns && arenaStore.canAddPane) {
    arenaStore.addPane(seed)
  }
  if (sessionId && arenaStore.panes[0]) {
    arenaStore.setPaneSession(arenaStore.panes[0].id, sessionId)
  }
  closeArenaContextMenu()
  void setActiveRuntime(seed)
}

function onGlobalPointerDown(event: PointerEvent): void {
  if (!arenaContextMenu.value) return
  const target = event.target as HTMLElement | null
  if (target?.closest?.('[data-arena-context-menu]')) return
  closeArenaContextMenu()
}

onMounted(() => {
  window.addEventListener('pointerdown', onGlobalPointerDown, true)
  window.addEventListener('keydown', onArenaMenuEscape, true)
})
onBeforeUnmount(() => {
  window.removeEventListener('pointerdown', onGlobalPointerDown, true)
  window.removeEventListener('keydown', onArenaMenuEscape, true)
})

function onArenaMenuEscape(event: KeyboardEvent): void {
  if (event.key === 'Escape') closeArenaContextMenu()
}

const claudeProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'claude'))
const activeExternalProvider = computed(() => appStore.providerForRuntime(sidebarActionRuntime.value))

function runtimeSlideX(): string {
  if (sidebarIsClaudeMode.value) return '100%'
  if (sidebarIsGrokMode.value) return '200%'
  if (sidebarIsGeminiMode.value) return '300%'
  if (sidebarIsOpenCodeMode.value) return '400%'
  return '0%'
}

function visibleClaudeSessions(group: { path: string; sessions: Array<{
  id: string
  name?: string
  preview?: string
  model?: string
  updatedAt?: number
}> }) {
  if (search.value) return group.sessions
  const limit = visibleCounts.value[group.path] ?? 30
  return group.sessions.slice(0, limit)
}

/** Open a session; if it lives under another project folder, switch workspace first (Codex-style). */
async function openClaudeSession(group: { path: string; active: boolean }, sessionId: string): Promise<void> {
  void group
  if (bindFocusedArenaSession('claude', sessionId)) return
  await claudeStore.openSession(sessionId)
}

async function openGrokSession(group: { path: string; active: boolean }, sessionId: string): Promise<void> {
  void group
  if (bindFocusedArenaSession('grok', sessionId)) return
  await grokStore.openSession(sessionId)
}

async function createSidebarSession(requestedWorkspace = ''): Promise<void> {
  const runtime = sidebarActionRuntime.value
  const paneId = arenaStore.isArenaMode ? (arenaStore.focusedPane?.id || '') : ''
  const previousSessionId = paneId ? arenaStore.sessionForPane(paneId) : ''
  let workspacePath = requestedWorkspace.trim()
  if (workspacePath) {
    if (usesCodexTimeline.value) await codexStore.switchProject(workspacePath)
    else if (!await workspaceStore.useWorkspace(workspacePath)) return
  } else {
    workspacePath = usesCodexTimeline.value
      ? await codexStore.selectProject()
      : await workspaceStore.selectWorkspace()
  }
  if (!workspacePath || !sameWorkspacePath(workspacePath, appStore.currentWorkspacePath)) return
  if (!arenaTargetIsCurrent(paneId, runtime, previousSessionId)) return
  if (runtime === 'codex' && !codexStore.isRuntimeReady(runtime)) {
    await codexStore.connect(workspacePath)
    if (!codexStore.isRuntimeReady(runtime)) return
  }
  if (runtime === 'grok') {
    grokStore.newSession()
    bindFocusedArenaSession(runtime, '', paneId)
    return
  }
  if (runtime === 'claude') {
    const sessionId = claudeStore.newSession(arenaStore.isArenaMode)
    if (sessionId) bindFocusedArenaSession(runtime, sessionId, paneId)
    return
  }
  const thread = arenaStore.isArenaMode
    ? await codexStore.newRuntimeThread(runtime, true, workspacePath)
    : await codexStore.newRuntimeThread(runtime, false, workspacePath)
  if (thread?.id && arenaTargetIsCurrent(paneId, runtime, previousSessionId)) {
    bindFocusedArenaSession(runtime, thread.id, paneId)
  }
}

function bindFocusedArenaSession(
  runtime: WorkspaceRuntime,
  sessionId: string,
  targetPaneId = arenaStore.focusedPane?.id || '',
): boolean {
  if (!arenaStore.isArenaMode) return false
  const pane = arenaStore.panes.find((item) => item.id === targetPaneId)
  if (!pane) return false
  if (pane.runtime !== runtime) arenaStore.setPaneRuntime(pane.id, runtime)
  const previousOwner = arenaStore.selectPaneSession(pane.id, sessionId)
  if (previousOwner) {
    notify('info', t('arena.sessionInUseTitle'), t('arena.sessionInUseHint'))
  }
  return true
}

function arenaTargetIsCurrent(
  paneId: string,
  runtime: WorkspaceRuntime,
  expectedSessionId?: string,
): boolean {
  if (!paneId) return !arenaStore.isArenaMode && appStore.activeRuntime === runtime
  const pane = arenaStore.panes.find((item) => item.id === paneId)
  return Boolean(
    arenaStore.isArenaMode
    && pane
    && pane.runtime === runtime
    && arenaStore.focusedPaneId === paneId
    && (expectedSessionId === undefined || arenaStore.sessionForPane(paneId) === expectedSessionId)
  )
}

async function newInClaudeProject(group: { path: string; active: boolean }, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  if (!group.path || group.path === '(unknown)') return
  const paneId = arenaStore.isArenaMode ? (arenaStore.focusedPane?.id || '') : ''
  const previousSessionId = paneId ? arenaStore.sessionForPane(paneId) : ''
  const ready = appStore.activeRuntime === 'claude'
    ? await appStore.ensureActiveRuntimeSynced('claude')
    : await appStore.setActiveRuntime('claude')
  if (!ready || !arenaTargetIsCurrent(paneId, 'claude', previousSessionId)) return
  if (!group.active) {
    if (!await workspaceStore.useWorkspace(group.path)) return
  }
  if (!arenaTargetIsCurrent(paneId, 'claude', previousSessionId)) return
  const sessionId = claudeStore.newSession(arenaStore.isArenaMode)
  if (sessionId) bindFocusedArenaSession('claude', sessionId, paneId)
}

async function newInGrokProject(group: { path: string; active: boolean }, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  if (!group.path || group.path === '(unknown)') return
  const paneId = arenaStore.isArenaMode ? (arenaStore.focusedPane?.id || '') : ''
  const previousSessionId = paneId ? arenaStore.sessionForPane(paneId) : ''
  const ready = appStore.activeRuntime === 'grok'
    ? await appStore.ensureActiveRuntimeSynced('grok')
    : await appStore.setActiveRuntime('grok')
  if (!ready || !arenaTargetIsCurrent(paneId, 'grok', previousSessionId)) return
  if (!group.active) {
    if (!await workspaceStore.useWorkspace(group.path)) return
  }
  if (!arenaTargetIsCurrent(paneId, 'grok', previousSessionId)) return
  grokStore.newSession()
  bindFocusedArenaSession('grok', '', paneId)
}

async function archiveClaudeSession(sessionID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const resolved = claudeStore.resolveSessionId(sessionID)
  await claudeStore.archiveSession(sessionID)
  if (!claudeStore.sessions.some((item) => claudeStore.sameSession(item.id, resolved))) {
    arenaStore.clearSessionBindings('claude', [sessionID, resolved])
  }
}

async function deleteClaudeSession(sessionID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const resolved = claudeStore.resolveSessionId(sessionID)
  await claudeStore.deleteSession(sessionID)
  if (!claudeStore.sessions.some((item) => claudeStore.sameSession(item.id, resolved))) {
    arenaStore.clearSessionBindings('claude', [sessionID, resolved])
  }
}

function formatClaudeUpdated(value: number): string {
  return formatGrokUpdated(value)
}

async function openThread(group: ThreadGroup, thread: ThreadSummary): Promise<void> {
  const runtime = codexStore.knownRuntimeIDForThread(thread.id)
    || (usesCodexTimeline.value ? sidebarActionRuntime.value : 'codex')
  if (bindFocusedArenaSession(runtime, thread.id)) return
  if (appStore.activeRuntime !== runtime && !await appStore.setActiveRuntime(runtime)) return
  await codexStore.openProjectThread(group.path, thread.id)
}

function addArenaPaneFromMenu(): void {
  arenaStore.addPane(arenaContextMenu.value?.runtime || appStore.activeRuntime)
  closeArenaContextMenu()
}

async function archiveThread(threadID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const runtime = codexStore.runtimeIDForThread(threadID)
  const resolved = codexStore.resolveThreadID(threadID)
  await codexStore.archiveThread(threadID)
  const exists = codexStore.threads.some((item) => codexStore.sameThread(item.id, resolved))
    || Object.values(codexStore.projectThreads).flat().some((item) => codexStore.sameThread(item.id, resolved))
  if (!exists) arenaStore.clearSessionBindings(runtime, [threadID, resolved])
}

async function deleteThread(threadID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const runtime = codexStore.runtimeIDForThread(threadID)
  const resolved = codexStore.resolveThreadID(threadID)
  await codexStore.deleteThread(threadID)
  const exists = codexStore.threads.some((item) => codexStore.sameThread(item.id, resolved))
    || Object.values(codexStore.projectThreads).flat().some((item) => codexStore.sameThread(item.id, resolved))
  if (!exists) arenaStore.clearSessionBindings(runtime, [threadID, resolved])
}

function beginRename(thread: { id: string; name?: string }, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  renamingThreadId.value = thread.id
  renamingRuntime.value = sidebarActionRuntime.value
  renameDraft.value = thread.name || ''
  void nextTick(() => {
    const input = document.querySelector<HTMLInputElement>('[data-thread-rename-input]')
    input?.focus()
    input?.select()
  })
}

async function commitRename(thread: { id: string; name?: string }): Promise<void> {
  if (renamingThreadId.value !== thread.id) return
  const next = renameDraft.value.trim()
  const runtime = renamingRuntime.value
  renamingThreadId.value = ''
  renamingRuntime.value = ''
  if (!next || next === thread.name) return
  if (runtime === 'grok') {
    await grokStore.renameSession(thread.id, next)
    return
  }
  if (runtime === 'claude') {
    await claudeStore.renameSession(thread.id, next)
    return
  }
  await codexStore.renameThread(thread.id, next)
}

function cancelRename(): void {
  renamingThreadId.value = ''
  renamingRuntime.value = ''
  renameDraft.value = ''
}

async function archiveGrokSession(sessionID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const resolved = grokStore.resolveSessionId(sessionID)
  await grokStore.archiveSession(sessionID)
  if (!grokStore.sessions.some((item) => grokStore.sameSession(item.id, resolved))) {
    arenaStore.clearSessionBindings('grok', [sessionID, resolved])
  }
}

async function deleteGrokSession(sessionID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const resolved = grokStore.resolveSessionId(sessionID)
  await grokStore.deleteSession(sessionID)
  if (!grokStore.sessions.some((item) => grokStore.sameSession(item.id, resolved))) {
    arenaStore.clearSessionBindings('grok', [sessionID, resolved])
  }
}

async function forkThread(threadID: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  const runtime = codexStore.runtimeIDForThread(threadID)
  const paneId = arenaStore.isArenaMode ? (arenaStore.focusedPane?.id || '') : ''
  const previousSessionId = paneId ? arenaStore.sessionForPane(paneId) : ''
  const thread = await codexStore.forkThread(threadID, !arenaStore.isArenaMode)
  if (thread?.id && arenaTargetIsCurrent(paneId, runtime, previousSessionId)) {
    bindFocusedArenaSession(runtime, thread.id, paneId)
  }
}

async function newInProject(group: ThreadGroup, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  if (creatingInProject.value || codexStore.creatingThread) return
  creatingInProject.value = group.path
  try {
    // Keep the chosen project expanded; the draft itself appears only after send.
    setGroupCollapsed(group, false)
    const runtime = sidebarActionRuntime.value
    const paneId = arenaStore.isArenaMode ? (arenaStore.focusedPane?.id || '') : ''
    const previousSessionId = paneId ? arenaStore.sessionForPane(paneId) : ''
    const thread = arenaStore.isArenaMode
      ? await codexStore.newRuntimeThread(runtime, true, group.path)
      : await codexStore.newThreadInProject(group.path)
    if (thread?.id && arenaTargetIsCurrent(paneId, runtime, previousSessionId)) {
      bindFocusedArenaSession(runtime, thread.id, paneId)
    }
  } finally {
    creatingInProject.value = ''
  }
}

function togglePin(thread: ThreadSummary, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  codexStore.toggleThreadPin(thread.id)
}

function providerLabel(thread: ThreadSummary): string {
  const provider = thread.modelProvider.trim()
  const normalized = provider.toLocaleLowerCase()
  if (normalized === '__gemini__' || normalized === '__antigravity__' || normalized === 'gemini-cli' || normalized === 'antigravity' || normalized === 'antigravity-cli' || normalized === 'agy') return geminiRuntimeName.value
  if (normalized === '__opencode__' || normalized === 'opencode-cli') {
    const modelProvider = thread.model.includes('/') ? thread.model.split('/', 1)[0]?.trim() : ''
    return modelProvider ? `OpenCode · ${modelProvider}` : 'OpenCode'
  }
  if (normalized === '__claude__' || normalized === 'claude-cli') return 'Claude Code'
  if (normalized === '__grok__' || normalized === 'grok-cli') return 'Grok'
  if (provider) return provider
  if (sidebarIsGeminiMode.value) return geminiRuntimeName.value
  if (sidebarIsOpenCodeMode.value) return 'OpenCode'
  return 'Codex / OpenAI'
}

const isCollapsed = shallowRef<Record<string, boolean>>({})
const visibleCounts = shallowRef<Record<string, number>>({})
const draggingProjectPath = shallowRef('')
const draggingProjectRuntime = shallowRef<WorkspaceRuntime | ''>('')
const dragOverProjectPath = shallowRef('')
const dragOverPosition = shallowRef<'before' | 'after' | ''>('')

watch(
  sidebarActionRuntime,
  () => {
    endProjectDrag()
  },
)

function currentProjectPaths(runtime: WorkspaceRuntime): string[] {
  if (runtime === 'grok') return grokStore.sessionGroups.map((group) => group.path)
  if (runtime === 'claude') return claudeStore.sessionGroups.map((group) => group.path)
  return codexStore.threadGroups.map((group) => group.path)
}

function canDragProject(path: string): boolean {
  return !search.value.trim() && Boolean(path) && path !== '(unknown)'
}

function startProjectDrag(event: DragEvent, path: string): void {
  if (!canDragProject(path)) {
    event.preventDefault()
    return
  }
  draggingProjectPath.value = path
  draggingProjectRuntime.value = sidebarActionRuntime.value
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', path)
  }
}

function updateProjectDropTarget(event: DragEvent, path: string): void {
  if (
    !draggingProjectPath.value
    || draggingProjectRuntime.value !== sidebarActionRuntime.value
    || draggingProjectPath.value === path
  ) return
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  const element = event.currentTarget as HTMLElement | null
  if (!element) return
  const bounds = element.getBoundingClientRect()
  dragOverProjectPath.value = path
  dragOverPosition.value = event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
}

function clearProjectDropTarget(event: DragEvent, path: string): void {
  if (dragOverProjectPath.value !== path) return
  const element = event.currentTarget as HTMLElement | null
  const related = event.relatedTarget
  if (element && related instanceof Node && element.contains(related)) return
  dragOverProjectPath.value = ''
  dragOverPosition.value = ''
}

function dropProject(event: DragEvent, targetPath: string): void {
  event.preventDefault()
  event.stopPropagation()
  const runtime = draggingProjectRuntime.value
  const sourcePath = draggingProjectPath.value
  const position = dragOverPosition.value
  if (!runtime || runtime !== sidebarActionRuntime.value || !sourcePath || !position || sourcePath === targetPath) {
    endProjectDrag()
    return
  }
  const paths = currentProjectPaths(runtime)
  const sourceIndex = paths.indexOf(sourcePath)
  if (sourceIndex < 0 || !paths.includes(targetPath)) {
    endProjectDrag()
    return
  }
  const next = paths.filter((path) => path !== sourcePath)
  const targetIndex = next.indexOf(targetPath)
  next.splice(targetIndex + (position === 'after' ? 1 : 0), 0, sourcePath)
  appStore.setWorkspaceOrder(runtime, next)
  endProjectDrag()
}

function endProjectDrag(): void {
  draggingProjectPath.value = ''
  draggingProjectRuntime.value = ''
  dragOverProjectPath.value = ''
  dragOverPosition.value = ''
}

function moveProjectByKeyboard(path: string, direction: -1 | 1): void {
  if (!canDragProject(path)) return
  const runtime = sidebarActionRuntime.value
  const next = [...currentProjectPaths(runtime)]
  const sourceIndex = next.indexOf(path)
  const targetIndex = sourceIndex + direction
  if (sourceIndex < 0 || targetIndex < 0 || targetIndex >= next.length) return
  const target = next[targetIndex]
  if (!target) return
  next[sourceIndex] = target
  next[targetIndex] = path
  appStore.setWorkspaceOrder(runtime, next)
}

function projectDropClass(path: string): string {
  if (dragOverProjectPath.value === path) return 'ring-1 ring-inset ring-sidebar-border/80'
  if (draggingProjectPath.value === path) return 'opacity-60'
  return ''
}

function showProjectDropIndicator(path: string, position: 'before' | 'after'): boolean {
  return dragOverProjectPath.value === path && dragOverPosition.value === position
}

function isGroupCollapsed(group: { path: string, active: boolean }): boolean {
  if (search.value) return false
  return isCollapsed.value[group.path] ?? !group.active
}

function setGroupCollapsed(group: { path: string }, collapsed: boolean): void {
  isCollapsed.value = {
    ...isCollapsed.value,
    [group.path]: collapsed,
  }
}

function visibleThreads(group: ThreadGroup): ThreadSummary[] {
  const defaultLimit = group.active ? 40 : 20
  const limit = visibleCounts.value[group.path] ?? defaultLimit
  return search.value ? group.threads : group.threads.slice(0, limit)
}

function visibleGrokSessions(group: { path: string, active: boolean, sessions: Array<{
  id: string
  name?: string
  preview?: string
  model?: string
  backend?: string
  updatedAt?: number
}> }) {
  const defaultLimit = group.active ? 40 : 20
  const limit = visibleCounts.value[group.path] ?? defaultLimit
  return search.value ? group.sessions : group.sessions.slice(0, limit)
}

function loadMore(group: { path: string, active?: boolean }): void {
  const current = visibleCounts.value[group.path] ?? (group.active ? 40 : 20)
  visibleCounts.value = { ...visibleCounts.value, [group.path]: current + 30 }
}

function formatGrokUpdated(value?: number | null): string {
  // Grok timestamps may be unix seconds or milliseconds depending on source.
  if (value == null || !Number.isFinite(value) || value <= 0) return ''
  const ms = value > 1e12 ? value : value * 1000
  const difference = Date.now() - ms
  const minutes = Math.floor(difference / 60_000)
  if (minutes < 1) return t('sidebar.now')
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  const date = new Date(ms)
  return `${date.getMonth() + 1}/${date.getDate()}`
}
</script>

<template>
  <Motion
    as="aside"
    class="flex h-full shrink-0 flex-col overflow-hidden bg-transparent max-md:absolute max-md:inset-y-0 max-md:left-0 max-md:z-40 max-md:bg-sidebar max-md:shadow-xl max-md:backdrop-blur-md"
    :class="collapsed ? 'pointer-events-none' : 'pointer-events-auto'"
    :initial="false"
    :animate="sidebarMotion"
    :transition="springPanel"
  >
    <div class="flex h-full w-[276px] flex-col">
    <div class="flex h-12 items-center justify-between px-3.5">
      <div class="flex min-w-0 items-center gap-2.5">
        <img src="/nice-mark.svg" alt="" aria-hidden="true" width="28" height="28" class="size-7 shrink-0" />
        <div class="min-w-0">
          <div class="flex items-center gap-1.5">
            <span class="text-[13px] font-semibold tracking-tight">Nice Codex</span>
            <span class="rounded-md bg-muted/80 px-1 py-0.5 font-mono text-[9px] tabular-nums text-muted-foreground">v{{ appStore.appVersion }}</span>
          </div>
          <button
            v-if="appStore.updateInfo?.updateAvailable"
            type="button"
            class="text-[10px] text-primary hover:underline"
            @click="appStore.openUpdateCheckDialog"
          >
            {{ t('updates.availableShort', { version: appStore.updateInfo.latestVersion }) }}
          </button>
        </div>
      </div>
      <TooltipProvider v-if="mobile">
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('sidebar.toggle')" @click="emit('toggle-sidebar')">
              <X :size="14" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{{ t('sidebar.toggle') }}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>

    <div class="space-y-2 px-3 pb-1">
      <!-- Product runtime tabs stay icon-first so all five providers remain
           reachable without shrinking the hit targets. -->
      <TooltipProvider>
      <div class="relative grid grid-cols-5 rounded-lg bg-foreground/[0.08] p-0.5 ring-1 ring-foreground/[0.06] dark:bg-white/10 dark:ring-white/10">
        <Motion
          class="pointer-events-none absolute inset-y-0.5 left-0.5 w-[calc((100%-4px)/5)] rounded-md bg-background shadow-sm dark:bg-card"
          :initial="false"
          :animate="{ x: runtimeSlideX() }"
          :transition="springSnappy"
        />
        <Tooltip><TooltipTrigger as-child><Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 justify-center rounded-md px-1 hover:bg-transparent"
          :class="sidebarIsCodexMode
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          :aria-label="t('sidebar.runtimeCodex')"
          @click="void setActiveRuntime('codex')"
          @contextmenu.prevent="openArenaContextMenu($event, 'codex')"
        >
          <OpenAIIcon :size="13" class="shrink-0 opacity-90" />
        </Button></TooltipTrigger><TooltipContent side="bottom">{{ t('arena.tabTip', { name: 'Codex' }) }}</TooltipContent></Tooltip>
        <Tooltip><TooltipTrigger as-child><Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 justify-center rounded-md px-1 hover:bg-transparent"
          :class="sidebarIsClaudeMode
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          :aria-label="t('sidebar.runtimeClaude')"
          @click="void setActiveRuntime('claude')"
          @contextmenu.prevent="openArenaContextMenu($event, 'claude')"
        >
          <ClaudeIcon :size="13" class="shrink-0 opacity-90" />
        </Button></TooltipTrigger><TooltipContent side="bottom">{{ t('arena.tabTip', { name: 'Claude' }) }}</TooltipContent></Tooltip>
        <Tooltip><TooltipTrigger as-child><Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 justify-center rounded-md px-1 hover:bg-transparent"
          :class="sidebarIsGrokMode
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          :aria-label="t('sidebar.runtimeGrok')"
          @click="void setActiveRuntime('grok')"
          @contextmenu.prevent="openArenaContextMenu($event, 'grok')"
        >
          <GrokIcon :size="13" class="shrink-0 opacity-90" />
        </Button></TooltipTrigger><TooltipContent side="bottom">{{ t('arena.tabTip', { name: 'Grok' }) }}</TooltipContent></Tooltip>
        <Tooltip><TooltipTrigger as-child><Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 justify-center rounded-md px-1 hover:bg-transparent"
          :class="sidebarIsGeminiMode ? 'font-medium text-foreground' : 'text-muted-foreground hover:text-foreground'"
          :aria-label="geminiRuntimeName"
          @click="void setActiveRuntime('gemini')"
          @contextmenu.prevent="openArenaContextMenu($event, 'gemini')"
        >
          <GeminiIcon :size="13" class="shrink-0 opacity-90" />
        </Button></TooltipTrigger><TooltipContent side="bottom">{{ t('arena.tabTip', { name: geminiRuntimeName }) }}</TooltipContent></Tooltip>
        <Tooltip><TooltipTrigger as-child><Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 justify-center rounded-md px-1 hover:bg-transparent"
          :class="sidebarIsOpenCodeMode ? 'font-medium text-foreground' : 'text-muted-foreground hover:text-foreground'"
          aria-label="OpenCode"
          @click="void setActiveRuntime('opencode')"
          @contextmenu.prevent="openArenaContextMenu($event, 'opencode')"
        >
          <OpenCodeIcon :size="13" class="shrink-0 opacity-90" />
        </Button></TooltipTrigger><TooltipContent side="bottom">{{ t('arena.tabTip', { name: 'OpenCode' }) }}</TooltipContent></Tooltip>
      </div>
      </TooltipProvider>

      <!-- Codex-only work mode (code / writing) — same equal-track math as runtime tabs. -->
      <div
        v-if="sidebarIsCodexMode"
        class="relative grid grid-cols-2 rounded-lg bg-foreground/[0.06] p-0.5 ring-1 ring-foreground/[0.05] dark:bg-white/[0.06]"
      >
        <Motion
          class="pointer-events-none absolute inset-y-0.5 left-0.5 w-[calc((100%-4px)/2)] rounded-md bg-background shadow-sm dark:bg-card"
          :initial="false"
          :animate="{ x: appStore.settings.workMode === 'cowork' ? '100%' : '0%' }"
          :transition="springSnappy"
        />
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-7 rounded-md text-[11px] hover:bg-transparent"
          :class="appStore.settings.workMode !== 'cowork'
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          @click="void setWorkMode('code')"
        >
          {{ t('sidebar.code') }}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-7 rounded-md text-[11px] hover:bg-transparent"
          :class="appStore.settings.workMode === 'cowork'
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          @click="void setWorkMode('cowork')"
        >
          {{ t('sidebar.cowork') }}
        </Button>
      </div>

      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button
            variant="ghost"
            class="h-9 w-full justify-start rounded-lg px-2.5 text-[13px] font-medium text-sidebar-foreground shadow-none hover:bg-sidebar-accent/60 disabled:opacity-60"
            :class="sidebarNewSessionActive ? 'bg-sidebar-accent/80 ring-1 ring-foreground/[0.035]' : ''"
            :disabled="sidebarNewSessionDisabled"
            :aria-label="t('sidebar.newTask')"
          >
            <LoaderCircle v-if="workspaceStore.switchingWorkspace || (usesCodexTimeline && codexStore.creatingThread)" :size="15" class="mr-2 animate-spin" />
            <Plus v-else :size="15" class="mr-2" stroke-width="1.8" />
            <span class="min-w-0 flex-1 truncate text-left">
              {{ workspaceStore.switchingWorkspace || (usesCodexTimeline && codexStore.creatingThread) ? t('common.loading') : t('sidebar.newTask') }}
            </span>
            <ChevronDown :size="13" class="ml-2 shrink-0 opacity-50" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" side="bottom" :side-offset="6" class="w-72 rounded-xl p-1.5 shadow-xl">
          <div class="px-2 pb-1.5 pt-1 text-[10px] font-medium uppercase tracking-[0.06em] text-muted-foreground">
            {{ t('sidebar.chooseFolder') }}
          </div>
          <DropdownMenuItem
            v-for="path in sidebarNewTaskWorkspacePaths"
            :key="path"
            class="gap-2 rounded-lg py-2"
            @select="void createSidebarSession(path)"
          >
            <Folder :size="14" class="shrink-0 text-muted-foreground" />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-xs font-medium">{{ sidebarWorkspaceName(path) }}</span>
              <span class="block truncate text-[10px] text-muted-foreground">{{ path }}</span>
            </span>
          </DropdownMenuItem>
          <DropdownMenuSeparator v-if="sidebarNewTaskWorkspacePaths.length" />
          <DropdownMenuItem class="gap-2 rounded-lg py-2" @select="void createSidebarSession()">
            <FolderOpen :size="14" class="shrink-0 text-muted-foreground" />
            <span class="text-xs font-medium">{{ t('sidebar.chooseFolder') }}</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Button
        variant="ghost"
        class="h-8 w-full justify-start rounded-lg px-2 text-xs text-muted-foreground hover:bg-sidebar-accent/60"
        @click="openCapabilities"
      >
        <SlidersHorizontal :size="14" class="mr-2 opacity-70" />
        {{ t('sidebar.customize') }}
      </Button>
    </div>

    <div class="flex items-center justify-between px-4 pb-1 pt-3">
      <span class="text-[10px] font-medium uppercase tracking-[0.06em] text-muted-foreground/80">{{ t('sidebar.recents') }}</span>
      <span class="rounded-full bg-muted/70 px-1.5 py-0.5 text-[10px] tabular-nums text-muted-foreground">{{ threadCount }}</span>
    </div>

    <div class="px-3 pb-2">
      <div class="relative">
        <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground/70" />
        <Input
          v-model="search"
          type="search"
          :placeholder="t('sidebar.searchPlaceholder')"
          class="h-8 rounded-lg border-transparent bg-muted/40 pl-8 text-xs shadow-none focus-visible:border-transparent focus-visible:bg-background"
        />
      </div>
    </div>

    <ScrollArea class="min-h-0 flex-1 px-2">
      <div class="space-y-1.5 pb-3">
        <!-- Grok sessions: project-grouped like Codex -->
        <template v-if="sidebarIsGrokMode">
          <Collapsible
            v-for="group in grokGroups"
            :key="`grok-${group.path}`"
            :open="!isGroupCollapsed(group)"
            @update:open="(open) => setGroupCollapsed(group, !open)"
          >
            <div class="sticky top-0 z-20 bg-sidebar">
              <div
                class="group/project relative flex items-center gap-0.5 rounded-lg px-1 transition-colors"
                :class="[
                  group.active ? 'text-foreground' : 'hover:bg-sidebar-accent/25',
                  projectDropClass(group.path),
                ]"
                @dragover="(event: DragEvent) => updateProjectDropTarget(event, group.path)"
                @dragleave="(event: DragEvent) => clearProjectDropTarget(event, group.path)"
                @drop="(event: DragEvent) => dropProject(event, group.path)"
              >
              <span
                v-if="showProjectDropIndicator(group.path, 'before')"
                aria-hidden="true"
                class="pointer-events-none absolute inset-x-1 top-0 z-10 h-0.5 -translate-y-1/2 rounded-full bg-foreground/70"
              />
              <span
                v-if="showProjectDropIndicator(group.path, 'after')"
                aria-hidden="true"
                class="pointer-events-none absolute inset-x-1 bottom-0 z-10 h-0.5 translate-y-1/2 rounded-full bg-foreground/70"
              />
              <TooltipProvider v-if="canDragProject(group.path)">
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      draggable="true"
                      class="absolute right-8 z-10 size-6 shrink-0 cursor-grab rounded-md text-muted-foreground/70 opacity-0 transition-opacity group-hover/project:opacity-60 hover:opacity-100 focus-visible:opacity-100 active:cursor-grabbing"
                      :aria-label="t('sidebar.reorderProject', { name: group.name })"
                      @click.stop.prevent
                      @dragstart="(event: DragEvent) => startProjectDrag(event, group.path)"
                      @dragend="endProjectDrag"
                      @keydown.up.stop.prevent="moveProjectByKeyboard(group.path, -1)"
                      @keydown.down.stop.prevent="moveProjectByKeyboard(group.path, 1)"
                    >
                      <GripVertical :size="13" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{{ t('sidebar.reorderProject', { name: group.name }) }}</TooltipContent>
                </Tooltip>
              </TooltipProvider>
              <CollapsibleTrigger as-child>
                <button
                  type="button"
                  class="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left text-[11.5px] font-medium text-muted-foreground transition-colors hover:text-foreground"
                >
                  <ChevronRight
                    :size="12"
                    class="shrink-0 opacity-50 transition-transform duration-200"
                    :class="{ 'rotate-90': !isGroupCollapsed(group) }"
                  />
                  <SimpleTooltip :content="group.path">
                    <span class="min-w-0 truncate" :class="group.active ? 'text-foreground' : ''">{{ group.name }}</span>
                  </SimpleTooltip>
                </button>
              </CollapsibleTrigger>
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 shrink-0 rounded-md opacity-0 transition-opacity group-hover/project:opacity-100 focus-visible:opacity-100"
                      :aria-label="t('sidebar.newTaskInProject')"
                      @click="(event: MouseEvent) => void newInGrokProject(group, event)"
                    >
                      <Plus :size="12" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{{ t('sidebar.newTaskInProject') }}</TooltipContent>
                </Tooltip>
              </TooltipProvider>
              </div>
            </div>

            <CollapsibleContent>
              <div class="space-y-0.5 py-0.5 pl-2">
                <div
                  v-for="session in visibleGrokSessions(group)"
                  :key="session.id"
                  class="group/thread relative"
                >
                  <div
                    role="button"
                    tabindex="0"
                    class="flex h-8 w-full cursor-pointer items-center gap-2 rounded-lg px-2 text-left text-xs transition-colors"
                    :class="isActiveSidebarSession('grok', session.id)
                      ? 'bg-sidebar-accent/80 text-accent-foreground'
                      : 'hover:bg-sidebar-accent/50'"
                    @click="renamingThreadId === session.id ? undefined : openGrokSession(group, session.id)"
                    @dblclick.stop="beginRename(session)"
                    @keydown.enter.prevent="renamingThreadId === session.id ? undefined : openGrokSession(group, session.id)"
                  >
                    <SimpleTooltip
                      :content="isGrokSessionRunning(session.id)
                        ? t('sidebar.runningInBackground')
                        : (session.model || 'Grok')"
                    >
                      <span class="relative grid size-3 shrink-0 place-items-center">
                        <span
                          class="size-2 rounded-full border border-muted-foreground/45"
                          :class="isGrokSessionRunning(session.id) ? 'border-emerald-500 bg-emerald-500' : ''"
                        />
                        <span v-if="isGrokSessionRunning(session.id)" class="absolute inset-0 animate-ping rounded-full bg-emerald-400/35" />
                      </span>
                    </SimpleTooltip>
                    <span class="flex min-w-0 flex-1 items-center gap-1 pr-1">
                        <Input
                          v-if="renamingThreadId === session.id"
                          data-thread-rename-input
                          v-model="renameDraft"
                          class="h-6 rounded-md px-1.5 text-[11px] font-medium"
                          maxlength="80"
                          :aria-label="t('threadActions.rename')"
                          @click.stop
                          @keydown.enter.prevent="commitRename(session)"
                          @keydown.esc.prevent="cancelRename"
                          @blur="commitRename(session)"
                        />
                        <SimpleTooltip v-else :content="t('sidebar.renameHint')">
                          <span class="min-w-0 flex-1 truncate leading-5 text-foreground/90">
                            {{ session.name || session.id }}
                          </span>
                        </SimpleTooltip>
                    </span>
                    <span
                      v-if="renamingThreadId !== session.id"
                      class="relative flex h-5 w-10 shrink-0 items-center justify-end"
                      :class="{ 'text-accent-foreground/70': isActiveSidebarSession('grok', session.id) }"
                    >
                      <span
                        v-if="grokStore.isSessionLoading(session.id)"
                        class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                      />
                      <span
                        v-else
                        class="whitespace-nowrap text-[10px] tabular-nums leading-none text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
                      >
                        {{ formatGrokUpdated(session.updatedAt) }}
                      </span>
                    </span>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger as-child>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="absolute right-1 top-1 size-6 rounded-md text-muted-foreground opacity-0 transition-opacity group-hover/thread:opacity-100 group-focus-within/thread:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100"
                        :aria-label="t('threadActions.title')"
                        :disabled="Boolean(grokStore.sessionMutationForSession(session.id))
                          || renamingThreadId === session.id
                          || isGrokSessionRunning(session.id)"
                        @click.stop
                      >
                        <MoreHorizontal :size="13" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" class="min-w-40">
                      <DropdownMenuItem
                        :disabled="Boolean(grokStore.sessionMutationForSession(session.id))"
                        @click="(event: Event) => beginRename(session, event)"
                      >
                        <Pencil :size="14" class="mr-2" />
                        {{ t('threadActions.rename') }}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        :disabled="Boolean(grokStore.sessionMutationForSession(session.id))
                          || session.id.startsWith('pending-grok-')
                          || isGrokSessionRunning(session.id)"
                        @click="(event: Event) => archiveGrokSession(session.id, event)"
                      >
                        <Archive :size="14" class="mr-2" />
                        {{ t('threadActions.archive') }}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        class="text-destructive focus:text-destructive"
                        :disabled="Boolean(grokStore.sessionMutationForSession(session.id)) || isGrokSessionRunning(session.id)"
                        @click="(event: Event) => deleteGrokSession(session.id, event)"
                      >
                        <Trash2 :size="14" class="mr-2" />
                        {{ t('threadActions.delete') }}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>

                <Button
                  v-if="!search && group.sessions.length > visibleGrokSessions(group).length"
                  variant="ghost"
                  size="xs"
                  class="h-7 w-full justify-start rounded-md px-2 text-[11px] text-muted-foreground"
                  @click="loadMore(group)"
                >
                  {{ t('sidebar.loadMore', { count: 30 }) }}
                </Button>
              </div>
            </CollapsibleContent>
          </Collapsible>

          <div
            v-if="grokGroups.length === 0 || (threadCount === 0 && !grokStore.workspacePath)"
            class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground"
          >
            <div class="grid size-10 place-items-center rounded-full bg-muted/60">
              <GrokIcon :size="16" class="opacity-70" />
            </div>
            <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.grokEmpty') }}</p>
            <p class="max-w-[200px] text-[10px] leading-4 text-muted-foreground/80">
              {{ t('sidebar.grokEmptyHint') }}
            </p>
          </div>
        </template>

        <Collapsible
          v-for="group in usesCodexTimeline ? groups : []"
          :key="group.path"
          :open="!isGroupCollapsed(group)"
          @update:open="(open) => setGroupCollapsed(group, !open)"
        >
          <div class="sticky top-0 z-20 bg-sidebar">
            <div
              class="group/project relative flex items-center gap-0.5 rounded-lg px-1 transition-colors"
              :class="[
                group.active ? 'text-foreground' : 'hover:bg-sidebar-accent/25',
                projectDropClass(group.path),
              ]"
              @dragover="(event: DragEvent) => updateProjectDropTarget(event, group.path)"
              @dragleave="(event: DragEvent) => clearProjectDropTarget(event, group.path)"
              @drop="(event: DragEvent) => dropProject(event, group.path)"
            >
            <span
              v-if="showProjectDropIndicator(group.path, 'before')"
              aria-hidden="true"
              class="pointer-events-none absolute inset-x-1 top-0 z-10 h-0.5 -translate-y-1/2 rounded-full bg-foreground/70"
            />
            <span
              v-if="showProjectDropIndicator(group.path, 'after')"
              aria-hidden="true"
              class="pointer-events-none absolute inset-x-1 bottom-0 z-10 h-0.5 translate-y-1/2 rounded-full bg-foreground/70"
            />
            <TooltipProvider v-if="canDragProject(group.path)">
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    draggable="true"
                    class="absolute right-8 z-10 size-6 shrink-0 cursor-grab rounded-md text-muted-foreground/70 opacity-0 transition-opacity group-hover/project:opacity-60 hover:opacity-100 focus-visible:opacity-100 active:cursor-grabbing"
                    :aria-label="t('sidebar.reorderProject', { name: group.name })"
                    @click.stop.prevent
                    @dragstart="(event: DragEvent) => startProjectDrag(event, group.path)"
                    @dragend="endProjectDrag"
                    @keydown.up.stop.prevent="moveProjectByKeyboard(group.path, -1)"
                    @keydown.down.stop.prevent="moveProjectByKeyboard(group.path, 1)"
                  >
                    <GripVertical :size="13" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="top">{{ t('sidebar.reorderProject', { name: group.name }) }}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
            <CollapsibleTrigger as-child>
              <button
                type="button"
                class="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left text-[11.5px] font-medium text-muted-foreground transition-colors hover:text-foreground"
              >
                <ChevronRight
                  :size="12"
                  class="shrink-0 opacity-50 transition-transform duration-200"
                  :class="{ 'rotate-90': !isGroupCollapsed(group) }"
                />
                <SimpleTooltip :content="group.path">
                  <span class="min-w-0 truncate" :class="group.active ? 'text-foreground' : ''">{{ group.name }}</span>
                </SimpleTooltip>
              </button>
            </CollapsibleTrigger>

            <span v-if="group.loading" class="mr-1 inline-block size-3 shrink-0 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />

            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    class="size-6 shrink-0 rounded-md opacity-0 transition-opacity group-hover/project:opacity-100 focus-visible:opacity-100"
                    :class="creatingInProject === group.path ? 'opacity-100' : ''"
                    :disabled="Boolean(creatingInProject) || codexStore.creatingThread || (!arenaStore.isArenaMode && workspaceStore.switchingWorkspace)"
                    :aria-label="t('sidebar.newTaskInProject')"
                    @click="(event: MouseEvent) => void newInProject(group, event)"
                  >
                    <LoaderCircle v-if="creatingInProject === group.path" :size="12" class="animate-spin" />
                    <Plus v-else :size="12" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="top">{{ t('sidebar.newTaskInProject') }}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
            </div>
          </div>

          <CollapsibleContent>
            <div class="space-y-0.5 py-0.5 pl-2">
              <div v-if="group.error" class="rounded-lg bg-destructive/10 p-2 text-[10px] text-destructive">
                {{ group.error }}
                <Button size="xs" variant="ghost" class="mt-1 h-6" @click="codexStore.reloadProject(group.path)">
                  <RefreshCw :size="11" class="mr-1" />
                  {{ t('sidebar.retryProject') }}
                </Button>
              </div>

              <div
                v-for="thread in visibleThreads(group)"
                :key="thread.id"
                class="group/thread relative"
              >
                <Motion
                  as="div"
                  role="button"
                  tabindex="0"
                  class="flex h-8 w-full cursor-pointer items-center gap-2 rounded-lg px-2 text-left text-xs transition-colors"
                  :class="isActiveSidebarSession(codexStore.runtimeIDForThread(thread.id), thread.id)
                    ? 'bg-sidebar-accent/80 text-accent-foreground'
                    : 'hover:bg-sidebar-accent/50'"
                  :whilePress="{ scale: 0.985 }"
                  :transition="springSnappy"
                  @click="renamingThreadId === thread.id ? undefined : openThread(group, thread)"
                  @dblclick.stop="beginRename(thread)"
                  @keydown.enter.prevent="renamingThreadId === thread.id ? undefined : openThread(group, thread)"
                >
                  <SimpleTooltip
                    :content="isCodexThreadRunning(thread.id)
                      ? t('sidebar.runningInBackground')
                      : providerLabel(thread)"
                  >
                    <span class="relative grid size-3 shrink-0 place-items-center">
                      <span
                        class="size-2 rounded-full border border-muted-foreground/45"
                        :class="isCodexThreadRunning(thread.id) ? 'border-emerald-500 bg-emerald-500' : ''"
                      />
                      <span v-if="isCodexThreadRunning(thread.id)" class="absolute inset-0 animate-ping rounded-full bg-emerald-400/35" />
                    </span>
                  </SimpleTooltip>

                  <span class="flex min-w-0 flex-1 items-center gap-1 pr-1">
                      <Pin
                        v-if="codexStore.pinnedThreadIds.includes(thread.id)"
                        :size="10"
                        class="shrink-0 fill-current opacity-70"
                      />
                      <Input
                        v-if="renamingThreadId === thread.id"
                        data-thread-rename-input
                        v-model="renameDraft"
                        class="h-6 rounded-md px-1.5 text-[11px] font-medium"
                        maxlength="80"
                        :aria-label="t('threadActions.rename')"
                        @click.stop
                        @keydown.enter.prevent="commitRename(thread)"
                        @keydown.esc.prevent="cancelRename"
                        @blur="commitRename(thread)"
                      />
                      <SimpleTooltip v-else :content="t('sidebar.renameHint')">
                        <span class="min-w-0 flex-1 truncate leading-5">{{ thread.name }}</span>
                      </SimpleTooltip>
                  </span>

                  <span
                    v-if="renamingThreadId !== thread.id"
                    class="relative flex h-5 w-10 shrink-0 items-center justify-end"
                    :class="{ 'text-accent-foreground/70': isActiveSidebarSession(codexStore.runtimeIDForThread(thread.id), thread.id) }"
                  >
                    <span
                      v-if="codexStore.threadIsLoading(thread.id)"
                      class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                    />
                    <span
                      v-else
                      class="whitespace-nowrap text-[10px] tabular-nums leading-none text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
                    >
                      {{ formatUpdated(thread.updatedAt) }}
                    </span>
                  </span>
                </Motion>

                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="absolute right-1 top-1 size-6 rounded-md text-muted-foreground opacity-0 transition-opacity group-hover/thread:opacity-100 group-focus-within/thread:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100"
                      :aria-label="t('threadActions.title')"
                      :disabled="Boolean(codexStore.threadMutationForThread(thread.id)) || renamingThreadId === thread.id"
                      @click.stop
                    >
                      <MoreHorizontal :size="13" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" class="min-w-40">
                    <DropdownMenuItem @click="(event: Event) => beginRename(thread, event)">
                      <Pencil :size="14" class="mr-2" />
                      {{ t('threadActions.rename') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      :disabled="Boolean(codexStore.threadMutationForThread(thread.id)) || thread.id.startsWith('pending-thread-')"
                      @click="(event: Event) => forkThread(thread.id, event)"
                    >
                      <Copy :size="14" class="mr-2" />
                      {{ t('threadActions.fork') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem @click="(event: Event) => togglePin(thread, event)">
                      <Pin :size="14" class="mr-2" :class="codexStore.pinnedThreadIds.includes(thread.id) ? 'fill-current' : ''" />
                      {{ codexStore.pinnedThreadIds.includes(thread.id) ? t('sidebar.unpin') : t('sidebar.pin') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      :disabled="Boolean(codexStore.threadMutationForThread(thread.id))"
                      @click="(event: Event) => archiveThread(thread.id, event)"
                    >
                      <Archive :size="14" class="mr-2" />
                      {{ t('threadActions.archive') }}
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      class="text-destructive focus:text-destructive"
                      :disabled="Boolean(codexStore.threadMutationForThread(thread.id))"
                      @click="(event: Event) => deleteThread(thread.id, event)"
                    >
                      <Trash2 :size="14" class="mr-2" />
                      {{ t('threadActions.delete') }}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>

              <Button
                v-if="visibleThreads(group).length < group.threads.length"
                variant="ghost"
                size="sm"
                class="h-7 w-full justify-start rounded-md px-2 text-[10px] text-muted-foreground"
                @click="loadMore(group)"
              >
                {{ t('sidebar.loadMore', { count: group.threads.length - visibleThreads(group).length }) }}
              </Button>

              <div v-if="!group.loading && !group.error && group.threads.length === 0" class="px-2 py-2 text-[10px] text-muted-foreground">
                {{ t('sidebar.firstTask') }}
              </div>
            </div>
          </CollapsibleContent>
        </Collapsible>

        <!-- Claude sessions: project-grouped like Codex / Grok -->
        <template v-if="sidebarIsClaudeMode">
          <Collapsible
            v-for="group in claudeGroups"
            :key="`claude-${group.path}`"
            :open="!isGroupCollapsed(group)"
            @update:open="(open) => setGroupCollapsed(group, !open)"
          >
            <div class="sticky top-0 z-20 bg-sidebar">
              <div
                class="group/project relative flex items-center gap-0.5 rounded-lg px-1 transition-colors"
                :class="[
                  group.active ? 'text-foreground' : 'hover:bg-sidebar-accent/25',
                  projectDropClass(group.path),
                ]"
                @dragover="(event: DragEvent) => updateProjectDropTarget(event, group.path)"
                @dragleave="(event: DragEvent) => clearProjectDropTarget(event, group.path)"
                @drop="(event: DragEvent) => dropProject(event, group.path)"
              >
              <span
                v-if="showProjectDropIndicator(group.path, 'before')"
                aria-hidden="true"
                class="pointer-events-none absolute inset-x-1 top-0 z-10 h-0.5 -translate-y-1/2 rounded-full bg-foreground/70"
              />
              <span
                v-if="showProjectDropIndicator(group.path, 'after')"
                aria-hidden="true"
                class="pointer-events-none absolute inset-x-1 bottom-0 z-10 h-0.5 translate-y-1/2 rounded-full bg-foreground/70"
              />
              <TooltipProvider v-if="canDragProject(group.path)">
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      draggable="true"
                      class="absolute right-8 z-10 size-6 shrink-0 cursor-grab rounded-md text-muted-foreground/70 opacity-0 transition-opacity group-hover/project:opacity-60 hover:opacity-100 focus-visible:opacity-100 active:cursor-grabbing"
                      :aria-label="t('sidebar.reorderProject', { name: group.name })"
                      @click.stop.prevent
                      @dragstart="(event: DragEvent) => startProjectDrag(event, group.path)"
                      @dragend="endProjectDrag"
                      @keydown.up.stop.prevent="moveProjectByKeyboard(group.path, -1)"
                      @keydown.down.stop.prevent="moveProjectByKeyboard(group.path, 1)"
                    >
                      <GripVertical :size="13" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{{ t('sidebar.reorderProject', { name: group.name }) }}</TooltipContent>
                </Tooltip>
              </TooltipProvider>
              <CollapsibleTrigger as-child>
                <button
                  type="button"
                  class="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left text-[11.5px] font-medium text-muted-foreground transition-colors hover:text-foreground"
                >
                  <ChevronRight
                    :size="12"
                    class="shrink-0 opacity-50 transition-transform duration-200"
                    :class="{ 'rotate-90': !isGroupCollapsed(group) }"
                  />
                  <SimpleTooltip :content="group.path">
                    <span class="min-w-0 truncate" :class="group.active ? 'text-foreground' : ''">{{ group.name }}</span>
                  </SimpleTooltip>
                </button>
              </CollapsibleTrigger>
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 shrink-0 rounded-md opacity-0 transition-opacity group-hover/project:opacity-100 focus-visible:opacity-100"
                      :aria-label="t('sidebar.newTaskInProject')"
                      @click="(event: MouseEvent) => void newInClaudeProject(group, event)"
                    >
                      <Plus :size="12" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{{ t('sidebar.newTaskInProject') }}</TooltipContent>
                </Tooltip>
              </TooltipProvider>
              </div>
            </div>

            <CollapsibleContent>
              <div class="space-y-0.5 py-0.5 pl-2">
                <div
                  v-for="session in visibleClaudeSessions(group)"
                  :key="session.id"
                  class="group/thread relative"
                >
                  <div
                    role="button"
                    tabindex="0"
                    class="flex h-8 w-full cursor-pointer items-center gap-2 rounded-lg px-2 text-left text-xs transition-colors"
                    :class="isActiveSidebarSession('claude', session.id)
                      ? 'bg-sidebar-accent/80 text-accent-foreground'
                      : 'hover:bg-sidebar-accent/50'"
                    @click="renamingThreadId === session.id ? undefined : openClaudeSession(group, session.id)"
                    @dblclick.stop="beginRename(session)"
                    @keydown.enter.prevent="renamingThreadId === session.id ? undefined : openClaudeSession(group, session.id)"
                  >
                    <SimpleTooltip
                      :content="isClaudeSessionRunning(session.id)
                        ? t('sidebar.runningInBackground')
                        : (session.model || 'Claude')"
                    >
                      <span class="relative grid size-3 shrink-0 place-items-center">
                        <span
                          class="size-2 rounded-full border border-muted-foreground/45"
                          :class="isClaudeSessionRunning(session.id) ? 'border-emerald-500 bg-emerald-500' : ''"
                        />
                        <span v-if="isClaudeSessionRunning(session.id)" class="absolute inset-0 animate-ping rounded-full bg-emerald-400/35" />
                      </span>
                    </SimpleTooltip>
                    <span class="flex min-w-0 flex-1 items-center gap-1 pr-1">
                        <Input
                          v-if="renamingThreadId === session.id"
                          data-thread-rename-input
                          v-model="renameDraft"
                          class="h-6 rounded-md px-1.5 text-[11px] font-medium"
                          maxlength="80"
                          :aria-label="t('threadActions.rename')"
                          @click.stop
                          @keydown.enter.prevent="commitRename(session)"
                          @keydown.esc.prevent="cancelRename"
                          @blur="commitRename(session)"
                        />
                        <SimpleTooltip v-else :content="t('sidebar.renameHint')">
                          <span class="min-w-0 flex-1 truncate leading-5 text-foreground/90">
                            {{ session.name || session.id }}
                          </span>
                        </SimpleTooltip>
                    </span>
                    <span
                      v-if="renamingThreadId !== session.id"
                      class="relative flex h-5 w-10 shrink-0 items-center justify-end"
                      :class="{ 'text-accent-foreground/70': isActiveSidebarSession('claude', session.id) }"
                    >
                      <span
                        v-if="claudeStore.isSessionLoading(session.id)"
                        class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                      />
                      <span
                        v-else
                        class="whitespace-nowrap text-[10px] tabular-nums leading-none text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
                      >
                        {{ formatClaudeUpdated(session.updatedAt || 0) }}
                      </span>
                    </span>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger as-child>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="absolute right-1 top-1 size-6 rounded-md text-muted-foreground opacity-0 transition-opacity group-hover/thread:opacity-100 group-focus-within/thread:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100"
                        :aria-label="t('threadActions.title')"
                        :disabled="Boolean(claudeStore.sessionMutationForSession(session.id)) || renamingThreadId === session.id"
                        @click.stop
                      >
                        <MoreHorizontal :size="13" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" class="min-w-40">
                      <DropdownMenuItem
                        :disabled="Boolean(claudeStore.sessionMutationForSession(session.id))"
                        @click="(event: Event) => beginRename(session, event)"
                      >
                        <Pencil :size="14" class="mr-2" />
                        {{ t('threadActions.rename') }}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        :disabled="Boolean(claudeStore.sessionMutationForSession(session.id)) || session.id.startsWith('pending-claude-')"
                        @click="(event: Event) => archiveClaudeSession(session.id, event)"
                      >
                        <Archive :size="14" class="mr-2" />
                        {{ t('threadActions.archive') }}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        class="text-destructive focus:text-destructive"
                        :disabled="Boolean(claudeStore.sessionMutationForSession(session.id))"
                        @click="(event: Event) => deleteClaudeSession(session.id, event)"
                      >
                        <Trash2 :size="14" class="mr-2" />
                        {{ t('threadActions.delete') }}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>

                <Button
                  v-if="!search && group.sessions.length > visibleClaudeSessions(group).length"
                  variant="ghost"
                  size="xs"
                  class="h-7 w-full justify-start rounded-md px-2 text-[11px] text-muted-foreground"
                  @click="loadMore(group)"
                >
                  {{ t('sidebar.loadMore', { count: group.sessions.length - visibleClaudeSessions(group).length }) }}
                </Button>

                <div
                  v-if="group.sessions.length === 0"
                  class="px-2 py-2 text-[10px] text-muted-foreground"
                >
                  {{ t('sidebar.firstTask') }}
                </div>
              </div>
            </CollapsibleContent>
          </Collapsible>

          <div
            v-if="claudeGroups.length === 0 || (threadCount === 0 && !claudeStore.workspacePath)"
            class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground"
          >
            <div class="grid size-10 place-items-center rounded-full bg-muted/60">
              <ClaudeIcon :size="16" class="opacity-70" />
            </div>
            <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.claudeEmpty') }}</p>
            <p class="max-w-[200px] text-[10px] leading-4 text-muted-foreground/80">
              {{ claudeProvider?.runtimeReady || claudeStore.isReady
                ? t('sidebar.claudeEmptyHint')
                : t('sidebar.claudeRuntimeMissing') }}
            </p>
          </div>
        </template>

        <div
          v-if="usesCodexTimeline && groups.length === 0"
          class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground"
        >
          <div class="grid size-10 place-items-center rounded-full bg-muted/60">
            <GeminiIcon v-if="sidebarIsGeminiMode" :size="16" class="opacity-70" />
            <OpenCodeIcon v-else-if="sidebarIsOpenCodeMode" :size="16" class="opacity-70" />
            <MessageSquareText v-else :size="16" class="opacity-70" />
          </div>
          <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.firstTask') }}</p>
          <p v-if="!search && sidebarIsCodexMode" class="max-w-[200px] text-[10px] leading-4 text-muted-foreground/80">
            {{
              appStore.settings.workMode === 'cowork'
                ? t('sidebar.switchToCodeHint')
                : t('sidebar.switchToCoworkHint')
            }}
          </p>
          <Button
            v-if="!search && sidebarIsCodexMode"
            type="button"
            variant="outline"
            class="h-7 rounded-md px-2.5 text-[11px]"
            @click="void setWorkMode(appStore.settings.workMode === 'cowork' ? 'code' : 'cowork')"
          >
            {{ appStore.settings.workMode === 'cowork' ? t('sidebar.code') : t('sidebar.cowork') }}
          </Button>
        </div>
      </div>
    </ScrollArea>

    <div class="border-t border-sidebar-border/40 p-2">
      <div class="flex items-center gap-1">
        <!-- Grok: same token usage popover as Codex (today / 7d / 14d / 30d). -->
        <div v-if="sidebarIsGrokMode" class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :aria-label="t('sidebar.usageHint')"
              >
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border/60 bg-panel/80">
                  <GrokIcon :size="13" class="opacity-80" />
                </span>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">Grok</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>
                      {{ grokStore.isReady
                        ? (grokStore.runtime.buildVersion || appStore.settings.grokBackend || 'build')
                        : t('sidebar.grokRuntimeMissing') }}
                    </span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
                </div>
                <LoaderCircle v-if="usageLoading" :size="12" class="mt-0.5 animate-spin text-muted-foreground" />
              </div>

              <div class="mb-3 flex flex-wrap gap-1">
                <button
                  v-for="range in usageRanges"
                  :key="range.days"
                  type="button"
                  class="h-6 rounded-md px-2 text-[10px] transition-colors"
                  :class="usageRangeDays === range.days
                    ? 'bg-foreground text-background'
                    : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'"
                  @click="usageRangeDays = range.days"
                >
                  {{ range.label }}
                </button>
              </div>

              <div class="mb-3 rounded-lg border bg-muted/30 px-3 py-2.5">
                <p class="text-[10px] text-muted-foreground">{{ t('sidebar.usageRangeTotal') }}</p>
                <p class="mt-0.5 text-lg font-semibold tabular-nums tracking-tight">
                  {{ usageRangeTotalLabel }}
                  <span class="text-[11px] font-normal text-muted-foreground">tokens</span>
                </p>
                <p class="mt-1 text-[10px] text-muted-foreground">{{ usageRangeMeta }}</p>
              </div>

              <div v-if="usageRangeView.buckets.length" class="mb-3 max-h-36 space-y-1.5 overflow-y-auto pr-0.5">
                <div
                  v-for="bucket in usageRangeView.buckets"
                  :key="bucket.startDate"
                  class="grid grid-cols-[64px_1fr_40px] items-center gap-2 text-[10px]"
                >
                  <span class="truncate text-muted-foreground">{{ formatUsageDateLabel(bucket.startDate, usageLocale) }}</span>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div
                      class="h-full rounded-full bg-foreground/70"
                      :style="{ width: `${usageRangeView.maxTokens ? Math.max(6, (bucket.tokens / usageRangeView.maxTokens) * 100) : 0}%` }"
                    />
                  </div>
                  <span class="text-right tabular-nums text-foreground/80">{{ formatTokenCount(bucket.tokens) }}</span>
                </div>
              </div>
              <p v-else class="mb-3 text-[11px] text-muted-foreground">
                {{ t('sidebar.usageEmpty') }}
              </p>

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

              <div class="grid grid-cols-2 gap-2 text-[10px]">
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('inspector.lifetimeTokens') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeTokens) }}</p>
                </div>
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('sidebar.usagePeakDaily') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.peakDailyTokens) }}</p>
                </div>
              </div>
            </PopoverContent>
          </Popover>
        </div>
        <div v-else-if="sidebarIsClaudeMode" class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :aria-label="t('sidebar.usageHint')"
              >
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border/60 bg-panel/80">
                  <ClaudeIcon :size="13" class="opacity-80" />
                </span>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">Claude</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>
                      {{ claudeProvider?.runtimeReady
                        ? (claudeProvider.version || t('sidebar.claudeReady'))
                        : t('sidebar.claudeRuntimeMissing') }}
                    </span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
                </div>
                <LoaderCircle v-if="usageLoading" :size="12" class="mt-0.5 animate-spin text-muted-foreground" />
              </div>

              <div class="mb-3 flex flex-wrap gap-1">
                <button
                  v-for="range in usageRanges"
                  :key="range.days"
                  type="button"
                  class="h-6 rounded-md px-2 text-[10px] transition-colors"
                  :class="usageRangeDays === range.days
                    ? 'bg-foreground text-background'
                    : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'"
                  @click="usageRangeDays = range.days"
                >
                  {{ range.label }}
                </button>
              </div>

              <div class="mb-3 rounded-lg border bg-muted/30 px-3 py-2.5">
                <p class="text-[10px] text-muted-foreground">{{ t('sidebar.usageRangeTotal') }}</p>
                <p class="mt-0.5 text-lg font-semibold tabular-nums tracking-tight">
                  {{ usageRangeTotalLabel }}
                  <span class="text-[11px] font-normal text-muted-foreground">tokens</span>
                </p>
                <p class="mt-1 text-[10px] text-muted-foreground">{{ usageRangeMeta }}</p>
              </div>

              <div v-if="usageRangeView.buckets.length" class="mb-3 max-h-36 space-y-1.5 overflow-y-auto pr-0.5">
                <div
                  v-for="bucket in usageRangeView.buckets"
                  :key="bucket.startDate"
                  class="grid grid-cols-[64px_1fr_40px] items-center gap-2 text-[10px]"
                >
                  <span class="truncate text-muted-foreground">{{ formatUsageDateLabel(bucket.startDate, usageLocale) }}</span>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div
                      class="h-full rounded-full bg-foreground/70"
                      :style="{ width: `${usageRangeView.maxTokens ? Math.max(6, (bucket.tokens / usageRangeView.maxTokens) * 100) : 0}%` }"
                    />
                  </div>
                  <span class="text-right tabular-nums text-foreground/80">{{ formatTokenCount(bucket.tokens) }}</span>
                </div>
              </div>
              <p v-else class="mb-3 text-[11px] text-muted-foreground">
                {{ t('sidebar.usageEmpty') }}
              </p>

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

              <div class="grid grid-cols-2 gap-2 text-[10px]">
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('inspector.lifetimeTokens') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeTokens) }}</p>
                </div>
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('sidebar.usagePeakDaily') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.peakDailyTokens) }}</p>
                </div>
              </div>
            </PopoverContent>
          </Popover>
        </div>
        <div v-else-if="sidebarIsGeminiMode || sidebarIsOpenCodeMode" class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :aria-label="t('sidebar.usageHint')"
              >
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border/60 bg-panel/80">
                  <GeminiIcon v-if="sidebarIsGeminiMode" :size="13" class="opacity-80" />
                  <OpenCodeIcon v-else :size="13" class="opacity-80" />
                </span>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">{{ sidebarIsGeminiMode ? geminiRuntimeName : 'OpenCode' }}</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>
                      {{ activeExternalProvider?.runtimeReady
                        ? (activeExternalProvider.version || t('settings.runtimeReady'))
                        : t('settings.runtimeMissing') }}
                    </span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
                </div>
                <LoaderCircle v-if="usageLoading" :size="12" class="mt-0.5 animate-spin text-muted-foreground" />
              </div>

              <div class="mb-3 flex flex-wrap gap-1">
                <button
                  v-for="range in usageRanges"
                  :key="range.days"
                  type="button"
                  class="h-6 rounded-md px-2 text-[10px] transition-colors"
                  :class="usageRangeDays === range.days
                    ? 'bg-foreground text-background'
                    : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'"
                  @click="usageRangeDays = range.days"
                >
                  {{ range.label }}
                </button>
              </div>

              <div class="mb-3 rounded-lg border bg-muted/30 px-3 py-2.5">
                <p class="text-[10px] text-muted-foreground">{{ t('sidebar.usageRangeTotal') }}</p>
                <p class="mt-0.5 text-lg font-semibold tabular-nums tracking-tight">
                  {{ usageRangeTotalLabel }}
                  <span class="text-[11px] font-normal text-muted-foreground">tokens</span>
                </p>
                <p class="mt-1 text-[10px] text-muted-foreground">{{ usageRangeMeta }}</p>
              </div>

              <div v-if="usageRangeView.buckets.length" class="mb-3 max-h-36 space-y-1.5 overflow-y-auto pr-0.5">
                <div
                  v-for="bucket in usageRangeView.buckets"
                  :key="bucket.startDate"
                  class="grid grid-cols-[64px_1fr_40px] items-center gap-2 text-[10px]"
                >
                  <span class="truncate text-muted-foreground">{{ formatUsageDateLabel(bucket.startDate, usageLocale) }}</span>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div
                      class="h-full rounded-full bg-foreground/70"
                      :style="{ width: `${usageRangeView.maxTokens ? Math.max(6, (bucket.tokens / usageRangeView.maxTokens) * 100) : 0}%` }"
                    />
                  </div>
                  <span class="text-right tabular-nums text-foreground/80">{{ formatTokenCount(bucket.tokens) }}</span>
                </div>
              </div>
              <p v-else class="mb-3 text-[11px] text-muted-foreground">
                {{ t('sidebar.usageEmpty') }}
              </p>

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

              <div class="grid grid-cols-2 gap-2 text-[10px]">
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('inspector.lifetimeTokens') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeTokens) }}</p>
                </div>
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('sidebar.usagePeakDaily') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.peakDailyTokens) }}</p>
                </div>
              </div>
            </PopoverContent>
          </Popover>
        </div>
        <Button
          v-else-if="!appStore.account.authenticated"
          variant="ghost"
          class="h-8 flex-1 justify-start rounded-lg px-2 text-xs"
          @click="appStore.startLogin()"
        >
          <LogIn :size="14" class="mr-2" />
          {{ t('sidebar.signIn') }}
        </Button>
        <div v-else class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :aria-label="t('sidebar.usageHint')"
              >
                <Avatar class="size-6">
                  <AvatarFallback class="bg-primary text-[10px] text-primary-foreground">
                    {{ appStore.account.email.slice(0, 1).toUpperCase() || 'C' }}
                  </AvatarFallback>
                </Avatar>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">{{ appStore.account.email }}</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>{{ appStore.account.planType || appStore.account.type }}</span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
                </div>
                <LoaderCircle v-if="usageLoading" :size="12" class="mt-0.5 animate-spin text-muted-foreground" />
              </div>

              <div class="mb-3 flex flex-wrap gap-1">
                <button
                  v-for="range in usageRanges"
                  :key="range.days"
                  type="button"
                  class="h-6 rounded-md px-2 text-[10px] transition-colors"
                  :class="usageRangeDays === range.days
                    ? 'bg-foreground text-background'
                    : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'"
                  @click="usageRangeDays = range.days"
                >
                  {{ range.label }}
                </button>
              </div>

              <div class="mb-3 rounded-lg border bg-muted/30 px-3 py-2.5">
                <p class="text-[10px] text-muted-foreground">{{ t('sidebar.usageRangeTotal') }}</p>
                <p class="mt-0.5 text-lg font-semibold tabular-nums tracking-tight">
                  {{ usageRangeTotalLabel }}
                  <span class="text-[11px] font-normal text-muted-foreground">tokens</span>
                </p>
                <p class="mt-1 text-[10px] text-muted-foreground">{{ usageRangeMeta }}</p>
              </div>

              <div v-if="usageRangeView.buckets.length" class="mb-3 max-h-36 space-y-1.5 overflow-y-auto pr-0.5">
                <div
                  v-for="bucket in usageRangeView.buckets"
                  :key="bucket.startDate"
                  class="grid grid-cols-[64px_1fr_40px] items-center gap-2 text-[10px]"
                >
                  <span class="truncate text-muted-foreground">{{ formatUsageDateLabel(bucket.startDate, usageLocale) }}</span>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div
                      class="h-full rounded-full bg-foreground/70"
                      :style="{ width: `${usageRangeView.maxTokens ? Math.max(6, (bucket.tokens / usageRangeView.maxTokens) * 100) : 0}%` }"
                    />
                  </div>
                  <span class="text-right tabular-nums text-foreground/80">{{ formatTokenCount(bucket.tokens) }}</span>
                </div>
              </div>
              <p v-else class="mb-3 text-[11px] text-muted-foreground">
                {{ t('sidebar.usageEmpty') }}
              </p>

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

              <div class="grid grid-cols-2 gap-2 text-[10px]">
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('inspector.lifetimeTokens') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeTokens) }}</p>
                </div>
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('sidebar.usagePeakDaily') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.peakDailyTokens) }}</p>
                </div>
              </div>

              <div
                v-if="appStore.accountRateLimits?.primary || appStore.accountRateLimits?.secondary"
                class="mt-3 space-y-2 border-t pt-2"
              >
                <p class="text-[10px] font-medium text-muted-foreground">{{ t('inspector.rateLimits') }}</p>
                <div v-if="appStore.accountRateLimits?.primary" class="space-y-1">
                  <div class="flex justify-between text-[10px]">
                    <span class="text-muted-foreground">{{ t('inspector.primaryLimit') }}</span>
                    <span class="tabular-nums">{{ appStore.accountRateLimits.primary.usedPercent }}%</span>
                  </div>
                  <Progress :model-value="appStore.accountRateLimits.primary.usedPercent" class="h-1" />
                </div>
                <div v-if="appStore.accountRateLimits?.secondary" class="space-y-1">
                  <div class="flex justify-between text-[10px]">
                    <span class="text-muted-foreground">{{ t('inspector.secondaryLimit') }}</span>
                    <span class="tabular-nums">{{ appStore.accountRateLimits.secondary.usedPercent }}%</span>
                  </div>
                  <Progress :model-value="appStore.accountRateLimits.secondary.usedPercent" class="h-1" />
                </div>
              </div>
            </PopoverContent>
          </Popover>
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('sidebar.signOut')" @click="appStore.logout()">
                  <LogOut :size="14" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="top">{{ t('sidebar.signOut') }}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>

        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('capabilities.title')" @click="openCapabilities">
                <Blocks :size="14" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('capabilities.title') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>

        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('sidebar.openSettings')" @click="openSettings">
                <Settings :size="14" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('sidebar.openSettings') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </div>
    </div>

    <Teleport to="body">
      <div
        v-if="arenaContextMenu"
        data-arena-context-menu
        class="fixed z-[200] min-w-[200px] overflow-hidden rounded-lg border bg-popover p-1 text-popover-foreground shadow-lg"
        :style="{ left: `${arenaContextMenu.x}px`, top: `${arenaContextMenu.y}px` }"
        role="menu"
      >
        <p class="px-2 py-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
          {{ t('arena.menuTitle') }}
        </p>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="startArena(2)"
        >
          <Columns2 :size="13" class="opacity-80" />
          {{ t('arena.columns2') }}
        </button>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="startArena(3)"
        >
          <Columns3 :size="13" class="opacity-80" />
          {{ t('arena.columns3') }}
        </button>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="startArena(4)"
        >
          <Columns4 :size="13" class="opacity-80" />
          {{ t('arena.columns4') }}
        </button>
        <div class="my-1 h-px bg-border" />
        <p class="px-2 py-1 text-[10px] text-muted-foreground">
          {{ t('arena.sameProviderMenu') }}
        </p>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="startArenaSameProvider(2)"
        >
          <Columns2 :size="13" class="opacity-80" />
          {{ t('arena.sameProvider2') }}
        </button>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="startArenaSameProvider(3)"
        >
          <Columns3 :size="13" class="opacity-80" />
          {{ t('arena.sameProvider3') }}
        </button>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="startArenaSameProvider(4)"
        >
          <Columns4 :size="13" class="opacity-80" />
          {{ t('arena.sameProvider4') }}
        </button>
        <div v-if="arenaStore.isArenaMode" class="my-1 h-px bg-border" />
        <button
          v-if="arenaStore.isArenaMode && arenaStore.canAddPane"
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent"
          role="menuitem"
          @click="addArenaPaneFromMenu"
        >
          <Plus :size="13" class="opacity-80" />
          {{ t('arena.addPane') }}
        </button>
        <button
          v-if="arenaStore.isArenaMode"
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] text-destructive hover:bg-destructive/10"
          role="menuitem"
          @click="arenaStore.closeArena(); closeArenaContextMenu()"
        >
          <X :size="13" class="opacity-80" />
          {{ t('arena.exit') }}
        </button>
      </div>
    </Teleport>
  </Motion>
</template>

<script setup lang="ts">
import {
  AlertCircle,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronUp,
  Command,
  CornerDownLeft,
  Ellipsis,
  FileUp,
  Folder,
  FolderOpen,
  GitBranch,
  GitPullRequestArrow,
  Image as ImageIcon,
  ListOrdered,
  ListTodo,
  LoaderCircle,
  Maximize2,
  Minimize2,
  Octagon,
  Pencil,
  Plug,
  Plus,
  Puzzle,
  RotateCcw,
  Search,
  Shield,
  X,
  Zap,
} from '@lucide/vue'
import { computed, nextTick, onMounted, shallowRef, useTemplateRef, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import SearchableSelect from '@/components/SearchableSelect.vue'
import AttachmentImage from '@/components/AttachmentImage.vue'
import ClaudeIcon from '@/components/icons/ClaudeIcon.vue'
import GeminiIcon from '@/components/icons/GeminiIcon.vue'
import GrokIcon from '@/components/icons/GrokIcon.vue'
import OpenAIIcon from '@/components/icons/OpenAIIcon.vue'
import OpenCodeIcon from '@/components/icons/OpenCodeIcon.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { SimpleTooltip } from '@/components/ui/tooltip'
import { Textarea } from '@/components/ui/textarea'
import { useAppStore, useArenaStore, useCapabilitiesStore, useClaudeStore, useCodexStore, useDialogStore, useGrokStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import { useComposerWorkspaceSwitcher } from '@/composables/useComposerWorkspaceSwitcher'
import { useRuntimeMode } from '@/composables/useRuntimeMode'
import {
  buildContextUsageView,
  CODEX_CONTEXT_BASELINE_TOKENS,
  formatTokenCount,
  resolveProviderModelContextWindow,
} from '@/utils/accountUsage'
import { rememberLocalImagePreview, resolveImagePreview } from '@/utils/imagePreview'
import { notify } from '@/utils/notify'
import { sameWorkspacePath } from '@/utils/workspacePath'
import {
  DEFAULT_CODEX_REASONING,
  DEFAULT_GROK_REASONING,
  formatModelLabel,
  modelsForClaudeRuntime,
  modelsForGrokRuntime,
  modelsForRuntime,
} from '@/utils/runtimeProviders'

const appStore = useAppStore()
const {
  runtime: paneRuntime,
  paneId,
  isArenaPane,
  boundSessionId,
  isCodexMode,
  isClaudeMode,
  isGrokMode,
  isGeminiMode,
  isOpenCodeMode,
} = useRuntimeMode()
const codexStore = useCodexStore()
const arenaStore = useArenaStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const capabilitiesStore = useCapabilitiesStore()
const dialogStore = useDialogStore()
// claudeStore used for Claude Code runtime composer path
const router = useRouter()
const { t } = useI18n()

const props = defineProps<{
  draftKey: string
  sendPending: boolean
}>()
type EditableQueuedMessage = {
  id: string
  text: string
  images: string[]
  state: 'queued' | 'sending' | 'failed'
}
type ComposerSubmission = {
  message: string
  images: string[]
  draftKey: string
  arena: boolean
  targetPaneId: string
  targetRuntime: WorkspaceRuntime
  targetSessionId: string
  targetWorkspace: string
}
const modelValue = defineModel<string>({ required: true })
const attachedImages = defineModel<string[]>('images', { required: true })
const emit = defineEmits<{
  sent: [payload: { draftKey: string }]
  'send-pending-change': [payload: { requestId: string; draftKey: string; pending: boolean }]
  'consume-draft': [payload: { draftKey: string }]
  'restore-draft': [payload: { draftKey: string; text: string; images: string[] }]
  'append-draft-images': [payload: { draftKey: string; images: string[] }]
}>()
const composer = useTemplateRef<HTMLElement>('composer')
const composerInput = useTemplateRef<InstanceType<typeof Textarea>>('composerInput')
const composing = shallowRef(false)
const attachmentPreviews = shallowRef<Record<string, string>>({})
const slashIndex = shallowRef(0)
const skillIndex = shallowRef(0)
const pluginIndex = shallowRef(0)
const dragDepth = shallowRef(0)
const composerExpanded = shallowRef(false)
const composerRuntimeSwitching = shallowRef(false)
const branchMenuOpen = shallowRef(false)
const branchQuery = shallowRef('')
const branchSearchInput = useTemplateRef<InstanceType<typeof Input>>('branchSearchInput')
const sentHistoryIndex = shallowRef(-1)
const sentHistorySnapshot = shallowRef<string[]>([])
const sentHistoryDraft = shallowRef('')
const attachingImageTasks = shallowRef(0)
const sendAdmissionPendingByDraft = shallowRef<Record<string, boolean>>({})
const sendAdmissionPending = computed(() =>
  props.sendPending || Boolean(sendAdmissionPendingByDraft.value[props.draftKey]),
)
const effortPopoverOpen = shallowRef(false)
const effortPreviewIndex = shallowRef(-1)
const effortDragging = shallowRef(false)
const addMenuOpen = shallowRef(false)
const fileSelectionPending = shallowRef(false)
const githubIssuePending = shallowRef(false)
const githubIssueAvailable = shallowRef<boolean | null>(null)
const githubIssueOwnerRepo = shallowRef('')
const githubIssueUnavailableReason = shallowRef('')
const editingQueuedId = shallowRef('')
const queuedEditDraft = shallowRef('')
const queuedEditError = shallowRef('')
let applyingSentHistory = false
let githubContextRequest = 0
let sendAdmissionSequence = 0
const COMPOSER_MAX_COLLAPSED = 200
const COMPOSER_MAX_EXPANDED = 480

const composerSessionId = computed(() => {
  if (isArenaPane.value) return boundSessionId.value
  if (isGrokMode.value) return grokStore.activeSessionId
  if (isClaudeMode.value) return claudeStore.activeSessionId
  return codexStore.activeThreadId
})

function matchingCodexThreadKey(record: Record<string, unknown>, threadId: string): string {
  return Object.keys(record).find((id) => codexStore.sameThread(id, threadId)) || ''
}

const composerCodexThread = computed(() => {
  const id = composerSessionId.value
  if (!id) return null
  if (codexStore.activeThread && codexStore.sameThread(codexStore.activeThread.id, id)) return codexStore.activeThread
  return codexStore.threads.find((thread) => codexStore.sameThread(thread.id, id))
    || Object.values(codexStore.projectThreads).flat().find((thread) => codexStore.sameThread(thread.id, id))
    || null
})
const composerGrokSession = computed(() => {
  const id = composerSessionId.value
  return grokStore.sessions.find((session) => grokStore.sameSession(session.id, id)) || null
})
const composerClaudeSession = computed(() => {
  const id = composerSessionId.value
  return claudeStore.sessions.find((session) => claudeStore.sameSession(session.id, id)) || null
})
const composerTimelineThread = computed(() => {
  const thread = composerCodexThread.value
  return thread && codexStore.runtimeIDForThread(thread.id) === paneRuntime.value ? thread : null
})

function claudeSessionItems(sessionId: string) {
  if (!sessionId) return []
  if (claudeStore.sameSession(sessionId, claudeStore.activeSessionId)) return claudeStore.activeItems
  const key = Object.keys(claudeStore.itemsBySession)
    .find((id) => claudeStore.sameSession(id, sessionId))
  return key ? (claudeStore.itemsBySession[key] || []) : []
}

const sentMessageHistory = computed(() => {
  const sessionId = composerSessionId.value
  const codexKey = matchingCodexThreadKey(codexStore.itemsByThread, sessionId)
  const items = isGrokMode.value
    ? grokStore.itemsForSession(sessionId)
    : isClaudeMode.value
      ? claudeSessionItems(sessionId)
      : ((codexKey && codexStore.itemsByThread[codexKey]) || [])
  return items
    .filter((item) => item.type === 'userMessage' && item.text.trim())
    .map((item) => item.text.trim())
})
const attachingImages = computed(() => attachingImageTasks.value > 0)

type SlashCommand = {
  id: string
  label: string
  description: string
  run: () => void | Promise<void>
}

type UsageCommandRange = 'today' | 'week' | 'cumulative'

function runUsageCommand(range?: UsageCommandRange): void {
  // Open immediately. The popover owns its loading state and refresh, so a
  // slow native CLI/database read never makes the slash command look inert.
  window.dispatchEvent(new CustomEvent('nice-codex:open-usage', { detail: { range } }))
}

async function runAddCommand(): Promise<void> {
  // Native Codex calls this flow /mention. Nice Codex already has a validated
  // attachment picker, so /add and /mention share it and keep the files on the
  // next message instead of silently sending the slash text upstream.
  await attachImages()
}

const slashCommands = computed<SlashCommand[]>(() => {
  if (isGrokMode.value) {
    return [
      {
        id: 'rename',
        label: '/rename',
        description: t('slash.rename'),
        run: () => grokStore.renameSession(composerSessionId.value),
      },
      {
        id: 'archive',
        label: '/archive',
        description: t('slash.archive'),
        run: archiveComposerSession,
      },
      {
        id: 'delete',
        label: '/delete',
        description: t('slash.delete'),
        run: deleteComposerSession,
      },
      {
        id: 'mcp',
        label: '/mcp',
        description: t('slash.mcp'),
        run: () => { void router.push({ name: 'capabilities', query: { tab: 'mcp' } }) },
      },
    ]
  }
  if (isClaudeMode.value) {
    return [
      {
        id: 'compact',
        label: '/compact',
        description: t('slash.compact'),
        run: () => claudeStore.compactSession(composerSessionId.value),
      },
      {
        id: 'archive',
        label: '/archive',
        description: t('slash.archive'),
        run: archiveComposerSession,
      },
      {
        id: 'rename',
        label: '/rename',
        description: t('slash.rename'),
        run: () => claudeStore.renameSession(composerSessionId.value),
      },
      {
        id: 'delete',
        label: '/delete',
        description: t('slash.delete'),
        run: deleteComposerSession,
      },
    ]
  }
  if (isGeminiMode.value || isOpenCodeMode.value) {
    return [
      {
        id: 'usage',
        label: '/usage',
        description: t('slash.usage'),
        run: () => runUsageCommand(),
      },
      {
        id: 'add',
        label: '/add',
        description: t('slash.add'),
        run: () => runAddCommand(),
      },
      {
        id: 'mention',
        label: '/mention',
        description: t('slash.add'),
        run: () => runAddCommand(),
      },
      {
        id: 'summarize',
        label: '/summarize',
        description: t('slash.summarizeLocal'),
        run: compactComposerSession,
      },
      {
        id: 'fork',
        label: '/fork',
        description: t('slash.fork'),
        run: forkComposerSession,
      },
      {
        id: 'archive',
        label: '/archive',
        description: t('slash.archive'),
        run: archiveComposerSession,
      },
      {
        id: 'rename',
        label: '/rename',
        description: t('slash.rename'),
        run: () => codexStore.renameThread(composerSessionId.value),
      },
      {
        id: 'delete',
        label: '/delete',
        description: t('slash.delete'),
        run: deleteComposerSession,
      },
      {
        id: 'mcp',
        label: '/mcp',
        description: t('slash.mcp'),
        run: () => { void router.push({ name: 'capabilities', query: { tab: 'mcp' } }) },
      },
    ]
  }
  return [
  {
    id: 'usage',
    label: '/usage',
    description: t('slash.usage'),
    run: () => runUsageCommand(),
  },
  {
    id: 'add',
    label: '/add',
    description: t('slash.add'),
    run: () => runAddCommand(),
  },
  {
    id: 'mention',
    label: '/mention',
    description: t('slash.add'),
    run: () => runAddCommand(),
  },
  {
    id: 'review',
    label: '/review',
    description: t('slash.review'),
    run: reviewComposerSession,
  },
  {
    id: 'compact',
    label: '/compact',
    description: t('slash.compact'),
    run: compactComposerSession,
  },
  {
    id: 'fork',
    label: '/fork',
    description: t('slash.fork'),
    run: forkComposerSession,
  },
  {
    id: 'archive',
    label: '/archive',
    description: t('slash.archive'),
    run: archiveComposerSession,
  },
  {
    id: 'rename',
    label: '/rename',
    description: t('slash.rename'),
    run: () => codexStore.renameThread(composerSessionId.value),
  },
  {
    id: 'delete',
    label: '/delete',
    description: t('slash.delete'),
    run: deleteComposerSession,
  },
  {
    id: 'mcp',
    label: '/mcp',
    description: t('slash.mcp'),
    run: () => { void router.push({ name: 'capabilities', query: { tab: 'mcp' } }) },
  },
  {
    id: 'memories',
    label: '/memories',
    description: t('slash.memories'),
    run: () => { window.dispatchEvent(new Event('nice-codex:open-memories')) },
  },
  {
    id: 'plan',
    label: '/plan',
    description: t('chat.planModeToggleHint'),
    run: () => togglePlanMode(),
  },
]
})

const slashQuery = computed(() => {
  const text = modelValue.value
  if (!text.startsWith('/') || text.includes('\n')) return ''
  return text.slice(1).split(/\s+/, 1)[0].toLocaleLowerCase()
})
const slashOpen = computed(() => modelValue.value.startsWith('/') && !modelValue.value.includes('\n'))
const filteredSlashCommands = computed(() => {
  const query = slashQuery.value
  if (!query) return slashCommands.value
  return slashCommands.value.filter((command) =>
    command.id.includes(query) || command.label.toLocaleLowerCase().includes(query),
  )
})

watch(filteredSlashCommands, (commands) => {
  if (slashIndex.value >= commands.length) slashIndex.value = Math.max(0, commands.length - 1)
})

const skillQuery = computed(() => {
  const text = modelValue.value
  const match = text.match(/(?:^|\s)\$([^\s]*)$/)
  return match ? (match[1] || '').toLocaleLowerCase() : ''
})
const skillOpen = computed(() => /(?:^|\s)\$[^\s]*$/.test(modelValue.value) && !modelValue.value.includes('\n'))
const skillOptions = computed(() => {
  const skills = capabilitiesStore.skills.filter((skill) => skill.enabled && skill.name)
  const query = skillQuery.value
  const filtered = query
    ? skills.filter((skill) =>
      skill.name.toLocaleLowerCase().includes(query)
      || skill.displayName.toLocaleLowerCase().includes(query),
    )
    : skills
  return filtered.slice(0, 12)
})
watch(skillOpen, (open) => {
  if (open) void capabilitiesStore.loadCapabilities()
})
watch(skillOptions, (options) => {
  if (skillIndex.value >= options.length) skillIndex.value = Math.max(0, options.length - 1)
})

const pluginQuery = computed(() => {
  const text = modelValue.value
  const match = text.match(/(?:^|\s)@([^\s]*)$/)
  return match ? (match[1] || '').toLocaleLowerCase() : ''
})
const pluginOpen = computed(() => /(?:^|\s)@[^\s]*$/.test(modelValue.value) && !modelValue.value.includes('\n') && !slashOpen.value && !skillOpen.value)
const pluginOptions = computed(() => {
  const plugins = capabilitiesStore.plugins.filter((plugin) => plugin.installed && plugin.name)
  const query = pluginQuery.value
  const filtered = query
    ? plugins.filter((plugin) =>
      plugin.name.toLocaleLowerCase().includes(query)
      || plugin.displayName.toLocaleLowerCase().includes(query),
    )
    : plugins
  return filtered.slice(0, 12)
})
watch(pluginOpen, (open) => {
  if (open) void capabilitiesStore.loadCapabilities()
})
watch(pluginOptions, (options) => {
  if (pluginIndex.value >= options.length) pluginIndex.value = Math.max(0, options.length - 1)
})

const isDraggingFiles = computed(() => dragDepth.value > 0)
const activeTokenUsage = computed(() => {
  const sessionId = composerSessionId.value
  if (!sessionId) return null
  if (isGrokMode.value) {
    const key = Object.keys(grokStore.tokenUsageBySession)
      .find((id) => grokStore.sameSession(id, sessionId))
    return key ? (grokStore.tokenUsageBySession[key] || null) : null
  }
  if (isClaudeMode.value) {
    const key = Object.keys(claudeStore.tokenUsageBySession)
      .find((id) => claudeStore.sameSession(id, sessionId))
    return key ? (claudeStore.tokenUsageBySession[key] || null) : null
  }
  const key = matchingCodexThreadKey(codexStore.tokenUsageByThread, sessionId)
  const usage = (key && codexStore.tokenUsageByThread[key]) || null
  if (!usage || (!isGeminiMode.value && !isOpenCodeMode.value)) return usage
  const runtime = isGeminiMode.value ? 'gemini' : 'opencode'
  const contextWindow = resolveProviderModelContextWindow(appStore.agentProviders, runtime, displayModel.value)
  return contextWindow > 0 && usage.modelContextWindow !== contextWindow
    ? { ...usage, modelContextWindow: contextWindow }
    : usage
})
const contextUsage = computed(() => buildContextUsageView(
  activeTokenUsage.value,
  isCodexMode.value ? CODEX_CONTEXT_BASELINE_TOKENS : 0,
))
const contextWindow = computed(() => contextUsage.value.contextWindow)
const contextUsedTokens = computed(() => contextUsage.value.usedTokens)
const hasContextUsage = computed(() => contextUsage.value.available)
const contextUsedPercent = computed(() => contextUsage.value.usedPercent)
const contextUsageTone = computed(() => {
  if (!hasContextUsage.value) return 'text-muted-foreground'
  if (contextUsedPercent.value >= 95) return 'text-destructive'
  if (contextUsedPercent.value >= 80) return 'text-warning'
  return 'text-primary'
})
const contextUsageTooltip = computed(() => {
  if (!hasContextUsage.value) return `${t('inspector.contextUsage')} · ${t('common.unavailable')}`
  const precision = contextUsage.value.estimated ? ` · ${t('inspector.contextEstimated')}` : ''
  return `${t('inspector.contextUsage')} ${contextUsedPercent.value.toFixed(1)}% · ${formatTokenCount(contextUsedTokens.value)} / ${formatTokenCount(contextWindow.value)}${precision}`
})
const sessionLocked = computed(() => Boolean(
  (isCodexMode.value || isGeminiMode.value || isOpenCodeMode.value)
  && composerSessionId.value
  && !composerSessionId.value.startsWith('pending-thread-')
  && composerCodexThread.value
  && codexStore.runtimeIDForThread(composerSessionId.value) === paneRuntime.value,
))
const grokProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'grok'))
const claudeProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'claude'))
const externalProvider = computed(() => appStore.agentProviders.find((item) => item.kind === paneRuntime.value))
const externalModelCatalog = computed(() => {
  const custom = isGeminiMode.value
    ? (appStore.settings.geminiCustomModels ?? [])
    : (appStore.settings.openCodeCustomModels ?? [])
  const catalog = (externalProvider.value?.models ?? []).map((item) => ({
    model: item.model,
    displayName: item.displayName || formatModelLabel(item.model),
    description: item.description || item.model,
    isDefault: item.isDefault,
    supportedReasoningEfforts: (externalProvider.value?.reasoningEfforts ?? []).map((effort) => ({
      effort: effort.effort,
      description: effort.description,
    })),
    serviceTiers: [],
    defaultReasoningEffort: externalProvider.value?.reasoningEfforts?.find((effort) => effort.isDefault)?.effort
      || (isGeminiMode.value ? 'auto' : 'high'),
    defaultServiceTier: '',
  }))
  for (const model of custom) {
    const id = model.trim()
    if (!id || catalog.some((item) => item.model.toLocaleLowerCase() === id.toLocaleLowerCase())) continue
    catalog.push({
      model: id,
      displayName: formatModelLabel(id),
      description: id,
      isDefault: false,
      supportedReasoningEfforts: [],
      serviceTiers: [],
      defaultReasoningEffort: isGeminiMode.value ? 'auto' : 'high',
      defaultServiceTier: '',
    })
  }
  return catalog
})
const displayModel = computed(() => {
  if (isGrokMode.value) {
    return composerGrokSession.value?.model || (appStore.settings.grokBackend === 'api'
      ? (appStore.settings.grokAPIModel || appStore.settings.grokBuildModel || 'grok-4.6')
      : (appStore.settings.grokBuildModel || appStore.settings.grokAPIModel || 'grok-4.6'))
  }
  if (isClaudeMode.value) {
    return composerClaudeSession.value?.model || appStore.settings.claudeModel || 'sonnet'
  }
  if (isGeminiMode.value) {
    return composerTimelineThread.value?.model
      || appStore.settings.geminiModel
      || externalModelCatalog.value.find((model) => model.isDefault)?.model
      || externalModelCatalog.value[0]?.model
      || ''
  }
  if (isOpenCodeMode.value) {
    return composerTimelineThread.value?.model || appStore.settings.openCodeModel || 'anthropic/claude-sonnet-4-6'
  }
  return composerTimelineThread.value?.model || appStore.settings.model
})
const displayEffort = computed(() => {
  if (isGrokMode.value) return composerGrokSession.value?.effort || appStore.settings.grokEffort || 'high'
  if (isClaudeMode.value) return composerClaudeSession.value?.effort || appStore.settings.claudeEffort || 'high'
  if (isGeminiMode.value) return composerTimelineThread.value?.effort || appStore.settings.geminiEffort || 'auto'
  if (isOpenCodeMode.value) return composerTimelineThread.value?.effort || appStore.settings.openCodeEffort || 'high'
  return composerTimelineThread.value?.effort || appStore.settings.effort
})
const selectedModel = computed(() => {
  if (isGeminiMode.value || isOpenCodeMode.value) {
    return externalModelCatalog.value.find((model) => model.model === displayModel.value)
  }
  return appStore.models.find((model) => model.model === displayModel.value)
})
const selectableModels = computed(() => {
  if (isGrokMode.value) {
    return modelsForGrokRuntime(
      grokProvider.value?.models ?? [],
      displayModel.value,
      appStore.settings.grokCustomModels ?? [],
    )
  }
  if (isClaudeMode.value) {
    return modelsForClaudeRuntime(
      claudeProvider.value?.models ?? [],
      displayModel.value,
      appStore.settings.claudeCustomModels ?? [],
    )
  }
  if (isGeminiMode.value || isOpenCodeMode.value) {
    return externalModelCatalog.value
  }
  return modelsForRuntime(appStore.models, appStore.settings.customModels ?? []) ?? []
})
const composerModelOptions = computed(() => (selectableModels.value ?? []).map((model) => {
  const description = 'description' in model && typeof model.description === 'string'
    ? model.description
    : (model.displayName && model.displayName !== model.model ? model.model : '')
  return {
    value: model.model,
    label: model.displayName || formatModelLabel(model.model),
    // Show alias → resolved model id mapping (Claude) or raw id (others).
    description,
    badge: model.isDefault ? t('common.recommended') : '',
  }
}))
const composerModelSelection = computed({
  get: () => displayModel.value,
  set: (value: string) => { void applyModelSelection(value) },
})
const reasoningOptions = computed(() => {
  if (isGrokMode.value) {
    const fromProvider = grokProvider.value?.reasoningEfforts ?? []
    if (fromProvider.length) {
      return fromProvider.map((item) => ({
        effort: item.effort,
        displayName: item.displayName,
        description: item.description,
      }))
    }
    return [...DEFAULT_GROK_REASONING]
  }
  if (isClaudeMode.value) {
    const fromProvider = claudeProvider.value?.reasoningEfforts ?? []
    if (fromProvider.length) {
      return fromProvider.map((item) => ({
        effort: item.effort,
        displayName: item.displayName,
        description: item.description,
      }))
    }
    return [
      { effort: 'high', displayName: 'High', description: 'Deep reasoning' },
      { effort: 'medium', displayName: 'Medium', description: 'Balanced' },
      { effort: 'low', displayName: 'Low', description: 'Faster' },
      { effort: 'xhigh', displayName: 'Extra high', description: 'Extended' },
      { effort: 'max', displayName: 'Max', description: 'Maximum' },
    ]
  }
  if (isGeminiMode.value) return []
  const fromModel = selectedModel.value?.supportedReasoningEfforts ?? []
  return fromModel.length ? fromModel : [...DEFAULT_CODEX_REASONING]
})
/** Selected permission preset: ask | auto | strict — labels always match menu items. */
const permissionPreset = computed((): 'ask' | 'auto' | 'strict' => {
  if (isClaudeMode.value) {
    const mode = appStore.settings.claudePermissionMode || ''
    if (mode === 'bypassPermissions') return 'auto'
    if (mode === 'plan') return 'strict'
    if (mode === 'acceptEdits' || mode === 'auto' || mode === 'dontAsk' || mode === 'manual') return 'ask'
    // Fall back to legacy sandbox pair.
  }
  const sandbox = isGrokMode.value
    ? appStore.settings.grokSandbox
    : isClaudeMode.value
      ? appStore.settings.claudeSandbox
      : isGeminiMode.value
        ? appStore.settings.geminiSandbox
        : isOpenCodeMode.value
          ? appStore.settings.openCodeSandbox
      : appStore.settings.sandbox
  const approval = isGrokMode.value
    ? appStore.settings.grokApprovalPolicy
    : isClaudeMode.value
      ? appStore.settings.claudeApprovalPolicy
      : isGeminiMode.value
        ? appStore.settings.geminiApprovalPolicy
        : isOpenCodeMode.value
          ? appStore.settings.openCodeApprovalPolicy
      : appStore.settings.approvalPolicy
  if (sandbox === 'danger-full-access' && approval === 'never') return 'auto'
  if (sandbox === 'read-only') return 'strict'
  return 'ask'
})
const permissionLabel = computed(() => {
  if (permissionPreset.value === 'auto') return t('settings.permissionAuto')
  if (permissionPreset.value === 'strict') return t('settings.permissionStrict')
  return t('settings.permissionAsk')
})
/** Secondary hint under the permission control (Claude official mode). */
const permissionDetail = computed(() => {
  if (!isClaudeMode.value) return ''
  const mode = appStore.settings.claudePermissionMode
    || (permissionPreset.value === 'auto'
      ? 'bypassPermissions'
      : permissionPreset.value === 'strict'
        ? 'plan'
        : 'acceptEdits')
  return mode
})
const selectedEffortLabel = computed(() => {
  const effort = displayEffort.value
  const option = reasoningOptions.value.find((item) => item.effort === effort)
  if (option && 'displayName' in option && option.displayName) return String(option.displayName)
  if (!effort) return ''
  return effort.charAt(0).toUpperCase() + effort.slice(1)
})

const EFFORT_ORDER = ['auto', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']
const effortOptions = computed(() => {
  const options = reasoningOptions.value.map((item) => ({
    value: item.effort,
    label: 'displayName' in item && item.displayName ? String(item.displayName) : item.effort,
  }))
  const current = displayEffort.value.trim()
  if (current && !options.some((item) => item.value === current)) {
    options.push({ value: current, label: selectedEffortLabel.value || current })
  }
  return options.sort((left, right) => {
    const leftRank = EFFORT_ORDER.indexOf(left.value.toLowerCase())
    const rightRank = EFFORT_ORDER.indexOf(right.value.toLowerCase())
    if (leftRank < 0 && rightRank < 0) return left.label.localeCompare(right.label)
    if (leftRank < 0) return 1
    if (rightRank < 0) return -1
    return leftRank - rightRank
  })
})
const effortCurrentIndex = computed(() => Math.max(0, effortOptions.value.findIndex((item) => item.value === displayEffort.value)))
const effortDisplayIndex = computed(() => effortPreviewIndex.value >= 0 ? effortPreviewIndex.value : effortCurrentIndex.value)
const effortPopoverLabel = computed(() => effortOptions.value[effortDisplayIndex.value]?.label || selectedEffortLabel.value)

function effortMarkerPosition(index: number): string {
  if (effortOptions.value.length <= 1) return '0%'
  return `${index / (effortOptions.value.length - 1) * 100}%`
}

function previewEffort(event: Event): void {
  effortDragging.value = true
  effortPreviewIndex.value = Number((event.target as HTMLInputElement).value)
}

function commitEffort(event: Event): void {
  const index = Number((event.target as HTMLInputElement).value)
  const value = effortOptions.value[index]?.value
  effortDragging.value = false
  effortPreviewIndex.value = -1
  if (value) void onEffortChange(value)
}

function chooseEffort(index: number): void {
  const value = effortOptions.value[index]?.value
  effortDragging.value = false
  effortPreviewIndex.value = -1
  if (value) void onEffortChange(value)
}

function cancelEffortPreview(): void {
  effortDragging.value = false
  effortPreviewIndex.value = -1
}

watch(effortPopoverOpen, (open) => {
  if (!open) {
    effortDragging.value = false
    effortPreviewIndex.value = -1
  }
})

const composerWorkspacePath = computed(() =>
  composerTargetWorkspace(paneRuntime.value, composerSessionId.value) || workspaceStore.currentPath,
)
const composerWorkspaceName = computed(() => {
  const clean = composerWorkspacePath.value.replace(/[\\/]+$/, '')
  return clean.split(/[\\/]/).filter(Boolean).at(-1) || t('sidebar.chooseFolder')
})
function workspaceDisplayName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '')
  return clean.split(/[\\/]/).filter(Boolean).at(-1) || path
}
const composerBranch = computed(() => {
  if (!workspaceStore.branch || !sameWorkspacePath(composerWorkspacePath.value, workspaceStore.currentPath)) return ''
  return workspaceStore.branch
})
const {
  recentWorkspacePaths: composerRecentWorkspacePaths,
  switching: composerWorkspaceSwitching,
  selectWorkspace: selectWorkspaceForComposer,
  switchWorkspace: switchWorkspaceForComposer,
} = useComposerWorkspaceSwitcher({
  runtime: paneRuntime,
  paneId,
  isArenaPane,
  currentWorkspacePath: composerWorkspacePath,
})
watch(composerWorkspacePath, () => {
  branchMenuOpen.value = false
  branchQuery.value = ''
})
type ComposerRuntimeOption = {
  kind: WorkspaceRuntime
  name: string
  icon: Component
  ready: boolean
  message: string
}

const COMPOSER_RUNTIME_ORDER: WorkspaceRuntime[] = ['codex', 'claude', 'grok', 'gemini', 'opencode']
const COMPOSER_RUNTIME_FALLBACK_NAMES: Record<WorkspaceRuntime, string> = {
  codex: 'Codex',
  claude: 'Claude',
  grok: 'Grok',
  gemini: 'Gemini',
  opencode: 'OpenCode',
}
const COMPOSER_RUNTIME_ICONS: Record<WorkspaceRuntime, Component> = {
  codex: OpenAIIcon,
  claude: ClaudeIcon,
  grok: GrokIcon,
  gemini: GeminiIcon,
  opencode: OpenCodeIcon,
}
const composerRuntimeOptions = computed<ComposerRuntimeOption[]>(() =>
  COMPOSER_RUNTIME_ORDER.map((kind) => {
    const provider = appStore.agentProviders.find((item) => item.kind === kind)
    return {
      kind,
      name: provider?.name || COMPOSER_RUNTIME_FALLBACK_NAMES[kind],
      icon: COMPOSER_RUNTIME_ICONS[kind],
      ready: Boolean(provider?.runtimeReady),
      message: provider?.message || '',
    }
  }),
)
const composerRuntimeLabel = computed(() =>
  composerRuntimeOptions.value.find((item) => item.kind === paneRuntime.value)?.name
    || COMPOSER_RUNTIME_FALLBACK_NAMES[paneRuntime.value],
)
const composerRuntimeIcon = computed(() => COMPOSER_RUNTIME_ICONS[paneRuntime.value])

function relatedQueueRows<T extends { id: string }>(
  record: Record<string, T[]>,
  sessionId: string,
  sameSession: (left: string, right: string) => boolean,
): T[] {
  const rows = Object.entries(record)
    .filter(([id]) => sameSession(id, sessionId))
    .flatMap(([, queue]) => queue)
  return [...new Map(rows.map((row) => [row.id, row])).values()]
}

const activeQueuedMessages = computed(() => {
  const sessionId = composerSessionId.value
  if (!sessionId) return []
  if (isGrokMode.value) {
    return relatedQueueRows(grokStore.queuedBySession, sessionId, grokStore.sameSession)
  }
  if (isClaudeMode.value) {
    return relatedQueueRows(claudeStore.queueBySession, sessionId, claudeStore.sameSession)
  }
  return relatedQueueRows(codexStore.queuedMessagesByThread, sessionId, codexStore.sameThread)
})
/** Only show the queue strip when something is actually waiting / failed — not the in-flight send. */
const showQueueStrip = computed(() =>
  activeQueuedMessages.value.some((message) => message.state === 'queued' || message.state === 'failed'),
)
const queuedWaitingCount = computed(() =>
  activeQueuedMessages.value.filter((message) => message.state === 'queued').length,
)
const queuedFailedCount = computed(() =>
  activeQueuedMessages.value.filter((message) => message.state === 'failed').length,
)
const nextQueuedPreview = computed(() => {
  const next = activeQueuedMessages.value.find((message) => message.state === 'queued' || message.state === 'failed')
  if (!next) return ''
  const text = (next.text || '').replace(/\s+/g, ' ').trim()
  if (text) return text.length > 42 ? `${text.slice(0, 42)}…` : text
  return next.images.length ? t('chat.queuedImageOnly') : ''
})
const canSendDuringWorkspaceSwitch = computed(() => Boolean(
  (isCodexMode.value || isGeminiMode.value || isOpenCodeMode.value)
  && composerCodexThread.value?.cwd
  && codexStore.threadIsWorkspaceSelectionPending(composerSessionId.value),
))
/**
 * Follow-ups stay sendable while a turn runs. Codex may steer only when the
 * store has a stable live owner; every uncertain state remains queue-first.
 */
const canSend = computed(() => {
  const hasContent = Boolean(modelValue.value.trim()) || attachedImages.value.length > 0
  if (sendAdmissionPending.value) return false
  if (attachingImages.value) return false
  if (!isArenaPane.value && workspaceStore.switchingWorkspace && !canSendDuringWorkspaceSwitch.value) return false
  if (isArenaPane.value && workspaceStore.switchingWorkspace && !composerSessionId.value) return false
  if (isGrokMode.value) {
    return hasContent && grokStore.isReady
  }
  if (isClaudeMode.value) {
    return hasContent
      && claudeStore.isReady
      && Boolean(composerTargetWorkspace('claude', composerSessionId.value))
  }
  return hasContent && codexStore.isRuntimeReady(paneRuntime.value) && !codexStore.creatingThread
})

function canMoveQueued(index: number, direction: 'up' | 'down'): boolean {
  const messages = activeQueuedMessages.value
  const message = messages[index]
  if (!message || message.state === 'sending') return false
  let floor = 0
  while (floor < messages.length && messages[floor]?.state === 'sending') floor += 1
  if (direction === 'up') return index > floor
  return index < messages.length - 1
}

function reorderQueued(messageId: string, direction: 'up' | 'down'): void {
  if (isGrokMode.value) grokStore.reorderQueuedMessage(messageId, direction)
  else if (isClaudeMode.value) claudeStore.reorderQueuedMessage(messageId, direction)
  else codexStore.reorderQueuedMessage(messageId, direction, composerSessionId.value)
}

function sendQueuedNow(messageId: string): void {
  if (isGrokMode.value) void grokStore.sendQueuedMessageNow(messageId)
  else if (isClaudeMode.value) void claudeStore.sendQueuedMessageNow(messageId)
  else void codexStore.sendQueuedMessageNow(messageId, composerSessionId.value)
}

function retryQueued(messageId: string): void {
  if (isGrokMode.value) grokStore.retryQueuedMessage(messageId)
  else if (isClaudeMode.value) claudeStore.retryQueuedMessage(messageId)
  else codexStore.retryQueuedMessage(messageId, composerSessionId.value)
}

function removeQueued(messageId: string): void {
  if (isGrokMode.value) grokStore.removeQueuedMessage(messageId)
  else if (isClaudeMode.value) claudeStore.removeQueuedMessage(messageId)
  else codexStore.removeQueuedMessage(messageId, composerSessionId.value)
}

function updateQueued(messageId: string, text: string): boolean {
  if (isGrokMode.value) return grokStore.updateQueuedMessage(messageId, text)
  if (isClaudeMode.value) return claudeStore.updateQueuedMessage(messageId, text)
  return codexStore.updateQueuedMessage(messageId, text, composerSessionId.value)
}

function cancelQueuedEdit(): void {
  editingQueuedId.value = ''
  queuedEditDraft.value = ''
  queuedEditError.value = ''
}

function beginQueuedEdit(message: EditableQueuedMessage): void {
  if (message.state === 'sending') return
  editingQueuedId.value = message.id
  queuedEditDraft.value = message.text
  queuedEditError.value = ''
  void nextTick(() => {
    const selector = `[data-queue-edit="${CSS.escape(message.id)}"]`
    const field = composer.value?.querySelector<HTMLTextAreaElement>(selector)
    field?.focus()
    field?.setSelectionRange(field.value.length, field.value.length)
  })
}

function saveQueuedEdit(message: EditableQueuedMessage): void {
  if (message.id !== editingQueuedId.value || message.state === 'sending') {
    cancelQueuedEdit()
    return
  }
  const nextText = queuedEditDraft.value.trim()
  if (!nextText && message.images.length === 0) {
    queuedEditError.value = t('chat.queuedEditEmpty')
    return
  }
  if (!updateQueued(message.id, nextText)) {
    queuedEditError.value = t('chat.queuedEditUnavailable')
    return
  }
  cancelQueuedEdit()
  notify('success', t('chat.queuedEditSaved'))
}

function handleQueuedEditKeydown(event: KeyboardEvent, message: EditableQueuedMessage): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    cancelQueuedEdit()
    return
  }
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing) {
    event.preventDefault()
    saveQueuedEdit(message)
  }
}

watch(activeQueuedMessages, (messages) => {
  if (!editingQueuedId.value) return
  const message = messages.find((row) => row.id === editingQueuedId.value)
  if (!message || message.state === 'sending') cancelQueuedEdit()
})

function clearFailedQueued(): void {
  for (const message of activeQueuedMessages.value) {
    if (message.state === 'failed') removeQueued(message.id)
  }
}

const willQueueOnSend = computed(() => {
  const sessionId = composerSessionId.value
  if (isGrokMode.value) {
    const loadingActiveSession = Boolean(sessionId && grokStore.sameSession(grokStore.loadingSessionId, sessionId))
    const running = grokStore.runningSessionIds.some((id) => grokStore.sameSession(id, sessionId))
    return loadingActiveSession || running || activeQueuedMessages.value.some((item) => item.state === 'sending') || showQueueStrip.value
  }
  if (isClaudeMode.value) {
    const loadingActiveSession = Boolean(sessionId && claudeStore.sameSession(claudeStore.loadingSessionId, sessionId))
    const running = claudeStore.runningSessionIds.some((id) => claudeStore.sameSession(id, sessionId))
    return loadingActiveSession || running || activeQueuedMessages.value.some((item) => item.state === 'sending') || showQueueStrip.value
  }
  return Boolean(sessionId && codexStore.threadHasActiveWork(sessionId)) || showQueueStrip.value
})
const activeRuntimeTurnRunning = computed(() => {
  const sessionId = composerSessionId.value
  if (isGrokMode.value) return grokStore.runningSessionIds.some((id) => grokStore.sameSession(id, sessionId))
  if (isClaudeMode.value) return claudeStore.runningSessionIds.some((id) => claudeStore.sameSession(id, sessionId))
  return codexStore.threadHasActiveWork(sessionId)
})
const activeRuntimeSending = computed(() => {
  if (isGrokMode.value || isClaudeMode.value) {
    return activeQueuedMessages.value.some((item) => item.state === 'sending')
  }
  return codexStore.isThreadSubmitting(composerSessionId.value)
})
const composerRuntimeInteractionDisabled = computed(() => Boolean(
  composerRuntimeSwitching.value
  || activeRuntimeTurnRunning.value
  || activeRuntimeSending.value
  || sendAdmissionPending.value,
))

async function selectComposerRuntime(runtime: WorkspaceRuntime): Promise<void> {
  if (composerRuntimeInteractionDisabled.value || runtime === paneRuntime.value) return
  composerRuntimeSwitching.value = true
  try {
    if (isArenaPane.value && paneId.value) {
      arenaStore.setPaneRuntime(paneId.value, runtime)
      return
    }
    const switched = await appStore.setActiveRuntime(runtime)
    if (!switched) notify('error', t('sidebar.runtimeSwitchFailed'))
  } catch (error) {
    notify('error', t('sidebar.runtimeSwitchFailed'), error instanceof Error ? error.message : String(error))
  } finally {
    composerRuntimeSwitching.value = false
  }
}
const branchInteractionDisabled = computed(() => Boolean(
  composerWorkspaceSwitching.value
  || workspaceStore.switchingWorkspace
  || workspaceStore.branchSwitching
  || activeRuntimeTurnRunning.value
  || activeRuntimeSending.value
  || sendAdmissionPending.value,
))
const filteredGitBranches = computed(() => {
  const query = branchQuery.value.trim().toLocaleLowerCase()
  if (!query) return workspaceStore.gitBranches
  return workspaceStore.gitBranches.filter((branch) => branch.toLocaleLowerCase().includes(query))
})
watch(branchInteractionDisabled, (disabled) => {
  if (disabled) branchMenuOpen.value = false
})

async function onBranchMenuOpenChange(open: boolean): Promise<void> {
  if (!open) {
    branchMenuOpen.value = false
    branchQuery.value = ''
    return
  }
  if (branchInteractionDisabled.value) return
  branchMenuOpen.value = true
  await nextTick()
  const input = branchSearchInput.value?.$el as HTMLInputElement | undefined
  input?.focus()
  await workspaceStore.loadGitBranches(composerWorkspacePath.value)
}

async function selectGitBranch(branch: string): Promise<void> {
  if (!branch || branchInteractionDisabled.value) return
  branchMenuOpen.value = false
  branchQuery.value = ''
  if (branch === composerBranch.value) return
  await workspaceStore.switchBranch(branch, composerWorkspacePath.value)
}

function selectFirstFilteredBranch(): void {
  const branch = filteredGitBranches.value[0]
  if (branch) void selectGitBranch(branch)
}

const stopDisabled = computed(() => {
  if (!isArenaPane.value && workspaceStore.switchingWorkspace) return true
  if (isGrokMode.value) return grokStore.isSessionInterrupting(composerSessionId.value)
  if (isClaudeMode.value) return claudeStore.isSessionInterrupting(composerSessionId.value)
  return codexStore.isThreadInterrupting(composerSessionId.value)
})
const sendButtonLabel = computed(() => {
  if (willQueueOnSend.value) return t('chat.queueSend')
  return t('chat.send')
})
const primaryActionLabel = computed(() => {
  if (!activeRuntimeTurnRunning.value) return sendButtonLabel.value
  return stopDisabled.value ? t('chat.stopping') : t('chat.stop')
})
const composerPlaceholder = computed(() => {
  if (isGrokMode.value && willQueueOnSend.value) return t('chat.queuePlaceholder')
  if (isGrokMode.value) return t('chat.grokPlaceholder')
  if (isClaudeMode.value && willQueueOnSend.value) return t('chat.queuePlaceholder')
  if (isClaudeMode.value) return t('chat.claudePlaceholder')
  if ((isGeminiMode.value || isOpenCodeMode.value) && willQueueOnSend.value) return t('chat.queuePlaceholder')
  if (isGeminiMode.value) return t('chat.runtimePlaceholder', { runtime: 'Gemini CLI' })
  if (isOpenCodeMode.value) return t('chat.runtimePlaceholder', { runtime: 'OpenCode' })
  if (willQueueOnSend.value) return t('chat.queuePlaceholder')
  return t('chat.placeholder')
})
const composerShortcutHint = computed(() => {
  const mod = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl'
  if (appStore.settings.sendWithModifier) {
    return t('chat.shortcutModifier', { key: mod })
  }
  return t('chat.shortcut')
})

watch(modelValue, () => {
  if (!applyingSentHistory && sentHistoryIndex.value >= 0) resetSentHistoryNavigation()
  resize()
}, { flush: 'post' })
watch(composerExpanded, resize, { flush: 'post' })
watch(() => props.draftKey, resetSentHistoryNavigation, { flush: 'sync' })
watch(
  [selectableModels, displayModel],
  () => {
    const models = selectableModels.value ?? []
    if (!models.length) return
    const current = displayModel.value.trim()
    if (current && models.some((model) => model.model === current)) return
    const preferred = models.find((model) => model.isDefault)?.model || models[0]?.model
    if (preferred && preferred !== current) void applyModelSelection(preferred)
  },
  { flush: 'post' },
)
onMounted(resize)

function resize(): void {
  void nextTick(() => {
    const textarea = composerInput.value?.$el as HTMLTextAreaElement | undefined
    if (!textarea) return
    const max = composerExpanded.value
      ? Math.min(COMPOSER_MAX_EXPANDED, Math.max(220, Math.floor(window.innerHeight * 0.44)))
      : Math.min(COMPOSER_MAX_COLLAPSED, Math.max(120, Math.floor(window.innerHeight * 0.3)))
    textarea.style.height = '0px'
    if (composerExpanded.value) {
      textarea.style.height = `${max}px`
      textarea.style.overflowY = 'auto'
      return
    }
    const height = Math.min(textarea.scrollHeight, max)
    textarea.style.height = `${height}px`
    textarea.style.overflowY = textarea.scrollHeight > max ? 'auto' : 'hidden'
  })
}

function toggleComposerHeight(): void {
  composerExpanded.value = !composerExpanded.value
  void nextTick(() => {
    const textarea = composerInput.value?.$el as HTMLTextAreaElement | undefined
    textarea?.focus({ preventScroll: true })
  })
}

function resetSentHistoryNavigation(): void {
  sentHistoryIndex.value = -1
  sentHistorySnapshot.value = []
  sentHistoryDraft.value = ''
}

function canNavigateSentHistory(event: KeyboardEvent, direction: -1 | 1): event is KeyboardEvent & { target: HTMLTextAreaElement } {
  const target = event.target
  if (
    !(target instanceof HTMLTextAreaElement)
    || composing.value
    || event.isComposing
    || event.altKey
    || event.ctrlKey
    || event.metaKey
    || event.shiftKey
    || target.selectionStart !== target.selectionEnd
  ) return false
  if (sentHistoryIndex.value >= 0) return true
  if (direction > 0) return false
  return !target.value.slice(0, target.selectionStart).includes('\n')
}

function applySentHistoryValue(next: string): void {
  applyingSentHistory = true
  modelValue.value = next
  void nextTick(() => {
    const textarea = composerInput.value?.$el as HTMLTextAreaElement | undefined
    if (textarea) {
      const cursor = next.length
      textarea.setSelectionRange(cursor, cursor)
    }
    applyingSentHistory = false
    if (sentHistoryIndex.value < 0) {
      sentHistorySnapshot.value = []
      sentHistoryDraft.value = ''
    }
  })
}

function restoreSentHistoryDraft(event: KeyboardEvent): void {
  if (sentHistoryIndex.value < 0) return
  event.preventDefault()
  const draft = sentHistoryDraft.value
  sentHistoryIndex.value = -1
  applySentHistoryValue(draft)
}

function navigateSentHistory(event: KeyboardEvent, direction: -1 | 1): boolean {
  if (!canNavigateSentHistory(event, direction)) return false

  if (sentHistoryIndex.value < 0) {
    const history = sentMessageHistory.value
    if (!history.length || direction > 0) return false
    sentHistorySnapshot.value = [...history]
    sentHistoryDraft.value = modelValue.value
    sentHistoryIndex.value = history.length - 1
  } else if (direction < 0) {
    sentHistoryIndex.value = Math.max(0, sentHistoryIndex.value - 1)
  } else if (sentHistoryIndex.value < sentHistorySnapshot.value.length - 1) {
    sentHistoryIndex.value += 1
  } else {
    sentHistoryIndex.value = -1
  }

  const next = sentHistoryIndex.value >= 0
    ? (sentHistorySnapshot.value[sentHistoryIndex.value] ?? '')
    : sentHistoryDraft.value
  event.preventDefault()
  applySentHistoryValue(next)
  return true
}

function onKeydown(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.key.toLocaleLowerCase() === 'u') {
    event.preventDefault()
    void selectComposerFiles()
    return
  }
  if (pluginOpen.value && pluginOptions.value.length) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      pluginIndex.value = (pluginIndex.value + 1) % pluginOptions.value.length
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      pluginIndex.value = (pluginIndex.value - 1 + pluginOptions.value.length) % pluginOptions.value.length
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      modelValue.value = modelValue.value.replace(/(?:^|\s)@[^\s]*$/, (chunk) => chunk.startsWith(' ') ? ' ' : '')
      return
    }
    if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
      event.preventDefault()
      insertPlugin(pluginOptions.value[pluginIndex.value]?.name)
      return
    }
  }
  if (skillOpen.value && skillOptions.value.length) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      skillIndex.value = (skillIndex.value + 1) % skillOptions.value.length
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      skillIndex.value = (skillIndex.value - 1 + skillOptions.value.length) % skillOptions.value.length
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      modelValue.value = modelValue.value.replace(/(?:^|\s)\$[^\s]*$/, (chunk) => chunk.startsWith(' ') ? ' ' : '')
      return
    }
    if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
      event.preventDefault()
      insertSkill(skillOptions.value[skillIndex.value]?.name)
      return
    }
  }
  if (slashOpen.value && filteredSlashCommands.value.length) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      slashIndex.value = (slashIndex.value + 1) % filteredSlashCommands.value.length
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      slashIndex.value = (slashIndex.value - 1 + filteredSlashCommands.value.length) % filteredSlashCommands.value.length
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      modelValue.value = ''
      return
    }
    if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
      event.preventDefault()
      void runSlashCommand(filteredSlashCommands.value[slashIndex.value])
      return
    }
  }
  if (slashOpen.value && event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    notify('warning', t('slash.title'), `${t('slash.unknown')}: ${modelValue.value}`)
    modelValue.value = ''
    return
  }
  if (event.key === 'Escape' && sentHistoryIndex.value >= 0) {
    restoreSentHistoryDraft(event)
    return
  }
  if (event.key === 'ArrowUp' && navigateSentHistory(event, -1)) return
  if (event.key === 'ArrowDown' && navigateSentHistory(event, 1)) return
  if (
    sentHistoryIndex.value >= 0
    && (event.key === 'ArrowLeft' || event.key === 'ArrowRight' || event.key === 'Home' || event.key === 'End')
  ) resetSentHistoryNavigation()
  // Official Codex: Shift+Tab toggles plan mode.
  if (isCodexMode.value && event.key === 'Tab' && event.shiftKey) {
    event.preventDefault()
    void togglePlanMode()
    return
  }
  if (event.key !== 'Enter' || composing.value) return
  const requireModifier = Boolean(appStore.settings.sendWithModifier)
  if (requireModifier) {
    if (!(event.metaKey || event.ctrlKey) || event.shiftKey) return
    event.preventDefault()
    void send()
    return
  }
  if (event.shiftKey) return
  event.preventDefault()
  void send()
}

function insertSkill(name?: string): void {
  if (!name) return
  modelValue.value = modelValue.value.replace(/(?:^|\s)\$[^\s]*$/, (chunk) => {
    const prefix = chunk.startsWith(' ') ? ' ' : ''
    return `${prefix}$${name} `
  })
  skillIndex.value = 0
  resize()
}

function insertPlugin(name?: string): void {
  if (!name) return
  modelValue.value = modelValue.value.replace(/(?:^|\s)@[^\s]*$/, (chunk) => {
    const prefix = chunk.startsWith(' ') ? ' ' : ''
    return `${prefix}@${name} `
  })
  pluginIndex.value = 0
  resize()
}

async function runSlashCommand(command?: SlashCommand): Promise<void> {
  if (!command) return
  const args = modelValue.value.trim().split(/\s+/).slice(1)
  modelValue.value = ''
  if (command.id === 'usage' && args.length) {
    const range = args[0].toLocaleLowerCase()
    if (args.length > 1 || !['daily', 'today', 'weekly', 'week', 'cumulative', 'lifetime', 'all'].includes(range)) {
      notify('warning', t('slash.title'), t('slash.usageSyntax'))
      return
    }
    await runUsageCommand(
      range === 'daily' || range === 'today'
        ? 'today'
        : range === 'weekly' || range === 'week'
          ? 'week'
          : 'cumulative',
    )
    return
  }
  await command.run()
}

const collaborationMode = computed(() => {
  const sessionMode = composerCodexThread.value?.collaborationMode
  if (sessionMode === 'plan' || sessionMode === 'default') return sessionMode
  return appStore.settings.collaborationMode === 'plan' ? 'plan' : 'default'
})
const isPlanMode = computed(() => collaborationMode.value === 'plan')

async function togglePlanMode(): Promise<void> {
  if (!isCodexMode.value) return
  await codexStore.setCollaborationMode(
    isPlanMode.value ? 'default' : 'plan',
    composerSessionId.value,
  )
}

function composerSessionExists(runtime: WorkspaceRuntime, sessionId: string): boolean {
  if (runtime === 'grok') {
    return grokStore.sessions.some((item) => grokStore.sameSession(item.id, sessionId))
  }
  if (runtime === 'claude') {
    return claudeStore.sessions.some((item) => claudeStore.sameSession(item.id, sessionId))
  }
  return codexStore.threads.some((item) => codexStore.sameThread(item.id, sessionId))
    || Object.values(codexStore.projectThreads).flat()
      .some((item) => codexStore.sameThread(item.id, sessionId))
}

function composerTargetWorkspace(runtime: WorkspaceRuntime, sessionId: string): string {
  if (runtime === 'grok') {
    const session = grokStore.sessions.find((item) => grokStore.sameSession(item.id, sessionId))
    return session?.workspace || (sessionId ? '' : grokStore.workspacePath)
  }
  if (runtime === 'claude') {
    const session = claudeStore.sessions.find((item) => claudeStore.sameSession(item.id, sessionId))
    return session?.workspace || (sessionId ? '' : claudeStore.workspacePath)
  }
  const thread = codexStore.threads.find((item) => codexStore.sameThread(item.id, sessionId))
    || Object.values(codexStore.projectThreads).flat()
      .find((item) => codexStore.sameThread(item.id, sessionId))
  return thread?.cwd || (sessionId ? '' : appStore.currentWorkspacePath)
}

function clearRemovedComposerSession(
  runtime: WorkspaceRuntime,
  arena: boolean,
  sessionId: string,
  resolvedId: string,
): void {
  if (!arena || composerSessionExists(runtime, resolvedId || sessionId)) return
  arenaStore.clearSessionBindings(runtime, [sessionId, resolvedId])
}

async function archiveComposerSession(): Promise<void> {
  const sessionId = composerSessionId.value
  if (!sessionId) return
  const runtime = paneRuntime.value
  const arena = isArenaPane.value
  const resolvedId = runtime === 'grok'
    ? grokStore.resolveSessionId(sessionId)
    : runtime === 'claude'
      ? claudeStore.resolveSessionId(sessionId)
      : codexStore.resolveThreadID(sessionId)
  if (runtime === 'grok') await grokStore.archiveSession(sessionId)
  else if (runtime === 'claude') await claudeStore.archiveSession(sessionId)
  else await codexStore.archiveThread(sessionId)
  clearRemovedComposerSession(runtime, arena, sessionId, resolvedId)
}

async function deleteComposerSession(): Promise<void> {
  const sessionId = composerSessionId.value
  if (!sessionId) return
  const runtime = paneRuntime.value
  const arena = isArenaPane.value
  const resolvedId = runtime === 'grok'
    ? grokStore.resolveSessionId(sessionId)
    : runtime === 'claude'
      ? claudeStore.resolveSessionId(sessionId)
      : codexStore.resolveThreadID(sessionId)
  if (runtime === 'grok') await grokStore.deleteSession(sessionId)
  else if (runtime === 'claude') await claudeStore.deleteSession(sessionId)
  else await codexStore.deleteThread(sessionId)
  clearRemovedComposerSession(runtime, arena, sessionId, resolvedId)
}

function compactComposerSession(): void {
  const sessionId = composerSessionId.value
  if (sessionId) void codexStore.compactThread(sessionId, !isArenaPane.value)
}

function reviewComposerSession(): void {
  const sessionId = composerSessionId.value
  if (!sessionId) return
  void codexStore.startReview(
    { targetType: 'uncommittedChanges', delivery: 'inline' },
    sessionId,
  )
}

async function forkComposerSession(): Promise<void> {
  const sessionId = composerSessionId.value
  if (!sessionId) return
  const arena = isArenaPane.value
  const targetPaneId = paneId.value
  const runtime = paneRuntime.value
  const thread = await codexStore.forkThread(sessionId, !arena)
  const pane = arenaStore.panes.find((item) => item.id === targetPaneId)
  if (
    arena
    && thread?.id
    && arenaStore.isArenaMode
    && pane?.runtime === runtime
    && codexStore.sameThread(arenaStore.sessionForPane(targetPaneId), sessionId)
  ) arenaStore.selectPaneSession(targetPaneId, thread.id)
}

function arenaComposerTargetIsCurrent(
  targetPaneId: string,
  targetRuntime: WorkspaceRuntime,
  expectedSessionId: string,
): boolean {
  const pane = arenaStore.panes.find((item) => item.id === targetPaneId)
  return Boolean(
    arenaStore.isArenaMode
    && pane?.runtime === targetRuntime
    && arenaStore.sessionForPane(targetPaneId) === expectedSessionId,
  )
}

async function prepareArenaSession(
  targetPaneId: string,
  targetRuntime: WorkspaceRuntime,
): Promise<string | null> {
  let sessionId = arenaStore.sessionForPane(targetPaneId)
  if (
    sessionId
    && targetRuntime !== 'grok'
    && targetRuntime !== 'claude'
    && codexStore.knownRuntimeIDForThread(sessionId)
    && codexStore.knownRuntimeIDForThread(sessionId) !== targetRuntime
  ) {
    arenaStore.setPaneSession(targetPaneId, '')
    sessionId = ''
  }

  if (sessionId) return sessionId
  if (!arenaComposerTargetIsCurrent(targetPaneId, targetRuntime, '')) return null

  if (targetRuntime === 'grok') {
    grokStore.newSession()
    return ''
  }
  if (targetRuntime === 'claude') {
    sessionId = claudeStore.newSession(true)
  } else {
    const thread = await codexStore.newRuntimeThread(targetRuntime, true)
    sessionId = thread?.id || ''
  }
  if (!sessionId || !arenaComposerTargetIsCurrent(targetPaneId, targetRuntime, '')) return null
  arenaStore.setPaneSession(targetPaneId, sessionId)
  return sessionId
}

function consumeSubmittedDraft(draftKey: string): void {
  if (props.draftKey === draftKey) {
    modelValue.value = ''
    attachedImages.value = []
    return
  }
  emit('consume-draft', { draftKey })
}

async function performSend(submission: ComposerSubmission): Promise<void> {
  const {
    message,
    images,
    draftKey,
    arena,
    targetPaneId,
    targetRuntime,
    targetWorkspace,
  } = submission
  if (!message && !images.length) return
  const targetSessionId = arena && !submission.targetSessionId
    ? await prepareArenaSession(targetPaneId, targetRuntime)
    : submission.targetSessionId
  if (targetSessionId === null) return
  if (targetRuntime === 'grok') {
    // Do not gate on sending — busy turns enqueue like Codex.
    if (!grokStore.isReady) return
    resetSentHistoryNavigation()
    consumeSubmittedDraft(draftKey)
    const sendPromise = grokStore.sendMessage(
      message,
      images,
      targetSessionId || '',
      targetWorkspace,
    )
    if (
      arena
      && !targetSessionId
      && grokStore.activeSessionId
      && arenaComposerTargetIsCurrent(targetPaneId, targetRuntime, '')
    ) {
      arenaStore.setPaneSession(targetPaneId, grokStore.activeSessionId)
    }
    emit('sent', { draftKey })
    const ok = await sendPromise
    if (!ok) {
      emit('restore-draft', { draftKey, text: message, images })
    } else {
      releaseAttachmentPreviews(images)
    }
    return
  }
  if (targetRuntime === 'claude') {
    if (!claudeStore.isReady) return
    // Capture then clear immediately so a second Enter cannot re-send the same text.
    resetSentHistoryNavigation()
    consumeSubmittedDraft(draftKey)
    const sendPromise = claudeStore.sendMessage(
      message,
      images,
      targetSessionId || '',
      targetWorkspace,
    )
    emit('sent', { draftKey })
    const ok = await sendPromise
    if (!ok) {
      // Only restore when the send truly failed (not when it was queued).
      emit('restore-draft', { draftKey, text: message, images })
    } else {
      releaseAttachmentPreviews(images)
    }
    return
  }
  if (!codexStore.isRuntimeReady(targetRuntime)) return
  let targetThread = targetSessionId
    ? (codexStore.activeThread && codexStore.sameThread(codexStore.activeThread.id, targetSessionId)
        ? codexStore.activeThread
        : codexStore.threads.find((thread) => codexStore.sameThread(thread.id, targetSessionId))
          || Object.values(codexStore.projectThreads).flat()
            .find((thread) => codexStore.sameThread(thread.id, targetSessionId)))
    : null
  if (targetSessionId && !targetSessionId.startsWith('pending-thread-') && !targetThread) {
    await codexStore.openThread(targetSessionId, { activate: false, runtime: targetRuntime })
    if (
      arena
      && !arenaComposerTargetIsCurrent(targetPaneId, targetRuntime, targetSessionId)
    ) return
    targetThread = codexStore.threads.find((thread) => codexStore.sameThread(thread.id, targetSessionId))
      || Object.values(codexStore.projectThreads).flat()
        .find((thread) => codexStore.sameThread(thread.id, targetSessionId))
      || null
    if (!targetThread) return
  }
  const codexTargetWorkspace = targetThread?.cwd || targetWorkspace
  if (!codexTargetWorkspace) return
  resetSentHistoryNavigation()
  consumeSubmittedDraft(draftKey)
  const sendPromise = codexStore.sendMessage(
    message,
    images,
    targetSessionId || '',
    targetRuntime,
    codexTargetWorkspace,
  )
  emit('sent', { draftKey })
  const ok = await sendPromise
  if (!ok) {
    emit('restore-draft', { draftKey, text: message, images })
  } else {
    releaseAttachmentPreviews(images)
  }
}

function waitForOptimisticThinkingPaint(): Promise<void> {
  return new Promise((resolve) => {
    let settled = false
    const finish = (): void => {
      if (settled) return
      settled = true
      window.clearTimeout(fallback)
      resolve()
    }
    const fallback = window.setTimeout(finish, 120)
    requestAnimationFrame(() => requestAnimationFrame(finish))
  })
}

async function send(): Promise<void> {
  const draftKey = props.draftKey
  if (sendAdmissionPending.value) return
  if (attachingImages.value) return
  if (!isArenaPane.value && workspaceStore.switchingWorkspace && !canSendDuringWorkspaceSwitch.value) return
  if (isArenaPane.value && workspaceStore.switchingWorkspace && !composerSessionId.value) return
  const targetRuntime = paneRuntime.value
  const targetSessionId = composerSessionId.value
  const submission: ComposerSubmission = {
    message: modelValue.value.trim(),
    images: [...attachedImages.value],
    draftKey,
    arena: isArenaPane.value,
    targetPaneId: paneId.value,
    targetRuntime,
    targetSessionId,
    targetWorkspace: composerTargetWorkspace(targetRuntime, targetSessionId),
  }
  if (!submission.message && !submission.images.length) return
  const showOptimisticThinking = !willQueueOnSend.value
  sendAdmissionPendingByDraft.value = {
    ...sendAdmissionPendingByDraft.value,
    [draftKey]: true,
  }
  const requestId = `composer-send-${Date.now()}-${++sendAdmissionSequence}`
  try {
    if (showOptimisticThinking) {
      emit('send-pending-change', { requestId, draftKey, pending: true })
      await nextTick()
      // Let the optimistic Composing row paint before session creation or RPC work starts.
      await waitForOptimisticThinkingPaint()
    }
    await performSend(submission)
  } finally {
    const nextPending = { ...sendAdmissionPendingByDraft.value }
    delete nextPending[draftKey]
    sendAdmissionPendingByDraft.value = nextPending
    if (showOptimisticThinking) {
      emit('send-pending-change', { requestId, draftKey, pending: false })
    }
  }
}

function onStop(): void {
  if (!isArenaPane.value && workspaceStore.switchingWorkspace) return
  if (isGrokMode.value) {
    void grokStore.interruptTurn(composerSessionId.value)
    return
  }
  if (isClaudeMode.value) {
    void claudeStore.interruptActiveTurn(composerSessionId.value)
    return
  }
  void codexStore.interruptTurn(composerSessionId.value)
}

async function attachImages(): Promise<void> {
  const draftKey = props.draftKey
  try {
    const selected = await backend.SelectImages() ?? []
    if (!selected.length) return
    if (props.draftKey !== draftKey) {
      emit('append-draft-images', { draftKey, images: selected })
      return
    }
    const next = [...new Set([...attachedImages.value, ...selected])]
    attachedImages.value = next
    for (const path of selected) void loadAttachmentPreview(path)
  } catch (error) {
    notify('error', t('notifications.imagesNotSelected'), error instanceof Error ? error.message : t('notifications.unexpected'))
  }
}

function focusComposerInput(): void {
  void nextTick(() => {
    const textarea = composerInput.value?.$el as HTMLTextAreaElement | undefined
    textarea?.focus()
    if (textarea) textarea.setSelectionRange(textarea.value.length, textarea.value.length)
  })
}

function appendComposerText(value: string): void {
  const addition = value.trim()
  if (!addition) return
  const current = modelValue.value.trimEnd()
  modelValue.value = current ? `${current}\n\n${addition}` : addition
  focusComposerInput()
}

async function selectComposerFiles(): Promise<void> {
  if (fileSelectionPending.value) return
  const draftKey = props.draftKey
  fileSelectionPending.value = true
  try {
    const selected = await backend.SelectComposerFiles()
    const images = selected?.images ?? []
    const files = selected?.files ?? []
    const fileReferences = files.map((path) => `@${path}`).join('\n')
    if (props.draftKey !== draftKey) {
      if (images.length || fileReferences) {
        emit('restore-draft', { draftKey, text: fileReferences, images })
      }
      return
    }
    if (images.length) {
      attachedImages.value = [...new Set([...attachedImages.value, ...images])]
      for (const path of images) void loadAttachmentPreview(path)
    }
    if (fileReferences) appendComposerText(fileReferences)
  } catch (error) {
    notify('error', t('notifications.filesNotSelected'), error instanceof Error ? error.message : t('notifications.unexpected'))
  } finally {
    fileSelectionPending.value = false
  }
}

async function ensureComposerRuntimeActive(): Promise<boolean> {
  if (appStore.activeRuntime === paneRuntime.value) return true
  return await appStore.setActiveRuntime(paneRuntime.value as WorkspaceRuntime)
}

async function selectComposerWorkspace(): Promise<void> {
  if (await selectWorkspaceForComposer()) focusComposerInput()
}

async function switchComposerWorkspace(path: string): Promise<void> {
  if (await switchWorkspaceForComposer(path)) focusComposerInput()
}

function openSlashCommands(): void {
  const current = modelValue.value.trimEnd()
  modelValue.value = current ? `${current} /` : '/'
  focusComposerInput()
}

async function openComposerCapabilities(kind: 'connectors' | 'plugins'): Promise<void> {
  if (!await ensureComposerRuntimeActive()) return
  const tab = kind === 'plugins'
    ? 'plugins'
    : (isCodexMode.value ? 'apps' : 'mcp')
  await router.push({ name: 'capabilities', query: { tab } })
}

async function loadGitHubIssueContext(): Promise<void> {
  const workspace = composerWorkspacePath.value
  const request = ++githubContextRequest
  githubIssueAvailable.value = null
  githubIssueOwnerRepo.value = ''
  githubIssueUnavailableReason.value = ''
  if (!workspace) {
    githubIssueAvailable.value = false
    githubIssueUnavailableReason.value = t('chat.githubIssueNeedsWorkspace')
    return
  }
  try {
    const context = await backend.GetGitHubIssueImportContext(workspace)
    if (request !== githubContextRequest || composerWorkspacePath.value !== workspace) return
    githubIssueAvailable.value = Boolean(context.available)
    githubIssueOwnerRepo.value = context.ownerRepo || ''
    githubIssueUnavailableReason.value = context.reason || ''
  } catch (error) {
    if (request !== githubContextRequest || composerWorkspacePath.value !== workspace) return
    githubIssueAvailable.value = false
    githubIssueUnavailableReason.value = error instanceof Error ? error.message : t('notifications.unexpected')
  }
}

function onAddMenuOpenChange(open: boolean): void {
  addMenuOpen.value = open
  if (open) void loadGitHubIssueContext()
}

async function importGitHubIssue(): Promise<void> {
  if (githubIssuePending.value) return
  const workspace = composerWorkspacePath.value
  if (!workspace) {
    notify('warning', t('chat.githubIssueTitle'), t('chat.githubIssueNeedsWorkspace'))
    return
  }
  if (githubIssueAvailable.value !== true) await loadGitHubIssueContext()
  if (githubIssueAvailable.value !== true) {
    notify('warning', t('chat.githubIssueTitle'), githubIssueUnavailableReason.value || t('chat.githubIssueUnavailable'))
    return
  }
  const reference = await dialogStore.prompt({
    title: t('chat.githubIssueTitle'),
    description: t('chat.githubIssueDescription', { repository: githubIssueOwnerRepo.value }),
    placeholder: t('chat.githubIssuePlaceholder'),
    confirmLabel: t('chat.githubIssueImport'),
    maxlength: 240,
  })
  if (!reference) return
  const draftKey = props.draftKey
  githubIssuePending.value = true
  try {
    const issue = await backend.ImportGitHubIssue(workspace, reference)
    const labels = (issue.labels ?? []).filter(Boolean)
    const metadata = [
      issue.state ? `State: ${issue.state}` : '',
      issue.author ? `Author: @${issue.author}` : '',
      labels.length ? `Labels: ${labels.join(', ')}` : '',
    ].filter(Boolean).join(' · ')
    const issueContext = [
      `GitHub Issue #${issue.number}: ${issue.title}`,
      metadata,
      issue.body?.trim() || t('chat.githubIssueNoBody'),
      issue.url,
    ].filter(Boolean).join('\n\n')
    if (props.draftKey === draftKey) appendComposerText(issueContext)
    else emit('restore-draft', { draftKey, text: issueContext, images: [] })
    notify('success', t('chat.githubIssueImported'), `#${issue.number} ${issue.title}`)
  } catch (error) {
    notify('error', t('chat.githubIssueImportFailed'), error instanceof Error ? error.message : t('notifications.unexpected'))
  } finally {
    githubIssuePending.value = false
  }
}

const composerPluginsSupported = computed(() => isCodexMode.value || isClaudeMode.value || isGrokMode.value)

function isImageFile(file: File): boolean {
  if (file.type.startsWith('image/')) return true
  return /\.(png|jpe?g|webp|gif)$/i.test(file.name)
}

function collectImageFiles(list: FileList | File[] | null | undefined): File[] {
  if (!list) return []
  return Array.from(list).filter(isImageFile)
}

const MAX_PASTED_IMAGE_BYTES = 12 * 1024 * 1024

async function fileToBase64(file: File): Promise<string> {
  if (file.size > MAX_PASTED_IMAGE_BYTES) {
    throw new Error(t('chat.imageTooLarge', { size: '12 MB' }))
  }
  const buffer = await file.arrayBuffer()
  const bytes = new Uint8Array(buffer)
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}

async function loadAttachmentPreview(path: string): Promise<void> {
  if (!path || !attachedImages.value.includes(path) || attachmentPreviews.value[path]) return
  const url = await resolveImagePreview(path)
  if (!url) return
  if (!attachedImages.value.includes(path)) return
  attachmentPreviews.value = { ...attachmentPreviews.value, [path]: url }
}

function releaseAttachmentPreviews(paths: string[]): void {
  const next = { ...attachmentPreviews.value }
  let changed = false
  for (const path of paths) {
    if (attachedImages.value.includes(path)) continue
    if (!(path in next)) continue
    delete next[path]
    changed = true
  }
  if (changed) attachmentPreviews.value = next
}

watch(attachedImages, (paths, previousPaths) => {
  releaseAttachmentPreviews((previousPaths ?? []).filter((path) => !paths.includes(path)))
  for (const path of paths) void loadAttachmentPreview(path)
}, { immediate: true })

async function attachImageFiles(files: File[]): Promise<void> {
  if (attachingImages.value) return
  const images = files.filter(isImageFile)
  if (!images.length) return
  const draftKey = props.draftKey
  const existingImages = [...attachedImages.value]
  const added: string[] = []
  const previewPatch: Record<string, string> = {}
  attachingImageTasks.value += 1
  try {
    for (const file of images) {
      const dataBase64 = await fileToBase64(file)
      const path = await backend.AttachImageData(
        file.name || `paste-${Date.now()}.png`,
        file.type || '',
        dataBase64,
      )
      if (path && !existingImages.includes(path) && !added.includes(path)) {
        added.push(path)
        const localPreview = URL.createObjectURL(file)
        rememberLocalImagePreview(path, localPreview)
        previewPatch[path] = localPreview
      }
    }
  } catch (error) {
    notify('error', t('notifications.imagesNotSelected'), error instanceof Error ? error.message : t('notifications.unexpected'))
  } finally {
    attachingImageTasks.value = Math.max(0, attachingImageTasks.value - 1)
    if (added.length) {
      emit('append-draft-images', { draftKey, images: added })
      if (props.draftKey === draftKey) {
        attachmentPreviews.value = { ...attachmentPreviews.value, ...previewPatch }
      }
    }
  }
}

function onPaste(event: ClipboardEvent): void {
  const data = event.clipboardData
  if (!data) return
  const fromItems: File[] = []
  for (const item of Array.from(data.items || [])) {
    if (item.kind !== 'file') continue
    const file = item.getAsFile()
    if (file) fromItems.push(file)
  }
  const images = [
    ...collectImageFiles(fromItems),
    ...collectImageFiles(data.files),
  ]
  // Deduplicate by name+size+lastModified when possible.
  const seen = new Set<string>()
  const unique = images.filter((file) => {
    const key = `${file.name}:${file.size}:${file.lastModified}:${file.type}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
  if (!unique.length) return
  event.preventDefault()
  void attachImageFiles(unique)
}

function onDragEnter(event: DragEvent): void {
  if (!event.dataTransfer) return
  const hasFiles = Array.from(event.dataTransfer.types || []).includes('Files')
  if (!hasFiles) return
  event.preventDefault()
  dragDepth.value += 1
}

function onDragOver(event: DragEvent): void {
  if (!event.dataTransfer) return
  const hasFiles = Array.from(event.dataTransfer.types || []).includes('Files')
  if (!hasFiles) return
  event.preventDefault()
  event.dataTransfer.dropEffect = 'copy'
}

function onDragLeave(event: DragEvent): void {
  if (!event.dataTransfer) return
  const hasFiles = Array.from(event.dataTransfer.types || []).includes('Files')
  if (!hasFiles) return
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

function onDrop(event: DragEvent): void {
  dragDepth.value = 0
  const files = collectImageFiles(event.dataTransfer?.files)
  if (!files.length) return
  event.preventDefault()
  void attachImageFiles(files)
}

function removeAttachment(path: string): void {
  attachedImages.value = attachedImages.value.filter((item) => item !== path)
  releaseAttachmentPreviews([path])
}

function attachmentName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

async function applyModelSelection(value: string): Promise<void> {
  const modelID = value.trim()
  if (!modelID) return

  if (isGrokMode.value) {
    if (appStore.settings.grokBackend === 'api') {
      appStore.updateGrokPreferences({ grokAPIModel: modelID })
    } else {
      appStore.updateGrokPreferences({ grokBuildModel: modelID })
    }
    if (composerSessionId.value) {
      grokStore.patchSessionPreferences(composerSessionId.value, modelID, displayEffort.value)
    }
    return
  }
  if (isClaudeMode.value) {
    appStore.patchSettings({ claudeModel: modelID })
    if (composerSessionId.value) {
      claudeStore.patchSessionPreferences(composerSessionId.value, modelID, displayEffort.value)
    }
    return
  }
  if (isGeminiMode.value) {
    appStore.patchSettings({ geminiModel: modelID })
    if (composerTimelineThread.value) {
      codexStore.patchSessionPreferences(composerTimelineThread.value.id, modelID, displayEffort.value)
      if (!composerTimelineThread.value.id.startsWith('pending-thread-')) {
        void codexStore.updateSessionPreferences({ sessionId: composerTimelineThread.value.id, model: modelID, effort: displayEffort.value, collaborationMode: collaborationMode.value }).catch(() => undefined)
      }
    }
    return
  }
  if (isOpenCodeMode.value) {
    const provider = modelID.includes('/') ? modelID.slice(0, modelID.indexOf('/')).trim() : ''
    appStore.patchSettings({
      openCodeModel: modelID,
      ...(provider ? { openCodeProvider: provider } : {}),
    })
    if (composerTimelineThread.value) {
      codexStore.patchSessionPreferences(composerTimelineThread.value.id, modelID, displayEffort.value)
      if (!composerTimelineThread.value.id.startsWith('pending-thread-')) {
        void codexStore.updateSessionPreferences({ sessionId: composerTimelineThread.value.id, model: modelID, effort: displayEffort.value, collaborationMode: collaborationMode.value }).catch(() => undefined)
      }
    }
    return
  }

  let effort = displayEffort.value
  let serviceTier = appStore.settings.serviceTier
  const model = appStore.models.find((item) => item.model === modelID)
  if (model) {
    const supported = model.supportedReasoningEfforts
    effort = supported.some((option) => option.effort === effort)
      ? effort
      : model.defaultReasoningEffort || supported[0]?.effort || 'high'
    serviceTier = model.serviceTiers.some((tier) => tier.id === serviceTier)
      ? serviceTier
      : model.defaultServiceTier
  }

  if (sessionLocked.value && composerCodexThread.value) {
    codexStore.patchSessionPreferences(composerCodexThread.value.id, modelID, effort)
    try {
      await codexStore.updateSessionPreferences({
        sessionId: composerCodexThread.value.id,
        model: modelID,
        effort,
        collaborationMode: collaborationMode.value,
      })
    } catch {
      // Keep the in-memory session selection usable for this turn.
    }
    return
  }

  appStore.updateAgentPreferences(modelID, effort, serviceTier, appStore.settings.collaborationMode)
  if (composerSessionId.value.startsWith('pending-thread-') && composerCodexThread.value) {
    codexStore.patchSessionPreferences(composerSessionId.value, modelID, effort)
  }
}

function onEffortChange(value: string): void {
  if (isGrokMode.value) {
    appStore.updateGrokPreferences({ grokEffort: value })
    if (composerSessionId.value) {
      grokStore.patchSessionPreferences(composerSessionId.value, displayModel.value, value)
    }
    return
  }
  if (isClaudeMode.value) {
    appStore.patchSettings({ claudeEffort: value })
    if (composerSessionId.value) {
      claudeStore.patchSessionPreferences(composerSessionId.value, displayModel.value, value)
    }
    return
  }
  if (isGeminiMode.value) {
    appStore.patchSettings({ geminiEffort: value })
    if (composerTimelineThread.value) {
      codexStore.patchSessionPreferences(composerTimelineThread.value.id, displayModel.value, value)
      if (!composerTimelineThread.value.id.startsWith('pending-thread-')) {
        void codexStore.updateSessionPreferences({ sessionId: composerTimelineThread.value.id, model: displayModel.value, effort: value, collaborationMode: collaborationMode.value }).catch(() => undefined)
      }
    }
    return
  }
  if (isOpenCodeMode.value) {
    appStore.patchSettings({ openCodeEffort: value })
    if (composerTimelineThread.value) {
      codexStore.patchSessionPreferences(composerTimelineThread.value.id, displayModel.value, value)
      if (!composerTimelineThread.value.id.startsWith('pending-thread-')) {
        void codexStore.updateSessionPreferences({ sessionId: composerTimelineThread.value.id, model: displayModel.value, effort: value, collaborationMode: collaborationMode.value }).catch(() => undefined)
      }
    }
    return
  }
  if (sessionLocked.value && composerCodexThread.value) {
    codexStore.patchSessionPreferences(composerCodexThread.value.id, displayModel.value, value)
    void codexStore.updateSessionPreferences({
      sessionId: composerCodexThread.value.id,
      model: displayModel.value,
      effort: value,
      collaborationMode: collaborationMode.value,
    }).catch(() => undefined)
    return
  }
  appStore.updateAgentPreferences(displayModel.value || appStore.settings.model, value, appStore.settings.serviceTier, appStore.settings.collaborationMode)
  if (composerSessionId.value.startsWith('pending-thread-') && composerCodexThread.value) {
    codexStore.patchSessionPreferences(composerSessionId.value, displayModel.value, value)
  }
}

function setPermission(mode: 'ask' | 'auto' | 'strict'): void {
  if (isClaudeMode.value) {
    // Map composer presets to official Claude Code --permission-mode values.
    const values = mode === 'auto'
      ? {
          claudeSandbox: 'danger-full-access',
          claudeApprovalPolicy: 'never',
          claudePermissionMode: 'bypassPermissions',
        }
      : mode === 'strict'
        ? {
            claudeSandbox: 'read-only',
            claudeApprovalPolicy: 'on-request',
            claudePermissionMode: 'plan',
          }
        : {
            claudeSandbox: 'workspace-write',
            claudeApprovalPolicy: 'on-request',
            claudePermissionMode: 'acceptEdits',
          }
    appStore.patchSettings(values as any)
    return
  }
  if (isGrokMode.value) {
    const values = mode === 'auto'
      ? { grokSandbox: 'danger-full-access', grokApprovalPolicy: 'never' }
      : mode === 'strict'
        ? { grokSandbox: 'read-only', grokApprovalPolicy: 'on-request' }
        : { grokSandbox: 'workspace-write', grokApprovalPolicy: 'on-request' }
    if (values.grokSandbox === appStore.settings.grokSandbox && values.grokApprovalPolicy === appStore.settings.grokApprovalPolicy) return
    appStore.updateGrokPreferences(values)
    return
  }
  if (isGeminiMode.value || isOpenCodeMode.value) {
    const prefix = isGeminiMode.value ? 'gemini' : 'openCode'
    const values = mode === 'auto'
      ? { [`${prefix}Sandbox`]: 'danger-full-access', [`${prefix}ApprovalPolicy`]: 'never' }
      : mode === 'strict'
        ? { [`${prefix}Sandbox`]: 'read-only', [`${prefix}ApprovalPolicy`]: 'on-request' }
        : { [`${prefix}Sandbox`]: 'workspace-write', [`${prefix}ApprovalPolicy`]: 'on-request' }
    appStore.patchSettings(values as any)
    return
  }
  const values = mode === 'auto'
    ? { sandbox: 'danger-full-access', approvalPolicy: 'never' }
    : mode === 'strict'
      ? { sandbox: 'read-only', approvalPolicy: 'untrusted' }
      : { sandbox: 'workspace-write', approvalPolicy: 'on-request' }
  if (values.sandbox === appStore.settings.sandbox && values.approvalPolicy === appStore.settings.approvalPolicy) return
  appStore.patchSettings(values)
}
</script>

<template>
  <div class="shrink-0 px-3 pb-4 pt-1 sm:px-5">
    <div
      ref="composer"
      class="relative mx-auto flex w-full max-w-[980px] flex-col gap-1.5"
      @dragenter="onDragEnter"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <div class="composer-context-row flex min-w-0 items-center gap-1.5 overflow-x-auto px-0.5 pb-0.5">
        <DropdownMenu v-if="!isArenaPane">
          <DropdownMenuTrigger as-child>
            <button
              type="button"
              class="composer-context-chip composer-context-chip-button"
              :title="`${t('settings.modelProvider')}: ${composerRuntimeLabel}`"
              :aria-label="`${t('settings.modelProvider')}: ${composerRuntimeLabel}`"
              :aria-busy="composerRuntimeSwitching"
              :disabled="composerRuntimeInteractionDisabled"
            >
              <LoaderCircle v-if="composerRuntimeSwitching" :size="12" class="shrink-0 animate-spin" />
              <component :is="composerRuntimeIcon" v-else :size="12" class="shrink-0" />
              <span>{{ composerRuntimeLabel }}</span>
              <ChevronDown :size="11" class="shrink-0 opacity-55" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="start"
            side="bottom"
            :side-offset="6"
            class="w-64 rounded-xl p-1.5 shadow-xl"
          >
            <div class="px-2.5 pb-2 pt-1.5">
              <p class="text-xs font-medium">{{ t('settings.modelProvider') }}</p>
              <p class="mt-0.5 text-[10px] leading-4 text-muted-foreground">{{ t('chat.providerSelectorHint') }}</p>
            </div>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              v-for="runtime in composerRuntimeOptions"
              :key="runtime.kind"
              class="gap-2.5 rounded-lg px-2.5 py-2"
              :title="runtime.message || runtime.name"
              :disabled="composerRuntimeInteractionDisabled"
              @click="selectComposerRuntime(runtime.kind)"
            >
              <component :is="runtime.icon" :size="14" class="shrink-0 text-muted-foreground" />
              <span class="min-w-0 flex-1 truncate text-xs">{{ runtime.name }}</span>
              <span
                class="text-[10px]"
                :class="runtime.ready ? 'text-muted-foreground' : 'text-destructive'"
              >
                {{ runtime.ready ? t('settings.runtimeReady') : t('settings.runtimeMissing') }}
              </span>
              <Check
                :size="14"
                class="shrink-0"
                :class="runtime.kind === paneRuntime ? 'opacity-100' : 'opacity-0'"
              />
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <button
              type="button"
              class="composer-context-chip composer-context-chip-button max-w-64"
              :title="composerWorkspacePath || t('sidebar.chooseFolder')"
              :aria-label="`${t('sidebar.workspace')}: ${composerWorkspaceName}`"
              :aria-busy="composerWorkspaceSwitching"
              :disabled="composerWorkspaceSwitching"
            >
              <LoaderCircle v-if="composerWorkspaceSwitching" :size="12" class="shrink-0 animate-spin" />
              <Folder v-else :size="12" class="shrink-0" />
              <span class="max-w-52 truncate">{{ composerWorkspaceName }}</span>
              <ChevronDown :size="11" class="shrink-0 opacity-55" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="start"
            side="bottom"
            :side-offset="6"
            class="max-h-[min(20rem,calc(100vh-8rem))] w-72 max-w-[calc(100vw-1rem)] overflow-y-auto rounded-xl p-1.5 shadow-xl"
          >
            <DropdownMenuItem
              class="gap-2.5 rounded-lg px-2.5 py-2"
              :disabled="composerWorkspaceSwitching"
              @click="selectComposerWorkspace"
            >
              <FolderOpen :size="15" class="shrink-0 text-muted-foreground" />
              <span class="truncate text-xs">{{ t('sidebar.chooseAnother') }}</span>
            </DropdownMenuItem>
            <DropdownMenuSeparator v-if="composerRecentWorkspacePaths.length" />
            <DropdownMenuItem
              v-for="path in composerRecentWorkspacePaths"
              :key="path"
              class="gap-2.5 rounded-lg px-2.5 py-2"
              :disabled="composerWorkspaceSwitching"
              :title="path"
              @click="switchComposerWorkspace(path)"
            >
              <Folder :size="14" class="shrink-0 text-muted-foreground" />
              <span class="min-w-0 flex-1 truncate text-xs">
                {{ workspaceDisplayName(path) }}
              </span>
              <Check
                v-if="sameWorkspacePath(path, composerWorkspacePath)"
                :size="14"
                class="shrink-0 text-foreground"
              />
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Popover
          v-if="composerBranch"
          :open="branchMenuOpen"
          @update:open="onBranchMenuOpenChange"
        >
          <PopoverTrigger as-child>
            <button
              type="button"
              class="composer-context-chip composer-context-chip-button max-w-52"
              :title="composerBranch"
              :aria-label="`${t('settings.gitBranch')}: ${composerBranch}`"
              :aria-expanded="branchMenuOpen"
              :aria-busy="workspaceStore.gitBranchesLoading || workspaceStore.branchSwitching"
              :disabled="branchInteractionDisabled"
            >
              <LoaderCircle
                v-if="workspaceStore.gitBranchesLoading || workspaceStore.branchSwitching"
                :size="12"
                class="shrink-0 animate-spin"
              />
              <GitBranch v-else :size="12" class="shrink-0" />
              <span class="max-w-36 truncate">{{ composerBranch }}</span>
              <ChevronDown :size="11" class="shrink-0 opacity-55" />
            </button>
          </PopoverTrigger>
          <PopoverContent
            align="start"
            side="bottom"
            :side-offset="6"
            class="w-72 max-w-[calc(100vw-1rem)] overflow-hidden rounded-xl p-0 shadow-xl"
            @open-auto-focus.prevent
          >
            <div class="max-h-64 overflow-y-auto p-1.5">
              <div
                v-if="workspaceStore.gitBranchesLoading"
                class="flex items-center justify-center gap-2 px-3 py-8 text-xs text-muted-foreground"
              >
                <LoaderCircle :size="14" class="animate-spin" />
                {{ t('common.loading') }}
              </div>
              <template v-else>
                <button
                  v-for="branch in filteredGitBranches"
                  :key="branch"
                  type="button"
                  class="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs outline-none transition-colors hover:bg-accent focus-visible:bg-accent"
                  :disabled="workspaceStore.branchSwitching"
                  :title="branch"
                  @click="selectGitBranch(branch)"
                >
                  <GitBranch :size="13" class="shrink-0 text-muted-foreground" />
                  <span class="min-w-0 flex-1 truncate">{{ branch }}</span>
                  <Check
                    :size="14"
                    class="shrink-0"
                    :class="branch === composerBranch ? 'opacity-100' : 'opacity-0'"
                  />
                </button>
              </template>
              <p
                v-if="!workspaceStore.gitBranchesLoading && !filteredGitBranches.length"
                class="px-3 py-8 text-center text-[11px] text-muted-foreground"
              >
                {{ t('git.noBranches') }}
              </p>
            </div>
            <div class="flex items-center gap-2 border-t bg-muted/20 px-2.5">
              <Search :size="13" class="shrink-0 text-muted-foreground" />
              <Input
                ref="branchSearchInput"
                v-model="branchQuery"
                type="search"
                autocomplete="off"
                spellcheck="false"
                class="h-10 border-0 bg-transparent px-0 text-xs shadow-none focus-visible:border-0 focus-visible:ring-0"
                :placeholder="t('git.searchBranches')"
                @keydown.escape.stop="onBranchMenuOpenChange(false)"
                @keydown.enter.prevent="selectFirstFilteredBranch"
              />
            </div>
          </PopoverContent>
        </Popover>
      </div>

      <div
        class="composer-input-frame relative flex flex-col gap-1 rounded-2xl border bg-card px-3 pb-2 pt-2 shadow-sm transition-[border-color,box-shadow,background-color] duration-200"
        :class="[
          isDraggingFiles
            ? 'border-primary border-dashed bg-primary/5'
            : ((activeRuntimeTurnRunning || activeRuntimeSending) ? 'is-active border-foreground/20' : 'border-border/90'),
        ]"
      >
      <div
        v-if="isDraggingFiles"
        class="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-2xl bg-primary/8 text-xs font-medium text-primary"
      >
        {{ t('chat.dropImages') }}
      </div>

      <div class="absolute right-2 top-2 z-[2]">
        <SimpleTooltip :content="composerExpanded ? t('chat.collapseComposer') : t('chat.expandComposer')">
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            class="size-6 rounded-md bg-card/80 text-muted-foreground shadow-none backdrop-blur-sm hover:bg-muted"
            :aria-label="composerExpanded ? t('chat.collapseComposer') : t('chat.expandComposer')"
            :aria-pressed="composerExpanded"
            @click="toggleComposerHeight"
          >
            <Minimize2 v-if="composerExpanded" :size="12" />
            <Maximize2 v-else :size="12" />
          </Button>
        </SimpleTooltip>
      </div>

      <div v-if="attachedImages.length" class="flex flex-wrap gap-1.5 px-1 pr-8">
        <div
          v-for="path in attachedImages"
          :key="path"
          class="group relative overflow-hidden rounded-lg border border-border/70 bg-muted/40"
        >
          <AttachmentImage
            v-if="attachmentPreviews[path]"
            :source="attachmentPreviews[path]"
            kind="preview"
            :alt="attachmentName(path)"
            image-class="h-14 w-14 object-cover"
          />
          <div v-else class="flex h-14 w-14 items-center justify-center px-1">
            <ImageIcon :size="14" class="text-muted-foreground" />
          </div>
          <Button
            variant="ghost"
            size="icon-xs"
            class="absolute right-0.5 top-0.5 size-5 rounded-full bg-background/90 opacity-0 transition-opacity group-hover:opacity-100"
            :aria-label="t('chat.removeAttachment')"
            @click="removeAttachment(path)"
          >
            <X :size="11" />
          </Button>
        </div>
      </div>

      <div
        v-if="showQueueStrip"
        class="flex items-center justify-between gap-2 rounded-md border border-border/50 bg-muted/40 px-2 py-1"
      >
        <Popover>
          <PopoverTrigger as-child>
            <Button
              variant="ghost"
              size="sm"
              class="h-6 min-w-0 gap-1.5 rounded-full bg-background/70 px-2 text-[11px] font-medium text-foreground/80 hover:text-foreground"
            >
              <ListOrdered :size="12" class="shrink-0" />
              <span class="truncate">{{ t('chat.queuedCount', { count: queuedWaitingCount || queuedFailedCount || activeQueuedMessages.length }) }}</span>
            </Button>
          </PopoverTrigger>
          <PopoverContent
            align="start"
            side="top"
            class="p-2"
            :class="isArenaPane ? 'w-[min(22rem,calc(100vw-2rem))]' : 'w-[26rem] max-w-[calc(100vw-2rem)]'"
          >
            <div class="flex items-start justify-between gap-2 px-2 pb-2 pt-1">
              <div class="min-w-0">
                <p class="text-xs font-medium">{{ t('chat.queuedTitle') }}</p>
                <p class="mt-1 text-[11px] leading-4 text-muted-foreground">{{ t('chat.queuedHint') }}</p>
              </div>
              <Button
                v-if="queuedFailedCount"
                variant="ghost"
                size="sm"
                class="h-6 shrink-0 px-2 text-[11px] text-destructive hover:text-destructive"
                @click="clearFailedQueued"
              >
                {{ t('chat.clearFailedQueued') }}
              </Button>
            </div>
            <div class="max-h-64 space-y-0.5 overflow-y-auto">
              <div
                v-for="(message, queueIndex) in activeQueuedMessages"
                :key="message.id"
                class="group flex items-start gap-1.5 rounded-md px-1.5 py-1.5 hover:bg-muted/60"
              >
                <LoaderCircle
                  v-if="message.state === 'sending'"
                  :size="12"
                  class="mt-0.5 shrink-0 animate-spin text-muted-foreground"
                />
                <AlertCircle
                  v-else-if="message.state === 'failed'"
                  :size="12"
                  class="mt-0.5 shrink-0 text-destructive"
                />
                <span
                  v-else
                  class="mt-0.5 flex size-3 shrink-0 items-center justify-center text-[10px] tabular-nums text-muted-foreground"
                >{{ queueIndex + 1 }}</span>

                <div class="min-w-0 flex-1">
                  <Textarea
                    v-if="editingQueuedId === message.id"
                    v-model="queuedEditDraft"
                    :data-queue-edit="message.id"
                    rows="2"
                    class="min-h-14 resize-y rounded-md bg-background px-2 py-1.5 text-[12px] leading-4"
                    :aria-label="t('chat.editQueued')"
                    @input="queuedEditError = ''"
                    @keydown="handleQueuedEditKeydown($event, message)"
                  />
                  <p v-else class="line-clamp-2 text-[12px] leading-4 text-foreground/90">
                    {{ message.text || t('chat.queuedImageOnly') }}
                  </p>
                  <div class="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
                    <span v-if="message.state === 'sending'">{{ t('chat.queuedSending') }}</span>
                    <span v-else-if="message.state === 'failed'" class="text-destructive">{{ t('chat.queuedFailed') }}</span>
                    <span v-else>{{ t('chat.queuedWaiting') }}</span>
                    <span v-if="message.images.length">· {{ t('chat.queuedAttachments', { count: message.images.length }) }}</span>
                  </div>
                  <p
                    v-if="editingQueuedId === message.id && queuedEditError"
                    class="mt-1 text-[10px] leading-4 text-destructive"
                    role="alert"
                  >
                    {{ queuedEditError }}
                  </p>
                  <p
                    v-else-if="editingQueuedId === message.id"
                    class="mt-1 text-[10px] leading-4 text-muted-foreground"
                  >
                    {{ t('chat.queuedEditHint') }}
                  </p>
                  <p v-else-if="message.error" class="mt-0.5 line-clamp-2 text-[10px] text-destructive/90">
                    {{ message.error }}
                  </p>
                </div>

                <div
                  v-if="message.state !== 'sending'"
                  class="flex shrink-0 items-center"
                >
                  <template v-if="editingQueuedId === message.id">
                    <SimpleTooltip :content="t('chat.saveQueuedEdit')">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="size-6 text-muted-foreground hover:text-foreground"
                        :aria-label="t('chat.saveQueuedEdit')"
                        :disabled="!queuedEditDraft.trim() && !message.images.length"
                        @click="saveQueuedEdit(message)"
                      >
                        <Check :size="12" />
                      </Button>
                    </SimpleTooltip>
                    <SimpleTooltip :content="t('chat.cancelQueuedEdit')">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="size-6 text-muted-foreground"
                        :aria-label="t('chat.cancelQueuedEdit')"
                        @click="cancelQueuedEdit"
                      >
                        <X :size="12" />
                      </Button>
                    </SimpleTooltip>
                  </template>
                  <template v-else>
                  <SimpleTooltip :content="t('chat.editQueued')">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 text-muted-foreground"
                      :aria-label="t('chat.editQueued')"
                      @click="beginQueuedEdit(message)"
                    >
                      <Pencil :size="11" />
                    </Button>
                  </SimpleTooltip>
                  <SimpleTooltip :content="t('chat.queueMoveUp')">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 text-muted-foreground"
                      :aria-label="t('chat.queueMoveUp')"
                      :disabled="!canMoveQueued(queueIndex, 'up')"
                      @click="reorderQueued(message.id, 'up')"
                    >
                      <ChevronUp :size="11" />
                    </Button>
                  </SimpleTooltip>
                  <SimpleTooltip :content="t('chat.queueMoveDown')">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 text-muted-foreground"
                      :aria-label="t('chat.queueMoveDown')"
                      :disabled="!canMoveQueued(queueIndex, 'down')"
                      @click="reorderQueued(message.id, 'down')"
                    >
                      <ChevronDown :size="11" />
                    </Button>
                  </SimpleTooltip>
                  <SimpleTooltip :content="t('chat.queueSendNow')">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 text-muted-foreground"
                      :aria-label="t('chat.queueSendNow')"
                      @click="sendQueuedNow(message.id)"
                    >
                      <Zap :size="11" />
                    </Button>
                  </SimpleTooltip>
                  <SimpleTooltip v-if="message.state === 'failed'" :content="t('chat.retryQueued')">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6"
                      :aria-label="t('chat.retryQueued')"
                      @click="retryQueued(message.id)"
                    >
                      <RotateCcw :size="11" />
                    </Button>
                  </SimpleTooltip>
                  <SimpleTooltip :content="t('chat.removeQueued')">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 text-muted-foreground hover:text-destructive"
                      :aria-label="t('chat.removeQueued')"
                      @click="removeQueued(message.id)"
                    >
                      <X :size="11" />
                    </Button>
                  </SimpleTooltip>
                  </template>
                </div>
              </div>
            </div>
          </PopoverContent>
        </Popover>
        <span class="min-w-0 flex-1 truncate pr-1 text-[10px] text-muted-foreground">
          <template v-if="queuedFailedCount">{{ t('chat.queuedFailedCount', { count: queuedFailedCount }) }}</template>
          <template v-if="queuedFailedCount && nextQueuedPreview"> · </template>
          <template v-if="nextQueuedPreview">{{ t('chat.queuedNext', { text: nextQueuedPreview }) }}</template>
          <template v-else>{{ t('chat.queuedHint') }}</template>
        </span>
      </div>

      <div v-if="slashOpen && filteredSlashCommands.length" class="rounded-md border border-border/60 bg-muted/30 p-1">
        <div class="flex items-center gap-1.5 px-2 py-1 text-[10px] text-muted-foreground">
          <Command :size="11" />
          {{ t('slash.title') }}
        </div>
        <button
          v-for="(command, index) in filteredSlashCommands"
          :key="command.id"
          type="button"
          class="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
          :class="index === slashIndex ? 'bg-background text-foreground' : 'text-foreground/85 hover:bg-background/70'"
          @mousedown.prevent="runSlashCommand(command)"
          @mouseenter="slashIndex = index"
        >
          <span class="shrink-0 font-mono text-[11px] font-medium">{{ command.label }}</span>
          <span class="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">{{ command.description }}</span>
        </button>
      </div>

      <div v-else-if="skillOpen && skillOptions.length" class="rounded-md border border-border/60 bg-muted/30 p-1">
        <div class="flex items-center gap-1.5 px-2 py-1 text-[10px] text-muted-foreground">
          <Zap :size="11" />
          {{ t('slash.skillsTitle') }}
        </div>
        <button
          v-for="(skill, index) in skillOptions"
          :key="skill.path || skill.name"
          type="button"
          class="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
          :class="index === skillIndex ? 'bg-background text-foreground' : 'text-foreground/85 hover:bg-background/70'"
          @mousedown.prevent="insertSkill(skill.name)"
          @mouseenter="skillIndex = index"
        >
          <span class="shrink-0 font-mono text-[11px] font-medium">${{ skill.name }}</span>
          <span class="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">
            {{ skill.displayName || skill.shortDescription || skill.description }}
          </span>
        </button>
      </div>

      <div v-else-if="pluginOpen && pluginOptions.length" class="rounded-md border border-border/60 bg-muted/30 p-1">
        <div class="flex items-center gap-1.5 px-2 py-1 text-[10px] text-muted-foreground">
          <Command :size="11" />
          {{ t('slash.pluginsTitle') }}
        </div>
        <button
          v-for="(plugin, index) in pluginOptions"
          :key="plugin.id || plugin.name"
          type="button"
          class="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
          :class="index === pluginIndex ? 'bg-background text-foreground' : 'text-foreground/85 hover:bg-background/70'"
          @mousedown.prevent="insertPlugin(plugin.name)"
          @mouseenter="pluginIndex = index"
        >
          <span class="shrink-0 font-mono text-[11px] font-medium">@{{ plugin.name }}</span>
          <span class="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">
            {{ plugin.displayName || plugin.description }}
          </span>
        </button>
      </div>

      <Textarea
        ref="composerInput"
        v-model="modelValue"
        rows="1"
        :placeholder="composerPlaceholder"
        :aria-description="composerShortcutHint"
        class="min-h-12 resize-none border-0 bg-transparent px-1.5 py-2 pr-9 text-[15px] leading-6 shadow-none placeholder:text-muted-foreground/65 focus-visible:border-0 focus-visible:ring-0 focus-visible:outline-none"
        :class="composerExpanded ? 'overflow-y-auto' : 'overflow-y-hidden'"
        @compositionend="composing = false"
        @compositionstart="composing = true"
        @keydown="onKeydown"
        @paste="onPaste"
        @pointerdown="resetSentHistoryNavigation"
      />
      <span
        class="pointer-events-none absolute bottom-3 right-4 grid size-5 place-items-center text-muted-foreground/55"
        aria-hidden="true"
      >
        <CornerDownLeft :size="15" stroke-width="1.7" />
      </span>
      </div>

      <div class="composer-toolbar flex min-h-8 flex-wrap items-center justify-between gap-x-2 gap-y-1 px-1">
        <div class="flex min-w-0 flex-1 items-center gap-0.5">
          <DropdownMenu :open="addMenuOpen" @update:open="onAddMenuOpenChange">
            <DropdownMenuTrigger as-child>
              <Button
                variant="ghost"
                size="icon-sm"
                class="order-2 size-8 rounded-full text-muted-foreground hover:bg-muted/70"
                :aria-label="t('chat.addContext')"
                :disabled="fileSelectionPending || githubIssuePending"
                :aria-busy="fileSelectionPending || githubIssuePending"
              >
                <LoaderCircle v-if="fileSelectionPending || githubIssuePending" :size="14" class="animate-spin" />
                <Plus v-else :size="17" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" side="top" :side-offset="10" class="w-72 rounded-xl p-1.5 shadow-xl">
              <DropdownMenuItem class="gap-3 rounded-lg px-2.5 py-2" @click="selectComposerFiles">
                <FileUp :size="15" class="shrink-0 text-muted-foreground" />
                <span class="flex min-w-0 flex-1 flex-col">
                  <span class="text-xs">{{ t('chat.addFilesOrPhotos') }}</span>
                  <span class="text-[10px] text-muted-foreground">{{ t('chat.addFilesOrPhotosHint') }}</span>
                </span>
                <kbd class="text-[9px] text-muted-foreground">Ctrl U</kbd>
              </DropdownMenuItem>
              <DropdownMenuItem
                class="gap-3 rounded-lg px-2.5 py-2"
                :disabled="githubIssueAvailable !== true || githubIssuePending"
                @click="importGitHubIssue"
              >
                <LoaderCircle v-if="githubIssueAvailable === null || githubIssuePending" :size="15" class="shrink-0 animate-spin text-muted-foreground" />
                <GitPullRequestArrow v-else :size="15" class="shrink-0 text-muted-foreground" />
                <span class="flex min-w-0 flex-1 flex-col">
                  <span class="text-xs">{{ t('chat.importGitHubIssue') }}</span>
                  <span class="truncate text-[10px] text-muted-foreground">
                    {{ githubIssueAvailable ? githubIssueOwnerRepo : (githubIssueUnavailableReason || t('common.loading')) }}
                  </span>
                </span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem class="gap-3 rounded-lg px-2.5 py-2" @click="openSlashCommands">
                <Command :size="15" class="shrink-0 text-muted-foreground" />
                <span class="flex min-w-0 flex-1 flex-col">
                  <span class="text-xs">{{ t('slash.title') }}</span>
                  <span class="text-[10px] text-muted-foreground">{{ t('slash.hint') }}</span>
                </span>
              </DropdownMenuItem>
              <DropdownMenuItem class="gap-3 rounded-lg px-2.5 py-2" @click="openComposerCapabilities('connectors')">
                <Plug :size="15" class="shrink-0 text-muted-foreground" />
                <span class="flex min-w-0 flex-1 flex-col">
                  <span class="text-xs">{{ t('chat.connectors') }}</span>
                  <span class="text-[10px] text-muted-foreground">{{ t('chat.connectorsHint') }}</span>
                </span>
              </DropdownMenuItem>
              <DropdownMenuItem
                class="gap-3 rounded-lg px-2.5 py-2"
                :disabled="!composerPluginsSupported"
                @click="openComposerCapabilities('plugins')"
              >
                <Puzzle :size="15" class="shrink-0 text-muted-foreground" />
                <span class="flex min-w-0 flex-1 flex-col">
                  <span class="text-xs">{{ t('capabilities.plugins') }}</span>
                  <span class="text-[10px] text-muted-foreground">
                    {{ composerPluginsSupported ? t('chat.pluginsHint') : t('chat.pluginsUnsupported') }}
                  </span>
                </span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button
                v-if="!isArenaPane"
                variant="ghost"
                size="sm"
                class="order-1 hidden h-8 gap-1.5 rounded-lg px-2 text-[12px] font-normal text-foreground shadow-none hover:bg-muted/70 md:inline-flex"
                :title="permissionDetail ? `${permissionLabel} (${permissionDetail})` : permissionLabel"
              >
                <Shield :size="12" />
                {{ permissionLabel }}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" class="w-64">
              <DropdownMenuItem @click="setPermission('ask')">
                <span class="flex min-w-0 flex-1 flex-col">
                  <span>{{ t('settings.permissionAsk') }}</span>
                  <span class="text-[10px] text-muted-foreground">{{ t('settings.permissionAskHint') }}</span>
                </span>
                <span v-if="permissionPreset === 'ask'" class="ml-2 text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('auto')">
                <span class="flex min-w-0 flex-1 flex-col">
                  <span>{{ t('settings.permissionAuto') }}</span>
                  <span class="text-[10px] text-muted-foreground">{{ t('settings.permissionAutoHint') }}</span>
                </span>
                <span v-if="permissionPreset === 'auto'" class="ml-2 text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('strict')">
                <span class="flex min-w-0 flex-1 flex-col">
                  <span>{{ t('settings.permissionStrict') }}</span>
                  <span class="text-[10px] text-muted-foreground">{{ t('settings.permissionStrictHint') }}</span>
                </span>
                <span v-if="permissionPreset === 'strict'" class="ml-2 text-primary">✓</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <SimpleTooltip v-if="isCodexMode" :content="t('chat.planModeToggleHint')">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              class="order-3 h-7 gap-1.5 px-2 text-[11px] font-normal"
              :class="isPlanMode
                ? 'bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary'
                : 'text-muted-foreground'"
              :aria-pressed="isPlanMode"
              @click="togglePlanMode"
            >
              <ListTodo :size="12" />
              <span :class="isArenaPane ? 'hidden' : 'hidden sm:inline'">{{ isPlanMode ? t('chat.planModeOn') : t('chat.planModeOff') }}</span>
            </Button>
          </SimpleTooltip>

          <!-- Narrow screens: permission + reasoning in one overflow menu -->
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button
                variant="ghost"
                size="icon-sm"
                class="order-4 size-7 text-muted-foreground"
                :class="isArenaPane ? '' : 'md:hidden'"
                :aria-label="t('chat.composerMore')"
              >
                <Ellipsis :size="14" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" class="w-52">
              <DropdownMenuItem disabled class="text-[10px] text-muted-foreground">
                {{ t('settings.permissions') }} · {{ permissionLabel }}
                <span v-if="permissionDetail" class="ml-1 opacity-70">({{ permissionDetail }})</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('ask')">
                {{ t('settings.permissionAsk') }}
                <span v-if="permissionPreset === 'ask'" class="ml-auto text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('auto')">
                {{ t('settings.permissionAuto') }}
                <span v-if="permissionPreset === 'auto'" class="ml-auto text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('strict')">
                {{ t('settings.permissionStrict') }}
                <span v-if="permissionPreset === 'strict'" class="ml-auto text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem disabled class="text-[10px] text-muted-foreground">
                {{ t('chat.reasoning') }} · {{ selectedEffortLabel }}
              </DropdownMenuItem>
              <DropdownMenuItem
                v-for="option in reasoningOptions"
                :key="`mobile-${option.effort}`"
                @click="onEffortChange(option.effort)"
              >
                {{ 'displayName' in option ? option.displayName : option.effort }}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <div class="ml-auto flex min-w-0 items-center gap-0.5">
          <SearchableSelect
            v-model="composerModelSelection"
            class="h-7 w-auto border-0 bg-transparent px-2 text-[11px] text-muted-foreground shadow-none hover:bg-muted/50"
            :class="isArenaPane ? 'max-w-28' : 'max-w-44'"
            content-class="min-w-[280px]"
            align="end"
            :options="composerModelOptions"
            :aria-label="t('chat.model')"
            :placeholder="t('chat.defaultModel')"
            :search-placeholder="t('settings.modelSearch')"
          />

          <Popover v-if="effortOptions.length > 1 && !isArenaPane" v-model:open="effortPopoverOpen">
            <PopoverTrigger as-child>
              <Button
                type="button"
                variant="ghost"
                class="hidden h-7 max-w-28 px-2 text-[11px] font-normal text-muted-foreground md:flex"
                :aria-label="t('chat.reasoning')"
              >
                <span class="truncate">{{ effortPopoverLabel }}</span>
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" side="top" :side-offset="12" class="w-80 rounded-2xl p-5 shadow-xl">
              <div class="mb-5 flex items-start justify-between gap-3">
                <div>
                  <p class="text-[15px] font-medium">{{ t('chat.reasoning') }} · {{ effortPopoverLabel }}</p>
                  <p class="mt-1 text-[11px] leading-5 text-muted-foreground">{{ t('chat.effortSliderHint') }}</p>
                </div>
                <span class="grid size-8 shrink-0 place-items-center rounded-full bg-primary/10 text-primary">
                  <Zap :size="16" />
                </span>
              </div>
              <div
                class="effort-scale"
                :class="effortDragging ? 'is-dragging' : ''"
              >
                <div class="effort-scale-track" aria-hidden="true">
                  <span
                    class="effort-scale-progress"
                    :style="{ width: effortMarkerPosition(effortDisplayIndex) }"
                  />
                  <span
                    v-for="(_, index) in effortOptions"
                    :key="`effort-marker-${index}`"
                    class="effort-scale-marker"
                    :class="{
                      'is-passed': index <= effortDisplayIndex,
                      'is-current': index === effortDisplayIndex,
                    }"
                    :style="{ left: effortMarkerPosition(index) }"
                  />
                </div>
                <input
                  type="range"
                  min="0"
                  :max="Math.max(0, effortOptions.length - 1)"
                  step="1"
                  :value="effortDisplayIndex"
                  class="effort-slider-input"
                  :aria-label="t('chat.reasoning')"
                  :aria-valuetext="effortPopoverLabel"
                  @input="previewEffort"
                  @change="commitEffort"
                  @pointerdown="effortDragging = true"
                  @pointercancel="cancelEffortPreview"
                  @blur="cancelEffortPreview"
                >
              </div>
              <div class="effort-scale-labels">
                <button
                  v-for="(option, index) in effortOptions"
                  :key="`effort-choice-${option.value}`"
                  type="button"
                  class="effort-scale-label"
                  :class="{
                    'is-current': index === effortDisplayIndex,
                    'is-first': index === 0,
                    'is-last': index === effortOptions.length - 1,
                  }"
                  :style="{ left: effortMarkerPosition(index) }"
                  @click="chooseEffort(index)"
                >
                  {{ option.label }}
                </button>
              </div>
            </PopoverContent>
          </Popover>

          <SimpleTooltip :content="contextUsageTooltip">
            <span
              role="progressbar"
              :aria-label="contextUsageTooltip"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuenow="hasContextUsage ? Math.round(contextUsedPercent) : undefined"
              :aria-valuetext="contextUsageTooltip"
              class="grid size-7 shrink-0 place-items-center rounded-md"
              :class="contextUsageTone"
            >
              <svg class="size-5 -rotate-90" viewBox="0 0 24 24" aria-hidden="true">
                <circle
                  cx="12"
                  cy="12"
                  r="9"
                  fill="none"
                  stroke-width="2.5"
                  class="stroke-border"
                />
                <circle
                  cx="12"
                  cy="12"
                  r="9"
                  fill="none"
                  pathLength="100"
                  stroke="currentColor"
                  stroke-width="2.5"
                  stroke-linecap="round"
                  stroke-dasharray="100"
                  :stroke-dashoffset="100 - contextUsedPercent"
                  class="transition-[stroke-dashoffset] duration-300 motion-reduce:transition-none"
                />
                <circle
                  v-if="!hasContextUsage"
                  cx="12"
                  cy="12"
                  r="1.5"
                  fill="currentColor"
                  class="opacity-60"
                />
              </svg>
            </span>
          </SimpleTooltip>

          <SimpleTooltip :content="primaryActionLabel">
            <Button
              type="button"
              size="icon-sm"
              class="composer-primary-action ml-0.5 size-7 shrink-0 rounded-full transition-[transform,opacity,box-shadow] duration-200"
              :class="[
                activeRuntimeTurnRunning ? 'is-running text-destructive' : '',
                !activeRuntimeTurnRunning && !canSend ? 'opacity-60 shadow-none' : 'opacity-100',
              ]"
              :variant="activeRuntimeTurnRunning
                ? 'outline'
                : (!canSend && !sendAdmissionPending && !activeRuntimeSending ? 'secondary' : 'default')"
              :aria-label="activeRuntimeTurnRunning ? t('chat.stopLabel') : sendButtonLabel"
              :aria-busy="activeRuntimeTurnRunning ? stopDisabled : (sendAdmissionPending || activeRuntimeSending)"
              :disabled="activeRuntimeTurnRunning ? stopDisabled : !canSend"
              @click="activeRuntimeTurnRunning ? onStop() : send()"
            >
              <LoaderCircle
                v-if="activeRuntimeTurnRunning ? stopDisabled : (sendAdmissionPending || activeRuntimeSending)"
                :size="14"
                class="animate-spin"
              />
              <Octagon v-else-if="activeRuntimeTurnRunning" :size="13" fill="currentColor" />
              <ArrowUp v-else :size="16" stroke-width="2.5" />
            </Button>
          </SimpleTooltip>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import {
  AlertCircle,
  ArrowUp,
  ChevronDown,
  ChevronUp,
  Command,
  Ellipsis,
  Image as ImageIcon,
  ListOrdered,
  ListTodo,
  LoaderCircle,
  Maximize2,
  Minimize2,
  Octagon,
  Paperclip,
  RotateCcw,
  Shield,
  X,
  Zap,
} from '@lucide/vue'
import { computed, nextTick, onMounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import SearchableSelect from '@/components/SearchableSelect.vue'
import { Button } from '@/components/ui/button'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useAppStore, useCapabilitiesStore, useClaudeStore, useCodexStore, useGrokStore } from '@/stores'
import {
  buildContextUsageView,
  CODEX_CONTEXT_BASELINE_TOKENS,
  formatTokenCount,
} from '@/utils/accountUsage'
import { forgetImagePreview, rememberLocalImagePreview, resolveImagePreview } from '@/utils/imagePreview'
import { notify } from '@/utils/notify'
import {
  DEFAULT_CODEX_REASONING,
  DEFAULT_GROK_REASONING,
  formatModelLabel,
  modelsForGrokRuntime,
  modelsForRuntime,
} from '@/utils/runtimeProviders'

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const capabilitiesStore = useCapabilitiesStore()
// claudeStore used for Claude Code runtime composer path
const router = useRouter()
const { t } = useI18n()

const modelValue = defineModel<string>({ required: true })
const composer = useTemplateRef<HTMLElement>('composer')
const composing = shallowRef(false)
const attachedImages = shallowRef<string[]>([])
const attachmentPreviews = shallowRef<Record<string, string>>({})
const slashIndex = shallowRef(0)
const skillIndex = shallowRef(0)
const pluginIndex = shallowRef(0)
const dragDepth = shallowRef(0)
const composerExpanded = shallowRef(false)
const COMPOSER_MAX_COLLAPSED = 200
const COMPOSER_MAX_EXPANDED = 480

type SlashCommand = {
  id: string
  label: string
  description: string
  run: () => void | Promise<void>
}

const slashCommands = computed<SlashCommand[]>(() => {
  if (appStore.isGrokMode) {
    return [
      {
        id: 'rename',
        label: '/rename',
        description: t('slash.rename'),
        run: () => grokStore.renameActiveSession(),
      },
      {
        id: 'archive',
        label: '/archive',
        description: t('slash.archive'),
        run: () => grokStore.archiveActiveSession(),
      },
      {
        id: 'delete',
        label: '/delete',
        description: t('slash.delete'),
        run: () => grokStore.deleteActiveSession(),
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
    id: 'review',
    label: '/review',
    description: t('slash.review'),
    run: () => codexStore.startReview({ targetType: 'uncommittedChanges', delivery: 'inline' }),
  },
  {
    id: 'compact',
    label: '/compact',
    description: t('slash.compact'),
    run: () => codexStore.compactActiveThread(),
  },
  {
    id: 'fork',
    label: '/fork',
    description: t('slash.fork'),
    run: () => codexStore.forkActiveThread(),
  },
  {
    id: 'archive',
    label: '/archive',
    description: t('slash.archive'),
    run: () => codexStore.archiveActiveThread(),
  },
  {
    id: 'rename',
    label: '/rename',
    description: t('slash.rename'),
    run: () => codexStore.renameActiveThread(),
  },
  {
    id: 'delete',
    label: '/delete',
    description: t('slash.delete'),
    run: () => codexStore.deleteActiveThread(),
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
  if (!text.startsWith('/') || text.includes('\n') || text.includes(' ')) return ''
  return text.slice(1).toLocaleLowerCase()
})
const slashOpen = computed(() => modelValue.value.startsWith('/') && !modelValue.value.includes('\n') && !modelValue.value.slice(1).includes(' '))
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
  if (appStore.isGrokMode) return grokStore.activeTokenUsage
  if (appStore.isClaudeMode) return claudeStore.activeTokenUsage
  return codexStore.activeTokenUsage
})
const contextUsage = computed(() => buildContextUsageView(
  activeTokenUsage.value,
  appStore.isCodexMode ? CODEX_CONTEXT_BASELINE_TOKENS : 0,
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
  return `${t('inspector.contextUsage')} ${contextUsedPercent.value.toFixed(1)}% · ${formatTokenCount(contextUsedTokens.value)} / ${formatTokenCount(contextWindow.value)}`
})
const sessionLocked = computed(() => Boolean(
  appStore.isCodexMode
  && codexStore.activeThreadId
  && !codexStore.activeThreadId.startsWith('pending-thread-')
  && codexStore.activeThread,
))
const grokProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'grok'))
const claudeProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'claude'))
const displayModel = computed(() => {
  if (appStore.isGrokMode) {
    return appStore.settings.grokBackend === 'api'
      ? (appStore.settings.grokAPIModel || appStore.settings.grokBuildModel || 'grok-4.5')
      : (appStore.settings.grokBuildModel || appStore.settings.grokAPIModel || 'grok-4.5')
  }
  if (appStore.isClaudeMode) {
    return appStore.settings.claudeModel || 'sonnet'
  }
  return sessionLocked.value
    ? (codexStore.activeThread?.model || appStore.settings.model)
    : appStore.settings.model
})
const displayEffort = computed(() => {
  if (appStore.isGrokMode) return appStore.settings.grokEffort || 'high'
  if (appStore.isClaudeMode) return appStore.settings.claudeEffort || 'high'
  return sessionLocked.value
    ? (codexStore.activeThread?.effort || appStore.settings.effort)
    : appStore.settings.effort
})
const selectedModel = computed(() => appStore.models.find((model) => model.model === displayModel.value))
const selectableModels = computed(() => {
  if (appStore.isGrokMode) {
    return modelsForGrokRuntime(grokProvider.value?.models ?? [], displayModel.value)
  }
  if (appStore.isClaudeMode) {
    const models = claudeProvider.value?.models ?? []
    if (models.length) {
      return models.map((item) => ({
        model: item.model,
        displayName: item.displayName || formatModelLabel(item.model),
        description: item.description
          || (item.displayName ? `alias \`${item.model}\`` : item.model),
        isDefault: item.isDefault,
      }))
    }
    return [
      { model: 'sonnet', displayName: 'Claude Sonnet', description: 'alias `sonnet` → latest Sonnet', isDefault: true },
      { model: 'opus', displayName: 'Claude Opus', description: 'alias `opus` → latest Opus', isDefault: false },
      { model: 'haiku', displayName: 'Claude Haiku', description: 'alias `haiku` → latest Haiku', isDefault: false },
      { model: 'fable', displayName: 'Claude Fable', description: 'alias `fable` → latest Fable', isDefault: false },
    ]
  }
  return modelsForRuntime(appStore.models, appStore.settings.customModels ?? [])
})
const composerModelOptions = computed(() => selectableModels.value.map((model) => {
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
  if (appStore.isGrokMode) {
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
  if (appStore.isClaudeMode) {
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
  const fromModel = selectedModel.value?.supportedReasoningEfforts ?? []
  return fromModel.length ? fromModel : [...DEFAULT_CODEX_REASONING]
})
/** Selected permission preset: ask | auto | strict — labels always match menu items. */
const permissionPreset = computed((): 'ask' | 'auto' | 'strict' => {
  if (appStore.isClaudeMode) {
    const mode = appStore.settings.claudePermissionMode || ''
    if (mode === 'bypassPermissions') return 'auto'
    if (mode === 'plan') return 'strict'
    if (mode === 'acceptEdits' || mode === 'auto' || mode === 'dontAsk' || mode === 'manual') return 'ask'
    // Fall back to legacy sandbox pair.
  }
  const sandbox = appStore.isGrokMode
    ? appStore.settings.grokSandbox
    : appStore.isClaudeMode
      ? appStore.settings.claudeSandbox
      : appStore.settings.sandbox
  const approval = appStore.isGrokMode
    ? appStore.settings.grokApprovalPolicy
    : appStore.isClaudeMode
      ? appStore.settings.claudeApprovalPolicy
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
  if (!appStore.isClaudeMode) return ''
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
const activeQueuedMessages = computed(() => {
  if (appStore.isGrokMode) return grokStore.activeQueuedMessages
  if (appStore.isClaudeMode) return claudeStore.activeQueuedMessages
  return codexStore.activeQueuedMessages
})
/** Only show the queue strip when something is actually waiting / failed — not the in-flight send. */
const showQueueStrip = computed(() =>
  activeQueuedMessages.value.some((message) => message.state === 'queued' || message.state === 'failed'),
)
/**
 * Follow-ups must stay sendable while a turn runs (queue or steer).
 * Never gate this on isTurnRunning / sendingMessage — Grok uses the same queue path.
 */
const canSend = computed(() => {
  const hasContent = Boolean(modelValue.value.trim()) || attachedImages.value.length > 0
  if (appStore.isGrokMode) {
    return hasContent && grokStore.isReady
  }
  if (appStore.isClaudeMode) {
    return hasContent && claudeStore.isReady && Boolean(claudeStore.workspacePath)
  }
  return hasContent && codexStore.isReady && !codexStore.creatingThread
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
  if (appStore.isGrokMode) grokStore.reorderQueuedMessage(messageId, direction)
  else if (appStore.isClaudeMode) claudeStore.reorderQueuedMessage(messageId, direction)
  else codexStore.reorderQueuedMessage(messageId, direction)
}

function sendQueuedNow(messageId: string): void {
  if (appStore.isGrokMode) void grokStore.sendQueuedMessageNow(messageId)
  else if (appStore.isClaudeMode) void claudeStore.sendQueuedMessageNow(messageId)
  else void codexStore.sendQueuedMessageNow(messageId)
}

function retryQueued(messageId: string): void {
  if (appStore.isGrokMode) grokStore.retryQueuedMessage(messageId)
  else if (appStore.isClaudeMode) claudeStore.retryQueuedMessage(messageId)
  else codexStore.retryQueuedMessage(messageId)
}

function removeQueued(messageId: string): void {
  if (appStore.isGrokMode) grokStore.removeQueuedMessage(messageId)
  else if (appStore.isClaudeMode) claudeStore.removeQueuedMessage(messageId)
  else codexStore.removeQueuedMessage(messageId)
}

const canSteer = computed(() =>
  appStore.isCodexMode
  && (appStore.settings.followUpBehavior || 'queue') === 'steer'
  && codexStore.isTurnRunning
  && Boolean(codexStore.activeTurnId)
  && !codexStore.activeThreadId.startsWith('pending-thread-'),
)
const willQueueOnSend = computed(() => {
  if (canSteer.value) return false
  if (appStore.isGrokMode) {
    return grokStore.isTurnRunning || grokStore.sending || showQueueStrip.value
  }
  if (appStore.isClaudeMode) {
    return claudeStore.isTurnRunning || claudeStore.sending || showQueueStrip.value
  }
  return codexStore.isTurnRunning || codexStore.sendingMessage || showQueueStrip.value
})
const activeRuntimeTurnRunning = computed(() => {
  if (appStore.isGrokMode) return grokStore.isTurnRunning
  if (appStore.isClaudeMode) return claudeStore.isTurnRunning
  return codexStore.isTurnRunning
})
const activeRuntimeSending = computed(() => {
  if (appStore.isGrokMode) return grokStore.sending
  if (appStore.isClaudeMode) return claudeStore.sending
  return codexStore.sendingMessage
})
const stopDisabled = computed(() => appStore.isCodexMode && codexStore.interruptingTurn)
const sendButtonLabel = computed(() => {
  if (canSteer.value) return t('chat.steer')
  if (willQueueOnSend.value) return t('chat.queueSend')
  return t('chat.send')
})
const composerPlaceholder = computed(() => {
  if (appStore.isGrokMode && willQueueOnSend.value) return t('chat.queuePlaceholder')
  if (appStore.isGrokMode) return t('chat.grokPlaceholder')
  if (appStore.isClaudeMode && willQueueOnSend.value) return t('chat.queuePlaceholder')
  if (appStore.isClaudeMode) return t('chat.claudePlaceholder')
  if (canSteer.value) return t('chat.steerPlaceholder')
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

watch(modelValue, resize, { flush: 'post' })
watch(composerExpanded, resize, { flush: 'post' })
watch(
  [selectableModels, displayModel],
  () => {
    const models = selectableModels.value
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
    const textarea = composer.value?.querySelector('textarea')
    if (!textarea) return
    const max = composerExpanded.value ? COMPOSER_MAX_EXPANDED : COMPOSER_MAX_COLLAPSED
    textarea.style.height = '0px'
    if (composerExpanded.value) {
      textarea.style.height = `${max}px`
      return
    }
    textarea.style.height = `${Math.min(textarea.scrollHeight, max)}px`
  })
}

function toggleComposerHeight(): void {
  composerExpanded.value = !composerExpanded.value
  resize()
}

function onKeydown(event: KeyboardEvent): void {
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
  // Official Codex: Shift+Tab toggles plan mode.
  if (event.key === 'Tab' && event.shiftKey) {
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
  modelValue.value = ''
  await command.run()
}

const collaborationMode = computed(() => {
  const sessionMode = codexStore.activeThread?.collaborationMode
  if (sessionMode === 'plan' || sessionMode === 'default') return sessionMode
  return appStore.settings.collaborationMode === 'plan' ? 'plan' : 'default'
})
const isPlanMode = computed(() => collaborationMode.value === 'plan')

async function togglePlanMode(): Promise<void> {
  await codexStore.setCollaborationMode(isPlanMode.value ? 'default' : 'plan')
}

async function send(): Promise<void> {
  const message = modelValue.value.trim()
  const images = [...attachedImages.value]
  if (!message && !images.length) return
  if (appStore.isGrokMode) {
    // Do not gate on sending — busy turns enqueue like Codex.
    if (!grokStore.isReady) return
    modelValue.value = ''
    attachedImages.value = []
    const ok = await grokStore.sendMessage(message, images)
    if (!ok) {
      modelValue.value = message
      attachedImages.value = images
    } else {
      for (const path of images) forgetImagePreview(path)
      attachmentPreviews.value = {}
    }
    return
  }
  if (appStore.isClaudeMode) {
    if (!claudeStore.isReady) return
    // Capture then clear immediately so a second Enter cannot re-send the same text.
    modelValue.value = ''
    attachedImages.value = []
    const ok = await claudeStore.sendMessage(message, images)
    if (!ok) {
      // Only restore when the send truly failed (not when it was queued).
      modelValue.value = message
      attachedImages.value = images
    } else {
      for (const path of images) forgetImagePreview(path)
      attachmentPreviews.value = {}
    }
    return
  }
  if (!codexStore.isReady) return
  modelValue.value = ''
  attachedImages.value = []
  const ok = await codexStore.sendMessage(message, images)
  if (!ok) {
    modelValue.value = message
    attachedImages.value = images
  } else {
    for (const path of images) forgetImagePreview(path)
    attachmentPreviews.value = {}
  }
}

function onStop(): void {
  if (appStore.isGrokMode) {
    void grokStore.interruptTurn()
    return
  }
  if (appStore.isClaudeMode) {
    void claudeStore.interruptActiveTurn()
    return
  }
  void codexStore.interruptTurn()
}

async function attachImages(): Promise<void> {
  if (attachedImages.value.length >= 4) return
  try {
    const selected = await backend.SelectImages() ?? []
    if (!selected.length) return
    const next = [...new Set([...attachedImages.value, ...selected])].slice(0, 4)
    attachedImages.value = next
    for (const path of selected) void loadAttachmentPreview(path)
  } catch (error) {
    notify('error', t('notifications.imagesNotSelected'), error instanceof Error ? error.message : t('notifications.unexpected'))
  }
}

function isImageFile(file: File): boolean {
  if (file.type.startsWith('image/')) return true
  return /\.(png|jpe?g|webp|gif)$/i.test(file.name)
}

function collectImageFiles(list: FileList | File[] | null | undefined): File[] {
  if (!list) return []
  return Array.from(list).filter(isImageFile)
}

async function fileToBase64(file: File): Promise<string> {
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
  if (!path || attachmentPreviews.value[path]) return
  const url = await resolveImagePreview(path)
  if (!url) return
  attachmentPreviews.value = { ...attachmentPreviews.value, [path]: url }
}

async function attachImageFiles(files: File[]): Promise<void> {
  const images = files.filter(isImageFile)
  if (!images.length) return
  const room = 4 - attachedImages.value.length
  if (room <= 0) {
    notify('warning', t('chat.attachLimitTitle'), t('chat.attachLimit'))
    return
  }
  const next = [...attachedImages.value]
  const previewPatch: Record<string, string> = {}
  try {
    for (const file of images.slice(0, room)) {
      const dataBase64 = await fileToBase64(file)
      const path = await backend.AttachImageData(
        file.name || `paste-${Date.now()}.png`,
        file.type || '',
        dataBase64,
      )
      if (path && !next.includes(path)) {
        next.push(path)
        const localPreview = URL.createObjectURL(file)
        rememberLocalImagePreview(path, localPreview)
        previewPatch[path] = localPreview
      }
    }
    attachedImages.value = next.slice(0, 4)
    if (Object.keys(previewPatch).length) {
      attachmentPreviews.value = { ...attachmentPreviews.value, ...previewPatch }
    }
  } catch (error) {
    notify('error', t('notifications.imagesNotSelected'), error instanceof Error ? error.message : t('notifications.unexpected'))
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
  const next = { ...attachmentPreviews.value }
  delete next[path]
  attachmentPreviews.value = next
  forgetImagePreview(path)
}

function attachmentName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

async function applyModelSelection(value: string): Promise<void> {
  const modelID = value.trim()
  if (!modelID) return

  if (appStore.isGrokMode) {
    if (appStore.settings.grokBackend === 'api') {
      appStore.updateGrokPreferences({ grokAPIModel: modelID })
    } else {
      appStore.updateGrokPreferences({ grokBuildModel: modelID })
    }
    return
  }
  if (appStore.isClaudeMode) {
    appStore.patchSettings({ claudeModel: modelID })
    return
  }

  let effort = appStore.settings.effort
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

  if (sessionLocked.value && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(modelID, effort)
    try {
      await backend.UpdateSessionPreferences({
        sessionId: codexStore.activeThread.id,
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
  if (codexStore.activeThreadId.startsWith('pending-thread-') && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(modelID, effort)
  }
}

function onEffortChange(value: string): void {
  if (appStore.isGrokMode) {
    appStore.updateGrokPreferences({ grokEffort: value })
    return
  }
  if (appStore.isClaudeMode) {
    appStore.patchSettings({ claudeEffort: value })
    return
  }
  if (sessionLocked.value && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(displayModel.value, value)
    void backend.UpdateSessionPreferences({
      sessionId: codexStore.activeThread.id,
      model: displayModel.value,
      effort: value,
      collaborationMode: collaborationMode.value,
    }).catch(() => undefined)
    return
  }
  appStore.updateAgentPreferences(displayModel.value || appStore.settings.model, value, appStore.settings.serviceTier, appStore.settings.collaborationMode)
  if (codexStore.activeThreadId.startsWith('pending-thread-') && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(displayModel.value, value)
  }
}

function setPermission(mode: 'ask' | 'auto' | 'strict'): void {
  if (appStore.isClaudeMode) {
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
  if (appStore.isGrokMode) {
    const values = mode === 'auto'
      ? { grokSandbox: 'danger-full-access', grokApprovalPolicy: 'never' }
      : mode === 'strict'
        ? { grokSandbox: 'read-only', grokApprovalPolicy: 'on-request' }
        : { grokSandbox: 'workspace-write', grokApprovalPolicy: 'on-request' }
    if (values.grokSandbox === appStore.settings.grokSandbox && values.grokApprovalPolicy === appStore.settings.grokApprovalPolicy) return
    appStore.updateGrokPreferences(values)
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
  <div class="shrink-0 px-4 pb-4 pt-1 sm:px-6">
    <div
      ref="composer"
      class="relative mx-auto flex max-w-[680px] flex-col gap-1.5 rounded-xl border bg-card p-2 transition-colors"
      :class="[
        isDraggingFiles
          ? 'border-primary border-dashed bg-primary/5'
          : (
            (activeRuntimeTurnRunning || activeRuntimeSending)
              ? 'border-primary/35'
              : 'border-border'
          ),
      ]"
      @dragenter="onDragEnter"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <div
        v-if="isDraggingFiles"
        class="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-xl bg-primary/8 text-xs font-medium text-primary"
      >
        {{ t('chat.dropImages') }}
      </div>

      <div class="absolute right-1.5 top-1.5 z-[2]">
        <SimpleTooltip :content="composerExpanded ? t('chat.collapseComposer') : t('chat.expandComposer')">
          <Button
            variant="ghost"
            size="icon-xs"
            class="size-6 text-muted-foreground"
            :aria-label="composerExpanded ? t('chat.collapseComposer') : t('chat.expandComposer')"
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
          <img
            v-if="attachmentPreviews[path]"
            :src="attachmentPreviews[path]"
            :alt="attachmentName(path)"
            class="h-14 w-14 object-cover"
          >
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
              <span class="truncate">{{ t('chat.queuedCount', { count: activeQueuedMessages.filter((m) => m.state !== 'sending').length || activeQueuedMessages.length }) }}</span>
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" side="top" class="w-[26rem] max-w-[calc(100vw-2rem)] p-2">
            <div class="px-2 pb-2 pt-1">
              <p class="text-xs font-medium">{{ t('chat.queuedTitle') }}</p>
              <p class="mt-1 text-[11px] leading-4 text-muted-foreground">{{ t('chat.queuedHint') }}</p>
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
                  <p class="line-clamp-2 text-[12px] leading-4 text-foreground/90">{{ message.text || t('chat.queuedImageOnly') }}</p>
                  <div class="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
                    <span v-if="message.state === 'sending'">{{ t('chat.queuedSending') }}</span>
                    <span v-else-if="message.state === 'failed'" class="text-destructive">{{ t('chat.queuedFailed') }}</span>
                    <span v-else>{{ t('chat.queuedWaiting') }}</span>
                    <span v-if="message.images.length">· {{ t('chat.queuedAttachments', { count: message.images.length }) }}</span>
                  </div>
                  <p v-if="message.error" class="mt-0.5 line-clamp-2 text-[10px] text-destructive/90">
                    {{ message.error }}
                  </p>
                </div>

                <div
                  v-if="message.state !== 'sending'"
                  class="flex shrink-0 items-center"
                >
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
                </div>
              </div>
            </div>
          </PopoverContent>
        </Popover>
        <span class="hidden truncate pr-1 text-[10px] text-muted-foreground sm:block">{{ t('chat.queuedHint') }}</span>
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
        v-model="modelValue"
        rows="1"
        :placeholder="composerPlaceholder"
        :aria-description="composerShortcutHint"
        class="min-h-[44px] resize-none border-0 bg-transparent px-2 py-1.5 pr-8 text-[13.5px] leading-6 shadow-none placeholder:text-muted-foreground/70 focus-visible:border-0 focus-visible:ring-0 focus-visible:outline-none"
        :class="composerExpanded ? 'overflow-y-auto' : ''"
        @compositionend="composing = false"
        @compositionstart="composing = true"
        @keydown="onKeydown"
        @paste="onPaste"
      />

      <div class="flex items-center justify-between gap-1">
        <div class="flex min-w-0 items-center gap-0.5">
          <Button
            variant="ghost"
            size="icon-sm"
            class="size-7 text-muted-foreground"
            :aria-label="t('chat.attachImages')"
            :disabled="attachedImages.length >= 4"
            @click="attachImages"
          >
            <Paperclip :size="14" />
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button
                variant="ghost"
                size="sm"
                class="hidden h-7 gap-1.5 px-2 text-[11px] font-normal text-muted-foreground md:inline-flex"
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
                  <span v-if="appStore.isClaudeMode" class="text-[10px] text-muted-foreground">acceptEdits</span>
                </span>
                <span v-if="permissionPreset === 'ask'" class="ml-2 text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('auto')">
                <span class="flex min-w-0 flex-1 flex-col">
                  <span>{{ t('settings.permissionAuto') }}</span>
                  <span v-if="appStore.isClaudeMode" class="text-[10px] text-muted-foreground">bypassPermissions</span>
                </span>
                <span v-if="permissionPreset === 'auto'" class="ml-2 text-primary">✓</span>
              </DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('strict')">
                <span class="flex min-w-0 flex-1 flex-col">
                  <span>{{ t('settings.permissionStrict') }}</span>
                  <span v-if="appStore.isClaudeMode" class="text-[10px] text-muted-foreground">plan</span>
                </span>
                <span v-if="permissionPreset === 'strict'" class="ml-2 text-primary">✓</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <SimpleTooltip :content="t('chat.planModeToggleHint')">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              class="h-7 gap-1.5 px-2 text-[11px] font-normal"
              :class="isPlanMode
                ? 'bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary'
                : 'text-muted-foreground'"
              :aria-pressed="isPlanMode"
              @click="togglePlanMode"
            >
              <ListTodo :size="12" />
              <span class="hidden sm:inline">{{ isPlanMode ? t('chat.planModeOn') : t('chat.planModeOff') }}</span>
            </Button>
          </SimpleTooltip>

          <!-- Narrow screens: permission + reasoning in one overflow menu -->
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button
                variant="ghost"
                size="icon-sm"
                class="size-7 text-muted-foreground md:hidden"
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

        <div class="flex min-w-0 items-center gap-0.5">
          <SearchableSelect
            v-model="composerModelSelection"
            class="h-7 w-auto max-w-44 border-0 bg-transparent px-2 text-[11px] text-muted-foreground shadow-none hover:bg-muted/50"
            content-class="min-w-[280px]"
            align="end"
            :options="composerModelOptions"
            :aria-label="t('chat.model')"
            :placeholder="t('chat.defaultModel')"
            :search-placeholder="t('settings.modelSearch')"
          />

          <Select
            :model-value="displayEffort"
            @update:model-value="(value) => onEffortChange(String(value))"
          >
            <SelectTrigger
              :aria-label="t('chat.reasoning')"
              class="hidden h-7 w-auto min-w-0 max-w-24 gap-1 border-0 bg-transparent px-2 text-[11px] font-normal text-muted-foreground shadow-none md:flex"
            >
              <Zap :size="11" class="shrink-0" />
              <SelectValue>{{ selectedEffortLabel }}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem
                v-for="option in reasoningOptions"
                :key="option.effort"
                :value="option.effort"
              >
                {{ 'displayName' in option ? option.displayName : option.effort }}
              </SelectItem>
            </SelectContent>
          </Select>

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

          <Button
            v-if="activeRuntimeTurnRunning"
            type="button"
            variant="ghost"
            size="sm"
            class="h-7 px-2 text-[11px] text-destructive hover:bg-destructive/10 hover:text-destructive"
            :disabled="stopDisabled"
            :aria-label="t('chat.stopLabel')"
            @click.stop.prevent="onStop"
          >
            <Octagon :size="12" class="mr-1" fill="currentColor" />
            {{ stopDisabled ? t('chat.stopping') : t('chat.stop') }}
          </Button>
          <SimpleTooltip :content="sendButtonLabel">
            <Button
              type="button"
              size="icon-sm"
              class="size-7 rounded-md transition-opacity"
              :class="canSend ? 'opacity-100' : 'opacity-40'"
              :aria-label="sendButtonLabel"
              :disabled="!canSend"
              @click="send"
            >
              <ArrowUp :size="15" stroke-width="2.5" />
            </Button>
          </SimpleTooltip>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import {
  Anchor,
  Archive,
  ArrowLeft,
  Blocks,
  Check,
  Clock3,
  Compass,
  Download,
  FolderOpen,
  GitBranch,
  Laptop,
  LogIn,
  LogOut,
  Palette,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  Smile,
  Keyboard,
  Trash2,
  UserRound,
  Zap,
} from '@lucide/vue'
import { computed, onMounted, onUnmounted, shallowRef, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import GrokIcon from '@/components/icons/GrokIcon.vue'
import OpenAIIcon from '@/components/icons/OpenAIIcon.vue'
import SearchableSelect from '@/components/SearchableSelect.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Textarea } from '@/components/ui/textarea'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { supportedLocales } from '@/i18n'
import { ACCENT_OPTIONS, type AppAccent } from '@/lib/accents'
import ClaudeIcon from '@/components/icons/ClaudeIcon.vue'
import { useAppStore, useClaudeStore, useCodexStore, useDialogStore, useGrokStore, useWorkspaceStore } from '@/stores'
import {
  readClaudeGlobalInstructions,
  readClaudeProjectInstructions,
  saveClaudeGlobalInstructions,
  saveClaudeProjectInstructions,
} from '@/utils/claudeBindings'
import type { SelectOption } from '@/types/codex'
import { formatTokenCount } from '@/utils/accountUsage'
import { notify } from '@/utils/notify'
import {
  checkCLITools,
  installCLITool,
  type CLIToolStatus,
  type CLIToolsReport,
} from '@/utils/cliTools'
import { DEFAULT_GROK_REASONING, modelsForGrokRuntime, modelsForRuntime } from '@/utils/runtimeProviders'

type SettingsPanel =
  | 'general'
  | 'appearance'
  | 'shortcuts'
  | 'agent'
  | 'personalization'
  | 'account'
  | 'archived'
  | 'plugins'
  | 'browser'
  | 'hooks'
  | 'environment'
  | 'git'
  | 'scheduled'

type NavItem = {
  id: SettingsPanel
  label: string
  icon: Component
  keywords: string
  action?: 'panel' | 'capabilities'
  capabilityTab?: string
}

type NavGroup = {
  id: string
  label: string
  items: NavItem[]
}

const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const claudeModel = shallowRef(appStore.settings.claudeModel || 'sonnet')
const claudeEffort = shallowRef(appStore.settings.claudeEffort || 'high')
const claudeSandbox = shallowRef(appStore.settings.claudeSandbox || 'workspace-write')
const claudeApprovalPolicy = shallowRef(appStore.settings.claudeApprovalPolicy || 'on-request')
/** Official Claude Code --permission-mode values. */
const claudePermissionMode = shallowRef(appStore.settings.claudePermissionMode || 'acceptEdits')
const claudePermissionOptions = [
  { value: 'acceptEdits', label: 'acceptEdits' },
  { value: 'auto', label: 'auto' },
  { value: 'plan', label: 'plan' },
  { value: 'dontAsk', label: 'dontAsk' },
  { value: 'manual', label: 'manual' },
  { value: 'bypassPermissions', label: 'bypassPermissions' },
]
const workspaceStore = useWorkspaceStore()
const dialogStore = useDialogStore()
const { t } = useI18n()

const saving = shallowRef(false)
const saved = shallowRef(false)
const settingsSearch = shallowRef('')
const archivedSearch = shallowRef('')
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
const grokBackend = shallowRef(appStore.settings.grokBackend === 'api' ? 'api' : 'build')
const grokBuildModel = shallowRef(appStore.settings.grokBuildModel || '')
const grokAPIModel = shallowRef(appStore.settings.grokAPIModel || 'grok-4.5')
const grokEffort = shallowRef(appStore.settings.grokEffort || 'high')
const grokSandbox = shallowRef(appStore.settings.grokSandbox || 'workspace-write')
const grokApprovalPolicy = shallowRef(appStore.settings.grokApprovalPolicy || 'on-request')
const grokWebSearch = shallowRef(appStore.settings.grokWebSearch !== false)
const grokXSearch = shallowRef(Boolean(appStore.settings.grokXSearch))
const grokAPIKey = shallowRef(appStore.settings.grokAPIKey || '')
const grokAPIBaseURL = shallowRef(appStore.settings.grokAPIBaseURL || '')
const theme = shallowRef(appStore.settings.theme)
const accentColor = shallowRef(appStore.settings.accentColor)
const fontFamily = shallowRef(appStore.settings.fontFamily)
const translucentSidebar = shallowRef(appStore.settings.translucentSidebar !== false)
const highContrast = shallowRef(Boolean(appStore.settings.highContrast))
const pointerCursor = shallowRef(Boolean(appStore.settings.pointerCursor))
const reduceMotion = shallowRef(Boolean(appStore.settings.reduceMotion))
const uiFontSize = shallowRef(appStore.settings.uiFontSize === 'sm' || appStore.settings.uiFontSize === 'lg' ? appStore.settings.uiFontSize : 'md')
const codeFontSize = shallowRef(appStore.settings.codeFontSize === 'sm' || appStore.settings.codeFontSize === 'lg' ? appStore.settings.codeFontSize : 'md')
const terminalProfile = shallowRef(appStore.settings.terminalProfile)
const language = shallowRef(appStore.settings.language)
const autoConnect = shallowRef(appStore.settings.autoConnect)
const sendWithModifier = shallowRef(Boolean(appStore.settings.sendWithModifier))
const followUpBehavior = shallowRef(appStore.settings.followUpBehavior === 'steer' ? 'steer' : 'queue')
watch(followUpBehavior, (value) => {
  const next = value === 'steer' ? 'steer' : 'queue'
  if (appStore.settings.followUpBehavior === next) return
  // Apply immediately so composer queue/steer matches the picker before Save.
  appStore.patchSettings({ followUpBehavior: next })
})
const notifyOnTurnComplete = shallowRef(appStore.settings.notifyOnTurnComplete !== false)
const preventSleepWhileRunning = shallowRef(Boolean(appStore.settings.preventSleepWhileRunning))
const alwaysOnTop = shallowRef(Boolean(appStore.settings.alwaysOnTop))
const gitBranchPrefix = shallowRef(appStore.settings.gitBranchPrefix ?? '')
const gitCommitPrefix = shallowRef(appStore.settings.gitCommitPrefix ?? '')
const gitOpenPRAfterPush = shallowRef(Boolean(appStore.settings.gitOpenPRAfterPush))
const gitPRBodyTemplate = shallowRef(appStore.settings.gitPRBodyTemplate ?? '')
const gitBranchDraft = shallowRef('')
const gitCommitDraft = shallowRef('')
const gitActionBusy = shallowRef(false)
const shortcutCommandPalette = shallowRef(appStore.settings.shortcutCommandPalette || 'Ctrl+K')
const shortcutNewThread = shallowRef(appStore.settings.shortcutNewThread || 'Ctrl+N')
const shortcutTerminal = shallowRef(appStore.settings.shortcutTerminal || 'Ctrl+`')
const shortcutBrowser = shallowRef(appStore.settings.shortcutBrowser || 'Ctrl+Shift+B')
const codexClientName = shallowRef(appStore.settings.codexClientName ?? '')
const codexClientTitle = shallowRef(appStore.settings.codexClientTitle ?? '')
const codexClientVersion = shallowRef(appStore.settings.codexClientVersion ?? '')
const browserAllowedHostsText = shallowRef((appStore.settings.browserAllowedHosts ?? []).join('\n'))
const browserBlockedHostsText = shallowRef((appStore.settings.browserBlockedHosts ?? []).join('\n'))
const browserDownloadDir = shallowRef(appStore.settings.browserDownloadDir ?? '')
const browserFullCDP = shallowRef(Boolean(appStore.settings.browserFullCDP))
const memoriesEnabled = shallowRef(false)
const memoriesGenerate = shallowRef(true)
const memoriesUse = shallowRef(true)
const memoriesDisableExternal = shallowRef(false)
const scheduledTasks = shallowRef<Array<{
  id: string
  title: string
  prompt: string
  workspace: string
  enabled: boolean
  intervalMin: number
  useWorktree: boolean
  lastRunAt: number
  nextRunAt: number
  lastError?: string
}>>([])
const scheduledDraftTitle = shallowRef('')
const scheduledDraftPrompt = shallowRef('')
const scheduledDraftInterval = shallowRef(60)
const scheduledDraftWorktree = shallowRef(true)
const scheduledLoading = shallowRef(false)
const customInstructions = shallowRef(appStore.settings.customInstructions ?? '')
const globalInstructionsPath = shallowRef('')
const globalInstructionsSource = shallowRef('AGENTS.md')
const globalInstructionsExists = shallowRef(false)
const globalInstructionsEmptyFile = shallowRef(false)
const projectInstructions = shallowRef('')
const projectInstructionsAvailable = shallowRef(false)
const projectInstructionsPath = shallowRef('')
const projectInstructionsSource = shallowRef('AGENTS.md')
const projectInstructionsExists = shallowRef(false)
const projectInstructionsEmptyFile = shallowRef(false)
const projectInstructionsWorkspace = shallowRef('')
const projectInstructionsWorkspaceName = shallowRef('')
const instructionsLoading = shallowRef(false)
const customInstructionsLength = computed(() => customInstructions.value.length)
const projectInstructionsLength = computed(() => projectInstructions.value.length)

function instructionsStatusLabel(exists: boolean, emptyFile: boolean): string {
  if (exists) return t('settings.instructionsFileHasContent')
  if (emptyFile) return t('settings.instructionsFileEmpty')
  return t('settings.instructionsFileMissing')
}
const activePanel = shallowRef<SettingsPanel>('general')
const modelSelection = computed({
  get: () => model.value || DEFAULT_MODEL_VALUE,
  set: (value: string) => { model.value = value === DEFAULT_MODEL_VALUE ? '' : value },
})

const selectedModel = computed(() => appStore.models.find((item) => item.model === model.value))
const codexStatus = computed(() => appStore.agentProviders.find((provider) => provider.kind === 'codex'))
const isGrokSettings = computed(() => appStore.isGrokMode)
const isClaudeSettings = computed(() => appStore.isClaudeMode)
const claudeStatus = computed(() => {
  const fromProviders = appStore.agentProviders.find((provider) => provider.kind === 'claude')
  const rt = claudeStore.runtime
  const ready = Boolean(rt.available || fromProviders?.runtimeReady)
  return {
    name: 'Claude',
    kind: 'claude',
    runtimeReady: ready,
    version: rt.version || fromProviders?.version || '',
    executable: rt.executable || fromProviders?.executable || '',
    message: fromProviders?.message || rt.message || t('sidebar.claudeRuntimeMissing'),
    models: fromProviders?.models ?? null,
    reasoningEfforts: fromProviders?.reasoningEfforts ?? null,
  }
})
/** Prefer live Grok probe over Bootstrap snapshot (PATH may miss ~/.grok/bin). */
const grokStatus = computed(() => {
  const fromProviders = appStore.agentProviders.find((provider) => provider.kind === 'grok')
  const rt = grokStore.runtime
  const ready = Boolean(rt.buildAvailable || rt.apiConfigured || fromProviders?.runtimeReady)
  return {
    name: 'Grok',
    kind: 'grok',
    runtimeReady: ready,
    version: rt.buildVersion || fromProviders?.version || '',
    executable: rt.buildExecutable || fromProviders?.executable || '',
    message: fromProviders?.message
      || (rt.buildAvailable
        ? (rt.buildAuthenticated ? 'Grok Build ready' : 'Grok Build installed')
        : (rt.apiConfigured ? 'Grok API configured' : 'Install Grok Build CLI or configure a Grok API key')),
    models: fromProviders?.models ?? null,
    reasoningEfforts: fromProviders?.reasoningEfforts ?? null,
  }
})

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

const grokModelOptions = computed<SelectOption[]>(() => {
  const preferred = grokBackend.value === 'api' ? grokAPIModel.value : grokBuildModel.value
  const catalog = (grokStatus.value.models ?? []).map((item) => ({
    model: item.model,
    displayName: item.displayName,
    isDefault: item.isDefault,
  }))
  return modelsForGrokRuntime(catalog, preferred).map((option) => ({
    value: option.model,
    label: option.displayName,
    description: option.model,
    badge: option.isDefault ? t('common.recommended') : '',
  }))
})

const grokModelSelection = computed({
  get: () => (grokBackend.value === 'api' ? grokAPIModel.value : grokBuildModel.value) || DEFAULT_MODEL_VALUE,
  set: (value: string) => {
    const next = value === DEFAULT_MODEL_VALUE ? '' : value
    if (grokBackend.value === 'api') grokAPIModel.value = next
    else grokBuildModel.value = next
  },
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

const grokEffortOptions = computed(() => {
  const fromProvider = grokStatus.value.reasoningEfforts ?? []
  if (fromProvider.length) {
    return fromProvider.map((item: { effort: string, displayName?: string, description?: string }) => ({
      effort: item.effort,
      displayName: item.displayName,
      description: item.description,
    }))
  }
  return [...DEFAULT_GROK_REASONING]
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

/** Exclusive permission levels — bind to Codex or Grok fields by active runtime. */
const permissionLevel = computed<'default' | 'autoReview' | 'full' | 'strict'>(() => {
  const box = isGrokSettings.value ? grokSandbox.value : sandbox.value
  const approval = isGrokSettings.value ? grokApprovalPolicy.value : approvalPolicy.value
  if (box === 'danger-full-access' && approval === 'never') return 'full'
  if (box === 'workspace-write' && approval === 'never') return 'autoReview'
  if (box === 'read-only') return 'strict'
  return 'default'
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

const followUpOptions = computed<SelectOption[]>(() => [
  { value: 'steer', label: t('settings.followUpSteer'), description: t('settings.followUpSteerHint') },
  { value: 'queue', label: t('settings.followUpQueue'), description: t('settings.followUpQueueHint') },
])

/** Official client identities accepted by most Codex reverse-proxy channels. */
const CODEX_CLIENT_PRESETS = [
  { id: 'desktop', name: 'codex_desktop', title: 'Codex Desktop', version: '0.1.0' },
  { id: 'cli', name: 'codex_cli_rs', title: 'Codex CLI', version: '0.1.0' },
  { id: 'vscode', name: 'codex_vscode', title: 'Codex VS Code Extension', version: '0.1.0' },
] as const

const codexClientPreset = computed({
  get: () => {
    const name = codexClientName.value.trim()
    const title = codexClientTitle.value.trim()
    const version = codexClientVersion.value.trim()
    // Empty settings → runtime defaults to official desktop.
    if (!name && !title && !version) return 'desktop'
    const match = CODEX_CLIENT_PRESETS.find((item) => {
      if (item.name !== name) return false
      if (title && item.title !== title) return false
      if (version && item.version !== version) return false
      return true
    })
    return match?.id ?? 'custom'
  },
  set: (value: string) => {
    if (value === 'custom') {
      if (!codexClientName.value.trim()) codexClientName.value = 'codex_desktop'
      if (!codexClientTitle.value.trim()) codexClientTitle.value = 'Codex Desktop'
      if (!codexClientVersion.value.trim()) codexClientVersion.value = '0.1.0'
      return
    }
    const preset = CODEX_CLIENT_PRESETS.find((item) => item.id === value)
    if (!preset) return
    codexClientName.value = preset.name
    codexClientTitle.value = preset.title
    codexClientVersion.value = preset.version
  },
})

const codexClientPresetOptions = computed<SelectOption[]>(() => [
  { value: 'desktop', label: t('settings.codexClientPresetDesktop'), description: 'codex_desktop' },
  { value: 'cli', label: t('settings.codexClientPresetCli'), description: 'codex_cli_rs' },
  { value: 'vscode', label: t('settings.codexClientPresetVscode'), description: 'codex_vscode' },
  { value: 'custom', label: t('settings.codexClientPresetCustom'), description: t('settings.codexClientPresetCustomHint') },
])

const sendModifierLabel = computed(() =>
  /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl',
)

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

const accentLabelKey: Record<AppAccent, string> = {
  codex: 'settings.accentCodex',
  amber: 'settings.accentAmber',
  gold: 'settings.accentGold',
  rose: 'settings.accentRose',
  coral: 'settings.accentCoral',
  emerald: 'settings.accentEmerald',
  moss: 'settings.accentMoss',
  ocean: 'settings.accentOcean',
  sky: 'settings.accentSky',
  slate: 'settings.accentSlate',
  graphite: 'settings.accentGraphite',
}

const accentOptions = computed(() =>
  ACCENT_OPTIONS.map((option) => ({
    ...option,
    label: t(accentLabelKey[option.value]),
  })),
)

const navGroups = computed<NavGroup[]>(() => [
  {
    id: 'personal',
    label: t('settings.navPersonal'),
    items: [
      { id: 'general', label: t('settings.navGeneral'), icon: Settings2, keywords: 'general permission language terminal notify send follow-up always on top 常规 权限 语言 终端 通知 发送 跟进 置顶' },
      { id: 'appearance', label: t('settings.navAppearance'), icon: Palette, keywords: 'appearance theme font 外观 主题 字体' },
      { id: 'shortcuts', label: t('settings.navShortcuts'), icon: Keyboard, keywords: 'keyboard shortcuts hotkeys 快捷键' },
      { id: 'agent', label: t('settings.navAgent'), icon: OpenAIIcon, keywords: 'agent model codex 配置 模型' },
      { id: 'personalization', label: t('settings.navPersonalization'), icon: Smile, keywords: 'personality collaboration instructions AGENTS memories 个性化 记忆 全局提示词 项目提示词' },
      { id: 'account', label: t('settings.navAccount'), icon: UserRound, keywords: 'account login usage token 账户 登录 用量' },
      { id: 'archived', label: t('settings.navArchived'), icon: Archive, keywords: 'archived restore conversations 已归档 恢复 对话' },
    ],
  },
  {
    id: 'integration',
    label: t('settings.navIntegration'),
    items: [
      { id: 'plugins', label: t('settings.navPlugins'), icon: Blocks, keywords: 'plugins mcp skills 插件', action: 'capabilities', capabilityTab: 'plugins' },
      { id: 'browser', label: t('settings.navBrowser'), icon: Compass, keywords: 'browser cdp allowlist 浏览器' },
      { id: 'hooks', label: t('settings.navHooks'), icon: Anchor, keywords: 'hooks automation 钩子', action: 'capabilities', capabilityTab: 'automation' },
      { id: 'scheduled', label: t('settings.navScheduled'), icon: Clock3, keywords: 'scheduled tasks automation 定时任务' },
    ],
  },
  {
    id: 'coding',
    label: t('settings.navCoding'),
    items: [
      { id: 'environment', label: t('settings.navEnvironment'), icon: Laptop, keywords: 'environment codex cli client user-agent originator 环境 客户端 标识' },
      { id: 'git', label: t('settings.navGit'), icon: GitBranch, keywords: 'git branch pr prefix 工作区' },
    ],
  },
])

const filteredNavGroups = computed(() => {
  const query = settingsSearch.value.trim().toLocaleLowerCase()
  // Grok/Claude mode: keep agent config + archived; hide pure Codex product surfaces.
  const hideExternal = new Set(['plugins', 'hooks', 'scheduled', 'environment', 'account'])
  let base = navGroups.value
  if (isGrokSettings.value || isClaudeSettings.value) {
    base = navGroups.value
      .map((group) => ({
        ...group,
        items: group.items
          .filter((item) => !hideExternal.has(item.id))
          .map((item) => {
            if (item.id !== 'agent') return item
            if (isGrokSettings.value) {
              return { ...item, label: t('settings.navGrokAgent'), keywords: 'grok model backend xai 配置 模型 后端' }
            }
            return { ...item, label: t('settings.navClaudeAgent'), keywords: 'claude model permission sonnet opus 配置 模型 权限' }
          }),
      }))
      .filter((group) => group.items.length > 0)
  }
  if (!query) return base
  return base
    .map((group) => ({
      ...group,
      items: group.items.filter((item) =>
        `${item.label} ${item.keywords} ${group.label}`.toLocaleLowerCase().includes(query),
      ),
    }))
    .filter((group) => group.items.length > 0)
})

const activeNavItem = computed(() =>
  navGroups.value.flatMap((group) => group.items).find((item) => item.id === activePanel.value),
)

const archivedThreads = computed(() => {
  const query = archivedSearch.value.trim().toLocaleLowerCase()
  const list = appStore.isGrokMode
    ? grokStore.archivedSessions.map((session) => ({
        id: session.id,
        name: session.name,
        preview: session.preview,
      }))
    : appStore.isClaudeMode
      ? claudeStore.archivedSessions.map((session) => ({
          id: session.id,
          name: session.name,
          preview: session.preview,
        }))
      : codexStore.archivedThreads
  if (!query) return list
  return list.filter((thread) => `${thread.name} ${thread.preview}`.toLocaleLowerCase().includes(query))
})

async function restoreArchived(threadID: string): Promise<void> {
  if (appStore.isGrokMode) {
    await grokStore.unarchiveSession(threadID)
    if (grokStore.activeSessionId === threadID) router.push('/')
    return
  }
  if (appStore.isClaudeMode) {
    await claudeStore.unarchiveSession(threadID)
    if (claudeStore.activeSessionId === threadID) router.push('/')
    return
  }
  await codexStore.unarchiveThread(threadID)
  if (codexStore.activeThreadId === threadID) router.push('/')
}

function deleteArchived(threadID: string): void {
  if (appStore.isGrokMode) {
    void grokStore.deleteSession(threadID)
    return
  }
  if (appStore.isClaudeMode) {
    void claudeStore.deleteSession(threadID)
    return
  }
  void codexStore.deleteThread(threadID)
}

watch([theme, accentColor, fontFamily, translucentSidebar, highContrast, pointerCursor, reduceMotion, uiFontSize, codeFontSize], () => {
  appStore.previewAppearance({
    theme: theme.value as 'light' | 'dark' | 'system',
    accentColor: accentColor.value as AppAccent,
    fontFamily: fontFamily.value,
    translucentSidebar: translucentSidebar.value,
    highContrast: highContrast.value,
    pointerCursor: pointerCursor.value,
    reduceMotion: reduceMotion.value,
    uiFontSize: uiFontSize.value,
    codeFontSize: codeFontSize.value,
  })
})

watch(
  () => appStore.settings.workspace,
  () => {
    void loadProjectInstructions()
  },
)

watch(activePanel, (panel) => {
  if (panel === 'personalization') {
    void loadAgentsInstructions()
    void loadFeatureFlags()
  }
  if (panel === 'scheduled') void loadScheduledTasks()
  if (panel === 'browser') void loadFeatureFlags()
  if (panel === 'archived') {
    if (appStore.isGrokMode) void grokStore.loadArchivedSessions()
    else if (appStore.isClaudeMode) void claudeStore.loadArchivedSessions()
    else void codexStore.loadThreads().catch(() => undefined)
  }
  if (panel === 'environment') void refreshCLITools({ silent: true })
})

onMounted(() => {
  syncFromStore()
  void loadCollaborationModes()
  if (!isGrokSettings.value) void appStore.refreshAccountData().catch(() => undefined)
  else void grokStore.refreshRuntime()
  const section = typeof route.query.section === 'string' ? route.query.section : ''
  if (isSettingsPanel(section)) activePanel.value = section
  clampPanelForRuntime()
})

watch(isGrokSettings, (grok) => {
  syncFromStore()
  clampPanelForRuntime()
  if (grok) void grokStore.refreshRuntime()
})

function clampPanelForRuntime(): void {
  if (!isGrokSettings.value) return
  // Archived is supported for Grok (local archive index).
  const hideInGrok = new Set(['plugins', 'hooks', 'scheduled', 'environment', 'account'])
  if (hideInGrok.has(activePanel.value)) activePanel.value = 'agent'
}

onUnmounted(() => {
  if (!saved.value) appStore.restoreAppearance()
})

function isSettingsPanel(value: string): value is SettingsPanel {
  return [
    'general', 'appearance', 'shortcuts', 'agent', 'personalization', 'account', 'archived',
    'plugins', 'browser', 'hooks', 'environment', 'git', 'scheduled',
  ].includes(value)
}

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
  grokBackend.value = settings.grokBackend === 'api' ? 'api' : 'build'
  grokBuildModel.value = settings.grokBuildModel || ''
  grokAPIModel.value = settings.grokAPIModel || 'grok-4.5'
  grokEffort.value = settings.grokEffort || 'high'
  grokSandbox.value = settings.grokSandbox || 'workspace-write'
  grokApprovalPolicy.value = settings.grokApprovalPolicy || 'on-request'
  grokWebSearch.value = settings.grokWebSearch !== false
  grokXSearch.value = Boolean(settings.grokXSearch)
  grokAPIKey.value = settings.grokAPIKey || ''
  grokAPIBaseURL.value = settings.grokAPIBaseURL || ''
  claudeModel.value = settings.claudeModel || 'sonnet'
  claudeEffort.value = settings.claudeEffort || 'high'
  claudeSandbox.value = settings.claudeSandbox || 'workspace-write'
  claudeApprovalPolicy.value = settings.claudeApprovalPolicy || 'on-request'
  claudePermissionMode.value = settings.claudePermissionMode || 'acceptEdits'
  theme.value = settings.theme
  accentColor.value = settings.accentColor
  fontFamily.value = settings.fontFamily
  translucentSidebar.value = settings.translucentSidebar !== false
  highContrast.value = Boolean(settings.highContrast)
  pointerCursor.value = Boolean(settings.pointerCursor)
  reduceMotion.value = Boolean(settings.reduceMotion)
  uiFontSize.value = settings.uiFontSize === 'sm' || settings.uiFontSize === 'lg' ? settings.uiFontSize : 'md'
  codeFontSize.value = settings.codeFontSize === 'sm' || settings.codeFontSize === 'lg' ? settings.codeFontSize : 'md'
  terminalProfile.value = settings.terminalProfile
  language.value = settings.language
  autoConnect.value = settings.autoConnect
  sendWithModifier.value = Boolean(settings.sendWithModifier)
  followUpBehavior.value = settings.followUpBehavior === 'steer' ? 'steer' : 'queue'
  notifyOnTurnComplete.value = settings.notifyOnTurnComplete !== false
  preventSleepWhileRunning.value = Boolean(settings.preventSleepWhileRunning)
  alwaysOnTop.value = Boolean(settings.alwaysOnTop)
  gitBranchPrefix.value = settings.gitBranchPrefix ?? ''
  gitCommitPrefix.value = settings.gitCommitPrefix ?? ''
  gitOpenPRAfterPush.value = Boolean(settings.gitOpenPRAfterPush)
  gitPRBodyTemplate.value = settings.gitPRBodyTemplate ?? ''
  browserAllowedHostsText.value = (settings.browserAllowedHosts ?? []).join('\n')
  browserBlockedHostsText.value = (settings.browserBlockedHosts ?? []).join('\n')
  browserDownloadDir.value = settings.browserDownloadDir ?? ''
  browserFullCDP.value = Boolean(settings.browserFullCDP)
  shortcutCommandPalette.value = settings.shortcutCommandPalette || 'Ctrl+K'
  shortcutNewThread.value = settings.shortcutNewThread || 'Ctrl+N'
  shortcutTerminal.value = settings.shortcutTerminal || 'Ctrl+`'
  shortcutBrowser.value = settings.shortcutBrowser || 'Ctrl+Shift+B'
  codexClientName.value = settings.codexClientName ?? ''
  codexClientTitle.value = settings.codexClientTitle ?? ''
  codexClientVersion.value = settings.codexClientVersion ?? ''
  void loadAgentsInstructions()
  void loadFeatureFlags()
}

function parseHostList(text: string): string[] {
  return text
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

async function loadFeatureFlags(): Promise<void> {
  try {
    const flags = await backend.ReadCodexFeatureFlags()
    memoriesEnabled.value = Boolean(flags?.memoriesEnabled)
    memoriesGenerate.value = flags?.memoriesGenerate !== false
    memoriesUse.value = flags?.memoriesUse !== false
    memoriesDisableExternal.value = Boolean(flags?.memoriesDisableExternalContext)
    browserFullCDP.value = Boolean(flags?.browserUseFullCDP || appStore.settings.browserFullCDP)
  } catch {
    // Keep local defaults when Codex home is unavailable.
  }
}

async function loadScheduledTasks(): Promise<void> {
  scheduledLoading.value = true
  try {
    const list = await backend.ListScheduledTasks()
    scheduledTasks.value = Array.isArray(list) ? list : []
  } catch {
    scheduledTasks.value = []
  } finally {
    scheduledLoading.value = false
  }
}

async function saveScheduledDraft(): Promise<void> {
  if (!scheduledDraftTitle.value.trim() || !scheduledDraftPrompt.value.trim()) return
  try {
    await backend.SaveScheduledTask({
      id: '',
      title: scheduledDraftTitle.value.trim(),
      prompt: scheduledDraftPrompt.value.trim(),
      workspace: appStore.settings.workspace || '',
      enabled: true,
      intervalMin: Math.max(5, Number(scheduledDraftInterval.value) || 60),
      useWorktree: scheduledDraftWorktree.value,
      lastRunAt: 0,
      nextRunAt: 0,
      createdAt: 0,
      updatedAt: 0,
    })
    scheduledDraftTitle.value = ''
    scheduledDraftPrompt.value = ''
    scheduledDraftInterval.value = 60
    await loadScheduledTasks()
  } catch (error) {
    notify('error', t('settings.scheduledSaveFailed'), error instanceof Error ? error.message : String(error))
  }
}

async function toggleScheduledTask(task: {
  id: string
  title: string
  prompt: string
  workspace: string
  enabled: boolean
  intervalMin: number
  useWorktree: boolean
  lastRunAt: number
  nextRunAt: number
  lastError?: string
}, enabled: boolean): Promise<void> {
  await backend.SaveScheduledTask({
    id: task.id,
    title: task.title,
    prompt: task.prompt,
    workspace: task.workspace,
    enabled,
    intervalMin: task.intervalMin,
    useWorktree: task.useWorktree,
    lastRunAt: task.lastRunAt,
    nextRunAt: task.nextRunAt,
    lastError: task.lastError || '',
    createdAt: 0,
    updatedAt: 0,
  })
  await loadScheduledTasks()
}

async function removeScheduledTask(id: string): Promise<void> {
  await backend.DeleteScheduledTask(id)
  await loadScheduledTasks()
}

function openEmbeddedBrowser(): void {
  void router.replace({ name: 'workbench', query: { openBrowser: '1' } })
}

async function loadAgentsInstructions(): Promise<void> {
  instructionsLoading.value = true
  try {
    await Promise.all([loadGlobalInstructions(), loadProjectInstructions()])
  } finally {
    instructionsLoading.value = false
  }
}

async function loadGlobalInstructions(): Promise<void> {
  try {
    const info = isGrokSettings.value
      ? await (await import('@/utils/grokBindings')).readGrokGlobalInstructions()
      : isClaudeSettings.value
        ? await readClaudeGlobalInstructions() as any
        : await backend.ReadGlobalInstructions()
    customInstructions.value = info?.content ?? ''
    globalInstructionsPath.value = info?.path ?? ''
    globalInstructionsSource.value = info?.source
      || (isGrokSettings.value
        ? 'AGENTS.md (~/.grok)'
        : isClaudeSettings.value
          ? 'CLAUDE.md (~/.claude)'
          : 'AGENTS.md')
    globalInstructionsExists.value = Boolean(info?.exists)
    globalInstructionsEmptyFile.value = Boolean(info?.emptyFile)
    if (!isGrokSettings.value && !isClaudeSettings.value) {
      appStore.settings = {
        ...appStore.settings,
        customInstructions: customInstructions.value,
      }
    }
  } catch {
    customInstructions.value = (isGrokSettings.value || isClaudeSettings.value)
      ? ''
      : (appStore.settings.customInstructions ?? '')
    globalInstructionsPath.value = ''
    globalInstructionsSource.value = isGrokSettings.value
      ? 'AGENTS.md (~/.grok)'
      : isClaudeSettings.value
        ? 'CLAUDE.md (~/.claude)'
        : 'AGENTS.md'
    globalInstructionsExists.value = false
    globalInstructionsEmptyFile.value = false
  }
}

async function loadProjectInstructions(): Promise<void> {
  try {
    const info = isGrokSettings.value
      ? await (await import('@/utils/grokBindings')).readGrokProjectInstructions()
      : isClaudeSettings.value
        ? await readClaudeProjectInstructions() as any
        : await backend.ReadProjectInstructions()
    projectInstructionsAvailable.value = Boolean(info?.available)
    projectInstructionsPath.value = info?.path ?? ''
    projectInstructionsSource.value = info?.source || (isClaudeSettings.value ? 'CLAUDE.md' : 'AGENTS.md')
    projectInstructionsExists.value = Boolean(info?.exists)
    projectInstructionsEmptyFile.value = Boolean(info?.emptyFile)
    projectInstructionsWorkspace.value = info?.workspace ?? ''
    projectInstructionsWorkspaceName.value = info?.workspaceName ?? ''
    projectInstructions.value = info?.content ?? ''
  } catch {
    projectInstructionsAvailable.value = false
    projectInstructionsPath.value = ''
    projectInstructionsSource.value = isClaudeSettings.value ? 'CLAUDE.md' : 'AGENTS.md'
    projectInstructionsExists.value = false
    projectInstructionsEmptyFile.value = false
    projectInstructionsWorkspace.value = isGrokSettings.value
      ? (appStore.settings.grokWorkspace || '')
      : isClaudeSettings.value
        ? (appStore.settings.claudeWorkspace || '')
        : (appStore.settings.workspace || '')
    projectInstructionsWorkspaceName.value = ''
    projectInstructions.value = ''
  }
}

async function pickProjectWorkspace(): Promise<void> {
  const path = await workspaceStore.selectWorkspace()
  if (!path) return
  await loadProjectInstructions()
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

function applyPermissionLevel(level: 'default' | 'autoReview' | 'full' | 'strict'): void {
  if (isGrokSettings.value) {
    if (level === 'full') {
      grokSandbox.value = 'danger-full-access'
      grokApprovalPolicy.value = 'never'
      return
    }
    if (level === 'autoReview') {
      grokSandbox.value = 'workspace-write'
      grokApprovalPolicy.value = 'never'
      return
    }
    if (level === 'strict') {
      grokSandbox.value = 'read-only'
      grokApprovalPolicy.value = 'on-request'
      return
    }
    grokSandbox.value = 'workspace-write'
    grokApprovalPolicy.value = 'on-request'
    return
  }
  if (level === 'full') {
    sandbox.value = 'danger-full-access'
    approvalPolicy.value = 'never'
    return
  }
  if (level === 'autoReview') {
    sandbox.value = 'workspace-write'
    approvalPolicy.value = 'never'
    return
  }
  if (level === 'strict') {
    sandbox.value = 'read-only'
    approvalPolicy.value = 'untrusted'
    return
  }
  sandbox.value = 'workspace-write'
  approvalPolicy.value = 'on-request'
}

function setPermissionToggle(level: 'default' | 'autoReview' | 'full' | 'strict', enabled: boolean): void {
  if (!enabled) {
    // Turning off the active level falls back to default permissions.
    if (permissionLevel.value === level) applyPermissionLevel('default')
    return
  }
  applyPermissionLevel(level)
}

async function onAlwaysOnTopToggle(enabled: boolean): Promise<void> {
  alwaysOnTop.value = enabled
  try {
    await backend.SetAlwaysOnTop(enabled)
    appStore.patchSettings({ alwaysOnTop: enabled })
  } catch (error) {
    alwaysOnTop.value = !enabled
    notify('error', t('notifications.preferencesFailed'), error instanceof Error ? error.message : String(error))
  }
}

function captureShortcut(
  event: KeyboardEvent,
  which: 'palette' | 'newThread' | 'terminal' | 'browser',
): void {
  if (event.key === 'Tab' || event.key === 'Escape') return
  event.preventDefault()
  event.stopPropagation()
  if (['Control', 'Shift', 'Alt', 'Meta'].includes(event.key)) return
  const parts: string[] = []
  if (event.ctrlKey || event.metaKey) parts.push('Ctrl')
  if (event.altKey) parts.push('Alt')
  if (event.shiftKey) parts.push('Shift')
  let key = event.key
  if (event.code === 'Backquote') key = '`'
  else if (key.length === 1) key = key.toUpperCase()
  else if (key === ' ') key = 'Space'
  parts.push(key)
  const binding = parts.join('+')
  if (which === 'palette') shortcutCommandPalette.value = binding
  else if (which === 'newThread') shortcutNewThread.value = binding
  else if (which === 'terminal') shortcutTerminal.value = binding
  else shortcutBrowser.value = binding
}

async function pickBrowserDownloadDir(): Promise<void> {
  try {
    const path = await backend.SelectBrowserDownloadDir()
    if (path) browserDownloadDir.value = path
  } catch (error) {
    notify('error', t('settings.browserDownloadDir'), error instanceof Error ? error.message : String(error))
  }
}

async function openBrowserDownloadDir(): Promise<void> {
  // Persist current draft first so Open uses the path shown in the form.
  try {
    if (browserDownloadDir.value.trim() !== (appStore.settings.browserDownloadDir ?? '')) {
      await appStore.savePreferences({
        ...appStore.settings,
        browserDownloadDir: browserDownloadDir.value.trim(),
      }, { silent: true })
    }
    await backend.OpenBrowserDownloadDir()
  } catch (error) {
    notify('error', t('settings.browserDownloadDir'), error instanceof Error ? error.message : String(error))
  }
}

async function reconnectCodexRuntime(options: { silent?: boolean } = {}): Promise<boolean> {
  const workspace = appStore.settings.workspace || workspaceStore.workspace?.path || ''
  if (!workspace) {
    if (!options.silent) {
      notify('error', t('settings.runtimeReconnect'), t('app.needWorkspaceHintReady'))
    }
    return false
  }
  try {
    // Prefer store connect so thread/queue state is reset with the new app-server.
    const ok = await codexStore.connect(workspace)
    if (ok) {
      if (!options.silent) {
        notify('success', t('settings.runtimeReconnect'), t('settings.runtimeReady'))
      }
      return true
    }
    if (!options.silent) {
      notify('error', t('settings.runtimeReconnect'), codexStore.connection.message || t('notifications.connectionFailed'))
    }
    return false
  } catch (error) {
    if (!options.silent) {
      notify('error', t('settings.runtimeReconnect'), error instanceof Error ? error.message : String(error))
    }
    return false
  }
}

const cliReport = shallowRef<CLIToolsReport | null>(null)
const cliLoading = shallowRef(false)
const cliInstalling = shallowRef<Record<string, boolean>>({})
const cliTools = computed(() => cliReport.value?.tools ?? [])

async function refreshCLITools(options: { silent?: boolean } = {}): Promise<void> {
  cliLoading.value = true
  try {
    cliReport.value = await checkCLITools()
  } catch (error) {
    if (!options.silent) {
      notify('error', t('settings.cliToolsTitle'), error instanceof Error ? error.message : String(error))
    }
  } finally {
    cliLoading.value = false
  }
}

async function installOrUpdateCLITool(tool: CLIToolStatus): Promise<void> {
  if (!tool?.id || cliInstalling.value[tool.id]) return
  cliInstalling.value = { ...cliInstalling.value, [tool.id]: true }
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
      void refreshCLITools({ silent: true })
      void appStore.refreshRuntimes()
    } else {
      notify('error', tool.name, result.message || t('onboarding.cliInstallFailed'))
    }
  } catch (error) {
    notify('error', tool.name, error instanceof Error ? error.message : String(error))
  } finally {
    cliInstalling.value = { ...cliInstalling.value, [tool.id]: false }
  }
}

function cliToolStatusLabel(tool: CLIToolStatus): string {
  if (!tool.installed) return t('onboarding.cliMissing')
  if (tool.updateAvailable) return t('onboarding.cliUpdateAvailable')
  return t('onboarding.cliReady')
}

function codexClientIdentityChanged(): boolean {
  const saved = appStore.settings
  return (
    codexClientName.value.trim() !== (saved.codexClientName ?? '').trim()
    || codexClientTitle.value.trim() !== (saved.codexClientTitle ?? '').trim()
    || codexClientVersion.value.trim() !== (saved.codexClientVersion ?? '').trim()
  )
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

function formatSettingsTokenCount(value: number | null | undefined): string {
  return formatTokenCount(value)
}

async function checkUpdatesNow(): Promise<void> {
  await appStore.openUpdateCheckDialog()
}

function selectNav(item: NavItem): void {
  if (item.action === 'capabilities') {
    void router.push({
      name: 'capabilities',
      query: { from: 'settings', ...(item.capabilityTab ? { tab: item.capabilityTab } : {}) },
    })
    return
  }
  activePanel.value = item.id
}

function closeSettings(): void {
  const from = typeof route.query.from === 'string' ? route.query.from : ''
  void router.replace(from === 'capabilities' ? { name: 'capabilities' } : { name: 'workbench' })
}

async function runCreateBranch(): Promise<void> {
  if (gitActionBusy.value) return
  gitActionBusy.value = true
  try {
    const ok = await workspaceStore.createBranch(gitBranchDraft.value)
    if (ok) gitBranchDraft.value = ''
  } finally {
    gitActionBusy.value = false
  }
}

async function runCommit(): Promise<void> {
  if (gitActionBusy.value) return
  gitActionBusy.value = true
  try {
    const ok = await workspaceStore.commitChanges(gitCommitDraft.value)
    if (ok) gitCommitDraft.value = ''
  } finally {
    gitActionBusy.value = false
  }
}

async function runPush(): Promise<void> {
  if (gitActionBusy.value) return
  gitActionBusy.value = true
  try {
    await workspaceStore.pushBranch()
  } finally {
    gitActionBusy.value = false
  }
}

async function save(): Promise<void> {
  if (saving.value) return

  // Upstream client identity only applies after app-server re-handshake (Codex only).
  const identityChanged = !isGrokSettings.value && !isClaudeSettings.value && codexClientIdentityChanged()
  let reconnectAfterSave = false
  if (identityChanged) {
    reconnectAfterSave = await dialogStore.confirm({
      title: t('settings.codexClientRestartTitle'),
      description: t('settings.codexClientRestartDesc'),
      confirmLabel: t('settings.codexClientRestartConfirm'),
      cancelLabel: t('settings.codexClientRestartLater'),
      destructive: true,
    })
    // confirm → save + restart; cancel → save only (user was already on Save).
  }

  saving.value = true
  try {
    // Persist instruction files first (Codex / Claude / Grok homes).
    if (isGrokSettings.value) {
      const grok = await import('@/utils/grokBindings')
      const globalInfo = await grok.saveGrokGlobalInstructions(customInstructions.value)
      customInstructions.value = globalInfo?.content ?? customInstructions.value
      globalInstructionsPath.value = globalInfo?.path ?? globalInstructionsPath.value
      globalInstructionsSource.value = globalInfo?.source || globalInstructionsSource.value
      globalInstructionsExists.value = Boolean(globalInfo?.exists)
      globalInstructionsEmptyFile.value = Boolean(globalInfo?.emptyFile)
      if (projectInstructionsAvailable.value) {
        const info = await grok.saveGrokProjectInstructions(projectInstructions.value)
        projectInstructions.value = info?.content ?? projectInstructions.value
        projectInstructionsPath.value = info?.path ?? projectInstructionsPath.value
        projectInstructionsSource.value = info?.source || projectInstructionsSource.value
        projectInstructionsExists.value = Boolean(info?.exists)
        projectInstructionsEmptyFile.value = Boolean(info?.emptyFile)
        projectInstructionsWorkspace.value = info?.workspace ?? projectInstructionsWorkspace.value
        projectInstructionsWorkspaceName.value = info?.workspaceName ?? projectInstructionsWorkspaceName.value
      }
    } else if (isClaudeSettings.value) {
      const globalInfo = await saveClaudeGlobalInstructions(customInstructions.value) as any
      customInstructions.value = globalInfo?.content ?? customInstructions.value
      globalInstructionsPath.value = globalInfo?.path ?? globalInstructionsPath.value
      globalInstructionsSource.value = globalInfo?.source || globalInstructionsSource.value
      globalInstructionsExists.value = Boolean(globalInfo?.exists)
      globalInstructionsEmptyFile.value = Boolean(globalInfo?.emptyFile)
      if (projectInstructionsAvailable.value) {
        const info = await saveClaudeProjectInstructions(projectInstructions.value) as any
        projectInstructions.value = info?.content ?? projectInstructions.value
        projectInstructionsPath.value = info?.path ?? projectInstructionsPath.value
        projectInstructionsSource.value = info?.source || projectInstructionsSource.value
        projectInstructionsExists.value = Boolean(info?.exists)
        projectInstructionsEmptyFile.value = Boolean(info?.emptyFile)
        projectInstructionsWorkspace.value = info?.workspace ?? projectInstructionsWorkspace.value
        projectInstructionsWorkspaceName.value = info?.workspaceName ?? projectInstructionsWorkspaceName.value
      }
    } else {
      const globalInfo = await backend.SaveGlobalInstructions(customInstructions.value)
      customInstructions.value = globalInfo?.content ?? customInstructions.value
      globalInstructionsPath.value = globalInfo?.path ?? globalInstructionsPath.value
      globalInstructionsSource.value = globalInfo?.source || globalInstructionsSource.value
      globalInstructionsExists.value = Boolean(globalInfo?.exists)
      globalInstructionsEmptyFile.value = Boolean(globalInfo?.emptyFile)
      if (projectInstructionsAvailable.value) {
        const info = await backend.SaveProjectInstructions(projectInstructions.value)
        projectInstructions.value = info?.content ?? projectInstructions.value
        projectInstructionsPath.value = info?.path ?? projectInstructionsPath.value
        projectInstructionsSource.value = info?.source || projectInstructionsSource.value
        projectInstructionsExists.value = Boolean(info?.exists)
        projectInstructionsEmptyFile.value = Boolean(info?.emptyFile)
        projectInstructionsWorkspace.value = info?.workspace ?? projectInstructionsWorkspace.value
        projectInstructionsWorkspaceName.value = info?.workspaceName ?? projectInstructionsWorkspaceName.value
      }
    }
    await appStore.savePreferences({
      ...appStore.settings,
      activeRuntime: appStore.settings.activeRuntime === 'grok'
        ? 'grok'
        : appStore.settings.activeRuntime === 'claude'
          ? 'claude'
          : 'codex',
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
      grokBackend: grokBackend.value === 'api' ? 'api' : 'build',
      grokBuildModel: grokBuildModel.value.trim(),
      grokAPIModel: grokAPIModel.value.trim() || 'grok-4.5',
      grokAPIKey: grokAPIKey.value.trim(),
      grokAPIBaseURL: grokAPIBaseURL.value.trim(),
      grokEffort: grokEffort.value || 'high',
      grokSandbox: grokSandbox.value || 'workspace-write',
      grokApprovalPolicy: grokApprovalPolicy.value || 'on-request',
      grokWebSearch: grokWebSearch.value,
      grokXSearch: grokXSearch.value,
      claudeModel: claudeModel.value.trim() || 'sonnet',
      claudeEffort: claudeEffort.value || 'high',
      claudeSandbox: claudeSandbox.value || 'workspace-write',
      claudeApprovalPolicy: claudeApprovalPolicy.value || 'on-request',
      claudePermissionMode: claudePermissionMode.value || 'acceptEdits',
      theme: theme.value,
      accentColor: accentColor.value,
      fontFamily: fontFamily.value,
      translucentSidebar: translucentSidebar.value,
      highContrast: highContrast.value,
      pointerCursor: pointerCursor.value,
      reduceMotion: reduceMotion.value,
      uiFontSize: uiFontSize.value,
      codeFontSize: codeFontSize.value,
      terminalProfile: terminalProfile.value,
      language: language.value,
      autoConnect: autoConnect.value,
      sendWithModifier: sendWithModifier.value,
      followUpBehavior: followUpBehavior.value,
      notifyOnTurnComplete: notifyOnTurnComplete.value,
      preventSleepWhileRunning: preventSleepWhileRunning.value,
      alwaysOnTop: alwaysOnTop.value,
      gitBranchPrefix: gitBranchPrefix.value,
      gitCommitPrefix: gitCommitPrefix.value,
      gitOpenPRAfterPush: gitOpenPRAfterPush.value,
      gitPRBodyTemplate: gitPRBodyTemplate.value,
      browserAllowedHosts: parseHostList(browserAllowedHostsText.value),
      browserBlockedHosts: parseHostList(browserBlockedHostsText.value),
      browserDownloadDir: browserDownloadDir.value,
      browserFullCDP: browserFullCDP.value,
      shortcutCommandPalette: shortcutCommandPalette.value,
      shortcutNewThread: shortcutNewThread.value,
      shortcutTerminal: shortcutTerminal.value,
      shortcutBrowser: shortcutBrowser.value,
      codexClientName: codexClientName.value.trim(),
      codexClientTitle: codexClientTitle.value.trim(),
      codexClientVersion: codexClientVersion.value.trim(),
      customInstructions: customInstructions.value,
      onboardingCompleted: true,
    })
    if (!isGrokSettings.value) {
      await backend.SaveCodexFeatureFlags({
        memoriesEnabled: memoriesEnabled.value,
        memoriesGenerate: memoriesGenerate.value,
        memoriesUse: memoriesUse.value,
        memoriesDisableExternalContext: memoriesDisableExternal.value,
        browserUseFullCDP: browserFullCDP.value,
        inAppBrowser: true,
      })
    }
    saved.value = true
    if (!isGrokSettings.value) await codexStore.loadModels()

    if (!isGrokSettings.value && identityChanged && reconnectAfterSave) {
      const ok = await reconnectCodexRuntime({ silent: true })
      if (ok) {
        notify('success', t('settings.codexClientRestartDone'), t('settings.codexClientRestartDoneHint'))
      } else {
        notify('warning', t('settings.codexClientRestartFailed'), t('settings.codexClientRestartSavedOnlyHint'))
      }
    } else if (identityChanged) {
      notify('info', t('settings.codexClientRestartSavedOnly'), t('settings.codexClientRestartSavedOnlyHint'))
    }
    // Stay on settings after save; user can leave via back/close.
  } catch {
    saved.value = false
  } finally {
    saving.value = false
  }
}

async function onNotifyToggle(enabled: boolean): Promise<void> {
  notifyOnTurnComplete.value = enabled
  if (!enabled || typeof Notification === 'undefined') return
  if (Notification.permission === 'default') {
    try {
      await Notification.requestPermission()
    } catch {
      // Permission prompt may be unavailable in embedded webviews.
    }
  }
}
</script>

<template>
  <div class="flex h-full w-full overflow-hidden bg-transparent text-foreground">
    <!-- Left menu sits on the gray shell, matching the main workbench sidebar. -->
    <aside class="flex w-[248px] shrink-0 flex-col">
      <div class="space-y-2 px-3 pb-2 pt-1">
        <Button variant="ghost" class="h-8 w-full justify-start px-2 text-xs text-muted-foreground" @click="closeSettings">
          <ArrowLeft :size="14" class="mr-2" />
          {{ t('settings.backToApp') }}
        </Button>
        <div class="relative">
          <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            v-model="settingsSearch"
            type="search"
            :placeholder="t('settings.searchPlaceholder')"
            class="h-8 rounded-lg border-transparent bg-foreground/[0.06] pl-8 text-xs shadow-none focus-visible:border-transparent focus-visible:bg-card"
          />
        </div>
      </div>

      <nav class="min-h-0 flex-1 space-y-4 overflow-y-auto px-2 pb-3">
        <div v-for="group in filteredNavGroups" :key="group.id" class="space-y-1">
          <p class="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground/80">
            {{ group.label }}
          </p>
          <button
            v-for="item in group.items"
            :key="item.id"
            type="button"
            class="flex h-8 w-full items-center gap-2 rounded-lg px-2 text-left text-[12.5px] transition-colors"
            :class="activePanel === item.id && item.action !== 'capabilities'
              ? 'bg-card font-medium text-foreground shadow-sm'
              : 'text-muted-foreground hover:bg-foreground/[0.05] hover:text-foreground'"
            @click="selectNav(item)"
          >
            <component :is="item.icon" :size="14" class="shrink-0 opacity-70" />
            <span class="truncate">{{ item.label }}</span>
          </button>
        </div>
        <p v-if="!filteredNavGroups.length" class="px-2 text-[11px] text-muted-foreground">
          {{ t('settings.searchEmpty') }}
        </p>
      </nav>

      <div class="px-4 py-2 text-[10px] text-muted-foreground">
        {{ isGrokSettings ? 'Grok' : `Codex ${appStore.codexVersion || 'app-server'}` }} · v{{ appStore.appVersion }}
      </div>
    </aside>

    <!-- Rounded content card -->
    <div class="flex min-h-0 min-w-0 flex-1 flex-col pb-2 pr-2 pl-1.5 pt-0">
      <section class="workbench-card relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-[14px] border bg-card">
        <header class="flex h-12 shrink-0 items-center gap-3 border-b px-5">
          <div class="min-w-0 flex-1">
            <h1 class="text-[15px] font-semibold tracking-tight">{{ activeNavItem?.label || t('settings.title') }}</h1>
          </div>
          <Button v-if="activePanel !== 'archived'" form="settings-form" type="submit" size="sm" :disabled="saving">
            {{ saving ? t('common.saving') : t('settings.save') }}
          </Button>
        </header>

        <main class="min-h-0 flex-1 overflow-y-auto px-5 py-5">
          <form id="settings-form" class="mx-auto max-w-3xl space-y-5" @submit.prevent="save">
            <!-- General -->
            <template v-if="activePanel === 'general'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.permissions') }}</h2>
                </div>
                <div class="divide-y">
                  <div class="flex items-start justify-between gap-4 px-4 py-3.5">
                    <div class="min-w-0 space-y-0.5">
                      <p class="text-[13px] font-medium">{{ t('settings.permDefault') }}</p>
                      <p class="text-[11px] leading-5 text-muted-foreground">{{ t('settings.permDefaultHint') }}</p>
                    </div>
                    <Switch
                      :checked="permissionLevel === 'default'"
                      :aria-label="t('settings.permDefault')"
                      @update:checked="setPermissionToggle('default', $event)"
                    />
                  </div>
                  <div class="flex items-start justify-between gap-4 px-4 py-3.5">
                    <div class="min-w-0 space-y-0.5">
                      <p class="text-[13px] font-medium">{{ t('settings.permAutoReview') }}</p>
                      <p class="text-[11px] leading-5 text-muted-foreground">{{ t('settings.permAutoReviewHint') }}</p>
                    </div>
                    <Switch
                      :checked="permissionLevel === 'autoReview'"
                      :aria-label="t('settings.permAutoReview')"
                      @update:checked="setPermissionToggle('autoReview', $event)"
                    />
                  </div>
                  <div class="flex items-start justify-between gap-4 px-4 py-3.5">
                    <div class="min-w-0 space-y-0.5">
                      <p class="text-[13px] font-medium">{{ t('settings.permFull') }}</p>
                      <p class="text-[11px] leading-5 text-muted-foreground">{{ t('settings.permFullHint') }}</p>
                    </div>
                    <Switch
                      :checked="permissionLevel === 'full'"
                      :aria-label="t('settings.permFull')"
                      @update:checked="setPermissionToggle('full', $event)"
                    />
                  </div>
                  <div class="flex items-start justify-between gap-4 px-4 py-3.5">
                    <div class="min-w-0 space-y-0.5">
                      <p class="text-[13px] font-medium">{{ t('settings.permStrict') }}</p>
                      <p class="text-[11px] leading-5 text-muted-foreground">{{ t('settings.permStrictHint') }}</p>
                    </div>
                    <Switch
                      :checked="permissionLevel === 'strict'"
                      :aria-label="t('settings.permStrict')"
                      @update:checked="setPermissionToggle('strict', $event)"
                    />
                  </div>
                </div>
                <p v-if="permissionLevel === 'full'" class="border-t bg-destructive/5 px-4 py-2.5 text-[11px] text-destructive">
                  {{ t('settings.fullAccessWarning') }}
                </p>
              </section>

              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.navGeneral') }}</h2>
                </div>
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.terminalProfile') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ selectedTerminalHint }}</p>
                    </div>
                    <Select v-model="terminalProfile">
                      <SelectTrigger class="h-8 w-[180px] text-xs" :aria-label="t('settings.terminalProfile')">
                        <SelectValue>{{ selectedOptionLabel(terminalOptions, terminalProfile) }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="option in terminalOptions" :key="option.value" :value="option.value" :disabled="option.disabled">
                          {{ option.label }}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.language') }}</p>
                    </div>
                    <Select v-model="language">
                      <SelectTrigger class="h-8 w-[180px] text-xs" :aria-label="t('settings.language')">
                        <SelectValue>{{ selectedOptionLabel(languageOptions, language) }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="option in languageOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.reconnect') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.reconnectHint') }}</p>
                    </div>
                    <Switch :checked="autoConnect" :aria-label="t('settings.reconnect')" @update:checked="autoConnect = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.sendWithModifier', { key: sendModifierLabel }) }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.sendWithModifierHint', { key: sendModifierLabel }) }}</p>
                    </div>
                    <Switch
                      :checked="sendWithModifier"
                      :aria-label="t('settings.sendWithModifier', { key: sendModifierLabel })"
                      @update:checked="sendWithModifier = $event"
                    />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.followUpBehavior') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.followUpBehaviorHint') }}</p>
                    </div>
                    <Select v-model="followUpBehavior">
                      <SelectTrigger class="h-8 w-[180px] text-xs" :aria-label="t('settings.followUpBehavior')">
                        <SelectValue>{{ selectedOptionLabel(followUpOptions, followUpBehavior) }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="option in followUpOptions" :key="option.value" :value="option.value">
                          {{ option.label }}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.notifyOnTurnComplete') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.notifyOnTurnCompleteHint') }}</p>
                    </div>
                    <Switch
                      :checked="notifyOnTurnComplete"
                      :aria-label="t('settings.notifyOnTurnComplete')"
                      @update:checked="onNotifyToggle"
                    />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.preventSleep') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.preventSleepHint') }}</p>
                    </div>
                    <Switch :checked="preventSleepWhileRunning" :aria-label="t('settings.preventSleep')" @update:checked="preventSleepWhileRunning = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.alwaysOnTop') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.alwaysOnTopHint') }}</p>
                    </div>
                    <Switch :checked="alwaysOnTop" :aria-label="t('settings.alwaysOnTop')" @update:checked="onAlwaysOnTopToggle" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('updates.about') }}</p>
                      <p class="text-[11px] text-muted-foreground">
                        {{ t('updates.currentVersion') }} v{{ appStore.appVersion }}
                        <template v-if="appStore.updateInfo?.latestVersion">
                          · {{ t('updates.latestVersion') }} v{{ appStore.updateInfo.latestVersion }}
                        </template>
                      </p>
                    </div>
                    <div class="flex shrink-0 gap-2">
                      <Button type="button" variant="outline" size="sm" class="h-8 text-xs" :disabled="appStore.updateChecking" @click="checkUpdatesNow">
                        <RefreshCw :size="12" class="mr-1.5" :class="appStore.updateChecking ? 'animate-spin' : ''" />
                        {{ appStore.updateChecking ? t('updates.checking') : t('updates.checkNow') }}
                      </Button>
                      <Button
                        v-if="appStore.updateInfo?.updateAvailable"
                        type="button"
                        size="sm"
                        class="h-8 text-xs"
                        @click="appStore.openUpdateCheckDialog"
                      >
                        <Download :size="12" class="mr-1.5" />
                        {{ t('updates.download') }}
                      </Button>
                    </div>
                  </div>
                </div>
              </section>

              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.advancedPermissions') }}</h2>
                </div>
                <div class="grid gap-3 p-4 sm:grid-cols-2">
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.sandbox') }}</Label>
                    <Select v-model="sandbox">
                      <SelectTrigger class="h-8 text-xs" :aria-label="t('settings.sandbox')">
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
                      <SelectTrigger class="h-8 text-xs" :aria-label="t('settings.approvals')">
                        <SelectValue>{{ selectedOptionLabel(approvalOptions, approvalPolicy) }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="option in approvalOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                </div>
              </section>
            </template>

            <!-- Shortcuts -->
            <template v-else-if="activePanel === 'shortcuts'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.navShortcuts') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.shortcutsHint') }}</p>
                </div>
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.shortcutPalette') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.shortcutPaletteHint') }}</p>
                    </div>
                    <Input
                      :model-value="shortcutCommandPalette"
                      readonly
                      class="h-8 w-[160px] cursor-pointer text-xs"
                      maxlength="48"
                      @keydown="captureShortcut($event, 'palette')"
                    />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.shortcutNewThread') }}</p>
                    </div>
                    <Input
                      :model-value="shortcutNewThread"
                      readonly
                      class="h-8 w-[160px] cursor-pointer text-xs"
                      maxlength="48"
                      @keydown="captureShortcut($event, 'newThread')"
                    />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.shortcutTerminal') }}</p>
                    </div>
                    <Input
                      :model-value="shortcutTerminal"
                      readonly
                      class="h-8 w-[160px] cursor-pointer text-xs"
                      maxlength="48"
                      @keydown="captureShortcut($event, 'terminal')"
                    />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.shortcutBrowser') }}</p>
                    </div>
                    <Input
                      :model-value="shortcutBrowser"
                      readonly
                      class="h-8 w-[160px] cursor-pointer text-xs"
                      maxlength="48"
                      @keydown="captureShortcut($event, 'browser')"
                    />
                  </div>
                  <div class="px-4 py-3 text-[11px] text-muted-foreground">
                    {{ t('settings.shortcutsCaptureHint') }}
                  </div>
                </div>
              </section>
            </template>

            <!-- Appearance -->
            <template v-else-if="activePanel === 'appearance'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.theme') }}</p>
                    <div class="flex rounded-md border p-0.5">
                      <Button
                        v-for="option in ['light', 'dark', 'system'] as const"
                        :key="option"
                        type="button"
                        variant="ghost"
                        size="sm"
                        class="h-7 px-2.5 text-xs"
                        :class="theme === option ? 'bg-muted' : ''"
                        @click="theme = option"
                      >
                        {{ t(`settings.${option}`) }}
                      </Button>
                    </div>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.fontFamily') }}</p>
                    <SearchableSelect
                      v-model="fontFamily"
                      class="w-[220px]"
                      content-class="min-w-[280px]"
                      :options="fontOptions"
                      :aria-label="t('settings.fontFamily')"
                      :search-placeholder="t('settings.fontSearch')"
                      preview-font
                    />
                  </div>
                  <div class="space-y-2 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.accentColor') }}</p>
                    <div class="grid grid-cols-2 gap-2 sm:grid-cols-5">
                      <Button
                        v-for="option in accentOptions"
                        :key="option.value"
                        type="button"
                        variant="outline"
                        size="sm"
                        class="h-8 justify-start gap-2 px-2 text-[11px]"
                        :class="accentColor === option.value ? 'border-primary bg-primary/5' : ''"
                        @click="accentColor = option.value"
                      >
                        <span class="size-3 shrink-0 rounded-full border" :style="{ backgroundColor: option.color }" />
                        {{ option.label }}
                      </Button>
                    </div>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.uiFontSize') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.uiFontSizeHint') }}</p>
                    </div>
                    <div class="flex rounded-md border p-0.5">
                      <Button
                        v-for="option in (['sm', 'md', 'lg'] as const)"
                        :key="option"
                        type="button"
                        variant="ghost"
                        size="sm"
                        class="h-7 px-2.5 text-xs"
                        :class="uiFontSize === option ? 'bg-muted' : ''"
                        @click="uiFontSize = option"
                      >
                        {{ t(`settings.fontSize.${option}`) }}
                      </Button>
                    </div>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.codeFontSize') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.codeFontSizeHint') }}</p>
                    </div>
                    <div class="flex rounded-md border p-0.5">
                      <Button
                        v-for="option in (['sm', 'md', 'lg'] as const)"
                        :key="option"
                        type="button"
                        variant="ghost"
                        size="sm"
                        class="h-7 px-2.5 text-xs"
                        :class="codeFontSize === option ? 'bg-muted' : ''"
                        @click="codeFontSize = option"
                      >
                        {{ t(`settings.fontSize.${option}`) }}
                      </Button>
                    </div>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.translucentSidebar') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.translucentSidebarHint') }}</p>
                    </div>
                    <Switch :checked="translucentSidebar" :aria-label="t('settings.translucentSidebar')" @update:checked="translucentSidebar = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.highContrast') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.highContrastHint') }}</p>
                    </div>
                    <Switch :checked="highContrast" :aria-label="t('settings.highContrast')" @update:checked="highContrast = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.pointerCursor') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.pointerCursorHint') }}</p>
                    </div>
                    <Switch :checked="pointerCursor" :aria-label="t('settings.pointerCursor')" @update:checked="pointerCursor = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.reduceMotion') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.reduceMotionHint') }}</p>
                    </div>
                    <Switch :checked="reduceMotion" :aria-label="t('settings.reduceMotion')" @update:checked="reduceMotion = $event" />
                  </div>
                </div>
              </section>
            </template>

            <!-- Agent / config (Codex / Claude / Grok) -->
            <template v-else-if="activePanel === 'agent'">
              <section v-if="isClaudeSettings" class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-center gap-3 border-b px-4 py-3">
                  <div class="grid size-8 place-items-center rounded-md border bg-muted/40">
                    <ClaudeIcon :size="16" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-[13px] font-medium">Claude Code</p>
                    <p class="truncate text-[11px] text-muted-foreground">
                      <span v-if="claudeStatus.version" class="mr-1 font-mono">{{ claudeStatus.version }}</span>
                      {{ t('settings.providerClaudeHint') }}
                    </p>
                    <p v-if="claudeStatus.executable" class="mt-0.5 truncate font-mono text-[10px] text-muted-foreground/80" :title="claudeStatus.executable">
                      {{ claudeStatus.executable }}
                    </p>
                  </div>
                  <Badge :variant="claudeStatus.runtimeReady ? 'default' : 'outline'" class="text-[9px]">
                    {{ claudeStatus.runtimeReady ? t('settings.runtimeReady') : t('settings.runtimeMissing') }}
                  </Badge>
                </div>
                <p class="border-b bg-muted/20 px-4 py-2 text-[11px] leading-4 text-muted-foreground">
                  {{ claudeStatus.message }}
                </p>
                <div class="space-y-4 p-4">
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.model') }}</Label>
                    <Input v-model="claudeModel" class="h-9 font-mono text-xs" placeholder="sonnet" maxlength="160" />
                    <p class="text-[10px] text-muted-foreground">{{ t('settings.claudeModelHint') }}</p>
                  </div>
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.reasoning') }}</Label>
                    <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
                      <Button
                        v-for="option in ['low', 'medium', 'high', 'xhigh', 'max']"
                        :key="option"
                        type="button"
                        variant="outline"
                        size="sm"
                        class="h-8 capitalize"
                        :class="claudeEffort === option ? 'border-primary bg-primary/5' : ''"
                        @click="claudeEffort = option"
                      >
                        {{ option }}
                      </Button>
                    </div>
                  </div>
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.claudePermissionMode') }}</Label>
                    <Select v-model="claudePermissionMode">
                      <SelectTrigger class="h-8 font-mono text-xs">
                        <SelectValue>{{ claudePermissionMode }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem
                          v-for="option in claudePermissionOptions"
                          :key="option.value"
                          :value="option.value"
                          class="font-mono text-xs"
                        >
                          {{ option.label }}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <p class="text-[10px] text-muted-foreground">{{ t('settings.claudePermissionModeHint') }}</p>
                  </div>
                  <div class="grid gap-3 sm:grid-cols-2">
                    <div class="space-y-1">
                      <Label class="text-xs">{{ t('settings.sandbox') }} <span class="text-muted-foreground">(legacy)</span></Label>
                      <Select v-model="claudeSandbox">
                        <SelectTrigger class="h-8 text-xs">
                          <SelectValue>{{ selectedOptionLabel(sandboxOptions, claudeSandbox) }}</SelectValue>
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem v-for="option in sandboxOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div class="space-y-1">
                      <Label class="text-xs">{{ t('settings.approvals') }} <span class="text-muted-foreground">(legacy)</span></Label>
                      <Select v-model="claudeApprovalPolicy">
                        <SelectTrigger class="h-8 text-xs">
                          <SelectValue>{{ selectedOptionLabel(approvalOptions, claudeApprovalPolicy) }}</SelectValue>
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem v-for="option in approvalOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                  <p class="text-[10px] leading-4 text-muted-foreground">{{ t('settings.claudeLegacyPermissionHint') }}</p>
                </div>
              </section>

              <section v-else-if="isGrokSettings" class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-center gap-3 border-b px-4 py-3">
                  <div class="grid size-8 place-items-center rounded-md border bg-muted/40">
                    <GrokIcon :size="16" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-[13px] font-medium">Grok</p>
                    <p class="truncate text-[11px] text-muted-foreground">
                      <span v-if="grokStatus.version" class="mr-1 font-mono">{{ grokStatus.version }}</span>
                      {{ t('settings.providerGrokHint') }}
                    </p>
                    <p v-if="grokStatus.executable" class="mt-0.5 truncate font-mono text-[10px] text-muted-foreground/80" :title="grokStatus.executable">
                      {{ grokStatus.executable }}
                    </p>
                  </div>
                  <Badge :variant="grokStatus.runtimeReady ? 'default' : 'outline'" class="text-[9px]">
                    {{ grokStatus.runtimeReady ? t('settings.runtimeReady') : t('settings.runtimeMissing') }}
                  </Badge>
                </div>
                <p class="border-b bg-muted/20 px-4 py-2 text-[11px] leading-4 text-muted-foreground">
                  {{ grokStatus.message || t('settings.grokConfigHint') }}
                </p>
                <div class="space-y-4 p-4">
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.grokBackend') }}</Label>
                    <div class="grid grid-cols-2 gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        class="h-9"
                        :class="grokBackend === 'build' ? 'border-primary bg-primary/5' : ''"
                        @click="grokBackend = 'build'"
                      >
                        Grok Build
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        class="h-9"
                        :class="grokBackend === 'api' ? 'border-primary bg-primary/5' : ''"
                        @click="grokBackend = 'api'"
                      >
                        Grok API
                      </Button>
                    </div>
                  </div>
                  <div v-if="grokBackend === 'api'" class="space-y-3 rounded-lg border bg-muted/20 p-3">
                    <p class="text-[11px] leading-4 text-muted-foreground">{{ t('settings.grokAPIConfigHint') }}</p>
                    <div class="space-y-1">
                      <Label class="text-xs">{{ t('settings.grokAPIBaseURL') }}</Label>
                      <Input
                        v-model="grokAPIBaseURL"
                        class="h-9 font-mono text-xs"
                        :placeholder="t('settings.grokAPIBaseURLPlaceholder')"
                        maxlength="512"
                      />
                    </div>
                    <div class="space-y-1">
                      <Label class="text-xs">{{ t('settings.grokAPIKey') }}</Label>
                      <Input
                        v-model="grokAPIKey"
                        type="password"
                        class="h-9 font-mono text-xs"
                        :placeholder="t('settings.grokAPIKeyPlaceholder')"
                        maxlength="512"
                        autocomplete="off"
                      />
                      <p class="text-[10px] text-muted-foreground">{{ t('settings.grokAPIKeyHint') }}</p>
                    </div>
                  </div>
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.model') }}</Label>
                    <SearchableSelect
                      v-model="grokModelSelection"
                      class="h-9"
                      content-class="min-w-[320px]"
                      align="start"
                      :options="grokModelOptions"
                      :aria-label="t('settings.model')"
                      :search-placeholder="t('settings.modelSearch')"
                    />
                  </div>
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.reasoning') }}</Label>
                    <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
                      <Button
                        v-for="option in grokEffortOptions"
                        :key="option.effort"
                        type="button"
                        variant="outline"
                        size="sm"
                        class="h-auto min-w-0 w-full shrink flex-col items-stretch justify-start gap-1 whitespace-normal px-3 py-2 text-left text-xs"
                        :class="grokEffort === option.effort ? 'border-primary bg-primary/5' : ''"
                        @click="grokEffort = option.effort"
                      >
                        <span class="flex w-full min-w-0 items-center justify-between gap-1">
                          <strong class="min-w-0 truncate capitalize">{{ 'displayName' in option ? option.displayName : option.effort }}</strong>
                          <Check v-if="grokEffort === option.effort" :size="13" class="shrink-0 text-primary" />
                        </span>
                        <small class="w-full whitespace-normal break-words line-clamp-2 text-[10px] font-normal leading-snug text-muted-foreground">
                          {{ option.description }}
                        </small>
                      </Button>
                    </div>
                  </div>
                  <div class="grid gap-3 sm:grid-cols-2">
                    <div class="space-y-1">
                      <Label class="text-xs">{{ t('settings.sandbox') }}</Label>
                      <Select v-model="grokSandbox">
                        <SelectTrigger class="h-9 text-xs"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem v-for="option in sandboxOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div class="space-y-1">
                      <Label class="text-xs">{{ t('settings.approval') }}</Label>
                      <Select v-model="grokApprovalPolicy">
                        <SelectTrigger class="h-9 text-xs"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="on-request">{{ t('settings.onRequest') }}</SelectItem>
                          <SelectItem value="never">{{ t('settings.never') }}</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                  <div class="flex items-center justify-between rounded-lg border px-3 py-2.5">
                    <div class="space-y-0.5">
                      <Label class="text-xs">{{ t('settings.grokWebSearch') }}</Label>
                      <p class="text-[10px] text-muted-foreground">{{ t('settings.grokWebSearchHint') }}</p>
                    </div>
                    <Switch :checked="grokWebSearch" @update:checked="grokWebSearch = $event" />
                  </div>
                  <div class="flex items-center justify-between rounded-lg border px-3 py-2.5">
                    <div class="space-y-0.5">
                      <Label class="text-xs">{{ t('settings.grokXSearch') }}</Label>
                      <p class="text-[10px] text-muted-foreground">{{ t('settings.grokXSearchHint') }}</p>
                    </div>
                    <Switch :checked="grokXSearch" @update:checked="grokXSearch = $event" />
                  </div>
                </div>
              </section>

              <section v-else class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-center gap-3 border-b px-4 py-3">
                  <div class="grid size-8 place-items-center rounded-md border bg-muted/40">
                    <OpenAIIcon :size="16" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-[13px] font-medium">{{ codexStatus?.name || 'Codex' }}</p>
                    <p class="truncate text-[11px] text-muted-foreground">
                      <span v-if="codexStatus?.version" class="mr-1 font-mono">{{ codexStatus.version }}</span>
                      {{ t('settings.providerCodexHint') }}
                    </p>
                  </div>
                  <Badge :variant="codexStatus?.runtimeReady ? 'default' : 'outline'" class="text-[9px]">
                    {{ codexStatus?.runtimeReady ? t('settings.runtimeReady') : t('settings.runtimeMissing') }}
                  </Badge>
                </div>
                <p
                  v-if="codexStatus?.runtimeReady"
                  class="border-b bg-muted/20 px-4 py-2 text-[11px] leading-4 text-muted-foreground"
                >
                  {{ t('settings.runtimeReadyHint') }}
                </p>
                <div class="space-y-4 p-4">
                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.model') }}</Label>
                    <SearchableSelect
                      v-model="modelSelection"
                      class="h-9"
                      content-class="min-w-[320px]"
                      align="start"
                      :options="modelOptions"
                      :aria-label="t('settings.model')"
                      :search-placeholder="t('settings.modelSearch')"
                      @update:model-value="onModelChange"
                    />
                  </div>

                  <div class="space-y-2">
                    <Label class="text-xs">{{ t('settings.customModel') }}</Label>
                    <div class="flex gap-2">
                      <Input v-model="customModelDraft" :placeholder="t('settings.customModelPlaceholder')" class="h-9 text-xs" maxlength="160" @keydown.enter.prevent="addCustomModel" />
                      <Button type="button" variant="outline" size="sm" class="h-9 shrink-0" :disabled="!customModelDraft.trim()" @click="addCustomModel">
                        <Plus :size="14" class="mr-1.5" />{{ t('common.add') }}
                      </Button>
                    </div>
                    <div v-if="customModels.length" class="divide-y rounded-md border">
                      <div v-for="customModel in customModels" :key="customModel" class="flex items-center gap-2 px-3 py-2">
                        <code class="min-w-0 flex-1 truncate text-[11px]">{{ customModel }}</code>
                        <Button type="button" variant="ghost" size="icon-xs" :aria-label="t('common.delete')" @click="removeCustomModel(customModel)">
                          <Trash2 :size="12" />
                        </Button>
                      </div>
                    </div>
                  </div>

                  <div class="space-y-1">
                    <Label class="text-xs">{{ t('settings.reasoning') }}</Label>
                    <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
                      <Button
                        v-for="option in effortOptions"
                        :key="option.effort"
                        type="button"
                        variant="outline"
                        size="sm"
                        class="h-auto min-w-0 w-full shrink flex-col items-stretch justify-start gap-1 whitespace-normal px-3 py-2 text-left text-xs"
                        :class="effort === option.effort ? 'border-primary bg-primary/5' : ''"
                        @click="effort = option.effort"
                      >
                        <span class="flex w-full min-w-0 items-center justify-between gap-1">
                          <strong class="min-w-0 truncate capitalize">{{ 'displayName' in option ? option.displayName : option.effort }}</strong>
                          <Check v-if="effort === option.effort" :size="13" class="shrink-0 text-primary" />
                        </span>
                        <small class="w-full whitespace-normal break-words line-clamp-2 text-[10px] font-normal leading-snug text-muted-foreground">
                          {{ option.description }}
                        </small>
                      </Button>
                    </div>
                  </div>

                  <div class="flex items-center justify-between rounded-lg border px-3 py-2.5">
                    <div class="space-y-0.5">
                      <Label class="flex items-center gap-2 text-xs">
                        <Zap :size="13" />
                        {{ t('settings.fastMode') }}
                      </Label>
                      <p class="text-[10px] text-muted-foreground">{{ fastTier?.description || t('settings.fastModeUnavailable') }}</p>
                    </div>
                    <Switch :checked="fastEnabled" :disabled="!fastTier" :aria-label="t('settings.fastMode')" @update:checked="toggleFast" />
                  </div>
                </div>
              </section>
            </template>

            <!-- Personalization -->
            <template v-else-if="activePanel === 'personalization'">
              <section v-if="isGrokSettings" class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.grokInstructionsTitle') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.grokInstructionsHint') }}</p>
                </div>
              </section>
              <section v-if="!isGrokSettings" class="overflow-hidden rounded-xl border bg-card">
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.collaborationMode') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ selectedOptionLabel(collaborationOptions, collaborationMode) }}</p>
                    </div>
                    <Select v-model="collaborationMode">
                      <SelectTrigger class="h-8 w-[180px] text-xs">
                        <SelectValue>{{ selectedOptionLabel(collaborationOptions, collaborationMode) }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="option in collaborationOptions" :key="option.value" :value="option.value">
                          {{ option.label }}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.personality') }}</p>
                    <Select v-model="personality">
                      <SelectTrigger class="h-8 w-[180px] text-xs">
                        <SelectValue>{{ selectedOptionLabel(personalityOptions, personality) }}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem v-for="option in personalityOptions" :key="option.value" :value="option.value">{{ option.label }}</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                </div>
              </section>
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-start justify-between gap-3 border-b px-4 py-3">
                  <div class="min-w-0">
                    <h2 class="text-[13px] font-semibold">
                      {{ isGrokSettings
                        ? t('settings.grokGlobalInstructions')
                        : isClaudeSettings
                          ? t('settings.claudeGlobalInstructions')
                          : t('settings.customInstructions') }}
                    </h2>
                    <p class="mt-0.5 text-[11px] text-muted-foreground">
                      {{ isGrokSettings
                        ? t('settings.grokGlobalInstructionsHint')
                        : isClaudeSettings
                          ? t('settings.claudeGlobalInstructionsHint')
                          : t('settings.customInstructionsHint') }}
                    </p>
                  </div>
                  <Button type="button" variant="ghost" size="sm" class="h-7 shrink-0 px-2 text-[11px]" :disabled="instructionsLoading" @click="loadGlobalInstructions">
                    <RefreshCw :size="12" class="mr-1" :class="instructionsLoading ? 'animate-spin' : ''" />
                    {{ t('settings.instructionsReload') }}
                  </Button>
                </div>
                <div class="space-y-2 p-4">
                  <div class="flex flex-wrap items-center gap-2 text-[10px] text-muted-foreground">
                    <Badge variant="outline" class="text-[10px]">{{ globalInstructionsSource }}</Badge>
                    <Badge variant="outline" class="text-[10px]">
                      {{ instructionsStatusLabel(globalInstructionsExists, globalInstructionsEmptyFile) }}
                    </Badge>
                    <span class="tabular-nums">{{ customInstructionsLength }} / 16000</span>
                    <span v-if="globalInstructionsPath" class="min-w-0 truncate" :title="globalInstructionsPath">{{ globalInstructionsPath }}</span>
                  </div>
                  <Textarea
                    v-model="customInstructions"
                    :placeholder="isGrokSettings ? t('settings.grokGlobalInstructionsPlaceholder') : t('settings.customInstructionsPlaceholder')"
                    class="min-h-[120px] resize-y text-xs leading-5"
                    maxlength="16000"
                  />
                  <p class="text-[10px] text-muted-foreground">
                    {{ isGrokSettings
                      ? t('settings.grokGlobalInstructionsSync')
                      : isClaudeSettings
                        ? t('settings.claudeGlobalInstructionsHint')
                        : t('settings.customInstructionsSync') }}
                  </p>
                </div>
              </section>
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-start justify-between gap-3 border-b px-4 py-3">
                  <div class="min-w-0">
                    <h2 class="text-[13px] font-semibold">
                      {{ isGrokSettings
                        ? t('settings.grokProjectInstructions')
                        : isClaudeSettings
                          ? t('settings.claudeProjectInstructions')
                          : t('settings.projectInstructions') }}
                    </h2>
                    <p class="mt-0.5 text-[11px] text-muted-foreground">
                      {{ isGrokSettings
                        ? t('settings.grokProjectInstructionsHint')
                        : isClaudeSettings
                          ? t('settings.claudeProjectInstructionsHint')
                          : t('settings.projectInstructionsHint') }}
                    </p>
                  </div>
                  <div class="flex shrink-0">
                    <Button type="button" variant="ghost" size="sm" class="h-7 px-2 text-[11px]" :disabled="instructionsLoading" @click="loadProjectInstructions">
                      <RefreshCw :size="12" class="mr-1" :class="instructionsLoading ? 'animate-spin' : ''" />
                      {{ t('settings.instructionsReload') }}
                    </Button>
                    <Button
                      v-if="!isGrokSettings && !isClaudeSettings"
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="h-7 px-2 text-[11px]"
                      @click="pickProjectWorkspace"
                    >
                      <FolderOpen :size="12" class="mr-1" />
                      {{ t('settings.projectInstructionsPick') }}
                    </Button>
                  </div>
                </div>
                <div class="space-y-2 p-4">
                  <div v-if="projectInstructionsAvailable" class="rounded-lg border bg-muted/30 px-3 py-2 text-[11px]">
                    <p class="font-medium text-foreground">
                      {{ t('settings.projectInstructionsWorkspace') }}:
                      {{ projectInstructionsWorkspaceName || t('common.unknown') }}
                    </p>
                    <p class="mt-0.5 truncate text-muted-foreground" :title="projectInstructionsWorkspace">
                      {{ projectInstructionsWorkspace }}
                    </p>
                    <div class="mt-1.5 flex flex-wrap items-center gap-2 text-[10px] text-muted-foreground">
                      <Badge variant="outline" class="text-[10px]">{{ projectInstructionsSource }}</Badge>
                      <Badge variant="outline" class="text-[10px]">
                        {{ instructionsStatusLabel(projectInstructionsExists, projectInstructionsEmptyFile) }}
                      </Badge>
                      <span class="tabular-nums">{{ projectInstructionsLength }} / 16000</span>
                      <span v-if="projectInstructionsPath" class="min-w-0 truncate" :title="projectInstructionsPath">{{ projectInstructionsPath }}</span>
                    </div>
                  </div>
                  <p v-else class="text-[11px] text-muted-foreground">{{ t('settings.projectInstructionsUnavailable') }}</p>
                  <Textarea
                    v-model="projectInstructions"
                    :placeholder="projectInstructionsAvailable ? t('settings.projectInstructionsPlaceholder') : t('settings.projectInstructionsUnavailable')"
                    class="min-h-[120px] resize-y text-xs leading-5"
                    maxlength="16000"
                    :disabled="!projectInstructionsAvailable"
                  />
                  <p class="text-[10px] text-muted-foreground">{{ t('settings.projectInstructionsSync') }}</p>
                </div>
              </section>
              <section v-if="!isGrokSettings" class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.multiAgent') }}</h2>
                </div>
                <div class="grid gap-2 p-3 sm:grid-cols-2">
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
                      <Check v-if="multiAgentMode === option.value" :size="13" class="text-primary" />
                    </span>
                    <small class="text-[10px] text-muted-foreground">{{ option.description }}</small>
                  </Button>
                </div>
              </section>
              <section v-if="!isGrokSettings" class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.memories') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.memoriesHint') }}</p>
                </div>
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.memoriesEnable') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.memoriesEnableHint') }}</p>
                    </div>
                    <Switch :checked="memoriesEnabled" @update:checked="memoriesEnabled = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.memoriesUse') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.memoriesUseHint') }}</p>
                    </div>
                    <Switch :checked="memoriesUse" :disabled="!memoriesEnabled" @update:checked="memoriesUse = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.memoriesGenerate') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.memoriesGenerateHint') }}</p>
                    </div>
                    <Switch :checked="memoriesGenerate" :disabled="!memoriesEnabled" @update:checked="memoriesGenerate = $event" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.memoriesDisableExternal') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.memoriesDisableExternalHint') }}</p>
                    </div>
                    <Switch :checked="memoriesDisableExternal" :disabled="!memoriesEnabled" @update:checked="memoriesDisableExternal = $event" />
                  </div>
                </div>
              </section>
            </template>

            <!-- Account -->
            <template v-else-if="activePanel === 'account'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-center gap-3 px-4 py-4">
                  <div class="grid size-10 place-items-center rounded-full bg-muted">
                    <OpenAIIcon v-if="appStore.account.authenticated" :size="18" />
                    <UserRound v-else :size="18" class="text-muted-foreground" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-[14px] font-medium">
                      {{ appStore.account.authenticated ? (appStore.account.email || t('sidebar.codexAccount')) : t('sidebar.signIn') }}
                    </p>
                    <p class="text-[11px] text-muted-foreground">
                      {{ appStore.account.authenticated
                        ? (appStore.account.planType || appStore.account.type || t('sidebar.chatgptAccount'))
                        : t('sidebar.chatgptAccount') }}
                    </p>
                  </div>
                  <Button
                    v-if="!appStore.account.authenticated"
                    type="button"
                    size="sm"
                    @click="appStore.startLogin()"
                  >
                    <LogIn :size="14" class="mr-1.5" />
                    {{ t('sidebar.signIn') }}
                  </Button>
                  <Button
                    v-else
                    type="button"
                    variant="outline"
                    size="sm"
                    @click="appStore.logout()"
                  >
                    <LogOut :size="14" class="mr-1.5" />
                    {{ t('sidebar.signOut') }}
                  </Button>
                </div>
              </section>
              <section
                v-if="appStore.account.authenticated && appStore.accountUsage"
                class="overflow-hidden rounded-xl border bg-card"
              >
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.usageSummary') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.usageSummaryHint') }}</p>
                </div>
                <div class="grid gap-3 p-4 sm:grid-cols-2">
                  <div class="rounded-lg border px-3 py-2.5">
                    <p class="text-[10px] text-muted-foreground">{{ t('inspector.lifetimeTokens') }}</p>
                    <p class="mt-0.5 text-sm font-semibold tabular-nums">
                      {{ formatSettingsTokenCount(appStore.accountUsage.lifetimeTokens) }}
                    </p>
                  </div>
                  <div class="rounded-lg border px-3 py-2.5">
                    <p class="text-[10px] text-muted-foreground">{{ t('sidebar.usagePeakDaily') }}</p>
                    <p class="mt-0.5 text-sm font-semibold tabular-nums">
                      {{ formatSettingsTokenCount(appStore.accountUsage.peakDailyTokens) }}
                    </p>
                  </div>
                  <div class="rounded-lg border px-3 py-2.5">
                    <p class="text-[10px] text-muted-foreground">{{ t('settings.usageStreak') }}</p>
                    <p class="mt-0.5 text-sm font-semibold tabular-nums">
                      {{ appStore.accountUsage.currentStreakDays ?? '—' }}
                      <span class="text-[11px] font-normal text-muted-foreground">{{ t('settings.usageDays') }}</span>
                    </p>
                  </div>
                  <div class="rounded-lg border px-3 py-2.5">
                    <p class="text-[10px] text-muted-foreground">{{ t('settings.usageLongestStreak') }}</p>
                    <p class="mt-0.5 text-sm font-semibold tabular-nums">
                      {{ appStore.accountUsage.longestStreakDays ?? '—' }}
                      <span class="text-[11px] font-normal text-muted-foreground">{{ t('settings.usageDays') }}</span>
                    </p>
                  </div>
                </div>
              </section>
            </template>

            <!-- Archived conversations -->
            <template v-else-if="activePanel === 'archived'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.archivedTitle') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.archivedHint') }}</p>
                </div>
                <div class="border-b px-4 py-2.5">
                  <div class="relative">
                    <Search :size="13" class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
                    <Input
                      v-model="archivedSearch"
                      class="h-8 rounded-lg pl-8 text-xs"
                      :placeholder="t('settings.archivedSearch')"
                      :aria-label="t('settings.archivedSearch')"
                    />
                  </div>
                </div>
                <div v-if="archivedThreads.length" class="divide-y">
                  <div
                    v-for="thread in archivedThreads"
                    :key="thread.id"
                    class="flex items-center gap-3 px-4 py-3"
                  >
                    <div class="min-w-0 flex-1">
                      <p class="truncate text-[13px] font-medium">{{ thread.name || t('sidebar.noPreview') }}</p>
                      <p class="mt-0.5 truncate text-[11px] text-muted-foreground">
                        {{ thread.preview || t('sidebar.noPreview') }}
                      </p>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      class="h-7 shrink-0 text-xs"
                      :disabled="appStore.isGrokMode ? Boolean(grokStore.sessionMutation) : Boolean(codexStore.threadMutation)"
                      @click="restoreArchived(thread.id)"
                    >
                      {{ t('sidebar.restore') }}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="h-7 shrink-0 text-xs text-destructive hover:text-destructive"
                      :disabled="appStore.isGrokMode ? Boolean(grokStore.sessionMutation) : Boolean(codexStore.threadMutation)"
                      @click="deleteArchived(thread.id)"
                    >
                      <Trash2 :size="13" class="mr-1" />
                      {{ t('threadActions.delete') }}
                    </Button>
                  </div>
                </div>
                <p v-else class="px-4 py-8 text-center text-[12px] text-muted-foreground">
                  {{ archivedSearch.trim() ? t('sidebar.noSearchResults') : t('sidebar.archivedEmpty') }}
                </p>
              </section>
            </template>

            <!-- Environment -->
            <template v-else-if="activePanel === 'environment'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-start justify-between gap-3 border-b px-4 py-3">
                  <div class="min-w-0">
                    <h2 class="text-[13px] font-semibold">{{ t('settings.cliToolsTitle') }}</h2>
                    <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.cliToolsHint') }}</p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    class="h-8 shrink-0 text-xs"
                    :disabled="cliLoading"
                    @click="refreshCLITools()"
                  >
                    <RefreshCw :size="12" class="mr-1.5" :class="cliLoading ? 'animate-spin' : ''" />
                    {{ t('onboarding.cliRecheck') }}
                  </Button>
                </div>
                <div
                  v-if="cliReport && !cliReport.nodeAvailable"
                  class="border-b bg-amber-500/10 px-4 py-2.5 text-[11px] text-amber-900 dark:text-amber-100"
                >
                  {{ t('onboarding.nodeMissingBody') }}
                </div>
                <div
                  v-if="cliReport && (cliReport.codexHome || cliReport.grokHome)"
                  class="space-y-1 border-b px-4 py-2.5 text-[11px] text-muted-foreground"
                >
                  <p class="text-[12px] font-medium text-foreground">{{ t('settings.cliToolsHomes') }}</p>
                  <p v-if="cliReport.codexHome" class="truncate font-mono text-[10px]" :title="cliReport.codexHome">
                    {{ t('settings.cliToolsCodexHome') }}: {{ cliReport.codexHome }}
                  </p>
                  <p v-if="cliReport.grokHome" class="truncate font-mono text-[10px]" :title="cliReport.grokHome">
                    {{ t('settings.cliToolsGrokHome') }}: {{ cliReport.grokHome }}
                  </p>
                  <p v-if="cliReport.platform" class="text-[10px]">
                    {{ t('onboarding.cliPlatformHint', { platform: cliReport.platform }) }}
                  </p>
                </div>
                <div class="divide-y">
                  <div
                    v-for="tool in cliTools"
                    :key="tool.id"
                    class="flex items-start justify-between gap-3 px-4 py-3"
                  >
                    <div class="min-w-0 flex-1">
                      <div class="flex flex-wrap items-center gap-2">
                        <p class="text-[13px] font-medium">{{ tool.name }}</p>
                        <Badge
                          :variant="tool.installed && !tool.updateAvailable ? 'default' : 'outline'"
                          class="text-[9px]"
                        >
                          {{ cliToolStatusLabel(tool) }}
                        </Badge>
                      </div>
                      <p class="mt-1 font-mono text-[11px] text-muted-foreground">{{ tool.package }}</p>
                      <p class="mt-0.5 text-[11px] text-muted-foreground">
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
                    <Button
                      v-if="!tool.installed || tool.updateAvailable"
                      type="button"
                      variant="outline"
                      size="sm"
                      class="h-8 shrink-0 text-xs"
                      :disabled="!tool.canInstall || Boolean(cliInstalling[tool.id])"
                      @click="installOrUpdateCLITool(tool)"
                    >
                      <RefreshCw
                        v-if="cliInstalling[tool.id]"
                        :size="12"
                        class="mr-1.5 animate-spin"
                      />
                      <Download v-else :size="12" class="mr-1.5" />
                      {{
                        cliInstalling[tool.id]
                          ? t('onboarding.cliInstalling')
                          : tool.installed
                            ? t('onboarding.cliUpdate')
                            : t('onboarding.cliInstall')
                      }}
                    </Button>
                    <Badge v-else variant="outline" class="shrink-0 text-[10px]">
                      {{ t('settings.cliToolsUpToDate') }}
                    </Badge>
                  </div>
                  <p
                    v-if="cliLoading && !cliTools.length"
                    class="px-4 py-6 text-center text-[12px] text-muted-foreground"
                  >
                    {{ t('onboarding.cliChecking') }}
                  </p>
                  <div v-if="!cliLoading && !cliReport" class="px-4 py-4 text-center">
                    <Button type="button" variant="outline" size="sm" class="h-8 text-xs" @click="refreshCLITools()">
                      {{ t('onboarding.cliRecheck') }}
                    </Button>
                  </div>
                </div>
              </section>

              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px] font-medium">Codex CLI / app-server</p>
                      <p class="text-[11px] text-muted-foreground">
                        {{ codexStatus?.runtimeReady ? t('settings.runtimeReadyHint') : (codexStatus?.message || t('settings.providerCodexHint')) }}
                      </p>
                    </div>
                    <Badge :variant="codexStatus?.runtimeReady ? 'default' : 'outline'">
                      {{ codexStatus?.runtimeReady ? t('settings.runtimeReady') : t('settings.runtimeMissing') }}
                    </Badge>
                  </div>
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.runtimeReconnect') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.runtimeReconnectHint') }}</p>
                    </div>
                    <Button type="button" variant="outline" size="sm" class="h-8 shrink-0 text-xs" @click="reconnectCodexRuntime">
                      <RefreshCw :size="12" class="mr-1.5" />
                      {{ t('settings.runtimeReconnect') }}
                    </Button>
                  </div>
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.agentEnvironment') }}</p>
                    <code class="rounded bg-muted px-2 py-1 text-[11px]">{{ t('settings.agentEnvironmentNative') }}</code>
                  </div>
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <p class="text-[13px]">{{ t('updates.currentVersion') }}</p>
                    <code class="rounded bg-muted px-2 py-1 font-mono text-[11px]">v{{ appStore.appVersion }}</code>
                  </div>
                </div>
              </section>

              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.codexClientTitle') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.codexClientHint') }}</p>
                </div>
                <div class="divide-y">
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.codexClientPreset') }}</p>
                    <Select v-model="codexClientPreset">
                      <SelectTrigger class="h-9 text-xs" :aria-label="t('settings.codexClientPreset')">
                        <SelectValue>
                          {{ selectedOptionLabel(codexClientPresetOptions, codexClientPreset) }}
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem
                          v-for="option in codexClientPresetOptions"
                          :key="option.value"
                          :value="option.value"
                        >
                          <div class="flex flex-col gap-0.5">
                            <span>{{ option.label }}</span>
                            <span v-if="option.description" class="text-[10px] text-muted-foreground">{{ option.description }}</span>
                          </div>
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.codexClientName') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.codexClientNameHint') }}</p>
                    <Input
                      v-model="codexClientName"
                      class="h-8 font-mono text-xs"
                      maxlength="64"
                      :placeholder="t('settings.codexClientNamePlaceholder')"
                    />
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.codexClientDisplayTitle') }}</p>
                    <Input
                      v-model="codexClientTitle"
                      class="h-8 text-xs"
                      maxlength="80"
                      :placeholder="t('settings.codexClientDisplayTitlePlaceholder')"
                    />
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.codexClientVersion') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.codexClientVersionHint') }}</p>
                    <Input
                      v-model="codexClientVersion"
                      class="h-8 font-mono text-xs"
                      maxlength="32"
                      :placeholder="t('settings.codexClientVersionPlaceholder')"
                    />
                  </div>
                  <div class="px-4 py-3">
                    <p class="text-[11px] leading-relaxed text-muted-foreground">
                      {{ t('settings.codexClientApplyHint') }}
                    </p>
                  </div>
                </div>
              </section>
            </template>

            <!-- Git -->
            <template v-else-if="activePanel === 'git'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitWorkspace') }}</p>
                    <span class="max-w-[60%] truncate text-[12px] text-muted-foreground">
                      {{ workspaceStore.workspace?.path || appStore.settings.workspace || t('sidebar.chooseFolder') }}
                    </span>
                  </div>
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitBranch') }}</p>
                    <span class="inline-flex items-center gap-1.5 text-[12px] text-muted-foreground">
                      <GitBranch :size="12" />
                      {{ workspaceStore.workspace?.branch || (workspaceStore.workspace?.isGit ? '—' : t('settings.gitNotRepo')) }}
                    </span>
                  </div>
                  <div class="flex items-center justify-between gap-3 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitChanges') }}</p>
                    <span class="text-[12px] tabular-nums text-muted-foreground">
                      {{ workspaceStore.workspace?.changes?.length ?? 0 }}
                    </span>
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitBranchPrefix') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.gitBranchPrefixHint') }}</p>
                    <Input v-model="gitBranchPrefix" class="h-8 text-xs" maxlength="64" :placeholder="t('settings.gitBranchPrefixPlaceholder')" />
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitCommitPrefix') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.gitCommitPrefixHint') }}</p>
                    <Input v-model="gitCommitPrefix" class="h-8 text-xs" maxlength="64" :placeholder="t('settings.gitCommitPrefixPlaceholder')" />
                  </div>
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.gitOpenPR') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.gitOpenPRHint') }}</p>
                    </div>
                    <Switch :checked="gitOpenPRAfterPush" @update:checked="gitOpenPRAfterPush = $event" />
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitPRBody') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.gitPRBodyHint') }}</p>
                    <Textarea v-model="gitPRBodyTemplate" class="min-h-[88px] resize-y text-xs" maxlength="4000" :placeholder="t('settings.gitPRBodyPlaceholder')" />
                  </div>
                  <div class="space-y-2 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.gitQuickActions') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.gitQuickActionsHint') }}</p>
                    <div class="flex flex-col gap-2 sm:flex-row">
                      <Input v-model="gitBranchDraft" class="h-8 flex-1 text-xs" maxlength="100" :placeholder="t('settings.gitBranchNamePlaceholder')" />
                      <Button type="button" variant="outline" size="sm" class="h-8 text-xs" :disabled="gitActionBusy || !gitBranchDraft.trim()" @click="runCreateBranch">
                        {{ t('settings.gitCreateBranch') }}
                      </Button>
                    </div>
                    <div class="flex flex-col gap-2 sm:flex-row">
                      <Input v-model="gitCommitDraft" class="h-8 flex-1 text-xs" maxlength="400" :placeholder="t('settings.gitCommitMessagePlaceholder')" />
                      <Button type="button" variant="outline" size="sm" class="h-8 text-xs" :disabled="gitActionBusy || !gitCommitDraft.trim()" @click="runCommit">
                        {{ t('settings.gitCommit') }}
                      </Button>
                    </div>
                    <Button type="button" variant="outline" size="sm" class="h-8 text-xs" :disabled="gitActionBusy || !workspaceStore.workspace?.isGit" @click="runPush">
                      {{ t('settings.gitPush') }}
                    </Button>
                  </div>
                </div>
              </section>
            </template>

            <!-- Browser -->
            <template v-else-if="activePanel === 'browser'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="flex items-start justify-between gap-3 border-b px-4 py-3">
                  <div class="min-w-0">
                    <h2 class="text-[13px] font-semibold">{{ t('settings.browserTitle') }}</h2>
                    <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.browserHint') }}</p>
                  </div>
                  <Button type="button" variant="outline" size="sm" class="h-7 shrink-0 text-[11px]" @click="openEmbeddedBrowser">
                    {{ t('settings.browserOpen') }}
                  </Button>
                </div>
                <div class="divide-y">
                  <div class="flex items-center justify-between gap-4 px-4 py-3">
                    <div class="min-w-0">
                      <p class="text-[13px]">{{ t('settings.browserFullCDP') }}</p>
                      <p class="text-[11px] text-muted-foreground">{{ t('settings.browserFullCDPHint') }}</p>
                    </div>
                    <Switch :checked="browserFullCDP" @update:checked="browserFullCDP = $event" />
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.browserDownloadDir') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.browserDownloadDirHint') }}</p>
                    <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
                      <Input v-model="browserDownloadDir" class="h-8 min-w-0 flex-1 text-xs" :placeholder="t('settings.browserDownloadDirPlaceholder')" />
                      <Button type="button" variant="outline" size="sm" class="h-8 shrink-0 text-xs" @click="pickBrowserDownloadDir">
                        <FolderOpen :size="12" class="mr-1.5" />
                        {{ t('settings.browserDownloadDirPick') }}
                      </Button>
                      <Button type="button" variant="outline" size="sm" class="h-8 shrink-0 text-xs" @click="openBrowserDownloadDir">
                        {{ t('settings.browserDownloadDirOpen') }}
                      </Button>
                    </div>
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.browserAllowed') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.browserAllowedHint') }}</p>
                    <Textarea v-model="browserAllowedHostsText" class="min-h-[88px] resize-y font-mono text-xs" :placeholder="t('settings.browserHostsPlaceholder')" />
                  </div>
                  <div class="space-y-1.5 px-4 py-3">
                    <p class="text-[13px]">{{ t('settings.browserBlocked') }}</p>
                    <p class="text-[11px] text-muted-foreground">{{ t('settings.browserBlockedHint') }}</p>
                    <Textarea v-model="browserBlockedHostsText" class="min-h-[88px] resize-y font-mono text-xs" :placeholder="t('settings.browserHostsPlaceholder')" />
                  </div>
                </div>
              </section>
            </template>

            <!-- Scheduled -->
            <template v-else-if="activePanel === 'scheduled'">
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.scheduledTitle') }}</h2>
                  <p class="mt-0.5 text-[11px] text-muted-foreground">{{ t('settings.scheduledHint') }}</p>
                </div>
                <div class="space-y-3 p-4">
                  <Input v-model="scheduledDraftTitle" class="h-8 text-xs" :placeholder="t('settings.scheduledTitlePlaceholder')" maxlength="120" />
                  <Textarea v-model="scheduledDraftPrompt" class="min-h-[88px] resize-y text-xs" :placeholder="t('settings.scheduledPromptPlaceholder')" maxlength="8000" />
                  <div class="flex flex-wrap items-center gap-3">
                    <label class="flex items-center gap-2 text-[11px] text-muted-foreground">
                      {{ t('settings.scheduledInterval') }}
                      <Input v-model.number="scheduledDraftInterval" type="number" min="5" class="h-8 w-24 text-xs" />
                    </label>
                    <label class="flex min-w-0 flex-col gap-1 text-[11px] text-muted-foreground sm:flex-row sm:items-center">
                      <span class="inline-flex items-center gap-2">
                        <Switch :checked="scheduledDraftWorktree" @update:checked="scheduledDraftWorktree = $event" />
                        {{ t('settings.scheduledWorktree') }}
                      </span>
                      <span class="text-[10px]">{{ t('settings.scheduledWorktreeHint') }}</span>
                    </label>
                    <Button type="button" size="sm" class="h-8 text-xs" :disabled="!scheduledDraftTitle.trim() || !scheduledDraftPrompt.trim()" @click="saveScheduledDraft">
                      <Plus :size="12" class="mr-1" />
                      {{ t('settings.scheduledAdd') }}
                    </Button>
                  </div>
                </div>
              </section>
              <section class="overflow-hidden rounded-xl border bg-card">
                <div class="border-b px-4 py-3">
                  <h2 class="text-[13px] font-semibold">{{ t('settings.scheduledList') }}</h2>
                </div>
                <div v-if="scheduledLoading" class="px-4 py-6 text-center text-[11px] text-muted-foreground">{{ t('common.loading') }}</div>
                <div v-else-if="scheduledTasks.length === 0" class="px-4 py-6 text-center text-[11px] text-muted-foreground">{{ t('settings.scheduledEmpty') }}</div>
                <div v-else class="divide-y">
                  <div v-for="task in scheduledTasks" :key="task.id" class="flex items-start gap-3 px-4 py-3">
                    <div class="min-w-0 flex-1">
                      <p class="text-[13px] font-medium">{{ task.title }}</p>
                      <p class="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{{ task.prompt }}</p>
                      <p class="mt-1 text-[10px] text-muted-foreground">
                        {{ t('settings.scheduledMeta', { minutes: task.intervalMin }) }}
                        <span v-if="task.lastError" class="text-destructive"> · {{ task.lastError }}</span>
                      </p>
                    </div>
                    <Switch :checked="task.enabled" @update:checked="(enabled: boolean) => toggleScheduledTask(task, enabled)" />
                    <Button type="button" variant="ghost" size="icon-xs" @click="removeScheduledTask(task.id)">
                      <Trash2 :size="12" />
                    </Button>
                  </div>
                </div>
              </section>
            </template>
          </form>
        </main>
      </section>
    </div>
  </div>
</template>

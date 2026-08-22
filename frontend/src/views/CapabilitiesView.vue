<script setup lang="ts">
import {
  AppWindow,
  ArrowLeft,
  Blocks,
  Bot,
  Braces,
  FileUp,
  FlaskConical,
  LoaderCircle,
  Pencil,
  Plus,
  PlugZap,
  Power,
  RefreshCw,
  Search,
  Settings2,
  Sparkles,
  Trash2,
  Unplug,
  Webhook,
} from '@lucide/vue'
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import ClaudeIcon from '@/components/icons/ClaudeIcon.vue'
import GrokIcon from '@/components/icons/GrokIcon.vue'
import GeminiIcon from '@/components/icons/GeminiIcon.vue'
import OpenCodeIcon from '@/components/icons/OpenCodeIcon.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { ExternalRuntimeCatalog, ProviderConfigurationView } from '../../bindings/nice_codex_desktop/models'
import { useAppStore, useCapabilitiesStore, useClaudeStore, useCodexStore, useDialogStore, useGrokStore } from '@/stores'
import {
  openClaudeConfigFile,
  openClaudeHome,
  readClaudeCapabilities,
  type ClaudeCapabilitiesCatalog,
} from '@/utils/claudeBindings'
import type { MCPServerView } from '@/types/codex'
import {
  openGrokConfigFile,
  openGrokHome,
  readGrokCapabilities,
  type GrokCapabilitiesCatalog,
} from '@/utils/grokBindings'
import { notify } from '@/utils/notify'
import { MCP_IMPORT_MAX_LENGTH, parseMCPImportJSON, type ImportedMCPServer } from '@/utils/mcpImport'

type CapabilityTab = 'plugins' | 'skills' | 'apps' | 'mcp' | 'automation'
type GrokCapTab = 'runtime' | 'mcp' | 'skills' | 'plugins' | 'instructions'
type ClaudeCapTab = 'runtime' | 'mcp' | 'skills' | 'plugins' | 'agents' | 'hooks' | 'instructions'

const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const capabilitiesStore = useCapabilitiesStore()
const dialogStore = useDialogStore()
const { t } = useI18n()
const isGrokMode = computed(() => appStore.isGrokMode)
const isClaudeMode = computed(() => appStore.isClaudeMode)
const isGeminiMode = computed(() => appStore.isGeminiMode)
const isOpenCodeMode = computed(() => appStore.isOpenCodeMode)
const externalRuntimeName = computed(() => isGeminiMode.value ? 'Gemini CLI' : 'OpenCode')
const externalProvider = computed(() => appStore.agentProviders.find((item) => item.kind === appStore.activeRuntime))
const grokProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'grok'))
const claudeProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'claude'))
const grokCatalog = shallowRef<GrokCapabilitiesCatalog | null>(null)
const grokCatalogLoading = shallowRef(false)
const grokTab = shallowRef<GrokCapTab>('runtime')
const claudeCatalog = shallowRef<ClaudeCapabilitiesCatalog | null>(null)
const claudeCatalogLoading = shallowRef(false)
const claudeTab = shallowRef<ClaudeCapTab>('runtime')
const externalTab = shallowRef<'runtime' | 'mcp' | 'instructions'>('runtime')
const externalCatalog = shallowRef<ExternalRuntimeCatalog | null>(null)
const externalCatalogLoading = shallowRef(false)
const externalInstructionScope = shallowRef<'global' | 'project'>('global')
const externalMcpScope = shallowRef<'global' | 'project'>('global')
const externalInstructionDraft = shallowRef('')
const externalMcpJSON = shallowRef('')
const externalMcpSaving = shallowRef(false)
const providerConfiguration = shallowRef<ProviderConfigurationView | null>(null)
const providerConfigurationLoading = shallowRef(false)
const providerRestarting = shallowRef(false)
const providerContextTokens = shallowRef('')
const providerCompactThreshold = shallowRef('')
const providerAutoCompactEnabled = shallowRef(true)
const providerPruneEnabled = shallowRef(false)
const providerThresholdScope = shallowRef('native')
const providerContextSaving = shallowRef(false)
let providerConfigurationRequestSequence = 0
let providerContextSaveSequence = 0
let providerRestartSequence = 0
let externalCatalogRequestSequence = 0
const providerApplyLabel = computed(() => {
  switch (providerConfiguration.value?.applyLevel) {
    case 'immediate': return t('providerConfig.applyImmediate')
    case 'reconnect': return t('providerConfig.applyReconnect')
    case 'new-session': return t('providerConfig.applyNewSession')
    default: return t('providerConfig.applyNextTurn')
  }
})
const providerContextLabel = computed(() => {
  const context = providerConfiguration.value?.context
  if (!context?.tokens) return t('providerConfig.contextUnknown')
  return `${Math.round(context.tokens / 1000)}K · ${context.source}`
})
const providerContextPolicy = computed(() => providerConfiguration.value?.context ?? null)
const providerContextCanSave = computed(() => Boolean(
  providerContextPolicy.value?.writable
  || providerContextPolicy.value?.thresholdConfigurable
  || providerContextPolicy.value?.autoCompactToggleable
  || providerContextPolicy.value?.pruneSupported,
))
const providerTokenModeLabel = computed(() => {
  const mode = providerContextPolicy.value?.tokenMode
  if (mode === 'client-limit') return t('providerConfig.clientLimit')
  if (mode === 'calculation-limit') return t('providerConfig.calculationLimit')
  if (mode === 'native-override') return t('providerConfig.nativeOverride')
  return t('providerConfig.modelFixed')
})
const providerThresholdLabel = computed(() => {
  if (providerContextPolicy.value?.thresholdUnit === 'percent') return t('providerConfig.compactPercent')
  if (providerContextPolicy.value?.thresholdUnit === 'reserved-tokens') return t('providerConfig.reservedTokens')
  return t('providerConfig.compactThreshold')
})
const providerThresholdSliderValue = computed(() => {
  if (!providerCompactThreshold.value.trim()) return providerContextPolicy.value?.thresholdMinimum || 0
  const value = Number(providerCompactThreshold.value)
  if (Number.isFinite(value)) return value
  return providerContextPolicy.value?.thresholdMinimum || 0
})
const providerThresholdProgress = computed(() => {
  const minimum = providerContextPolicy.value?.thresholdMinimum || 0
  const maximum = providerContextPolicy.value?.thresholdMaximum || minimum
  if (maximum <= minimum) return 0
  const value = Math.min(maximum, Math.max(minimum, providerThresholdSliderValue.value))
  return (value - minimum) / (maximum - minimum) * 100
})
function providerThresholdScopeLabel(scope: string): string {
  if (scope === 'total') return t('providerConfig.scopeTotal')
  if (scope === 'body_after_prefix') return t('providerConfig.scopeBodyAfterPrefix')
  return t('providerConfig.scopeNative')
}
const providerBusy = computed(() => {
  if (isClaudeMode.value) return claudeStore.runningSessionIds.length > 0
  if (isGrokMode.value) return grokStore.runningSessionIds.length > 0
  if (appStore.activeRuntime === 'codex') return codexStore.activeThreadBusy
  return codexStore.runningThreadIds.length > 0
})

const externalTabs = computed(() => [
  { value: 'runtime' as const, label: t('capabilities.externalTabRuntime'), icon: isGeminiMode.value ? GeminiIcon : OpenCodeIcon, count: externalCatalog.value?.models?.length ?? 0 },
  { value: 'mcp' as const, label: t('capabilities.externalTabMcp'), icon: PlugZap, count: externalCatalog.value?.mcp?.length ?? 0 },
  { value: 'instructions' as const, label: t('capabilities.externalTabInstructions'), icon: Settings2, count: 0 },
])

const grokTabs = computed(() => [
  { value: 'runtime' as const, label: t('capabilities.grokTabRuntime'), icon: GrokIcon, count: 0 },
  { value: 'mcp' as const, label: t('capabilities.grokTabMcp'), icon: PlugZap, count: grokCatalog.value?.mcp?.length ?? 0 },
  { value: 'skills' as const, label: t('capabilities.grokTabSkills'), icon: Sparkles, count: grokCatalog.value?.skills?.length ?? 0 },
  { value: 'plugins' as const, label: t('capabilities.grokTabPlugins'), icon: Blocks, count: grokCatalog.value?.plugins?.length ?? 0 },
  { value: 'instructions' as const, label: t('capabilities.grokTabInstructions'), icon: Settings2, count: 0 },
])

const claudeTabs = computed(() => [
  { value: 'runtime' as const, label: t('capabilities.claudeTabRuntime'), icon: ClaudeIcon, count: 0 },
  { value: 'mcp' as const, label: t('capabilities.claudeTabMcp'), icon: PlugZap, count: claudeCatalog.value?.mcp?.length ?? 0 },
  { value: 'skills' as const, label: t('capabilities.claudeTabSkills'), icon: Sparkles, count: claudeCatalog.value?.skills?.length ?? 0 },
  { value: 'plugins' as const, label: t('capabilities.claudeTabPlugins'), icon: Blocks, count: claudeCatalog.value?.plugins?.length ?? 0 },
  { value: 'agents' as const, label: t('capabilities.claudeTabAgents'), icon: Bot, count: (claudeCatalog.value?.agents?.length ?? 0) + (claudeCatalog.value?.commands?.length ?? 0) },
  { value: 'hooks' as const, label: t('capabilities.claudeTabHooks'), icon: Webhook, count: claudeCatalog.value?.hooks?.length ?? 0 },
  { value: 'instructions' as const, label: t('capabilities.claudeTabInstructions'), icon: Settings2, count: 0 },
])

function claudeScopeLabel(scope: string): string {
  if (scope === 'project') return t('capabilities.grokScopeProject')
  if (scope === 'plugin' || scope === 'bundled' || scope === 'cache') return t('capabilities.grokScopeBundled')
  return t('capabilities.grokScopeUser')
}

function openClaudeSettings(): void {
  router.push({ name: 'settings', query: { section: 'agent' } })
}

function openClaudeInstructionsSettings(): void {
  router.push({ name: 'settings', query: { section: 'personalization' } })
}

const activeTab = shallowRef<CapabilityTab>('plugins')
const query = shallowRef('')
const brokenLogos = shallowRef<Set<string>>(new Set())
const PAGE_SIZE = 40
const visibleLimit = shallowRef<Record<CapabilityTab, number>>({ plugins: PAGE_SIZE, skills: PAGE_SIZE, apps: PAGE_SIZE, mcp: PAGE_SIZE, automation: PAGE_SIZE })
const mcpEditorOpen = shallowRef(false)
const mcpImportOpen = shallowRef(false)
const mcpImportMode = shallowRef<'visual' | 'json'>('visual')
const mcpImportJSON = shallowRef('')
const mcpImportPreview = shallowRef<ImportedMCPServer[]>([])
const mcpImportError = shallowRef('')
const mcpImportSource = shallowRef('')
const mcpImportParsing = shallowRef(false)
const mcpImportFileInput = shallowRef<HTMLInputElement | null>(null)
let mcpImportParseTimer = 0
const mcpForm = ref({
  originalName: '',
  name: '',
  enabled: true,
  kind: 'command' as 'command' | 'url',
  command: '',
  args: [] as string[],
  url: '',
  transport: 'stdio',
  env: [] as Array<{ id: string, key: string, value: string }>,
})
const mcpFormValid = computed(() => {
  const form = mcpForm.value
  const name = form.name.trim()
  if (!name || name.length > 120 || /[\r\n]/.test(name)) return false
  if (form.kind === 'url') {
    try {
      const url = new URL(form.url.trim())
      if (url.protocol !== 'http:' && url.protocol !== 'https:') return false
    } catch {
      return false
    }
  } else if (!form.command.trim() || form.command.length > 2048 || form.args.length > 128) {
    return false
  }
  const envKeys = form.env.map((entry) => entry.key.trim()).filter(Boolean)
  return new Set(envKeys).size === envKeys.length
    && form.env.length <= 128
    && form.env.every((entry) => entry.key.length <= 256 && entry.value.length <= 16_384)
})
const mcpImportValid = computed(() => !mcpImportParsing.value && !mcpImportError.value && mcpImportPreview.value.length > 0)

const tabs = computed(() => [
  { value: 'plugins' as const, label: t('capabilities.plugins'), icon: Blocks, count: capabilitiesStore.plugins.length },
  { value: 'skills' as const, label: t('capabilities.skills'), icon: Sparkles, count: capabilitiesStore.skills.length },
  { value: 'apps' as const, label: t('capabilities.apps'), icon: AppWindow, count: capabilitiesStore.apps.length },
  { value: 'mcp' as const, label: 'MCP', icon: PlugZap, count: capabilitiesStore.mcpServers.length },
  { value: 'automation' as const, label: t('capabilities.automation'), icon: Webhook, count: capabilitiesStore.hooks.length + capabilitiesStore.features.length },
])

const normalizedQuery = computed(() => query.value.trim().toLocaleLowerCase())

function matches(...values: string[]): boolean {
  if (!normalizedQuery.value) return true
  return values.join(' ').toLocaleLowerCase().includes(normalizedQuery.value)
}

const plugins = computed(() => capabilitiesStore.plugins.filter((item) => matches(item.displayName, item.description, item.developerName)))
const skills = computed(() => capabilitiesStore.skills.filter((item) => matches(item.displayName, item.description, item.scope)))
const apps = computed(() => capabilitiesStore.apps.filter((item) => matches(item.name, item.description, item.pluginNames.join(' '))))
const mcpServers = computed(() => capabilitiesStore.mcpServers.filter((item) => matches(item.title, item.description, item.name)))
const hooks = computed(() => capabilitiesStore.hooks.filter((item) => matches(item.name, item.event, item.source)))
const features = computed(() => capabilitiesStore.features.filter((item) => matches(item.displayName, item.description, item.stage)))
const visiblePlugins = computed(() => plugins.value.slice(0, visibleLimit.value.plugins))
const visibleSkills = computed(() => skills.value.slice(0, visibleLimit.value.skills))
const visibleApps = computed(() => apps.value.slice(0, visibleLimit.value.apps))
const visibleMcpServers = computed(() => mcpServers.value.slice(0, visibleLimit.value.mcp))
const activeListCount = computed(() => ({
  plugins: plugins.value.length,
  skills: skills.value.length,
  apps: apps.value.length,
  mcp: mcpServers.value.length,
  automation: 0,
})[activeTab.value])
const remainingCount = computed(() => Math.max(0, activeListCount.value - visibleLimit.value[activeTab.value]))

const activeError = computed(() => {
  if (activeTab.value === 'automation') {
    return [capabilitiesStore.capabilityErrors.hooks, capabilitiesStore.capabilityErrors.features].filter(Boolean).join(' · ')
  }
  return capabilitiesStore.capabilityErrors[activeTab.value]
})

const capabilityStats = computed(() => [
  { label: t('capabilities.plugins'), value: capabilitiesStore.plugins.filter((item) => item.installed).length, total: capabilitiesStore.plugins.length },
  { label: t('capabilities.skills'), value: capabilitiesStore.skills.filter((item) => item.enabled).length, total: capabilitiesStore.skills.length },
  { label: 'MCP', value: capabilitiesStore.mcpServers.filter((item) => item.statusLoaded && item.enabled).length, total: capabilitiesStore.mcpServers.length },
  { label: t('capabilities.features'), value: capabilitiesStore.features.filter((item) => item.enabled).length, total: capabilitiesStore.features.length },
])

async function loadGrokCatalog(): Promise<void> {
  grokCatalogLoading.value = true
  try {
    // Always refresh runtime first so install detection is current.
    await grokStore.refreshRuntime()
    grokCatalog.value = await readGrokCapabilities()
    if (grokCatalog.value?.runtime) {
      grokStore.runtime = grokCatalog.value.runtime
    }
  } catch (error) {
    // Runtime may still be usable even if catalog binding is stale — surface clear text.
    const message = error instanceof Error ? error.message : String(error)
    notify('error', t('capabilities.loading'), message)
    // Fallback: still show runtime-only panel from store.
    grokCatalog.value = {
      runtime: grokStore.runtime,
      configPath: '',
      grokHome: '',
      mcp: [],
      skills: [],
      plugins: [],
      globalInstructions: { content: '', path: '', source: '', exists: false, emptyFile: false, available: false },
      projectInstructions: {
        content: '', workspace: '', workspaceName: '', path: '', source: '',
        exists: false, emptyFile: false, available: false,
      },
    }
  } finally {
    grokCatalogLoading.value = false
  }
}

async function loadClaudeCatalog(): Promise<void> {
  claudeCatalogLoading.value = true
  try {
    claudeCatalog.value = await readClaudeCapabilities()
    void claudeStore.refreshRuntime()
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    notify('error', t('capabilities.loading'), message)
    claudeCatalog.value = {
      runtime: claudeStore.runtime,
      configPath: '',
      claudeHome: '',
      claudeJsonPath: '',
      settings: {
        path: '', exists: false, model: '', permissionMode: '', allowRules: 0, denyRules: 0,
        envKeys: [], baseURL: '', skipDangerPrompt: false, hasStatusLine: false, rawPermissionMode: '',
      },
      mcp: [],
      skills: [],
      plugins: [],
      agents: [],
      commands: [],
      hooks: [],
      globalInstructions: { content: '', path: '', source: '', exists: false, emptyFile: false, available: false },
      projectInstructions: {
        content: '', workspace: '', workspaceName: '', path: '', source: '',
        exists: false, emptyFile: false, available: false,
      },
    }
  } finally {
    claudeCatalogLoading.value = false
  }
}

function externalRuntimeID(): 'gemini' | 'opencode' {
  return isGeminiMode.value ? 'gemini' : 'opencode'
}

function hydrateExternalEditors(): void {
  const catalog = externalCatalog.value
  if (!catalog) return
  const info = externalInstructionScope.value === 'global'
    ? catalog.globalInstructions
    : catalog.projectInstructions
  externalInstructionDraft.value = info?.content || ''
  const key = isGeminiMode.value ? 'mcpServers' : 'mcp'
  const mcpServers = (catalog.mcp || []).filter((server) => externalMcpScope.value === 'global'
    ? server.configPath === catalog.configPath
    : server.configPath !== catalog.configPath)
  const servers = Object.fromEntries(mcpServers.map((server) => [server.name, {
    type: server.type,
    command: server.command || undefined,
    args: server.args ? server.args.split(' ').filter(Boolean) : undefined,
    url: server.url || undefined,
    transport: server.transport || undefined,
    enabled: server.enabled,
  }]))
  externalMcpJSON.value = JSON.stringify({ [key]: servers }, null, 2)
}

async function loadExternalCatalog(): Promise<void> {
  const runtime = externalRuntimeID()
  const workspace = appStore.currentWorkspacePath || ''
  const sequence = ++externalCatalogRequestSequence
  externalCatalogLoading.value = true
  try {
    const catalog = await backend.ReadExternalRuntimeCatalog(runtime, workspace)
    if (sequence !== externalCatalogRequestSequence || runtime !== appStore.activeRuntime) return
    externalCatalog.value = catalog
    hydrateExternalEditors()
  } catch (error) {
    if (sequence !== externalCatalogRequestSequence || runtime !== appStore.activeRuntime) return
    notify('error', t('capabilities.externalRuntime'), error instanceof Error ? error.message : String(error))
  } finally {
    if (sequence === externalCatalogRequestSequence && runtime === appStore.activeRuntime) {
      externalCatalogLoading.value = false
    }
  }
}

async function saveExternalInstructions(): Promise<void> {
  try {
    await backend.SaveExternalRuntimeInstructions({
      runtime: externalRuntimeID(), workspace: appStore.currentWorkspacePath || '',
      scope: externalInstructionScope.value, content: externalInstructionDraft.value,
    })
    await loadExternalCatalog()
    notify('success', t('capabilities.externalInstructionsSaved'), t('settings.externalInstructionsSaved'))
  } catch (error) {
    notify('error', t('capabilities.externalInstructions'), error instanceof Error ? error.message : String(error))
  }
}

async function saveExternalMCP(): Promise<void> {
  externalMcpSaving.value = true
  try {
    await backend.SaveExternalRuntimeMCP({
      runtime: externalRuntimeID(), workspace: appStore.currentWorkspacePath || '', json: externalMcpJSON.value,
      scope: externalMcpScope.value,
    })
    await loadExternalCatalog()
    notify('success', t('capabilities.externalMcp'), t('capabilities.externalMcpSaved'))
  } catch (error) {
    notify('error', t('capabilities.externalMcp'), error instanceof Error ? error.message : String(error))
  } finally {
    externalMcpSaving.value = false
  }
}

async function checkProviderConfiguration(force = true): Promise<void> {
  const providerID = appStore.activeRuntime
  const sequence = ++providerConfigurationRequestSequence
  providerConfigurationLoading.value = true
  try {
    const configuration = await backend.CheckProviderConfiguration(providerID, force)
    if (sequence !== providerConfigurationRequestSequence || providerID !== appStore.activeRuntime) return
    applyProviderConfiguration(configuration)
  } catch (error) {
    if (sequence !== providerConfigurationRequestSequence || providerID !== appStore.activeRuntime) return
    notify('error', t('providerConfig.checkFailed'), error instanceof Error ? error.message : String(error))
  } finally {
    if (sequence === providerConfigurationRequestSequence && providerID === appStore.activeRuntime) {
      providerConfigurationLoading.value = false
    }
  }
}

function applyProviderConfiguration(configuration: ProviderConfigurationView): void {
  providerConfiguration.value = configuration
  hydrateProviderContext(configuration)
  const next = [...appStore.agentProviders]
  const index = next.findIndex((item) => item.kind === configuration.runtime.kind)
  if (index >= 0) next[index] = configuration.runtime
  else next.push(configuration.runtime)
  appStore.agentProviders = next
}

function hydrateProviderContext(configuration: ProviderConfigurationView): void {
  providerContextTokens.value = configuration.context.configuredTokens
    ? String(configuration.context.configuredTokens)
    : ''
  const threshold = Number(configuration.context.autoCompactThreshold)
  providerCompactThreshold.value = Number.isFinite(threshold) && (
    configuration.context.thresholdConfigured
    || (threshold > 0 && threshold >= configuration.context.thresholdMinimum)
  ) ? String(threshold) : ''
  providerAutoCompactEnabled.value = configuration.context.autoCompactEnabled
  providerPruneEnabled.value = configuration.context.pruneEnabled
  providerThresholdScope.value = configuration.context.thresholdScopeConfigured && configuration.context.thresholdScope
    ? configuration.context.thresholdScope
    : 'native'
}

async function reloadProviderConfiguration(): Promise<void> {
  const providerID = appStore.activeRuntime
  const sequence = ++providerConfigurationRequestSequence
  providerConfigurationLoading.value = true
  try {
    const result = await backend.ReloadProviderConfiguration(providerID)
    if (sequence !== providerConfigurationRequestSequence || providerID !== appStore.activeRuntime) return
    applyProviderConfiguration(result.configuration)
    loadNativeProviderDetails()
    notify('success', t('providerConfig.reloaded'), providerApplyLabel.value)
  } catch (error) {
    if (sequence !== providerConfigurationRequestSequence || providerID !== appStore.activeRuntime) return
    notify('error', t('providerConfig.reloadFailed'), error instanceof Error ? error.message : String(error))
  } finally {
    if (sequence === providerConfigurationRequestSequence && providerID === appStore.activeRuntime) {
      providerConfigurationLoading.value = false
    }
  }
}

async function restartProvider(): Promise<void> {
  const providerID = appStore.activeRuntime
  const sequence = ++providerRestartSequence
  providerRestarting.value = true
  try {
    const result = await backend.RestartProvider(providerID)
    if (sequence !== providerRestartSequence || providerID !== appStore.activeRuntime) return
    applyProviderConfiguration(result.configuration)
    loadNativeProviderDetails()
    notify('success', t('providerConfig.restarted'), providerApplyLabel.value)
  } catch (error) {
    if (sequence !== providerRestartSequence || providerID !== appStore.activeRuntime) return
    notify('error', t('providerConfig.restartFailed'), error instanceof Error ? error.message : String(error))
  } finally {
    if (sequence === providerRestartSequence && providerID === appStore.activeRuntime) {
      providerRestarting.value = false
    }
  }
}

async function saveProviderContextPolicy(): Promise<void> {
  const providerID = appStore.activeRuntime
  const sequence = ++providerContextSaveSequence
  providerContextSaving.value = true
  try {
    const result = await backend.UpdateProviderContextPolicy(
      providerID,
      Number(providerContextTokens.value) || 0,
      Number(providerCompactThreshold.value) || 0,
      providerAutoCompactEnabled.value,
      providerPruneEnabled.value,
      providerThresholdScope.value === 'native' ? '' : providerThresholdScope.value,
    )
    if (sequence !== providerContextSaveSequence || providerID !== appStore.activeRuntime) return
    applyProviderConfiguration(result.configuration)
    loadNativeProviderDetails()
    notify('success', t('providerConfig.contextSaved'), result.restartRequired ? t('providerConfig.applyReconnect') : providerApplyLabel.value)
  } catch (error) {
    if (sequence !== providerContextSaveSequence || providerID !== appStore.activeRuntime) return
    notify('error', t('providerConfig.contextSaveFailed'), error instanceof Error ? error.message : String(error))
  } finally {
    if (sequence === providerContextSaveSequence && providerID === appStore.activeRuntime) {
      providerContextSaving.value = false
    }
  }
}

function loadNativeProviderDetails(): void {
  if (isGrokMode.value) {
    void loadGrokCatalog()
    return
  }
  if (isClaudeMode.value) {
    void loadClaudeCatalog()
    return
  }
  if (isGeminiMode.value || isOpenCodeMode.value) {
    void loadExternalCatalog()
  }
}

function loadWhenReady(): void {
  void checkProviderConfiguration(false)
  loadNativeProviderDetails()
  if (appStore.isCodexMode && appStore.codexAvailable && !capabilitiesStore.capabilitiesLoading) {
    void capabilitiesStore.loadCapabilities()
  }
}

function applyRouteTab(tab: unknown): void {
  const value = String(tab || '')
  if (value === 'plugins' || value === 'skills' || value === 'apps' || value === 'mcp' || value === 'automation') {
    activeTab.value = value
  }
  if (value === 'runtime' || value === 'mcp' || value === 'skills' || value === 'plugins' || value === 'instructions') {
    grokTab.value = value
  }
  if (value === 'runtime' || value === 'mcp' || value === 'skills' || value === 'plugins' || value === 'agents' || value === 'hooks' || value === 'instructions') {
    claudeTab.value = value
  }
  if (value === 'runtime' || value === 'mcp' || value === 'instructions') {
    externalTab.value = value
  }
}

onMounted(() => {
  applyRouteTab(route.query.tab)
  loadWhenReady()
})
watch(() => appStore.codexAvailable, loadWhenReady)
watch(() => appStore.activeRuntime, () => {
  providerConfigurationRequestSequence += 1
  providerContextSaveSequence += 1
  providerRestartSequence += 1
  externalCatalogRequestSequence += 1
  providerConfigurationLoading.value = false
  providerContextSaving.value = false
  providerRestarting.value = false
  externalCatalogLoading.value = false
  providerConfiguration.value = null
  externalCatalog.value = null
  loadWhenReady()
})
watch([externalInstructionScope, externalMcpScope], () => hydrateExternalEditors())
watch(
  () => route.query.tab,
  applyRouteTab,
)

function setTab(tab: CapabilityTab): void {
  activeTab.value = tab
  query.value = ''
  visibleLimit.value = { ...visibleLimit.value, [tab]: PAGE_SIZE }
}

watch(query, () => {
  visibleLimit.value = { ...visibleLimit.value, [activeTab.value]: PAGE_SIZE }
})

function loadMore(): void {
  visibleLimit.value = { ...visibleLimit.value, [activeTab.value]: visibleLimit.value[activeTab.value] + PAGE_SIZE }
}

function logoFailed(key: string): boolean {
  return brokenLogos.value.has(key)
}

function markLogoFailed(key: string): void {
  if (brokenLogos.value.has(key)) return
  brokenLogos.value = new Set([...brokenLogos.value, key])
}

function mcpAuthLabel(status: string): string {
  const keys: Record<string, string> = {
    unsupported: 'capabilities.mcpAuthUnsupported',
    notLoggedIn: 'capabilities.mcpAuthRequired',
    bearerToken: 'capabilities.mcpAuthToken',
    oAuth: 'capabilities.mcpAuthOAuth',
    loading: 'capabilities.mcpChecking',
  }
  return t(keys[status] ?? 'capabilities.mcpConfigured')
}

function closeCapabilities(): void {
  // Preserve activeRuntime; only navigate back to the same product stack.
  void router.replace(route.query.from === 'settings' ? { name: 'settings' } : { name: 'workbench' })
}

function openExternalSettings(): void {
  void router.push({ name: 'settings', query: { section: 'agent', from: 'capabilities' } })
}

function openGrokSettings(): void {
  openExternalSettings()
}

function openGrokInstructionsSettings(): void {
  void router.push({ name: 'settings', query: { section: 'personalization', from: 'capabilities' } })
}

async function openConfig(): Promise<void> {
  try {
    await openGrokConfigFile()
  } catch (error) {
    notify('error', t('capabilities.grokOpenConfig'), error instanceof Error ? error.message : String(error))
  }
}

async function openHome(): Promise<void> {
  try {
    await openGrokHome()
  } catch (error) {
    notify('error', t('capabilities.grokOpenHome'), error instanceof Error ? error.message : String(error))
  }
}

function grokScopeLabel(scope: string): string {
  if (scope === 'project') return t('capabilities.grokScopeProject')
  if (scope === 'bundled') return t('capabilities.grokScopeBundled')
  return t('capabilities.grokScopeUser')
}

function openMcpEditor(server?: MCPServerView): void {
  mcpImportOpen.value = false
  const env = Object.entries(server?.env ?? {}).map(([key, value]) => ({
    id: crypto.randomUUID(),
    key,
    value,
  }))
  mcpForm.value = {
    originalName: server?.name ?? '',
    name: server?.name ?? '',
    enabled: server?.enabled ?? true,
    kind: server?.url ? 'url' : 'command',
    command: server?.command ?? '',
    args: [...(server?.args ?? [])],
    url: server?.url ?? '',
    transport: server?.transport || (server?.url ? 'http' : 'stdio'),
    env,
  }
  mcpEditorOpen.value = true
}

function addMcpArgument(): void {
  mcpForm.value.args.push('')
}

function removeMcpArgument(index: number): void {
  mcpForm.value.args.splice(index, 1)
}

function addMcpEnvironmentVariable(): void {
  mcpForm.value.env.push({ id: crypto.randomUUID(), key: '', value: '' })
}

function removeMcpEnvironmentVariable(id: string): void {
  mcpForm.value.env = mcpForm.value.env.filter((entry) => entry.id !== id)
}

async function saveMcpEditor(): Promise<void> {
  const form = mcpForm.value
  const env = Object.fromEntries(
    form.env
      .map((entry) => [entry.key.trim(), entry.value] as const)
      .filter(([key]) => key),
  )
  const ok = await capabilitiesStore.upsertMCPServer({
    name: form.name.trim(),
    enabled: form.enabled,
    command: form.kind === 'command' ? form.command.trim() : '',
    args: form.kind === 'command' ? form.args.map((arg) => arg.trim()).filter(Boolean) : [],
    url: form.kind === 'url' ? form.url.trim() : '',
    transport: form.transport.trim(),
    env,
  })
  if (ok) mcpEditorOpen.value = false
}

function openMcpImport(mode: 'visual' | 'json' = 'json'): void {
  mcpEditorOpen.value = false
  mcpImportMode.value = mode
  mcpImportOpen.value = true
}

watch(mcpImportJSON, (raw) => {
  if (mcpImportParseTimer) window.clearTimeout(mcpImportParseTimer)
  mcpImportParseTimer = 0
  mcpImportParsing.value = false
  mcpImportPreview.value = []
  mcpImportError.value = ''
  if (!raw.trim()) {
    return
  }
  mcpImportParsing.value = true
  mcpImportParseTimer = window.setTimeout(() => {
    mcpImportParseTimer = 0
    try {
      mcpImportPreview.value = parseMCPImportJSON(raw)
    } catch (error) {
      mcpImportPreview.value = []
      mcpImportError.value = error instanceof Error ? error.message : t('notifications.unexpected')
    } finally {
      mcpImportParsing.value = false
    }
  }, 180)
})

function onMcpImportPaste(event: ClipboardEvent): void {
  const pasted = event.clipboardData?.getData('text') ?? ''
  if (!pasted) return
  const target = event.currentTarget as HTMLTextAreaElement | null
  const start = target ? Math.max(0, target.selectionStart ?? mcpImportJSON.value.length) : mcpImportJSON.value.length
  const end = target ? Math.max(start, target.selectionEnd ?? start) : start
  const next = mcpImportJSON.value.slice(0, start) + pasted + mcpImportJSON.value.slice(end)
  // Wails/WebView can deliver paste without the passive useVModel update. Keep
  // the canonical ref authoritative so the preview watcher always sees it.
  event.preventDefault()
  if (next.length <= MCP_IMPORT_MAX_LENGTH) {
    mcpImportJSON.value = next
    requestAnimationFrame(() => {
      if (!target) return
      const caret = start + pasted.length
      target.setSelectionRange(caret, caret)
    })
    return
  }
  if (mcpImportParseTimer) window.clearTimeout(mcpImportParseTimer)
  mcpImportParseTimer = 0
  mcpImportParsing.value = false
  mcpImportPreview.value = []
  mcpImportError.value = t('capabilities.mcpImportFileTooLarge')
}

onUnmounted(() => {
  if (mcpImportParseTimer) window.clearTimeout(mcpImportParseTimer)
})

watch(() => mcpForm.value.kind, (kind) => {
  if (kind === 'command') {
    mcpForm.value.transport = 'stdio'
  } else if (mcpForm.value.transport === 'stdio' || !mcpForm.value.transport) {
    mcpForm.value.transport = 'http'
  }
})

async function readMcpImportFile(file: File | undefined): Promise<void> {
  if (!file) return
  if (mcpImportParseTimer) window.clearTimeout(mcpImportParseTimer)
  mcpImportParseTimer = 0
  mcpImportParsing.value = false
  if (file.size > MCP_IMPORT_MAX_LENGTH) {
    mcpImportSource.value = file.name
    mcpImportPreview.value = []
    mcpImportError.value = t('capabilities.mcpImportFileTooLarge')
    return
  }
  try {
    mcpImportSource.value = file.name
    const text = await file.text()
    if (mcpImportJSON.value !== text) {
      mcpImportJSON.value = text
      return
    }
    // Selecting the same file again does not trigger the watcher; re-parse it
    // so a previous file-size/read error cannot leave the import action stuck.
    mcpImportError.value = ''
    mcpImportPreview.value = parseMCPImportJSON(text)
  } catch (error) {
    mcpImportPreview.value = []
    mcpImportError.value = error instanceof Error ? error.message : t('notifications.unexpected')
  }
}

function onMcpImportFile(event: Event): void {
  const input = event.target as HTMLInputElement
  void readMcpImportFile(input.files?.[0])
  input.value = ''
}

function onMcpImportDrop(event: DragEvent): void {
  void readMcpImportFile(event.dataTransfer?.files?.[0])
}

function chooseMcpImportFile(): void {
  mcpImportFileInput.value?.click()
}

async function saveMcpImport(): Promise<void> {
  if (!mcpImportValid.value) return
  const saved = await capabilitiesStore.importMCPServers(mcpImportPreview.value)
  if (saved > 0) {
    mcpImportJSON.value = ''
    mcpImportPreview.value = []
    mcpImportError.value = ''
    mcpImportSource.value = ''
    mcpImportOpen.value = false
  }
}

async function deleteMcpServer(server: MCPServerView): Promise<void> {
  const confirmed = await dialogStore.confirm({
    title: t('capabilities.deleteMcp'),
    description: t('capabilities.deleteMcpConfirm', { name: server.title || server.name }),
    confirmLabel: t('common.delete'),
    destructive: true,
  })
  if (confirmed) await capabilitiesStore.deleteMCPServer(server.name)
}
</script>

<template>
  <div class="flex h-full w-full overflow-hidden bg-transparent text-foreground">
    <Dialog v-model:open="mcpImportOpen">
      <DialogContent class="gap-0 overflow-hidden p-0 sm:max-w-2xl">
        <DialogHeader class="border-b px-5 py-4 text-left">
          <DialogTitle class="flex items-center gap-2 text-[14px]">
            <Braces :size="15" class="text-primary" />
            {{ t('capabilities.importMcpTitle') }}
          </DialogTitle>
          <DialogDescription class="text-[11px] leading-5">
            {{ t('capabilities.importMcpJsonHint') }}
          </DialogDescription>
        </DialogHeader>

        <div class="max-h-[min(68vh,640px)] space-y-4 overflow-y-auto px-5 py-4">
          <div class="grid w-full grid-cols-2 rounded-lg border bg-muted/40 p-1" role="tablist" :aria-label="t('capabilities.importMcpTitle')">
            <Button
              type="button"
              size="sm"
              :variant="mcpImportMode === 'visual' ? 'secondary' : 'ghost'"
              class="h-9 text-[11px]"
              role="tab"
              :aria-selected="mcpImportMode === 'visual'"
              @click="mcpImportMode = 'visual'"
            >
              <FileUp :size="13" class="mr-1.5" />
              {{ t('capabilities.mcpImportFile') }}
            </Button>
            <Button
              type="button"
              size="sm"
              :variant="mcpImportMode === 'json' ? 'secondary' : 'ghost'"
              class="h-9 text-[11px]"
              role="tab"
              :aria-selected="mcpImportMode === 'json'"
              @click="mcpImportMode = 'json'"
            >
              <Braces :size="13" class="mr-1.5" />
              {{ t('capabilities.mcpImportPasteJson') }}
            </Button>
          </div>

          <div
            v-if="mcpImportMode === 'visual'"
            class="rounded-xl border border-dashed bg-muted/20 px-5 py-8 text-center"
            @dragover.prevent
            @drop.prevent="onMcpImportDrop"
          >
            <input
              ref="mcpImportFileInput"
              type="file"
              accept=".json,application/json"
              class="hidden"
              @change="onMcpImportFile"
            >
            <FileUp :size="26" class="mx-auto text-muted-foreground" />
            <p class="mt-3 text-[12px] font-medium">{{ t('capabilities.mcpImportDrop') }}</p>
            <p class="mt-1 text-[10px] leading-4 text-muted-foreground">{{ t('capabilities.mcpImportFileHint') }}</p>
            <Button type="button" variant="outline" size="sm" class="mt-4 h-8" @click="chooseMcpImportFile">
              {{ t('capabilities.mcpImportChoose') }}
            </Button>
          </div>

          <div v-else class="space-y-2">
            <div class="flex items-center justify-between gap-3">
              <Label for="mcp-import-json" class="text-[11px]">{{ t('capabilities.mcpImportPasteJson') }}</Label>
              <span class="text-[10px] tabular-nums text-muted-foreground">
                {{ mcpImportJSON.length.toLocaleString() }} / {{ MCP_IMPORT_MAX_LENGTH.toLocaleString() }}
              </span>
            </div>
            <Textarea
              id="mcp-import-json"
              v-model="mcpImportJSON"
              class="h-64 min-h-64 resize-y font-mono text-[11px] leading-5"
              :placeholder="t('capabilities.importMcpJsonPlaceholder')"
              :maxlength="MCP_IMPORT_MAX_LENGTH"
              :aria-invalid="Boolean(mcpImportError)"
              autofocus
              spellcheck="false"
              @paste="onMcpImportPaste"
            />
            <p class="text-[10px] leading-4 text-muted-foreground">{{ t('capabilities.mcpImportRawSafety') }}</p>
          </div>

          <div v-if="mcpImportError" class="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-[11px] leading-4 text-destructive" role="alert">
            {{ mcpImportError }}
          </div>

          <div v-else-if="mcpImportParsing" class="flex items-center gap-2 rounded-lg bg-muted/30 px-3 py-2 text-[11px] text-muted-foreground" role="status">
            <LoaderCircle :size="13" class="animate-spin" />
            {{ t('common.loading') }}
          </div>

          <div v-if="mcpImportPreview.length" class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <p class="text-[11px] font-medium">{{ t('capabilities.mcpImportPreview', { count: mcpImportPreview.length }) }}</p>
              <span v-if="mcpImportSource" class="max-w-56 truncate text-[10px] text-muted-foreground">{{ mcpImportSource }}</span>
            </div>
            <div class="max-h-48 space-y-1 overflow-y-auto rounded-lg border p-1.5">
              <div v-for="server in mcpImportPreview" :key="server.name" class="flex items-center gap-2 rounded-md px-2 py-1.5 text-[11px] hover:bg-muted/40">
                <Badge variant="outline" class="shrink-0 text-[9px]">{{ server.url ? 'HTTP' : 'STDIO' }}</Badge>
                <span class="min-w-0 flex-1 truncate font-medium">{{ server.name }}</span>
                <span class="max-w-[48%] truncate font-mono text-[9px] text-muted-foreground">{{ server.url || server.command }}</span>
              </div>
            </div>
          </div>
        </div>

        <DialogFooter class="border-t bg-background px-5 py-3">
          <Button type="button" variant="ghost" size="sm" @click="mcpImportOpen = false">{{ t('common.cancel') }}</Button>
          <Button type="button" size="sm" :disabled="!mcpImportValid || capabilitiesStore.capabilityMutation !== ''" @click="saveMcpImport">
            {{ t('capabilities.mcpImportConfirm', { count: mcpImportPreview.length }) }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Left tab rail on the gray shell -->
    <aside class="flex w-[248px] shrink-0 flex-col">
      <div class="space-y-2 px-3 pb-2 pt-1">
        <Button variant="ghost" class="h-8 w-full justify-start px-2 text-xs text-muted-foreground" @click="closeCapabilities">
          <ArrowLeft :size="14" class="mr-2" />
          {{ t('settings.backToApp') }}
        </Button>
        <div class="px-1">
          <p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            {{ isGrokMode
              ? t('capabilities.grokKicker')
              : isClaudeMode
                ? t('capabilities.claudeKicker')
                : (isGeminiMode || isOpenCodeMode)
                   ? t('capabilities.externalKicker')
                : t('capabilities.kicker') }}
          </p>
          <h1 class="text-[15px] font-semibold tracking-tight">
            {{ isGrokMode
              ? t('capabilities.grokTitle')
              : isClaudeMode
                ? t('capabilities.claudeTitle')
                : (isGeminiMode || isOpenCodeMode)
                   ? externalRuntimeName
                : t('capabilities.title') }}
          </h1>
          <p v-if="isGrokMode" class="mt-1 text-[10px] leading-4 text-muted-foreground">
            {{ t('capabilities.grokModeBanner') }}
          </p>
          <p v-else-if="isClaudeMode" class="mt-1 text-[10px] leading-4 text-muted-foreground">
            {{ t('capabilities.claudeModeBanner') }}
          </p>
          <p v-else-if="isGeminiMode || isOpenCodeMode" class="mt-1 text-[10px] leading-4 text-muted-foreground">
             {{ externalProvider?.message || t('capabilities.externalModeBanner', { runtime: externalRuntimeName }) }}
          </p>
        </div>
      </div>

      <nav
        v-if="!isGrokMode && !isClaudeMode && !isGeminiMode && !isOpenCodeMode"
        class="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-3"
        :aria-label="t('capabilities.title')"
      >
        <button
          v-for="tab in tabs"
          :key="tab.value"
          type="button"
          class="flex h-9 w-full items-center gap-2 rounded-lg px-2 text-left text-[12.5px] transition-colors"
          :class="activeTab === tab.value
            ? 'bg-card font-medium text-foreground shadow-sm'
            : 'text-muted-foreground hover:bg-foreground/[0.05] hover:text-foreground'"
          :aria-current="activeTab === tab.value ? 'page' : undefined"
          @click="setTab(tab.value)"
        >
          <component :is="tab.icon" :size="14" class="shrink-0 opacity-70" />
          <span class="min-w-0 flex-1 truncate">{{ tab.label }}</span>
          <span class="rounded-full bg-foreground/[0.06] px-1.5 text-[10px] tabular-nums text-muted-foreground">{{ tab.count }}</span>
        </button>
      </nav>
      <nav
        v-else-if="isClaudeMode"
        class="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-3"
        :aria-label="t('capabilities.claudeTitle')"
      >
        <button
          v-for="tab in claudeTabs"
          :key="tab.value"
          type="button"
          class="flex h-9 w-full items-center gap-2 rounded-lg px-2 text-left text-[12.5px] transition-colors"
          :class="claudeTab === tab.value
            ? 'bg-card font-medium text-foreground shadow-sm'
            : 'text-muted-foreground hover:bg-foreground/[0.05] hover:text-foreground'"
          @click="claudeTab = tab.value"
        >
          <component :is="tab.icon" :size="14" class="shrink-0 opacity-70" />
          <span class="min-w-0 flex-1 truncate">{{ tab.label }}</span>
          <span
            v-if="tab.count > 0"
            class="rounded-full bg-foreground/[0.06] px-1.5 text-[10px] tabular-nums text-muted-foreground"
          >{{ tab.count }}</span>
        </button>
      </nav>
      <nav
        v-else-if="isGrokMode"
        class="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-3"
        :aria-label="externalRuntimeName"
      >
        <button
          v-for="tab in grokTabs"
          :key="tab.value"
          type="button"
          class="flex h-9 w-full items-center gap-2 rounded-lg px-2 text-left text-[12.5px] transition-colors"
          :class="grokTab === tab.value
            ? 'bg-card font-medium text-foreground'
            : 'text-muted-foreground hover:bg-foreground/[0.05] hover:text-foreground'"
          @click="grokTab = tab.value"
        >
          <component :is="tab.icon" :size="14" class="shrink-0 opacity-70" />
          <span class="min-w-0 flex-1 truncate">{{ tab.label }}</span>
          <span
            v-if="tab.count > 0"
            class="rounded-full bg-foreground/[0.06] px-1.5 text-[10px] tabular-nums text-muted-foreground"
          >{{ tab.count }}</span>
        </button>
      </nav>
      <nav
        v-else-if="isGeminiMode || isOpenCodeMode"
        class="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-3"
        :aria-label="t('capabilities.grokTitle')"
      >
        <button
          v-for="tab in externalTabs"
          :key="tab.value"
          type="button"
          class="flex h-9 w-full items-center gap-2 rounded-lg px-2 text-left text-[12.5px] transition-colors"
          :class="externalTab === tab.value
            ? 'bg-card font-medium text-foreground shadow-sm'
            : 'text-muted-foreground hover:bg-foreground/[0.05] hover:text-foreground'"
          @click="externalTab = tab.value"
        >
          <component :is="tab.icon" :size="14" class="shrink-0 opacity-70" />
          <span class="min-w-0 flex-1 truncate">{{ tab.label }}</span>
          <span
            v-if="tab.count > 0"
            class="rounded-full bg-foreground/[0.06] px-1.5 text-[10px] tabular-nums text-muted-foreground"
          >{{ tab.count }}</span>
        </button>
      </nav>
    </aside>

    <!-- Rounded content card -->
    <div class="flex min-h-0 min-w-0 flex-1 flex-col pb-2 pr-2 pl-1.5 pt-0">
      <section class="workbench-card relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-[14px] border bg-card">
        <div class="shrink-0 border-b bg-muted/15 px-4 py-3">
          <div class="flex flex-wrap items-start gap-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <p class="text-[12px] font-semibold">{{ t('providerConfig.title') }}</p>
                <Badge variant="secondary" class="h-5 text-[9px] font-normal">{{ providerApplyLabel }}</Badge>
                <Badge variant="outline" class="h-5 max-w-56 truncate text-[9px] font-normal">{{ providerContextLabel }}</Badge>
              </div>
              <p class="mt-1 truncate font-mono text-[9px] text-muted-foreground">
                {{ providerConfiguration?.configPath || t('providerConfig.detecting') }}
              </p>
            </div>
            <div class="flex shrink-0 flex-wrap items-center gap-1.5">
              <Button variant="outline" size="sm" class="h-7 px-2 text-[10px]" :disabled="providerConfigurationLoading" @click="void checkProviderConfiguration(true)">
                <RefreshCw :size="11" class="mr-1.5" :class="{ 'animate-spin': providerConfigurationLoading }" />
                {{ t('providerConfig.check') }}
              </Button>
              <Button variant="outline" size="sm" class="h-7 px-2 text-[10px]" :disabled="providerConfigurationLoading" @click="void reloadProviderConfiguration()">
                <Settings2 :size="11" class="mr-1.5" />
                {{ t('providerConfig.reload') }}
              </Button>
              <Button
                v-if="providerConfiguration?.canRestart"
                variant="outline"
                size="sm"
                class="h-7 px-2 text-[10px]"
                :disabled="providerRestarting || providerBusy"
                @click="void restartProvider()"
              >
                <Power :size="11" class="mr-1.5" :class="{ 'animate-pulse': providerRestarting }" />
                {{ t('providerConfig.reconnect') }}
              </Button>
            </div>
          </div>

          <div
            v-if="providerConfiguration"
            class="mt-3 grid gap-3 lg:grid-cols-2 2xl:grid-cols-[minmax(280px,0.85fr)_minmax(360px,1fr)_minmax(420px,1.6fr)]"
          >
            <div class="rounded-lg border border-border/70 bg-background/70 p-3">
              <div class="flex items-start justify-between gap-2">
                <div>
                  <p class="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{{ t('providerConfig.contextWindow') }}</p>
                  <p class="mt-1 text-[11px] font-medium">{{ providerTokenModeLabel }}</p>
                </div>
                <Badge variant="outline" class="text-[9px] font-normal">
                  {{ providerContextPolicy?.tokens ? `${providerContextPolicy.tokens.toLocaleString()} tokens` : t('providerConfig.contextUnknown') }}
                </Badge>
              </div>
              <div v-if="providerContextPolicy?.writable" class="mt-2 flex items-center gap-2">
                <Input
                  v-model="providerContextTokens"
                  type="number"
                  :min="providerContextPolicy.tokenMinimum || 0"
                  :max="providerContextPolicy.tokenMaximum || undefined"
                  :step="providerContextPolicy.tokenStep || 1024"
                  class="h-8 min-w-0 flex-1 text-[11px]"
                  :placeholder="t('providerConfig.useNativeDefault')"
                />
                <Button type="button" variant="ghost" size="sm" class="h-8 px-2 text-[10px]" @click="providerContextTokens = ''">
                  {{ t('providerConfig.resetNative') }}
                </Button>
              </div>
              <p class="mt-2 text-[9px] leading-4 text-muted-foreground">
                {{ providerContextPolicy?.tokenMode === 'client-limit'
                  ? t('providerConfig.clientLimitHint')
                  : providerContextPolicy?.tokenMode === 'calculation-limit'
                    ? t('providerConfig.calculationLimitHint')
                    : providerContextPolicy?.writable
                      ? t('providerConfig.nativeOverrideHint')
                      : t('providerConfig.modelFixedHint') }}
              </p>
            </div>

            <div class="rounded-lg border border-border/70 bg-background/70 p-3">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <p class="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{{ t('providerConfig.autoCompact') }}</p>
                  <p class="mt-1 text-[11px] font-medium">
                    {{ providerContextPolicy?.autoCompactSupported ? providerThresholdLabel : t('providerConfig.notSupported') }}
                  </p>
                </div>
                <Switch
                  v-if="providerContextPolicy?.autoCompactToggleable"
                  :model-value="providerAutoCompactEnabled"
                  :aria-label="t('providerConfig.autoCompact')"
                  @update:model-value="providerAutoCompactEnabled = Boolean($event)"
                />
                <Badge v-else variant="outline" class="text-[9px] font-normal">
                  {{ providerContextPolicy?.autoCompactSupported ? t('providerConfig.nativeManaged') : t('providerConfig.notSupported') }}
                </Badge>
              </div>
              <div v-if="providerContextPolicy?.thresholdConfigurable" class="mt-3 grid grid-cols-[minmax(0,1fr)_112px] items-center gap-3">
                <input
                  type="range"
                  :min="providerContextPolicy.thresholdMinimum"
                  :max="providerContextPolicy.thresholdMaximum"
                  :step="providerContextPolicy.thresholdStep || 1"
                  :value="providerThresholdSliderValue"
                  class="policy-slider w-full"
                  :style="`--policy-progress: ${providerThresholdProgress}%`"
                  :disabled="providerContextPolicy.autoCompactToggleable && !providerAutoCompactEnabled"
                  :aria-label="providerThresholdLabel"
                  @input="providerCompactThreshold = String(($event.target as HTMLInputElement).value)"
                >
                <div class="relative">
                  <Input
                    v-model="providerCompactThreshold"
                    type="number"
                    :min="providerContextPolicy.thresholdMinimum"
                    :max="providerContextPolicy.thresholdMaximum"
                    :step="providerContextPolicy.thresholdStep || 1"
                    class="h-8 pr-7 text-[11px]"
                    :placeholder="t('providerConfig.useNativeDefault')"
                    :disabled="providerContextPolicy.autoCompactToggleable && !providerAutoCompactEnabled"
                  />
                  <span class="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-[8px] text-muted-foreground">
                    {{ providerContextPolicy.thresholdUnit === 'percent' ? '%' : '' }}
                  </span>
                </div>
              </div>
              <div v-if="providerContextPolicy?.thresholdScopeSupported" class="mt-3 rounded-md bg-muted/40 px-2.5 py-2">
                <div class="grid grid-cols-[minmax(0,1fr)_minmax(180px,220px)] items-center gap-3">
                  <Label for="provider-threshold-scope" class="text-[10px] text-muted-foreground">
                    {{ t('providerConfig.thresholdScope') }}
                  </Label>
                  <Select v-model="providerThresholdScope">
                    <SelectTrigger id="provider-threshold-scope" class="h-8 w-full text-[10px]">
                      <SelectValue :placeholder="t('providerConfig.scopeNative')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="native">{{ t('providerConfig.scopeNative') }}</SelectItem>
                      <SelectItem
                        v-for="scope in providerContextPolicy.thresholdScopeOptions"
                        :key="scope"
                        :value="scope"
                      >
                        {{ providerThresholdScopeLabel(scope) }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <p class="mt-1.5 text-[9px] leading-4 text-muted-foreground">
                  {{ t('providerConfig.thresholdScopeHint') }}
                </p>
              </div>
              <div v-if="providerContextPolicy?.pruneSupported" class="mt-2 flex items-center justify-between rounded-md bg-muted/40 px-2.5 py-2">
                <span class="text-[10px] text-muted-foreground">{{ t('providerConfig.pruneTools') }}</span>
                <Switch
                  :model-value="providerPruneEnabled"
                  :aria-label="t('providerConfig.pruneTools')"
                  @update:model-value="providerPruneEnabled = Boolean($event)"
                />
              </div>
              <p class="mt-2 text-[9px] leading-4 text-muted-foreground">
                {{ providerContextPolicy?.thresholdUnit === 'reserved-tokens'
                  ? t('providerConfig.reservedTokensHint')
                  : providerContextPolicy?.thresholdUnit === 'percent'
                    ? t('providerConfig.percentHint')
                    : t('providerConfig.tokenThresholdHint') }}
              </p>
            </div>

            <div class="flex min-w-0 flex-col justify-between gap-2 rounded-lg border border-border/70 bg-background/70 p-3 lg:col-span-2 2xl:col-span-1">
              <div>
                <p class="text-[10px] font-medium">{{ providerConfiguration.runtime.name }}</p>
                <p class="mt-1 text-[9px] leading-4 text-muted-foreground">{{ providerContextPolicy?.description }}</p>
              </div>
              <Button
                v-if="providerContextCanSave"
                size="sm"
                class="h-8 text-[10px]"
                :disabled="providerContextSaving"
                @click="void saveProviderContextPolicy()"
              >
                <LoaderCircle v-if="providerContextSaving" :size="11" class="mr-1.5 animate-spin" />
                {{ t('providerConfig.saveContext') }}
              </Button>
            </div>
          </div>
          <div v-else class="mt-3 flex h-20 items-center justify-center rounded-lg border border-dashed text-[11px] text-muted-foreground">
            <LoaderCircle v-if="providerConfigurationLoading" :size="13" class="mr-2 animate-spin" />
            {{ providerConfigurationLoading ? t('providerConfig.detecting') : t('providerConfig.contextUnknown') }}
          </div>
          <p v-if="providerConfiguration?.warnings?.length" class="mt-2 text-[9px] leading-4 text-amber-700 dark:text-amber-300">
            {{ providerConfiguration.warnings.join(' · ') }}
          </p>
        </div>

        <!-- Claude capability center (aligned with ~/.claude official layout) -->
        <template v-if="isClaudeMode">
          <header class="flex h-12 shrink-0 items-center gap-2 border-b px-4">
            <ClaudeIcon :size="16" class="opacity-80" />
            <h2 class="text-[14px] font-semibold">
              {{ claudeTabs.find((item) => item.value === claudeTab)?.label || t('capabilities.claudeTitle') }}
            </h2>
            <div class="flex-1" />
            <Button variant="outline" size="sm" class="h-8" :disabled="claudeCatalogLoading" @click="void loadClaudeCatalog()">
              <RefreshCw :size="13" class="mr-1.5" :class="{ 'animate-spin': claudeCatalogLoading }" />
              {{ t('common.refresh') }}
            </Button>
            <Button variant="outline" size="sm" class="h-8" @click="void openClaudeConfigFile()">
              {{ t('capabilities.claudeOpenConfig') }}
            </Button>
            <Button size="sm" class="h-8" @click="openClaudeSettings">
              <Settings2 :size="13" class="mr-1.5" />
              {{ t('capabilities.claudeOpenSettings') }}
            </Button>
          </header>
          <ScrollArea class="min-h-0 flex-1">
              <div class="mx-auto max-w-3xl space-y-4 p-5">
                <div v-if="claudeCatalogLoading && !claudeCatalog" class="flex items-center gap-2 py-16 text-[12px] text-muted-foreground">
                  <LoaderCircle :size="14" class="animate-spin" />
                  {{ t('capabilities.loading') }}
                </div>

                <template v-else-if="claudeTab === 'runtime'">
                  <Card>
                    <CardHeader class="pb-2">
                      <CardTitle class="text-[13px]">Claude Code CLI</CardTitle>
                    </CardHeader>
                    <CardContent class="space-y-3 text-[12px]">
                      <p class="text-muted-foreground">{{ t('capabilities.claudeRuntimeHint') }}</p>
                      <div class="grid gap-2 sm:grid-cols-2">
                        <div class="rounded-lg border px-3 py-2">
                          <p class="text-[10px] uppercase tracking-wide text-muted-foreground">CLI</p>
                          <p class="mt-1 font-medium">
                            {{ (claudeCatalog?.runtime.available || claudeProvider?.runtimeReady)
                              ? t('capabilities.ready')
                              : t('capabilities.unavailable') }}
                          </p>
                          <p class="mt-0.5 font-mono text-[10px] text-muted-foreground">
                            {{ claudeCatalog?.runtime.version || claudeProvider?.version || '—' }}
                          </p>
                        </div>
                        <div class="rounded-lg border px-3 py-2">
                          <p class="text-[10px] uppercase tracking-wide text-muted-foreground">Auth</p>
                          <p class="mt-1 font-medium">
                            {{ claudeCatalog?.runtime.authenticated
                              ? t('capabilities.ready')
                              : t('capabilities.unavailable') }}
                          </p>
                          <p class="mt-0.5 text-[10px] text-muted-foreground">
                            {{ claudeCatalog?.runtime.message || claudeProvider?.message || '—' }}
                          </p>
                        </div>
                      </div>
                      <div class="rounded-lg border px-3 py-2">
                        <p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('settings.model') }}</p>
                        <p class="mt-1 font-medium">{{ appStore.settings.claudeModel || claudeCatalog?.settings?.model || 'sonnet' }}</p>
                        <p class="mt-0.5 text-[10px] text-muted-foreground">
                          effort={{ appStore.settings.claudeEffort || 'high' }}
                          · permission={{ appStore.settings.claudePermissionMode || claudeCatalog?.settings?.permissionMode || 'acceptEdits' }}
                        </p>
                      </div>
                      <div v-if="claudeCatalog?.settings" class="rounded-lg border px-3 py-2 space-y-1">
                        <p class="text-[10px] uppercase tracking-wide text-muted-foreground">settings.json</p>
                        <p class="font-mono text-[10px] text-muted-foreground break-all">{{ claudeCatalog.settings.path }}</p>
                        <p v-if="claudeCatalog.settings.baseURL" class="font-mono text-[10px]">
                          ANTHROPIC_BASE_URL={{ claudeCatalog.settings.baseURL }}
                        </p>
                        <p class="text-[10px] text-muted-foreground">
                          allow={{ claudeCatalog.settings.allowRules }} · deny={{ claudeCatalog.settings.denyRules }}
                          <span v-if="claudeCatalog.settings.envKeys?.length"> · env={{ claudeCatalog.settings.envKeys.length }} keys</span>
                        </p>
                      </div>
                      <div class="flex flex-wrap gap-2">
                        <Button size="sm" variant="outline" class="h-8" @click="void openClaudeHome()">{{ t('capabilities.claudeOpenHome') }}</Button>
                        <Button size="sm" variant="outline" class="h-8" @click="void openClaudeConfigFile()">{{ t('capabilities.claudeOpenConfig') }}</Button>
                      </div>
                      <p class="text-[10px] text-muted-foreground">{{ t('capabilities.claudeNoCodexPlugins') }}</p>
                    </CardContent>
                  </Card>
                </template>

                <template v-else-if="claudeTab === 'mcp'">
                  <Card v-for="server in (claudeCatalog?.mcp || [])" :key="`${server.scope}:${server.name}`" class="p-4">
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <p class="text-[13px] font-medium">{{ server.name }}</p>
                        <p class="mt-1 font-mono text-[11px] text-muted-foreground">
                          {{ server.command || server.url || '—' }}
                        </p>
                        <p v-if="server.args" class="mt-0.5 line-clamp-2 font-mono text-[10px] text-muted-foreground/80">{{ server.args }}</p>
                        <p class="mt-1 text-[10px] text-muted-foreground">{{ server.transport || 'stdio' }} · {{ claudeScopeLabel(server.scope) }}</p>
                      </div>
                      <Badge :variant="server.enabled ? 'default' : 'outline'" class="text-[9px]">
                        {{ server.enabled ? t('capabilities.ready') : t('capabilities.disabled') }}
                      </Badge>
                    </div>
                  </Card>
                  <div
                    v-if="!(claudeCatalog?.mcp?.length)"
                    class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                  >
                    <p>{{ t('capabilities.claudeMcpEmpty') }}</p>
                    <Button class="mt-3" size="sm" variant="outline" @click="void openClaudeConfigFile()">{{ t('capabilities.claudeOpenConfig') }}</Button>
                  </div>
                </template>

                <template v-else-if="claudeTab === 'skills'">
                  <Card v-for="skill in (claudeCatalog?.skills || [])" :key="skill.path" class="p-4">
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <p class="text-[13px] font-medium">{{ skill.displayName || skill.name }}</p>
                        <p class="mt-1 text-[11px] text-muted-foreground">{{ skill.description || skill.path }}</p>
                      </div>
                      <Badge variant="outline" class="text-[9px]">{{ claudeScopeLabel(skill.scope) }}</Badge>
                    </div>
                  </Card>
                  <div
                    v-if="!(claudeCatalog?.skills?.length)"
                    class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                  >
                    {{ t('capabilities.claudeSkillsEmpty') }}
                  </div>
                </template>

                <template v-else-if="claudeTab === 'plugins'">
                  <Card v-for="plugin in (claudeCatalog?.plugins || [])" :key="plugin.path + plugin.name" class="p-4">
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <p class="text-[13px] font-medium">{{ plugin.name }}</p>
                        <p class="mt-1 font-mono text-[10px] text-muted-foreground">{{ plugin.path }}</p>
                      </div>
                      <div class="flex shrink-0 flex-col items-end gap-1">
                        <Badge v-if="plugin.version" variant="outline" class="text-[9px]">v{{ plugin.version }}</Badge>
                        <Badge variant="secondary" class="text-[9px]">{{ claudeScopeLabel(plugin.scope || 'user') }}</Badge>
                      </div>
                    </div>
                  </Card>
                  <div
                    v-if="!(claudeCatalog?.plugins?.length)"
                    class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                  >
                    {{ t('capabilities.claudePluginsEmpty') }}
                  </div>
                </template>

                <template v-else-if="claudeTab === 'agents'">
                  <p class="text-[11px] text-muted-foreground">{{ t('capabilities.claudeAgentsHint') }}</p>
                  <Card v-for="agent in (claudeCatalog?.agents || [])" :key="agent.path" class="p-4">
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <p class="text-[13px] font-medium">{{ agent.displayName || agent.name }}</p>
                        <p class="mt-1 text-[11px] text-muted-foreground">{{ agent.description || agent.path }}</p>
                      </div>
                      <Badge variant="outline" class="text-[9px]">{{ claudeScopeLabel(agent.scope) }}</Badge>
                    </div>
                  </Card>
                  <Card v-for="cmd in (claudeCatalog?.commands || [])" :key="cmd.path" class="p-4">
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <p class="text-[13px] font-medium">/{{ cmd.name }}</p>
                        <p class="mt-1 font-mono text-[10px] text-muted-foreground">{{ cmd.path }}</p>
                      </div>
                      <Badge variant="secondary" class="text-[9px]">{{ t('capabilities.claudeCommand') }} · {{ claudeScopeLabel(cmd.scope) }}</Badge>
                    </div>
                  </Card>
                  <div
                    v-if="!(claudeCatalog?.agents?.length) && !(claudeCatalog?.commands?.length)"
                    class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                  >
                    {{ t('capabilities.claudeAgentsEmpty') }}
                  </div>
                </template>

                <template v-else-if="claudeTab === 'hooks'">
                  <Card v-for="(hook, index) in (claudeCatalog?.hooks || [])" :key="`${hook.event}-${index}`" class="p-4">
                    <p class="text-[13px] font-medium">{{ hook.event }}</p>
                    <p class="mt-1 font-mono text-[11px] text-muted-foreground">{{ hook.command }}</p>
                    <p class="mt-1 font-mono text-[10px] text-muted-foreground/80">{{ hook.source }}</p>
                  </Card>
                  <div
                    v-if="!(claudeCatalog?.hooks?.length)"
                    class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                  >
                    {{ t('capabilities.claudeHooksEmpty') }}
                  </div>
                </template>

                <template v-else-if="claudeTab === 'instructions'">
                  <Card class="p-4 space-y-3 text-[12px]">
                    <div>
                      <p class="text-[13px] font-medium">{{ t('settings.claudeGlobalInstructions') }}</p>
                      <p class="mt-1 text-muted-foreground">{{ t('settings.claudeGlobalInstructionsHint') }}</p>
                      <code class="mt-2 block rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-[11px]">
                        {{ claudeCatalog?.globalInstructions?.path || '~/.claude/CLAUDE.md' }}
                      </code>
                      <p class="mt-1 text-[10px] text-muted-foreground">
                        {{ claudeCatalog?.globalInstructions?.exists
                          ? (claudeCatalog.globalInstructions.emptyFile ? t('settings.instructionsFileEmpty') : t('settings.instructionsFileHasContent'))
                          : t('settings.instructionsFileMissing') }}
                      </p>
                    </div>
                    <div>
                      <p class="text-[13px] font-medium">{{ t('settings.claudeProjectInstructions') }}</p>
                      <p class="mt-1 text-muted-foreground">{{ t('settings.claudeProjectInstructionsHint') }}</p>
                      <code class="mt-2 block rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-[11px]">
                        {{ claudeCatalog?.projectInstructions?.path || 'CLAUDE.md' }}
                      </code>
                    </div>
                    <Button size="sm" class="h-8" @click="openClaudeInstructionsSettings">
                      {{ t('capabilities.claudeOpenSettings') }}
                    </Button>
                  </Card>
                </template>
              </div>
          </ScrollArea>
        </template>

        <!-- Grok capability center (no Codex plugin catalog) -->
        <template v-else-if="isGrokMode">
          <header class="flex h-12 shrink-0 items-center gap-2 border-b px-4">
            <GrokIcon :size="16" class="opacity-80" />
            <h2 class="text-[14px] font-semibold">{{ grokTabs.find((item) => item.value === grokTab)?.label || t('capabilities.grokTitle') }}</h2>
            <div class="flex-1" />
            <Button variant="outline" size="sm" class="h-8" :disabled="grokCatalogLoading" @click="void loadGrokCatalog()">
              <RefreshCw :size="13" class="mr-1.5" :class="{ 'animate-spin': grokCatalogLoading }" />
              {{ t('common.refresh') }}
            </Button>
            <Button variant="outline" size="sm" class="h-8" @click="void openConfig()">
              {{ t('capabilities.grokOpenConfig') }}
            </Button>
            <Button size="sm" class="h-8" @click="openGrokSettings">
              <Settings2 :size="13" class="mr-1.5" />
              {{ t('capabilities.grokOpenSettings') }}
            </Button>
          </header>
          <ScrollArea class="min-h-0 flex-1">
            <div class="mx-auto max-w-3xl space-y-4 p-5">
              <div v-if="grokCatalogLoading && !grokCatalog" class="flex items-center gap-2 py-16 text-[12px] text-muted-foreground">
                <LoaderCircle :size="14" class="animate-spin" />
                {{ t('capabilities.loading') }}
              </div>

              <template v-else-if="grokTab === 'runtime'">
                <Card>
                  <CardHeader class="pb-2">
                    <CardTitle class="text-[13px]">Grok Build / API</CardTitle>
                  </CardHeader>
                  <CardContent class="space-y-3 text-[12px]">
                    <p class="text-muted-foreground">{{ t('capabilities.grokRuntimeHint') }}</p>
                    <div class="grid gap-2 sm:grid-cols-2">
                      <div class="rounded-lg border px-3 py-2">
                        <p class="text-[10px] uppercase tracking-wide text-muted-foreground">Build</p>
                        <p class="mt-1 font-medium">
                          {{ (grokCatalog?.runtime || grokStore.runtime).buildAvailable ? t('capabilities.ready') : t('capabilities.unavailable') }}
                        </p>
                        <p v-if="(grokCatalog?.runtime || grokStore.runtime).buildVersion" class="mt-0.5 font-mono text-[10px] text-muted-foreground">
                          {{ (grokCatalog?.runtime || grokStore.runtime).buildVersion }}
                        </p>
                      </div>
                      <div class="rounded-lg border px-3 py-2">
                        <p class="text-[10px] uppercase tracking-wide text-muted-foreground">API</p>
                        <p class="mt-1 font-medium">
                          {{ (grokCatalog?.runtime || grokStore.runtime).apiConfigured || appStore.settings.grokAPIKey
                            ? t('capabilities.ready')
                            : t('capabilities.unavailable') }}
                        </p>
                        <p class="mt-0.5 text-[10px] text-muted-foreground">
                          {{ appStore.settings.grokAPIBaseURL || 'https://api.x.ai/v1' }}
                        </p>
                      </div>
                    </div>
                    <div class="rounded-lg border px-3 py-2">
                      <p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('settings.model') }}</p>
                      <p class="mt-1 font-medium">
                        {{ appStore.settings.grokBackend === 'api'
                          ? (appStore.settings.grokAPIModel || 'grok-4.5')
                          : (appStore.settings.grokBuildModel || 'grok-4.5') }}
                      </p>
                      <p class="mt-0.5 text-[10px] text-muted-foreground">
                        backend={{ appStore.settings.grokBackend || 'build' }} · effort={{ appStore.settings.grokEffort || 'high' }}
                      </p>
                    </div>
                    <div v-if="grokProvider?.models?.length" class="rounded-lg border px-3 py-2">
                      <p class="text-[10px] uppercase tracking-wide text-muted-foreground">Model catalog</p>
                      <div class="mt-2 flex flex-wrap gap-1.5">
                        <Badge
                          v-for="model in grokProvider.models"
                          :key="model.model"
                          variant="secondary"
                          class="text-[10px] font-normal"
                        >
                          {{ model.displayName || model.model }}
                        </Badge>
                      </div>
                    </div>
                    <div class="flex flex-wrap gap-2">
                      <Button size="sm" variant="outline" class="h-8" @click="void openHome()">{{ t('capabilities.grokOpenHome') }}</Button>
                    </div>
                    <p class="text-[10px] text-muted-foreground">{{ t('capabilities.grokNoCodexPlugins') }}</p>
                  </CardContent>
                </Card>
              </template>

              <template v-else-if="grokTab === 'mcp'">
                <Card v-for="server in (grokCatalog?.mcp || [])" :key="server.name" class="p-4">
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <p class="text-[13px] font-medium">{{ server.name }}</p>
                      <p class="mt-1 font-mono text-[11px] text-muted-foreground">
                        {{ server.command || server.url || '—' }}
                      </p>
                      <p v-if="server.args" class="mt-0.5 line-clamp-2 font-mono text-[10px] text-muted-foreground/80">{{ server.args }}</p>
                    </div>
                    <Badge :variant="server.enabled ? 'default' : 'outline'" class="text-[9px]">
                      {{ server.enabled ? t('capabilities.ready') : t('capabilities.disabled') }}
                    </Badge>
                  </div>
                </Card>
                <div
                  v-if="!(grokCatalog?.mcp?.length)"
                  class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                >
                  <p>{{ t('capabilities.grokMcpEmpty') }}</p>
                  <Button class="mt-3" size="sm" variant="outline" @click="void openConfig()">{{ t('capabilities.grokOpenConfig') }}</Button>
                </div>
              </template>

              <template v-else-if="grokTab === 'skills'">
                <Card v-for="skill in (grokCatalog?.skills || [])" :key="skill.path" class="p-4">
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <p class="text-[13px] font-medium">{{ skill.displayName || skill.name }}</p>
                      <p class="mt-1 text-[11px] text-muted-foreground">{{ skill.description || skill.path }}</p>
                    </div>
                    <Badge variant="outline" class="text-[9px]">{{ grokScopeLabel(skill.scope) }}</Badge>
                  </div>
                </Card>
                <div
                  v-if="!(grokCatalog?.skills?.length)"
                  class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                >
                  {{ t('capabilities.grokSkillsEmpty') }}
                </div>
              </template>

              <template v-else-if="grokTab === 'plugins'">
                <Card v-for="plugin in (grokCatalog?.plugins || [])" :key="plugin.path" class="p-4">
                  <p class="text-[13px] font-medium">{{ plugin.name }}</p>
                  <p class="mt-1 font-mono text-[10px] text-muted-foreground">{{ plugin.path }}</p>
                </Card>
                <div
                  v-if="!(grokCatalog?.plugins?.length)"
                  class="rounded-lg border border-dashed px-4 py-10 text-center text-[12px] text-muted-foreground"
                >
                  {{ t('capabilities.grokPluginsEmpty') }}
                </div>
              </template>

              <template v-else-if="grokTab === 'instructions'">
                <Card class="p-4 space-y-3 text-[12px]">
                  <div>
                    <p class="text-[13px] font-medium">{{ t('settings.grokGlobalInstructions') }}</p>
                    <p class="mt-1 text-muted-foreground">{{ t('settings.grokGlobalInstructionsHint') }}</p>
                    <code class="mt-2 block rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-[11px]">
                      {{ grokCatalog?.globalInstructions?.path || '~/.grok/AGENTS.md' }}
                    </code>
                    <p class="mt-1 text-[10px] text-muted-foreground">
                      {{ grokCatalog?.globalInstructions?.exists
                        ? (grokCatalog.globalInstructions.emptyFile ? t('settings.instructionsFileEmpty') : t('settings.instructionsFileHasContent'))
                        : t('settings.instructionsFileMissing') }}
                    </p>
                  </div>
                  <div>
                    <p class="text-[13px] font-medium">{{ t('settings.grokProjectInstructions') }}</p>
                    <p class="mt-1 text-muted-foreground">{{ t('settings.grokProjectInstructionsHint') }}</p>
                    <code class="mt-2 block rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-[11px]">
                      {{ grokCatalog?.projectInstructions?.path || t('settings.projectInstructionsUnavailable') }}
                    </code>
                  </div>
                  <Button size="sm" class="h-8" @click="openGrokInstructionsSettings">
                    {{ t('capabilities.grokOpenSettings') }}
                  </Button>
                </Card>
              </template>
            </div>
          </ScrollArea>
        </template>

        <template v-else-if="isGeminiMode || isOpenCodeMode">
          <header class="flex h-12 shrink-0 items-center gap-2 border-b px-4">
            <GeminiIcon v-if="isGeminiMode" :size="16" class="opacity-80" />
            <OpenCodeIcon v-else :size="16" class="opacity-80" />
            <h2 class="text-[14px] font-semibold">{{ externalTabs.find((item) => item.value === externalTab)?.label }}</h2>
            <div class="flex-1" />
            <Button variant="outline" size="sm" class="h-8" :disabled="externalCatalogLoading" @click="void loadExternalCatalog()">
              <RefreshCw :size="13" class="mr-1.5" :class="{ 'animate-spin': externalCatalogLoading }" />{{ t('common.refresh') }}
            </Button>
             <Button size="sm" class="h-8" @click="openExternalSettings">
               <Settings2 :size="13" class="mr-1.5" />{{ t('capabilities.externalOpenSettings') }}
            </Button>
          </header>
          <ScrollArea class="min-h-0 flex-1">
            <div class="mx-auto max-w-4xl space-y-4 p-5">
              <div v-if="externalCatalogLoading && !externalCatalog" class="flex items-center gap-2 py-16 text-[12px] text-muted-foreground">
                <LoaderCircle :size="14" class="animate-spin" />{{ t('capabilities.externalLoading', { runtime: externalRuntimeName }) }}
              </div>

              <template v-else-if="externalTab === 'runtime'">
                <Card>
                  <CardHeader class="pb-2"><CardTitle class="text-[13px]">{{ t('capabilities.externalRuntimeTitle', { runtime: externalRuntimeName }) }}</CardTitle></CardHeader>
                  <CardContent class="space-y-3 text-[12px]">
                    <p class="text-muted-foreground">{{ externalCatalog?.readOnlyNotice || externalProvider?.message || t('capabilities.cliRuntimeStatus', { runtime: externalRuntimeName }) }}</p>
                    <div class="grid gap-2 sm:grid-cols-3">
                      <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('capabilities.externalCli') }}</p><p class="mt-1 font-medium">{{ externalProvider?.runtimeReady ? t('capabilities.ready') : t('capabilities.unavailable') }}</p><p class="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">{{ externalProvider?.version || externalProvider?.executable || '—' }}</p></div>
                      <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ isGeminiMode ? t('capabilities.externalAuth') : t('capabilities.externalActiveProvider') }}</p><p class="mt-1 font-medium">{{ externalCatalog?.activeProvider || '—' }}</p><p class="mt-0.5 truncate text-[10px] text-muted-foreground">{{ externalCatalog?.providerSource || '—' }}</p></div>
                      <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('capabilities.externalNativeUsage') }}</p><p class="mt-1 font-medium tabular-nums">{{ externalCatalog?.usage?.totalTokens?.toLocaleString() || 0 }} {{ t('capabilities.tokens') }}</p><p class="mt-0.5 text-[10px] text-muted-foreground">{{ externalCatalog?.usage?.sessions || 0 }} {{ t('capabilities.sessions') }} · {{ t('capabilities.lifetime') }}</p></div>
                    </div>
                    <div v-if="isOpenCodeMode" class="space-y-2">
                       <p class="text-[11px] font-medium">{{ t('capabilities.openCodeProviders') }}</p>
                      <div class="grid gap-2 sm:grid-cols-2">
                        <div v-for="provider in (externalCatalog?.providers || [])" :key="provider.id" class="rounded-lg border px-3 py-2">
                           <div class="flex items-center gap-2"><span class="font-medium">{{ provider.name }}</span><Badge :variant="provider.configured ? 'default' : 'outline'" class="text-[9px]">{{ provider.configured ? t('capabilities.configured') : t('capabilities.catalog') }}</Badge></div>
                           <p class="mt-1 text-[10px] text-muted-foreground">{{ provider.id }} · {{ provider.models?.length || 0 }} {{ t('capabilities.models') }} · {{ provider.authenticated ? t('capabilities.credentialReady') : t('capabilities.credentialMissing') }}</p>
                        </div>
                      </div>
                    </div>
                    <div>
                       <div class="mb-2 flex items-center justify-between"><p class="text-[11px] font-medium">{{ isOpenCodeMode ? t('capabilities.externalProviderCatalog') : t('capabilities.geminiModels') }}</p><span class="text-[10px] text-muted-foreground">{{ externalCatalog?.models?.length || 0 }} {{ t('capabilities.models') }}</span></div>
                      <div class="max-h-64 space-y-1 overflow-y-auto rounded-lg border p-1.5">
                        <div v-for="model in (externalCatalog?.models || [])" :key="model.model" class="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-muted/40">
                           <span class="min-w-0 flex-1 truncate font-mono text-[11px]">{{ model.model }}</span><Badge v-if="model.isDefault" variant="secondary" class="text-[9px]">{{ t('common.default') }}</Badge><span v-if="model.contextWindow" class="text-[9px] tabular-nums text-muted-foreground">{{ Math.round(model.contextWindow / 1000) }}K {{ t('capabilities.context') }}</span>
                        </div>
                      </div>
                    </div>
                    <div class="grid gap-2 sm:grid-cols-4">
                       <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('capabilities.externalNativeHistory') }}</p><p class="mt-1 text-lg font-semibold tabular-nums">{{ externalCatalog?.sessions?.length || 0 }}</p><p class="text-[10px] text-muted-foreground">{{ isGeminiMode ? t('capabilities.geminiHistorySource') : t('capabilities.openCodeHistorySource') }}</p></div>
                       <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('capabilities.messages') }}</p><p class="mt-1 text-lg font-semibold tabular-nums">{{ externalCatalog?.usage?.messages || 0 }}</p><p class="text-[10px] text-muted-foreground">{{ t('capabilities.last30Days') }}</p></div>
                       <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('capabilities.cost') }}</p><p class="mt-1 text-lg font-semibold tabular-nums">{{ externalCatalog?.usage?.cost?.toFixed(4) || '0.0000' }}</p><p class="text-[10px] text-muted-foreground">{{ t('capabilities.nativeReported') }}</p></div>
                       <div class="rounded-lg border px-3 py-2"><p class="text-[10px] uppercase tracking-wide text-muted-foreground">{{ t('capabilities.tokenBreakdown') }}</p><p class="mt-1 text-[10px] text-muted-foreground">{{ t('capabilities.tokenBreakdownValue', { input: externalCatalog?.usage?.inputTokens?.toLocaleString() || 0, output: externalCatalog?.usage?.outputTokens?.toLocaleString() || 0, reasoning: externalCatalog?.usage?.reasoningTokens?.toLocaleString() || 0, cache: externalCatalog?.usage?.cachedTokens?.toLocaleString() || 0 }) }}</p></div>
                    </div>
                    <div v-if="externalCatalog?.usage?.byModel?.length" class="space-y-1.5">
                       <div class="flex items-center justify-between"><p class="text-[11px] font-medium">{{ t('capabilities.usageByModel') }}</p><span class="text-[10px] text-muted-foreground">{{ t('capabilities.nativeSource') }}</span></div>
                      <div v-for="item in (externalCatalog?.usage?.byModel || [])" :key="`${item.provider}:${item.model}`" class="flex items-center gap-2 rounded-md border px-3 py-2 text-[10px]">
                        <span class="min-w-0 flex-1 truncate font-mono">{{ item.model }}</span>
                         <span class="shrink-0 tabular-nums text-muted-foreground">{{ item.totalTokens?.toLocaleString() || 0 }} {{ t('capabilities.tokens') }}</span>
                        <span class="shrink-0 tabular-nums text-muted-foreground">${{ item.cost?.toFixed(4) || '0.0000' }}</span>
                      </div>
                    </div>
                  </CardContent>
                </Card>
                <Card v-if="externalCatalog?.sessions?.length">
                   <CardHeader class="pb-2"><CardTitle class="text-[13px]">{{ t('capabilities.externalNativeHistory') }}</CardTitle></CardHeader>
                  <CardContent class="space-y-1 text-[11px]">
                    <div v-for="session in (externalCatalog?.sessions || []).slice(0, 20)" :key="session.id" class="flex items-center gap-3 rounded-md border px-3 py-2">
                      <div class="min-w-0 flex-1"><p class="truncate font-medium">{{ session.title || session.id }}</p><p class="truncate text-[10px] text-muted-foreground">{{ session.preview || session.model || session.id }}</p></div><Badge variant="outline" class="shrink-0 font-mono text-[9px]">{{ session.provider || (isGeminiMode ? 'gemini' : 'opencode') }}</Badge>
                    </div>
                  </CardContent>
                </Card>
              </template>

              <template v-else-if="externalTab === 'mcp'">
                <Card>
                   <CardHeader class="pb-2"><CardTitle class="text-[13px]">{{ t('capabilities.externalMcpTitle', { runtime: externalRuntimeName }) }}</CardTitle></CardHeader>
                  <CardContent class="space-y-3">
                     <p class="text-[11px] text-muted-foreground">{{ t('capabilities.nativeConfigFile') }}：<code class="font-mono">{{ externalCatalog?.configPath }}</code></p>
                    <div class="grid grid-cols-2 rounded-md border bg-muted/40 p-0.5">
                       <Button type="button" size="xs" :variant="externalMcpScope === 'global' ? 'secondary' : 'ghost'" @click="externalMcpScope = 'global'">{{ t('capabilities.globalMcp') }}</Button>
                       <Button type="button" size="xs" :variant="externalMcpScope === 'project' ? 'secondary' : 'ghost'" @click="externalMcpScope = 'project'">{{ t('capabilities.projectMcp') }}</Button>
                    </div>
                    <p class="truncate font-mono text-[10px] text-muted-foreground">{{ externalMcpScope === 'global' ? externalCatalog?.configPath : (externalCatalog?.mcp?.find((item) => item.configPath !== externalCatalog?.configPath)?.configPath || t('capabilities.projectConfig')) }}</p>
                    <div v-if="externalCatalog?.mcp?.filter((item) => externalMcpScope === 'global' ? item.configPath === externalCatalog?.configPath : item.configPath !== externalCatalog?.configPath).length" class="space-y-1">
                      <div v-for="server in (externalCatalog?.mcp || []).filter((item) => externalMcpScope === 'global' ? item.configPath === externalCatalog?.configPath : item.configPath !== externalCatalog?.configPath)" :key="server.name" class="flex items-center gap-3 rounded-md border px-3 py-2 text-[11px]"><span class="min-w-0 flex-1 truncate font-medium">{{ server.name }}</span><span class="max-w-[45%] truncate font-mono text-[10px] text-muted-foreground">{{ server.command || server.url || '—' }}</span><Badge :variant="server.enabled ? 'default' : 'outline'" class="text-[9px]">{{ server.enabled ? t('capabilities.enabled') : t('capabilities.disabled') }}</Badge></div>
                    </div>
                     <p v-else class="rounded-md border border-dashed px-3 py-5 text-center text-[11px] text-muted-foreground">{{ t('capabilities.externalMcpEmpty', { runtime: externalRuntimeName }) }}</p>
                    <Textarea v-model="externalMcpJSON" class="min-h-52 font-mono text-[11px] leading-5" spellcheck="false" :placeholder="isGeminiMode ? '{ &quot;mcpServers&quot;: {} }' : '{ &quot;mcp&quot;: {} }'" />
                     <div class="flex justify-end"><Button size="sm" :disabled="externalMcpSaving || !externalMcpJSON.trim()" @click="void saveExternalMCP()"><LoaderCircle v-if="externalMcpSaving" :size="13" class="mr-1.5 animate-spin" />{{ t('capabilities.saveNativeMcp') }}</Button></div>
                  </CardContent>
                </Card>
              </template>

              <template v-else>
                <Card>
                   <CardHeader class="pb-2"><CardTitle class="text-[13px]">{{ t('capabilities.externalInstructionsTitle', { runtime: externalRuntimeName }) }}</CardTitle></CardHeader>
                  <CardContent class="space-y-3">
                     <div class="grid grid-cols-2 rounded-md border bg-muted/40 p-0.5"><Button type="button" size="xs" :variant="externalInstructionScope === 'global' ? 'secondary' : 'ghost'" @click="externalInstructionScope = 'global'">{{ t('settings.instructionsGlobal') }}</Button><Button type="button" size="xs" :variant="externalInstructionScope === 'project' ? 'secondary' : 'ghost'" @click="externalInstructionScope = 'project'">{{ t('settings.instructionsProject') }}</Button></div>
                    <p class="text-[10px] text-muted-foreground">{{ externalInstructionScope === 'global' ? externalCatalog?.globalInstructions?.path : externalCatalog?.projectInstructions?.path }}</p>
                    <Textarea v-model="externalInstructionDraft" class="min-h-72 font-mono text-[11px] leading-5" spellcheck="false" :placeholder="isGeminiMode ? t('capabilities.geminiInstructionsPlaceholder') : t('capabilities.openCodeInstructionsPlaceholder')" />
                     <div class="flex justify-end"><Button size="sm" @click="void saveExternalInstructions()">{{ t('settings.saveNativeInstructions') }}</Button></div>
                     <p v-if="externalCatalog?.configInstructions" class="rounded-md border bg-muted/20 px-3 py-2 text-[10px] leading-4 text-muted-foreground">{{ t('capabilities.openCodeConfigInstructions') }}: {{ externalCatalog.configInstructions }}</p>
                  </CardContent>
                </Card>
              </template>
            </div>
          </ScrollArea>
        </template>
        <template v-else>
        <header class="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <div class="relative min-w-0 flex-1">
            <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input v-model="query" type="search" :placeholder="t('capabilities.search')" class="h-8 pl-8 text-xs" />
          </div>
          <Button v-if="activeTab === 'mcp'" variant="outline" size="sm" class="h-8" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="openMcpEditor()">
            <Plus :size="13" class="mr-1.5" />
            {{ t('capabilities.addMcp') }}
          </Button>
          <Button v-if="activeTab === 'mcp'" variant="outline" size="sm" class="h-8" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="openMcpImport('json')">
            <Braces :size="13" class="mr-1.5" />
            {{ t('capabilities.mcpImportPasteJson') }}
          </Button>
          <Button v-if="activeTab === 'mcp'" variant="outline" size="sm" class="h-8" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="capabilitiesStore.refreshMCPServers()">
            <RefreshCw :size="13" class="mr-1.5" />
            {{ t('capabilities.reloadMcp') }}
          </Button>
          <Button variant="outline" size="sm" class="h-8" :disabled="capabilitiesStore.capabilitiesLoading" @click="capabilitiesStore.loadCapabilities(true)">
            <RefreshCw :size="14" class="mr-1.5" :class="{ 'animate-spin': capabilitiesStore.capabilitiesLoading }" />
            {{ t('common.refresh') }}
          </Button>
        </header>

        <Tabs v-model="activeTab" class="flex min-h-0 flex-1 flex-col overflow-hidden">
          <ScrollArea class="min-h-0 flex-1 overflow-hidden">
            <div class="mx-auto max-w-5xl p-4">
              <div class="mb-5 grid grid-cols-2 gap-x-8 gap-y-4 border-b pb-5 lg:grid-cols-4">
                <div v-for="stat in capabilityStats" :key="stat.label" class="min-w-0">
                  <div class="mb-1.5 flex items-end justify-between gap-2">
                    <span class="truncate text-[10px] font-medium text-muted-foreground">{{ stat.label }}</span>
                    <strong class="text-sm tabular-nums">{{ stat.value }}<span class="font-normal text-muted-foreground">/{{ stat.total }}</span></strong>
                  </div>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div class="h-full rounded-full bg-primary transition-[width] duration-300" :style="{ width: `${stat.total ? Math.round(stat.value / stat.total * 100) : 0}%` }" />
                  </div>
                </div>
              </div>
              <div v-if="!appStore.codexAvailable" class="rounded-lg border border-warning/30 bg-warning/10 p-4 text-xs text-warning">
                {{ t('capabilities.connectionRequired') }}
              </div>

              <div v-else-if="activeError" class="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">
                {{ activeError }}
              </div>

            <TabsContent value="plugins" class="mt-0 space-y-3">
              <Card v-for="plugin in visiblePlugins" :key="plugin.id" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="flex items-start gap-3 py-3">
                  <div class="flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-xl border bg-muted text-primary">
                    <img
                      v-if="plugin.logoUrl && !logoFailed(`plugin:${plugin.id}`)"
                      :src="plugin.logoUrl"
                      class="size-full object-cover"
                      alt=""
                      @error="markLogoFailed(`plugin:${plugin.id}`)"
                    >
                    <Blocks v-else :size="18" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <div class="flex items-center gap-2">
                      <span class="truncate text-xs font-semibold">{{ plugin.displayName }}</span>
                      <Badge v-if="plugin.version" variant="outline" class="text-[9px]">v{{ plugin.version }}</Badge>
                    </div>
                    <p class="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{{ plugin.description || plugin.name }}</p>
                    <p class="mt-1 text-[10px] text-muted-foreground">{{ plugin.developerName || plugin.marketplaceName }} · {{ plugin.sourceType }}</p>
                  </div>
                  <Button v-if="plugin.installed" variant="outline" size="sm" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="capabilitiesStore.uninstallPlugin(plugin.id)">
                    <Unplug :size="13" class="mr-1.5" />
                    {{ t('capabilities.uninstall') }}
                  </Button>
                  <Button v-else size="sm" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="capabilitiesStore.installPlugin(plugin.id)">
                    <Blocks :size="13" class="mr-1.5" />
                    {{ t('capabilities.install') }}
                  </Button>
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="skills" class="mt-0 space-y-3">
              <Card v-for="skill in visibleSkills" :key="skill.path" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="flex items-center gap-3 py-3">
                  <div class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                    <Sparkles :size="16" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-xs font-semibold">{{ skill.displayName }}</p>
                    <p class="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{{ skill.shortDescription || skill.description }}</p>
                    <p class="mt-1 text-[10px] text-muted-foreground">{{ skill.scope }} · {{ skill.path }}</p>
                  </div>
                  <Switch
                    :checked="skill.enabled"
                    :disabled="capabilitiesStore.capabilityMutation !== ''"
                    @update:checked="capabilitiesStore.setSkillEnabled(skill.name, skill.path, $event as boolean)"
                  />
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="apps" class="mt-0 space-y-3">
              <p class="text-[11px] text-muted-foreground">{{ t('capabilities.appsManageHint') }}</p>
              <Card v-for="app in visibleApps" :key="app.id" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="flex items-center gap-3 py-3">
                  <div class="flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-xl border bg-muted text-primary">
                    <img
                      v-if="app.logoUrl && !logoFailed(`app:${app.id}`)"
                      :src="app.logoUrl"
                      class="size-full object-cover"
                      alt=""
                      @error="markLogoFailed(`app:${app.id}`)"
                    >
                    <AppWindow v-else :size="18" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-xs font-semibold">{{ app.name }}</p>
                    <p class="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{{ app.description }}</p>
                    <p class="mt-1 text-[10px] text-muted-foreground">{{ app.pluginNames.join(' · ') }}</p>
                  </div>
                  <Switch
                    :checked="app.enabled"
                    :disabled="capabilitiesStore.capabilityMutation !== ''"
                    @update:checked="capabilitiesStore.setAppEnabled(app.id, $event as boolean)"
                  />
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="mcp" class="mt-0 space-y-3">
              <Card v-if="mcpEditorOpen" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="space-y-4 py-4">
                  <div class="grid gap-2 sm:grid-cols-2">
                    <div class="space-y-1">
                      <Label class="text-[11px]">{{ t('capabilities.mcpName') }}</Label>
                      <Input v-model="mcpForm.name" class="h-8 text-xs" maxlength="120" :disabled="Boolean(mcpForm.originalName)" />
                    </div>
                    <div class="space-y-1">
                      <Label class="text-[11px]">{{ t('capabilities.mcpConnectionType') }}</Label>
                      <Select v-model="mcpForm.kind">
                        <SelectTrigger class="h-8 text-xs">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="command">{{ t('capabilities.mcpLocalCommand') }}</SelectItem>
                          <SelectItem value="url">{{ t('capabilities.mcpRemoteUrl') }}</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div v-if="mcpForm.kind === 'command'" class="space-y-1 sm:col-span-2">
                      <Label class="text-[11px]">{{ t('capabilities.mcpCommand') }}</Label>
                      <Input v-model="mcpForm.command" class="h-8 font-mono text-xs" maxlength="2048" placeholder="npx" />
                    </div>
                    <div v-if="mcpForm.kind === 'command'" class="space-y-2 sm:col-span-2">
                      <div class="flex items-center justify-between gap-2">
                        <Label class="text-[11px]">{{ t('capabilities.mcpArgs') }}</Label>
                        <Button type="button" variant="ghost" size="xs" class="h-6 text-[10px]" @click="addMcpArgument">
                          <Plus :size="11" class="mr-1" />{{ t('capabilities.mcpAddArg') }}
                        </Button>
                      </div>
                      <div v-if="mcpForm.args.length" class="space-y-1.5">
                        <div v-for="(_, index) in mcpForm.args" :key="index" class="flex gap-1.5">
                          <Input v-model="mcpForm.args[index]" class="h-8 flex-1 font-mono text-xs" maxlength="4096" :placeholder="`arg ${index + 1}`" />
                          <Button type="button" variant="ghost" size="icon-sm" :aria-label="t('common.delete')" @click="removeMcpArgument(index)">
                            <Trash2 :size="12" />
                          </Button>
                        </div>
                      </div>
                      <p v-else class="text-[10px] text-muted-foreground">{{ t('capabilities.mcpArgsEmpty') }}</p>
                    </div>
                    <div v-if="mcpForm.kind === 'url'" class="space-y-1 sm:col-span-2">
                      <Label class="text-[11px]">{{ t('capabilities.mcpUrl') }}</Label>
                      <Input v-model="mcpForm.url" type="url" class="h-8 font-mono text-xs" maxlength="4096" placeholder="https://example.com/mcp" />
                    </div>
                    <div class="space-y-1 sm:col-span-2">
                      <Label class="text-[11px]">{{ t('capabilities.mcpTransport') }}</Label>
                      <Select v-model="mcpForm.transport">
                        <SelectTrigger class="h-8 text-xs">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem v-if="mcpForm.kind === 'command'" value="stdio">stdio</SelectItem>
                          <template v-else>
                            <SelectItem value="http">HTTP</SelectItem>
                            <SelectItem value="sse">SSE</SelectItem>
                          </template>
                        </SelectContent>
                      </Select>
                    </div>
                  </div>

                  <div class="space-y-2 border-t pt-3">
                    <div class="flex items-center justify-between gap-2">
                      <div>
                        <Label class="text-[11px]">{{ t('capabilities.mcpEnvironment') }}</Label>
                        <p class="text-[10px] text-muted-foreground">{{ t('capabilities.mcpEnvironmentHint') }}</p>
                      </div>
                      <Button type="button" variant="outline" size="xs" class="h-7 text-[10px]" @click="addMcpEnvironmentVariable">
                        <Plus :size="11" class="mr-1" />{{ t('capabilities.mcpAddEnvironment') }}
                      </Button>
                    </div>
                    <div v-if="mcpForm.env.length" class="space-y-1.5">
                      <div v-for="entry in mcpForm.env" :key="entry.id" class="grid grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_32px] gap-1.5">
                        <Input v-model="entry.key" class="h-8 font-mono text-xs" maxlength="256" placeholder="API_KEY" />
                        <Input v-model="entry.value" class="h-8 font-mono text-xs" maxlength="16384" :placeholder="t('capabilities.mcpEnvironmentValue')" />
                        <Button type="button" variant="ghost" size="icon-sm" :aria-label="t('common.delete')" @click="removeMcpEnvironmentVariable(entry.id)">
                          <Trash2 :size="12" />
                        </Button>
                      </div>
                    </div>
                  </div>
                  <div class="flex items-center justify-between gap-2">
                    <label class="flex items-center gap-2 text-[11px] text-muted-foreground">
                      <Switch
                        :checked="mcpForm.enabled"
                        @update:checked="mcpForm = { ...mcpForm, enabled: $event as boolean }"
                      />
                      {{ mcpForm.enabled ? t('capabilities.ready') : t('capabilities.disabled') }}
                    </label>
                    <div class="flex gap-2">
                      <Button size="sm" variant="ghost" @click="mcpEditorOpen = false">{{ t('common.cancel') }}</Button>
                      <Button size="sm" :disabled="!mcpFormValid || capabilitiesStore.capabilityMutation !== ''" @click="saveMcpEditor">
                        {{ t('capabilities.saveMcp') }}
                      </Button>
                    </div>
                  </div>
                </CardContent>
              </Card>
              <Card v-for="server in visibleMcpServers" :key="server.name" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="flex items-center gap-3 py-3">
                  <div class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-primary">
                    <LoaderCircle v-if="capabilitiesStore.mcpStatusLoading && !server.statusLoaded" :size="16" class="animate-spin" />
                    <Bot v-else :size="16" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <div class="flex items-center gap-2">
                      <span class="truncate text-xs font-semibold">{{ server.title }}</span>
                      <Badge v-if="!server.enabled" variant="outline" class="text-[9px]">{{ t('capabilities.disabled') }}</Badge>
                    </div>
                    <p class="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{{ server.description || server.name }}</p>
                    <p v-if="server.statusLoaded" class="mt-1 text-[10px] text-muted-foreground">
                      {{ t('capabilities.tools', { count: server.toolCount }) }} · {{ t('capabilities.resources', { count: server.resourceCount }) }} · {{ mcpAuthLabel(server.authStatus) }}
                    </p>
                    <p v-else class="mt-1 text-[10px] text-muted-foreground">
                      {{ capabilitiesStore.mcpStatusLoading ? t('capabilities.mcpChecking') : server.statusMessage || t('capabilities.mcpConfigured') }}
                    </p>
                  </div>
                  <div class="flex shrink-0 items-center gap-1">
                    <Button v-if="server.enabled && server.authStatus === 'notLoggedIn'" size="sm" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="capabilitiesStore.startMCPLogin(server.name)">
                      {{ t('capabilities.connect') }}
                    </Button>
                    <Button size="icon-sm" variant="ghost" :aria-label="t('capabilities.editMcp')" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="openMcpEditor(server)">
                      <Pencil :size="13" />
                    </Button>
                    <Button size="icon-sm" variant="ghost" class="text-destructive" :aria-label="t('capabilities.deleteMcp')" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="deleteMcpServer(server)">
                      <Trash2 :size="13" />
                    </Button>
                  </div>
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="automation" class="mt-0 space-y-4">
              <Card>
                <CardHeader class="pb-2">
                  <CardTitle class="flex items-center gap-2 text-xs">
                    <Webhook :size="14" class="text-primary" />
                    {{ t('capabilities.hooks') }}
                  </CardTitle>
                </CardHeader>
                <CardContent class="space-y-2">
                  <div v-for="hook in hooks" :key="`${hook.event}:${hook.key || hook.name}`" class="flex items-center gap-3">
                    <div class="min-w-0 flex-1">
                      <p class="text-xs font-medium">{{ hook.name }}</p>
                      <p class="text-[10px] text-muted-foreground">{{ hook.event }} · {{ hook.source }}</p>
                    </div>
                    <Switch
                      :checked="hook.enabled"
                      :disabled="capabilitiesStore.capabilityMutation !== '' || !(hook.key || hook.name)"
                      @update:checked="capabilitiesStore.setHookEnabled(hook.key || hook.name, $event as boolean)"
                    />
                  </div>
                </CardContent>
              </Card>

              <Card>
                <CardHeader class="pb-2">
                  <CardTitle class="flex items-center gap-2 text-xs">
                    <FlaskConical :size="14" class="text-primary" />
                    {{ t('capabilities.features') }}
                  </CardTitle>
                </CardHeader>
                <CardContent class="space-y-3">
                  <div v-for="feature in features" :key="feature.name" class="flex items-start gap-3">
                    <div class="min-w-0 flex-1">
                      <p class="text-xs font-medium">{{ feature.displayName }}</p>
                      <p class="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{{ feature.description }}</p>
                      <Badge variant="outline" class="mt-1 text-[9px]">{{ feature.stage }}</Badge>
                    </div>
                    <Switch
                      :checked="feature.enabled"
                      :disabled="capabilitiesStore.capabilityMutation !== ''"
                      @update:checked="capabilitiesStore.setExperimentalFeature(feature.name, $event as boolean)"
                    />
                  </div>
                </CardContent>
              </Card>
            </TabsContent>

            <div v-if="remainingCount > 0" class="flex justify-center py-4">
              <Button variant="outline" size="sm" @click="loadMore">
                {{ t('sidebar.loadMore', { count: Math.min(PAGE_SIZE, remainingCount) }) }}
              </Button>
            </div>

            <div v-if="(capabilitiesStore.capabilitiesLoading || (activeTab === 'mcp' && capabilitiesStore.mcpStatusLoading)) && ((activeTab === 'plugins' && plugins.length === 0) || (activeTab === 'skills' && skills.length === 0) || (activeTab === 'apps' && apps.length === 0) || (activeTab === 'mcp' && mcpServers.length === 0))" class="grid min-h-48 place-items-center text-center text-xs text-muted-foreground">
              <div>
                <LoaderCircle :size="24" class="mx-auto mb-2 animate-spin text-primary" />
                <p>{{ t('capabilities.loading') }}</p>
              </div>
            </div>
            <div v-else-if="(activeTab === 'plugins' && plugins.length === 0) || (activeTab === 'skills' && skills.length === 0) || (activeTab === 'apps' && apps.length === 0) || (activeTab === 'mcp' && mcpServers.length === 0)" class="grid min-h-48 place-items-center text-center text-xs text-muted-foreground">
              <div>
                <PlugZap v-if="activeTab === 'mcp'" :size="24" class="mx-auto mb-2 text-primary" />
                <Blocks v-else :size="24" class="mx-auto mb-2 text-primary" />
                <p>{{ activeError || (activeTab === 'mcp' ? t('capabilities.mcpEmpty') : t('capabilities.empty')) }}</p>
                <div v-if="activeTab === 'mcp' && !activeError" class="mt-3 flex justify-center gap-2">
                  <Button size="sm" variant="outline" @click="openMcpEditor()">
                    <Plus :size="13" class="mr-1.5" />{{ t('capabilities.addMcp') }}
                  </Button>
                  <Button size="sm" variant="outline" @click="openMcpImport('json')">
                    <Braces :size="13" class="mr-1.5" />{{ t('capabilities.mcpImportPasteJson') }}
                  </Button>
                </div>
                <Button v-else class="mt-3" size="sm" variant="outline" :disabled="capabilitiesStore.capabilitiesLoading" @click="capabilitiesStore.loadCapabilities(true)">
                  <RefreshCw :size="13" class="mr-1.5" />{{ t('common.retry') }}
                </Button>
              </div>
            </div>
            </div>
          </ScrollArea>
        </Tabs>
        </template>
      </section>
    </div>
  </div>
</template>

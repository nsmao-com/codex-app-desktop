import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { CapabilityCatalog, MCPServerView } from '../types/codex'
import { notify } from '../utils/notify'
import { translate } from '../i18n'
import {
  normalizeApps,
  normalizeExperimentalFeatures,
  normalizeHooks,
  normalizeMCPServers,
  normalizePlugins,
  normalizeSkills,
} from '../utils/capabilities'
import { parseMCPImportJSON } from '../utils/mcpImport'
import { asRecord, asString } from '../utils/protocol'
import { useAppStore } from './app'

function emptyCapabilities(): CapabilityCatalog {
  return {
    plugins: [],
    skills: [],
    apps: [],
    mcpServers: [],
    hooks: [],
    features: [],
  }
}

export const useCapabilitiesStore = defineStore('capabilities', () => {
  const appStore = useAppStore()

  const capabilities = shallowRef<CapabilityCatalog>(emptyCapabilities())
  const capabilityLoaded = shallowRef<Record<string, boolean>>({})
  const capabilityErrors = shallowRef<Record<string, string>>({})
  const capabilitiesLoading = shallowRef(false)
  const mcpStatusLoading = shallowRef(false)
  const capabilityMutation = shallowRef('')

  let capabilityRefreshTimer = 0

  const plugins = computed(() => capabilities.value.plugins)
  const skills = computed(() => capabilities.value.skills)
  const apps = computed(() => capabilities.value.apps)
  const mcpServers = computed(() => capabilities.value.mcpServers)
  const hooks = computed(() => capabilities.value.hooks)
  const features = computed(() => capabilities.value.features)

  async function loadCapabilities(force = false): Promise<void> {
    if (capabilitiesLoading.value) return
    if (!appStore.codexAvailable) {
      const message = translate('capabilities.connectionRequired')
      capabilityErrors.value = Object.fromEntries(['plugins', 'skills', 'apps', 'mcp', 'hooks', 'features'].map((key) => [key, message]))
      return
    }

    const sections = [
      ['plugins', 'plugins', backend.ListPlugins, normalizePlugins],
      ['skills', 'skills', backend.ListSkills, normalizeSkills],
      ['apps', 'apps', backend.ListApps, normalizeApps],
      ['mcpServers', 'mcp', backend.ListMCPServers, normalizeMCPServers],
      ['hooks', 'hooks', backend.ListHooks, normalizeHooks],
      ['features', 'features', backend.ListExperimentalFeatures, normalizeExperimentalFeatures],
    ] as const
    const pending = sections.filter(([, errorKey]) => force || !capabilityLoaded.value[errorKey])
    if (!pending.length) {
      loadMCPServerStatus()
      return
    }

    capabilitiesLoading.value = true
    try {
      await mapWithConcurrency(pending, 2, ([key, errorKey, loader, normalize]) =>
        loadCapabilitySection(key, errorKey, loader, normalize),
      )
    } finally {
      capabilitiesLoading.value = false
    }
    loadMCPServerStatus()
  }

  async function loadCapabilitySection<K extends keyof CapabilityCatalog>(
    key: K,
    errorKey: string,
    loader: () => Promise<unknown>,
    normalize: (value: unknown) => CapabilityCatalog[K],
  ): Promise<void> {
    try {
      const items = normalize(await loader())
      capabilities.value = { ...capabilities.value, [key]: items }
      capabilityLoaded.value = { ...capabilityLoaded.value, [errorKey]: true }
      clearCapabilityError(errorKey)
    } catch (error) {
      capabilityLoaded.value = { ...capabilityLoaded.value, [errorKey]: false }
      setCapabilityError(errorKey, errorMessage(error))
    }
  }

  async function loadMCPServerStatus(): Promise<void> {
    if (!appStore.codexAvailable || mcpStatusLoading.value) return
    mcpStatusLoading.value = true
    try {
      const response = await backend.ListMCPServerStatus()
      const result = asRecord(response)
      const status = normalizeMCPServers(response)
      capabilities.value = {
        ...capabilities.value,
        mcpServers: mergeMCPServers(capabilities.value.mcpServers, status),
      }
      if (result.statusTimedOut === true) {
        capabilities.value = {
          ...capabilities.value,
          mcpServers: capabilities.value.mcpServers.map((server) => ({
            ...server,
            statusMessage: translate('capabilities.mcpStatusUnavailable'),
          })),
        }
        setCapabilityError('mcp', translate('capabilities.mcpStatusUnavailable'))
      } else {
        clearCapabilityError('mcp')
      }
    } catch {
      setCapabilityError('mcp', translate('capabilities.mcpStatusUnavailable'))
    } finally {
      mcpStatusLoading.value = false
    }
  }

  function setCapabilityError(key: string, message: string): void {
    capabilityErrors.value = { ...capabilityErrors.value, [key]: message }
  }

  function clearCapabilityError(key: string): void {
    if (!capabilityErrors.value[key]) return
    const next = { ...capabilityErrors.value }
    delete next[key]
    capabilityErrors.value = next
  }

  async function installPlugin(pluginID: string): Promise<void> {
    const plugin = capabilities.value.plugins.find((item) => item.id === pluginID)
    if (!plugin || capabilityMutation.value) return
    capabilityMutation.value = `plugin:${pluginID}`
    try {
      await backend.InstallPlugin({
        pluginName: plugin.name,
        marketplacePath: plugin.marketplacePath,
        remoteMarketplaceName: plugin.marketplacePath ? '' : plugin.marketplaceName,
      })
      notify('success', translate('capabilities.pluginInstalled'), plugin.displayName)
      await loadCapabilities(true)
    } catch (error) {
      notify('error', translate('capabilities.pluginInstallFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function uninstallPlugin(pluginID: string): Promise<void> {
    const plugin = capabilities.value.plugins.find((item) => item.id === pluginID)
    if (!plugin || capabilityMutation.value) return
    capabilityMutation.value = `plugin:${pluginID}`
    try {
      await backend.UninstallPlugin(pluginID)
      notify('success', translate('capabilities.pluginRemoved'), plugin.displayName)
      await loadCapabilities(true)
    } catch (error) {
      notify('error', translate('capabilities.pluginRemoveFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function setSkillEnabled(name: string, path: string, enabled: boolean): Promise<void> {
    if (capabilityMutation.value) return
    capabilityMutation.value = `skill:${path}`
    try {
      await backend.SetSkillEnabled({ name, path, enabled })
      capabilities.value = {
        ...capabilities.value,
        skills: capabilities.value.skills.map((skill) => skill.path === path ? { ...skill, enabled } : skill),
      }
    } catch (error) {
      notify('error', translate('capabilities.skillUpdateFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function setExperimentalFeature(name: string, enabled: boolean): Promise<void> {
    if (capabilityMutation.value) return
    capabilityMutation.value = `feature:${name}`
    try {
      await backend.SetExperimentalFeature(name, enabled)
      capabilities.value = {
        ...capabilities.value,
        features: capabilities.value.features.map((feature) =>
          feature.name === name ? { ...feature, enabled } : feature,
        ),
      }
    } catch (error) {
      notify('error', translate('capabilities.featureUpdateFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function refreshMCPServers(): Promise<void> {
    if (capabilityMutation.value) return
    capabilityMutation.value = 'mcp:refresh'
    try {
      await backend.RefreshMCPServers()
      await loadCapabilitySection('mcpServers', 'mcp', backend.ListMCPServers, normalizeMCPServers)
      await loadMCPServerStatus()
    } catch (error) {
      notify('error', translate('capabilities.mcpRefreshFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function startMCPLogin(name: string): Promise<void> {
    if (!name || capabilityMutation.value) return
    capabilityMutation.value = `mcp:${name}`
    try {
      const response = await backend.StartMCPLogin(name)
      const authorizationURL = asString(asRecord(response).authorizationUrl)
      if (authorizationURL) await backend.OpenExternal(authorizationURL)
    } catch (error) {
      notify('error', translate('capabilities.mcpLoginFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function upsertMCPServer(input: {
    name: string
    enabled: boolean
    command?: string
    args?: string[]
    url?: string
    transport?: string
    env?: Record<string, string>
  }): Promise<boolean> {
    if (!input.name || capabilityMutation.value) return false
    capabilityMutation.value = `mcp:upsert:${input.name}`
    try {
      await backend.UpsertMCPServer({
        name: input.name,
        enabled: input.enabled,
        command: input.command ?? '',
        args: input.args ?? [],
        url: input.url ?? '',
        transport: input.transport ?? '',
        env: input.env ?? null,
      })
      notify('success', translate('capabilities.mcpSaved'), input.name)
      await loadCapabilitySection('mcpServers', 'mcp', backend.ListMCPServers, normalizeMCPServers)
      await loadMCPServerStatus()
      return true
    } catch (error) {
      notify('error', translate('capabilities.mcpSaveFailed'), errorMessage(error))
      return false
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function importMCPServersJSON(raw: string): Promise<number> {
    if (capabilityMutation.value) return 0
    const servers = parseMCPImportJSON(raw)
    capabilityMutation.value = 'mcp:import'
    let saved = 0
    try {
      for (const server of servers) {
        await backend.UpsertMCPServer({
          name: server.name,
          enabled: server.enabled,
          command: server.command,
          args: server.args,
          url: server.url,
          transport: server.transport,
          env: Object.keys(server.env).length ? server.env : null,
        })
        saved += 1
      }
      notify(
        'success',
        translate('capabilities.mcpImportSaved'),
        translate('capabilities.mcpImportSavedHint', { count: saved }),
      )
      await loadCapabilitySection('mcpServers', 'mcp', backend.ListMCPServers, normalizeMCPServers)
      await loadMCPServerStatus()
      return saved
    } catch (error) {
      notify('error', translate('capabilities.mcpImportFailed'), errorMessage(error))
      if (saved > 0) {
        await loadCapabilitySection('mcpServers', 'mcp', backend.ListMCPServers, normalizeMCPServers)
        await loadMCPServerStatus()
      }
      return saved
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function deleteMCPServer(name: string): Promise<void> {
    if (!name || capabilityMutation.value) return
    capabilityMutation.value = `mcp:delete:${name}`
    try {
      await backend.DeleteMCPServer(name)
      notify('success', translate('capabilities.mcpDeleted'), name)
      await loadCapabilitySection('mcpServers', 'mcp', backend.ListMCPServers, normalizeMCPServers)
      await loadMCPServerStatus()
    } catch (error) {
      notify('error', translate('capabilities.mcpDeleteFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function setHookEnabled(key: string, enabled: boolean): Promise<void> {
    if (!key || capabilityMutation.value) return
    capabilityMutation.value = `hook:${key}`
    try {
      await backend.SetHookEnabled(key, enabled)
      capabilities.value = {
        ...capabilities.value,
        hooks: capabilities.value.hooks.map((hook) =>
          (hook.key === key || hook.name === key) ? { ...hook, enabled } : hook,
        ),
      }
    } catch (error) {
      notify('error', translate('capabilities.hookUpdateFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  async function setAppEnabled(appID: string, enabled: boolean): Promise<void> {
    if (!appID || capabilityMutation.value) return
    capabilityMutation.value = `app:${appID}`
    try {
      await backend.SetAppEnabled(appID, enabled)
      capabilities.value = {
        ...capabilities.value,
        apps: capabilities.value.apps.map((app) => app.id === appID ? { ...app, enabled } : app),
      }
    } catch (error) {
      notify('error', translate('capabilities.appUpdateFailed'), errorMessage(error))
    } finally {
      capabilityMutation.value = ''
    }
  }

  function handleMcpStatusUpdate(payload: Record<string, unknown>): void {
    const name = asString(payload.name)
    const state = asString(payload.status)
    const message = asString(payload.error) || translate(`capabilities.mcp${state[0]?.toUpperCase()}${state.slice(1)}`)
    capabilities.value = {
      ...capabilities.value,
      mcpServers: capabilities.value.mcpServers.map((server) => server.name === name
        ? { ...server, statusMessage: message }
        : server),
    }
  }

  function scheduleRefresh(): void {
    if (!Object.values(capabilities.value).some((items) => items.length > 0)) return
    if (capabilityRefreshTimer) window.clearTimeout(capabilityRefreshTimer)
    capabilityRefreshTimer = window.setTimeout(() => {
      capabilityRefreshTimer = 0
      void loadCapabilities(true)
    }, 240)
  }

  return {
    capabilities,
    capabilityLoaded,
    capabilityErrors,
    capabilitiesLoading,
    mcpStatusLoading,
    capabilityMutation,
    plugins,
    skills,
    apps,
    mcpServers,
    hooks,
    features,
    loadCapabilities,
    loadMCPServerStatus,
    installPlugin,
    uninstallPlugin,
    setSkillEnabled,
    setExperimentalFeature,
    refreshMCPServers,
    startMCPLogin,
    upsertMCPServer,
    importMCPServersJSON,
    deleteMCPServer,
    setHookEnabled,
    setAppEnabled,
    handleMcpStatusUpdate,
    scheduleRefresh,
  }
})

function mergeMCPServers(configured: MCPServerView[], status: MCPServerView[]): MCPServerView[] {
  const statusByName = new Map(status.map((server) => [server.name, server]))
  const merged = configured.map((server) => {
    const current = statusByName.get(server.name)
    statusByName.delete(server.name)
    return current ? { ...server, ...current, enabled: server.enabled } : server
  })
  return [...merged, ...statusByName.values()]
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}

async function mapWithConcurrency<T>(items: T[], limit: number, worker: (item: T) => Promise<void>): Promise<void> {
  let cursor = 0
  const run = async () => {
    while (cursor < items.length) {
      const index = cursor
      cursor += 1
      const item = items[index]
      if (item !== undefined) await worker(item)
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, run))
}

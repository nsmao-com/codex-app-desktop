import type {
  AppView,
  ExperimentalFeatureView,
  HookView,
  MCPServerView,
  PluginView,
  SkillView,
} from '../types/codex'
import { asArray, asRecord, asString } from './protocol'

export function normalizePlugins(value: unknown): PluginView[] {
  const response = asRecord(value)
  const marketplaces = asArray(response.marketplaces).length ? asArray(response.marketplaces) : asArray(response.data)
  return marketplaces.flatMap((marketplaceValue) => {
    const marketplace = asRecord(marketplaceValue)
    const marketplaceName = asString(marketplace.name)
    const marketplacePath = asString(marketplace.path)
    return asArray(marketplace.plugins).map((pluginValue) => {
      const plugin = asRecord(pluginValue)
      const ui = asRecord(plugin.interface)
      const source = asRecord(plugin.source)
      return {
        id: asString(plugin.id),
        name: asString(plugin.name),
        displayName: asString(ui.displayName, asString(plugin.name)),
        description: asString(ui.shortDescription, asString(ui.longDescription)),
        developerName: asString(ui.developerName),
        category: asString(ui.category),
        installed: plugin.installed === true,
        enabled: plugin.enabled === true,
        version: asString(plugin.localVersion, asString(plugin.version)),
        marketplaceName,
        marketplacePath,
        sourceType: asString(source.type),
        logoUrl: asString(ui.logoUrl, asString(ui.logo)),
      }
    })
  }).filter((plugin) => plugin.id && plugin.name)
}

export function normalizeSkills(value: unknown): SkillView[] {
  const response = asRecord(value)
  const entries = asArray(response.data).length ? asArray(response.data) : asArray(value)
  return entries.flatMap((entryValue) => {
    const entry = asRecord(entryValue)
    const entryErrors = asArray(entry.errors).map((error) => asString(asRecord(error).message)).filter(Boolean)
    const skills = asArray(entry.skills).length ? asArray(entry.skills) : (entry.name ? [entryValue] : [])
    return skills.map((skillValue) => {
      const skill = asRecord(skillValue)
      const ui = asRecord(skill.interface)
      const config = asRecord(skill.config)
      const enabledRaw = skill.enabled ?? config.enabled ?? skill.isEnabled
      return {
        name: asString(skill.name),
        path: asString(skill.path, asString(skill.absolutePath, asString(skill.file))),
        description: asString(skill.description),
        shortDescription: asString(ui.shortDescription, asString(skill.shortDescription)),
        displayName: asString(ui.displayName, asString(skill.name)),
        scope: asString(skill.scope, asString(entry.cwd)),
        enabled: enabledRaw !== false && enabledRaw !== 'false',
        error: entryErrors.join(' · '),
      }
    })
  }).filter((skill) => skill.name && skill.path)
}

export function normalizeApps(value: unknown): AppView[] {
  const response = asRecord(value)
  const entries = asArray(response.data).length ? asArray(response.data) : asArray(value)
  return entries.map((appValue) => {
    const app = asRecord(appValue)
    return {
      id: asString(app.id),
      name: asString(app.name),
      description: asString(app.description),
      enabled: app.isEnabled === true,
      accessible: app.isAccessible !== false,
      logoUrl: asString(app.logoUrl),
      pluginNames: asArray(app.pluginDisplayNames).map((name) => asString(name)).filter(Boolean),
    }
  }).filter((app) => app.id && app.name)
}

export function normalizeMCPServers(value: unknown): MCPServerView[] {
  const response = asRecord(value)
  const entries = asArray(response.data).length ? asArray(response.data) : asArray(value)
  return entries.map((serverValue) => {
    const server = asRecord(serverValue)
    const info = asRecord(server.serverInfo)
    const tools = asRecord(server.tools)
    const args = asArray(server.args).map((arg) => asString(arg)).filter(Boolean)
    return {
      name: asString(server.name),
      title: asString(info.title, asString(server.name)),
      description: asString(info.description),
      authStatus: asString(server.authStatus, 'loading'),
      toolCount: Object.keys(tools).length,
      resourceCount: asArray(server.resources).length + asArray(server.resourceTemplates).length,
      enabled: server.enabled !== false,
      statusLoaded: server.statusLoaded !== false && (server.serverInfo !== undefined || server.authStatus !== undefined),
      statusMessage: asString(server.statusMessage),
      command: asString(server.command),
      url: asString(server.url),
      transport: asString(server.transport, asString(server.type)),
      args,
    }
  }).filter((server) => server.name)
}

export function normalizeHooks(value: unknown): HookView[] {
  const response = asRecord(value)
  const entries = asArray(response.data).length ? asArray(response.data) : asArray(value)
  return entries.flatMap((entryValue) => {
    const entry = asRecord(entryValue)
    const errors = asArray(entry.errors).map((error) => asString(asRecord(error).message)).filter(Boolean)
    return asArray(entry.hooks).map((hookValue) => {
      const hook = asRecord(hookValue)
      const key = asString(hook.key, asString(hook.command))
      return {
        name: asString(hook.command, key),
        key,
        event: asString(hook.eventName),
        source: asString(hook.source, asString(hook.sourcePath)),
        enabled: hook.enabled !== false,
        error: errors.join(' · '),
      }
    })
  }).filter((hook) => hook.key || hook.name)
}

export function normalizeExperimentalFeatures(value: unknown): ExperimentalFeatureView[] {
  const response = asRecord(value)
  const entries = asArray(response.data).length ? asArray(response.data) : asArray(value)
  return entries.map((featureValue) => {
    const feature = asRecord(featureValue)
    return {
      name: asString(feature.name),
      displayName: asString(feature.displayName, asString(feature.name)),
      description: asString(feature.description),
      stage: asString(feature.stage),
      enabled: feature.enabled === true,
    }
  }).filter((feature) => feature.name)
}

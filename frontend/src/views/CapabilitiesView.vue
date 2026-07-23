<script setup lang="ts">
import {
  AppWindow,
  ArrowLeft,
  Blocks,
  Bot,
  FlaskConical,
  LoaderCircle,
  Pencil,
  Plus,
  PlugZap,
  RefreshCw,
  Search,
  Sparkles,
  Trash2,
  Unplug,
  Webhook,
} from '@lucide/vue'
import { computed, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { useAppStore, useCapabilitiesStore } from '@/stores'
import type { MCPServerView } from '@/types/codex'
import { notify } from '@/utils/notify'
import { parseMCPImportJSON } from '@/utils/mcpImport'

type CapabilityTab = 'plugins' | 'skills' | 'apps' | 'mcp' | 'automation'

const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const capabilitiesStore = useCapabilitiesStore()
const { t } = useI18n()

const activeTab = shallowRef<CapabilityTab>('plugins')
const query = shallowRef('')
const brokenLogos = shallowRef<Set<string>>(new Set())
const PAGE_SIZE = 40
const visibleLimit = shallowRef<Record<CapabilityTab, number>>({ plugins: PAGE_SIZE, skills: PAGE_SIZE, apps: PAGE_SIZE, mcp: PAGE_SIZE, automation: PAGE_SIZE })
const mcpEditorOpen = shallowRef(false)
const mcpImportOpen = shallowRef(false)
const mcpImportJSON = shallowRef(`{
  "mcpServers": {
    "example": {
      "command": "npx",
      "args": ["-y", "mcp-server-example"]
    }
  }
}`)
const mcpForm = shallowRef({
  name: '',
  enabled: true,
  command: '',
  args: '',
  url: '',
  transport: 'http',
})

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

function loadWhenReady(): void {
  if (appStore.codexAvailable && !capabilitiesStore.capabilitiesLoading) {
    void capabilitiesStore.loadCapabilities()
  }
}

onMounted(() => {
  const tab = String(route.query.tab || '')
  if (tab === 'plugins' || tab === 'skills' || tab === 'apps' || tab === 'mcp' || tab === 'automation') {
    activeTab.value = tab
  }
  loadWhenReady()
})
watch(() => appStore.codexAvailable, loadWhenReady)
watch(
  () => route.query.tab,
  (tab) => {
    const value = String(tab || '')
    if (value === 'plugins' || value === 'skills' || value === 'apps' || value === 'mcp' || value === 'automation') {
      activeTab.value = value
    }
  },
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
  void router.replace(route.query.from === 'settings' ? { name: 'settings' } : { name: 'workbench' })
}

function openMcpEditor(server?: MCPServerView): void {
  mcpImportOpen.value = false
  mcpForm.value = {
    name: server?.name ?? '',
    enabled: server?.enabled ?? true,
    command: server?.command ?? '',
    args: (server?.args ?? []).join(' '),
    url: server?.url ?? '',
    transport: server?.transport || 'http',
  }
  mcpEditorOpen.value = true
}

async function saveMcpEditor(): Promise<void> {
  const form = mcpForm.value
  const ok = await capabilitiesStore.upsertMCPServer({
    name: form.name.trim(),
    enabled: form.enabled,
    command: form.command.trim(),
    args: form.args.trim().split(/\s+/).filter(Boolean),
    url: form.url.trim(),
    transport: form.transport.trim(),
  })
  if (ok) mcpEditorOpen.value = false
}

function openMcpImport(): void {
  mcpEditorOpen.value = false
  mcpImportOpen.value = true
}

async function saveMcpImport(): Promise<void> {
  try {
    parseMCPImportJSON(mcpImportJSON.value)
  } catch (error) {
    notify('error', t('capabilities.mcpImportFailed'), error instanceof Error ? error.message : t('notifications.unexpected'))
    return
  }
  const saved = await capabilitiesStore.importMCPServersJSON(mcpImportJSON.value)
  if (saved > 0) mcpImportOpen.value = false
}
</script>

<template>
  <div class="flex h-full w-full overflow-hidden bg-transparent text-foreground">
    <!-- Left tab rail on the gray shell -->
    <aside class="flex w-[248px] shrink-0 flex-col">
      <div class="space-y-2 px-3 pb-2 pt-1">
        <Button variant="ghost" class="h-8 w-full justify-start px-2 text-xs text-muted-foreground" @click="closeCapabilities">
          <ArrowLeft :size="14" class="mr-2" />
          {{ t('settings.backToApp') }}
        </Button>
        <div class="px-1">
          <p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capabilities.kicker') }}</p>
          <h1 class="text-[15px] font-semibold tracking-tight">{{ t('capabilities.title') }}</h1>
        </div>
      </div>

      <nav class="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-3" :aria-label="t('capabilities.title')">
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
    </aside>

    <!-- Rounded content card -->
    <div class="flex min-h-0 min-w-0 flex-1 flex-col pb-2 pr-2 pl-1.5 pt-0">
      <section class="workbench-card relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-[14px] border bg-card">
        <header class="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <div class="relative min-w-0 flex-1">
            <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input v-model="query" type="search" :placeholder="t('capabilities.search')" class="h-8 pl-8 text-xs" />
          </div>
          <Button v-if="activeTab === 'mcp'" variant="outline" size="sm" class="h-8" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="openMcpEditor()">
            <Plus :size="13" class="mr-1.5" />
            {{ t('capabilities.addMcp') }}
          </Button>
          <Button v-if="activeTab === 'mcp'" variant="outline" size="sm" class="h-8" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="openMcpImport">
            <Bot :size="13" class="mr-1.5" />
            {{ t('capabilities.importMcpJson') }}
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
              <Card v-if="mcpImportOpen" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="space-y-3 py-3">
                  <div class="space-y-1">
                    <Label class="text-[11px]">{{ t('capabilities.importMcpJson') }}</Label>
                    <p class="text-[10px] text-muted-foreground">{{ t('capabilities.importMcpJsonHint') }}</p>
                    <Textarea
                      v-model="mcpImportJSON"
                      class="min-h-40 font-mono text-[11px] leading-5"
                      :placeholder="t('capabilities.importMcpJsonPlaceholder')"
                    />
                  </div>
                  <div class="flex items-center justify-end gap-2">
                    <Button variant="ghost" size="sm" @click="mcpImportOpen = false">{{ t('common.cancel') }}</Button>
                    <Button size="sm" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="saveMcpImport">
                      {{ t('capabilities.importMcpJsonSave') }}
                    </Button>
                  </div>
                </CardContent>
              </Card>
              <Card v-if="mcpEditorOpen" class="gap-0 rounded-md py-0 shadow-none">
                <CardContent class="space-y-3 py-3">
                  <div class="grid gap-2 sm:grid-cols-2">
                    <div class="space-y-1">
                      <Label class="text-[11px]">{{ t('capabilities.mcpName') }}</Label>
                      <Input v-model="mcpForm.name" class="h-8 text-xs" />
                    </div>
                    <div class="space-y-1">
                      <Label class="text-[11px]">{{ t('capabilities.mcpTransport') }}</Label>
                      <Input v-model="mcpForm.transport" class="h-8 text-xs" placeholder="stdio / http" />
                    </div>
                    <div class="space-y-1 sm:col-span-2">
                      <Label class="text-[11px]">{{ t('capabilities.mcpCommand') }}</Label>
                      <Input v-model="mcpForm.command" class="h-8 text-xs" />
                    </div>
                    <div class="space-y-1 sm:col-span-2">
                      <Label class="text-[11px]">{{ t('capabilities.mcpArgs') }}</Label>
                      <Input v-model="mcpForm.args" class="h-8 text-xs" />
                    </div>
                    <div class="space-y-1 sm:col-span-2">
                      <Label class="text-[11px]">{{ t('capabilities.mcpUrl') }}</Label>
                      <Input v-model="mcpForm.url" class="h-8 text-xs" />
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
                      <Button size="sm" :disabled="!mcpForm.name.trim() || capabilitiesStore.capabilityMutation !== ''" @click="saveMcpEditor">
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
                    <Button size="icon-sm" variant="ghost" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="openMcpEditor(server)">
                      <Pencil :size="13" />
                    </Button>
                    <Button size="icon-sm" variant="ghost" class="text-destructive" :disabled="capabilitiesStore.capabilityMutation !== ''" @click="capabilitiesStore.deleteMCPServer(server.name)">
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
                <Blocks :size="24" class="mx-auto mb-2 text-primary" />
                <p>{{ activeError || t('capabilities.empty') }}</p>
                <Button class="mt-3" size="sm" variant="outline" :disabled="capabilitiesStore.capabilitiesLoading" @click="capabilitiesStore.loadCapabilities(true)">
                  <RefreshCw :size="13" class="mr-1.5" />
                  {{ t('common.retry') }}
                </Button>
              </div>
            </div>
            </div>
          </ScrollArea>
        </Tabs>
      </section>
    </div>
  </div>
</template>

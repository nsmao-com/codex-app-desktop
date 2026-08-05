<script setup lang="ts">
import {
  Activity,
  ArrowDown,
  ArrowUp,
  Bot,
  Eye,
  EyeOff,
  KeyRound,
  LoaderCircle,
  Plus,
  RefreshCw,
  RotateCcw,
  Route,
  ShieldCheck,
  Terminal,
  Trash2,
} from '@lucide/vue'
import { computed, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

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
import { SimpleTooltip } from '@/components/ui/tooltip'
import { notify } from '@/utils/notify'
import * as backend from '../../bindings/nice_codex_desktop/appservice'

type RouterState = 'idle' | 'healthy' | 'warning' | 'open' | 'half-open' | 'disabled'
type AuthMode = 'bearer' | 'x-api-key' | 'passthrough'

interface RouterUpstreamView {
  id: string
  name: string
  baseUrl: string
  authMode: string
  enabled: boolean
  hasApiKey: boolean
  state: string
  consecutiveFailures: number
  recoverySuccesses: number
  openUntil: number
  lastStatus: number
  lastLatencyMs: number
  lastError: string
  requestCount: number
}

interface RuntimeProviderView {
  runtime: 'codex' | 'claude'
  provider: string
  name: string
  baseUrl: string
  configPath: string
  source: string
  configured: boolean
}

interface RouterView {
  enabled: boolean
  running: boolean
  port: number
  listenUrl: string
  failureThreshold: number
  recoverySuccessThreshold: number
  cooldownSeconds: number
  firstByteTimeoutSeconds: number
  codexApplied: boolean
  lastError: string
  currentProviders: RuntimeProviderView[]
  upstreams: RouterUpstreamView[]
}

interface RouterUpstreamForm extends RouterUpstreamView {
  authMode: AuthMode
  apiKey: string
  clearApiKey: boolean
}

interface RouterForm {
  enabled: boolean
  port: number
  failureThreshold: number
  recoverySuccessThreshold: number
  cooldownSeconds: number
  firstByteTimeoutSeconds: number
  upstreams: RouterUpstreamForm[]
}

const { t } = useI18n()
const loading = shallowRef(true)
const busy = shallowRef('')
const view = shallowRef<RouterView | null>(null)
const visibleKeys = ref(new Set<string>())
const form = ref<RouterForm>({
  enabled: false,
  port: 15722,
  failureThreshold: 4,
  recoverySuccessThreshold: 2,
  cooldownSeconds: 60,
  firstByteTimeoutSeconds: 60,
  upstreams: [],
})

const endpoint = computed(() => `http://127.0.0.1:${form.value.port}`)
const canApply = computed(() => form.value.enabled && form.value.upstreams.some((item) => item.enabled))
const currentProviders = computed(() => view.value?.currentProviders ?? [])

function runtimeProviderLabel(runtime: RuntimeProviderView['runtime']): string {
  return runtime === 'claude' ? 'Claude Code' : 'Codex'
}

function applyView(next: RouterView): void {
  view.value = next
  form.value = {
    enabled: Boolean(next.enabled),
    port: Number(next.port) || 15722,
    failureThreshold: Number(next.failureThreshold) || 4,
    recoverySuccessThreshold: Number(next.recoverySuccessThreshold) || 2,
    cooldownSeconds: Number(next.cooldownSeconds) || 60,
    firstByteTimeoutSeconds: Number(next.firstByteTimeoutSeconds) || 60,
    upstreams: (next.upstreams || []).map((item) => ({
      ...item,
      authMode: normalizeAuthMode(item.authMode),
      apiKey: '',
      clearApiKey: false,
    })),
  }
}

function normalizeAuthMode(value: string): AuthMode {
  if (value === 'x-api-key' || value === 'passthrough') return value
  return 'bearer'
}

async function refreshRouter(silent = false): Promise<void> {
  if (busy.value) return
  if (!silent) busy.value = 'refresh'
  try {
    applyView(await backend.ReadProviderRouterConfig() as unknown as RouterView)
  } catch (error) {
    notify('error', t('settings.routingLoadFailed'), errorMessage(error))
  } finally {
    loading.value = false
    if (!silent) busy.value = ''
  }
}

function addProvider(): void {
  if (form.value.upstreams.length >= 16) return
  form.value.upstreams.push({
    id: crypto.randomUUID(),
    name: t('settings.routingProviderDefault', { index: form.value.upstreams.length + 1 }),
    baseUrl: 'https://api.openai.com/v1',
    authMode: 'bearer',
    enabled: true,
    hasApiKey: false,
    apiKey: '',
    clearApiKey: false,
    state: 'idle',
    consecutiveFailures: 0,
    recoverySuccesses: 0,
    openUntil: 0,
    lastStatus: 0,
    lastLatencyMs: 0,
    lastError: '',
    requestCount: 0,
  })
}

function removeProvider(index: number): void {
  const provider = form.value.upstreams[index]
  if (provider) visibleKeys.value.delete(provider.id)
  form.value.upstreams.splice(index, 1)
}

function moveProvider(index: number, direction: -1 | 1): void {
  const target = index + direction
  if (target < 0 || target >= form.value.upstreams.length) return
  const next = [...form.value.upstreams]
  const current = next[index]
  const replacement = next[target]
  if (!current || !replacement) return
  next[index] = replacement
  next[target] = current
  form.value.upstreams = next
}

function toggleKeyVisibility(id: string): void {
  const next = new Set(visibleKeys.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  visibleKeys.value = next
}

function clearProviderKey(provider: RouterUpstreamForm): void {
  provider.apiKey = ''
  provider.hasApiKey = false
  provider.clearApiKey = true
}

function validateForm(): string {
  const isIntegerInRange = (value: number, minimum: number, maximum: number) =>
    Number.isInteger(Number(value)) && Number(value) >= minimum && Number(value) <= maximum
  if (!isIntegerInRange(form.value.port, 1024, 65535)) return t('settings.routingPortInvalid')
  if (
    !isIntegerInRange(form.value.firstByteTimeoutSeconds, 5, 120)
    || !isIntegerInRange(form.value.failureThreshold, 1, 20)
    || !isIntegerInRange(form.value.recoverySuccessThreshold, 1, 10)
    || !isIntegerInRange(form.value.cooldownSeconds, 5, 300)
  ) return t('settings.routingTuningInvalid')
  if (form.value.enabled && !form.value.upstreams.some((item) => item.enabled)) return t('settings.routingProviderRequired')
  for (const provider of form.value.upstreams) {
    if (!provider.name.trim() || !provider.baseUrl.trim()) return t('settings.routingProviderInvalid')
    try {
      const parsed = new URL(provider.baseUrl.trim())
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return t('settings.routingProviderInvalid')
    } catch {
      return t('settings.routingProviderInvalid')
    }
  }
  return ''
}

async function saveRouter(showNotice = true): Promise<boolean> {
  if (busy.value) return false
  const validation = validateForm()
  if (validation) {
    notify('error', t('settings.routingSaveFailed'), validation)
    return false
  }
  busy.value = 'save'
  try {
    const next = await backend.SaveProviderRouterConfig({
      enabled: form.value.enabled,
      port: Number(form.value.port),
      failureThreshold: Number(form.value.failureThreshold),
      recoverySuccessThreshold: Number(form.value.recoverySuccessThreshold),
      cooldownSeconds: Number(form.value.cooldownSeconds),
      firstByteTimeoutSeconds: Number(form.value.firstByteTimeoutSeconds),
      upstreams: form.value.upstreams.map((provider) => ({
        id: provider.id,
        name: provider.name.trim(),
        baseUrl: provider.baseUrl.trim(),
        apiKey: provider.apiKey.trim(),
        keepExistingKey: provider.hasApiKey && !provider.apiKey && !provider.clearApiKey,
        clearApiKey: provider.clearApiKey,
        authMode: provider.authMode,
        enabled: provider.enabled,
      })),
    })
    applyView(next as unknown as RouterView)
    if (showNotice) notify('success', t('settings.routingSaved'), t('settings.routingSavedHint'))
    return true
  } catch (error) {
    notify('error', t('settings.routingSaveFailed'), errorMessage(error))
    return false
  } finally {
    busy.value = ''
  }
}

async function resetCircuits(): Promise<void> {
  if (busy.value) return
  busy.value = 'reset'
  try {
    applyView(await backend.ResetProviderRouterCircuits() as unknown as RouterView)
    notify('success', t('settings.routingCircuitsReset'), t('settings.routingCircuitsResetHint'))
  } catch (error) {
    notify('error', t('settings.routingResetFailed'), errorMessage(error))
  } finally {
    busy.value = ''
  }
}

async function applyToCodex(): Promise<void> {
  if (!await saveRouter(false)) return
  busy.value = 'apply'
  try {
    applyView(await backend.ApplyProviderRouterToCodex() as unknown as RouterView)
    notify('success', t('settings.routingApplied'), t('settings.routingAppliedHint'))
  } catch (error) {
    notify('error', t('settings.routingApplyFailed'), errorMessage(error))
  } finally {
    busy.value = ''
  }
}

async function restoreCodex(): Promise<void> {
  if (busy.value) return
  busy.value = 'restore'
  try {
    applyView(await backend.RestoreCodexProviderRoute() as unknown as RouterView)
    notify('success', t('settings.routingRestored'), t('settings.routingRestoredHint'))
  } catch (error) {
    notify('error', t('settings.routingRestoreFailed'), errorMessage(error))
  } finally {
    busy.value = ''
  }
}

function stateLabel(state: string): string {
  const key: Record<RouterState, string> = {
    idle: 'settings.routingStateIdle',
    healthy: 'settings.routingStateHealthy',
    warning: 'settings.routingStateWarning',
    open: 'settings.routingStateOpen',
    'half-open': 'settings.routingStateHalfOpen',
    disabled: 'settings.routingStateDisabled',
  }
  return t(key[state as RouterState] || key.idle)
}

function stateClass(state: string): string {
  if (state === 'healthy') return 'border-positive/40 text-positive'
  if (state === 'warning' || state === 'half-open') return 'border-amber-500/40 text-amber-600 dark:text-amber-400'
  if (state === 'open') return 'border-destructive/40 text-destructive'
  return 'text-muted-foreground'
}

function formatOpenUntil(value: number): string {
  if (!value || value <= Date.now()) return ''
  return new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(value)
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

void refreshRouter(true)
</script>

<template>
  <div class="space-y-5">
    <div v-if="loading" class="grid min-h-48 place-items-center text-xs text-muted-foreground">
      <div class="text-center">
        <LoaderCircle :size="24" class="mx-auto mb-2 animate-spin text-primary" />
        {{ t('common.loading') }}
      </div>
    </div>

    <template v-else>
      <section class="overflow-hidden rounded-xl border bg-card">
        <div class="border-b px-4 py-3">
          <h2 class="text-[13px] font-semibold">{{ t('settings.routingCurrentProviders') }}</h2>
          <p class="mt-1 text-[11px] text-muted-foreground">{{ t('settings.routingCurrentProvidersHint') }}</p>
        </div>
        <div class="grid divide-y sm:grid-cols-2 sm:divide-x sm:divide-y-0">
          <div v-for="provider in currentProviders" :key="provider.runtime" class="min-w-0 space-y-2 px-4 py-3">
            <div class="flex min-w-0 items-center gap-2">
              <Terminal v-if="provider.runtime === 'codex'" :size="14" class="shrink-0 text-primary" />
              <Bot v-else :size="14" class="shrink-0 text-primary" />
              <span class="text-[12px] font-medium">{{ runtimeProviderLabel(provider.runtime) }}</span>
              <Badge variant="outline" class="min-w-0 max-w-full truncate text-[9px]">
                {{ provider.name || provider.provider }}
              </Badge>
            </div>
            <p class="truncate font-mono text-[10px] text-foreground/80">
              {{ provider.baseUrl || t('settings.routingOfficialEndpoint') }}
            </p>
            <SimpleTooltip :content="provider.configPath">
              <p class="truncate text-[10px] text-muted-foreground">
                {{ provider.provider }} · {{ provider.source }} · {{ provider.configPath }}
              </p>
            </SimpleTooltip>
          </div>
        </div>
      </section>

      <section class="overflow-hidden rounded-xl border bg-card">
        <div class="flex items-start justify-between gap-4 border-b px-4 py-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <Route :size="15" class="text-primary" />
              <h2 class="text-[13px] font-semibold">{{ t('settings.routingTitle') }}</h2>
              <Badge variant="outline" :class="view?.running ? 'border-positive/40 text-positive' : 'text-muted-foreground'">
                {{ view?.running ? t('settings.routingRunning') : t('settings.routingStopped') }}
              </Badge>
            </div>
            <p class="mt-1 max-w-2xl text-[11px] leading-5 text-muted-foreground">{{ t('settings.routingHint') }}</p>
          </div>
          <Switch v-model:checked="form.enabled" :aria-label="t('settings.routingEnabled')" />
        </div>

        <div class="grid gap-3 px-4 py-4 sm:grid-cols-2 lg:grid-cols-3">
          <div class="space-y-1">
            <Label for="routing-endpoint" class="text-[11px]">{{ t('settings.routingEndpoint') }}</Label>
            <Input id="routing-endpoint" :model-value="endpoint" readonly class="h-8 font-mono text-[11px]" />
          </div>
          <div class="space-y-1">
            <Label for="routing-port" class="text-[11px]">{{ t('settings.routingPort') }}</Label>
            <Input id="routing-port" v-model.number="form.port" type="number" min="1024" max="65535" class="h-8 text-xs" />
          </div>
          <div class="space-y-1">
            <Label for="routing-first-byte" class="text-[11px]">{{ t('settings.routingFirstByteTimeout') }}</Label>
            <Input id="routing-first-byte" v-model.number="form.firstByteTimeoutSeconds" type="number" min="5" max="120" class="h-8 text-xs" />
          </div>
          <div class="space-y-1">
            <Label for="routing-failure-threshold" class="text-[11px]">{{ t('settings.routingFailureThreshold') }}</Label>
            <Input id="routing-failure-threshold" v-model.number="form.failureThreshold" type="number" min="1" max="20" class="h-8 text-xs" />
          </div>
          <div class="space-y-1">
            <Label for="routing-recovery-threshold" class="text-[11px]">{{ t('settings.routingRecoveryThreshold') }}</Label>
            <Input id="routing-recovery-threshold" v-model.number="form.recoverySuccessThreshold" type="number" min="1" max="10" class="h-8 text-xs" />
          </div>
          <div class="space-y-1">
            <Label for="routing-cooldown" class="text-[11px]">{{ t('settings.routingCooldown') }}</Label>
            <Input id="routing-cooldown" v-model.number="form.cooldownSeconds" type="number" min="5" max="300" class="h-8 text-xs" />
          </div>
        </div>

        <div v-if="view?.lastError" class="mx-4 mb-4 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-[11px] text-destructive" role="alert">
          {{ view.lastError }}
        </div>
      </section>

      <section class="overflow-hidden rounded-xl border bg-card">
        <div class="flex items-start justify-between gap-3 border-b px-4 py-3">
          <div>
            <h2 class="text-[13px] font-semibold">{{ t('settings.routingProviders') }}</h2>
            <p class="mt-1 text-[11px] text-muted-foreground">{{ t('settings.routingProvidersHint') }}</p>
          </div>
          <Button type="button" variant="outline" size="sm" :disabled="form.upstreams.length >= 16" @click="addProvider">
            <Plus :size="13" class="mr-1.5" />{{ t('settings.routingAddProvider') }}
          </Button>
        </div>

        <div v-if="form.upstreams.length" class="divide-y">
          <article v-for="(provider, index) in form.upstreams" :key="provider.id" class="space-y-3 px-4 py-4">
            <div class="flex flex-wrap items-center gap-2">
              <span class="flex size-6 shrink-0 items-center justify-center rounded-md bg-muted text-[10px] font-semibold tabular-nums">{{ index + 1 }}</span>
              <Input v-model="provider.name" maxlength="80" class="h-8 min-w-0 flex-1 text-xs font-medium" :aria-label="t('settings.routingProviderName')" />
              <Badge variant="outline" :class="stateClass(provider.state)">{{ stateLabel(provider.enabled ? provider.state : 'disabled') }}</Badge>
              <Switch v-model:checked="provider.enabled" :aria-label="t('settings.routingProviderEnabled')" />
              <SimpleTooltip :content="t('settings.routingMoveUp')">
                <Button type="button" variant="ghost" size="icon-sm" :disabled="index === 0" :aria-label="t('settings.routingMoveUp')" @click="moveProvider(index, -1)">
                  <ArrowUp :size="13" />
                </Button>
              </SimpleTooltip>
              <SimpleTooltip :content="t('settings.routingMoveDown')">
                <Button type="button" variant="ghost" size="icon-sm" :disabled="index === form.upstreams.length - 1" :aria-label="t('settings.routingMoveDown')" @click="moveProvider(index, 1)">
                  <ArrowDown :size="13" />
                </Button>
              </SimpleTooltip>
              <SimpleTooltip :content="t('common.delete')">
                <Button type="button" variant="ghost" size="icon-sm" class="text-destructive" :aria-label="t('common.delete')" @click="removeProvider(index)">
                  <Trash2 :size="13" />
                </Button>
              </SimpleTooltip>
            </div>

            <div class="grid gap-3 sm:grid-cols-[minmax(0,1.5fr)_minmax(140px,0.5fr)]">
              <div class="space-y-1">
                <Label :for="`routing-base-url-${index}`" class="text-[11px]">{{ t('settings.routingBaseUrl') }}</Label>
                <Input :id="`routing-base-url-${index}`" v-model="provider.baseUrl" type="url" maxlength="2048" class="h-8 font-mono text-[11px]" placeholder="https://api.example.com/v1" />
              </div>
              <div class="space-y-1">
                <Label :for="`routing-auth-${index}`" class="text-[11px]">{{ t('settings.routingAuthMode') }}</Label>
                <Select v-model="provider.authMode">
                  <SelectTrigger :id="`routing-auth-${index}`" class="h-8 text-xs"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="bearer">Bearer Token</SelectItem>
                    <SelectItem value="x-api-key">X-API-Key</SelectItem>
                    <SelectItem value="passthrough">{{ t('settings.routingAuthPassthrough') }}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div class="space-y-1">
              <Label :for="`routing-api-key-${index}`" class="flex items-center gap-1.5 text-[11px]"><KeyRound :size="12" />API Key</Label>
              <div class="flex gap-1.5">
                <Input
                  v-model="provider.apiKey"
                  :id="`routing-api-key-${index}`"
                  :type="visibleKeys.has(provider.id) ? 'text' : 'password'"
                  maxlength="4096"
                  autocomplete="off"
                  class="h-8 flex-1 font-mono text-xs"
                  :disabled="provider.authMode === 'passthrough'"
                  :placeholder="provider.hasApiKey && !provider.clearApiKey ? t('settings.routingKeySaved') : t('settings.routingKeyPlaceholder')"
                  @update:model-value="provider.clearApiKey = false"
                />
                <Button type="button" variant="outline" size="icon-sm" :aria-label="t('settings.routingToggleKey')" @click="toggleKeyVisibility(provider.id)">
                  <EyeOff v-if="visibleKeys.has(provider.id)" :size="13" />
                  <Eye v-else :size="13" />
                </Button>
                <Button v-if="provider.hasApiKey || provider.apiKey" type="button" variant="ghost" size="sm" class="h-8 text-[10px] text-destructive" @click="clearProviderKey(provider)">
                  {{ t('settings.routingClearKey') }}
                </Button>
              </div>
              <p class="text-[10px] text-muted-foreground">{{ t('settings.routingKeyHint') }}</p>
            </div>

            <div class="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg bg-muted/30 px-3 py-2 text-[10px] text-muted-foreground">
              <span class="inline-flex items-center gap-1"><Activity :size="11" />{{ t('settings.routingRequests', { count: provider.requestCount }) }}</span>
              <span v-if="provider.lastLatencyMs > 0">{{ t('settings.routingLatency', { value: provider.lastLatencyMs }) }}</span>
              <span v-if="provider.lastStatus">HTTP {{ provider.lastStatus }}</span>
              <span v-if="provider.consecutiveFailures">{{ t('settings.routingFailures', { count: provider.consecutiveFailures }) }}</span>
              <span v-if="formatOpenUntil(provider.openUntil)">{{ t('settings.routingRetryAt', { time: formatOpenUntil(provider.openUntil) }) }}</span>
              <span v-if="provider.lastError" class="min-w-0 flex-1 truncate text-destructive">{{ provider.lastError }}</span>
            </div>
          </article>
        </div>

        <div v-else class="grid min-h-32 place-items-center px-4 py-8 text-center text-xs text-muted-foreground">
          <div>
            <ShieldCheck :size="22" class="mx-auto mb-2 text-primary" />
            <p>{{ t('settings.routingProvidersEmpty') }}</p>
            <Button type="button" variant="outline" size="sm" class="mt-3" @click="addProvider">
              <Plus :size="13" class="mr-1.5" />{{ t('settings.routingAddProvider') }}
            </Button>
          </div>
        </div>
      </section>

      <section class="sticky bottom-0 z-10 flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card/95 px-4 py-3 shadow-sm backdrop-blur">
        <div class="min-w-0">
          <div class="flex items-center gap-2 text-[12px] font-medium">
            <Badge variant="outline" :class="view?.codexApplied ? 'border-positive/40 text-positive' : 'text-muted-foreground'">
              {{ view?.codexApplied ? t('settings.routingCodexApplied') : t('settings.routingCodexNotApplied') }}
            </Badge>
          </div>
          <p class="mt-1 text-[10px] text-muted-foreground">{{ t('settings.routingCodexHint') }}</p>
        </div>
        <div class="flex flex-wrap items-center justify-end gap-2">
          <Button type="button" variant="ghost" size="sm" :disabled="Boolean(busy)" @click="refreshRouter()">
            <RefreshCw :size="13" class="mr-1.5" :class="busy === 'refresh' ? 'animate-spin' : ''" />{{ t('common.refresh') }}
          </Button>
          <Button type="button" variant="ghost" size="sm" :disabled="Boolean(busy)" @click="resetCircuits">
            <RotateCcw :size="13" class="mr-1.5" />{{ t('settings.routingResetCircuits') }}
          </Button>
          <Button v-if="view?.codexApplied" type="button" variant="outline" size="sm" :disabled="Boolean(busy)" @click="restoreCodex">
            {{ t('settings.routingRestoreCodex') }}
          </Button>
          <Button type="button" variant="outline" size="sm" :disabled="Boolean(busy)" @click="saveRouter()">
            <LoaderCircle v-if="busy === 'save'" :size="13" class="mr-1.5 animate-spin" />{{ t('settings.routingSave') }}
          </Button>
          <Button type="button" size="sm" :disabled="Boolean(busy) || !canApply" @click="applyToCodex">
            <LoaderCircle v-if="busy === 'apply'" :size="13" class="mr-1.5 animate-spin" />{{ t('settings.routingApplyCodex') }}
          </Button>
        </div>
      </section>
    </template>
  </div>
</template>

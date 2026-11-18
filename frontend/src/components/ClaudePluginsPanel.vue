<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Card } from '@/components/ui/card'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { ClaudePluginCatalog, ClaudePluginView } from '../../bindings/nice_codex_desktop/models'

const props = defineProps<{ workspace: string; refreshKey?: number }>()
const emit = defineEmits<{ loaded: [plugins: ClaudePluginView[]] }>()
const catalog = shallowRef<ClaudePluginCatalog | null>(null)
const loading = shallowRef(false)
const mutation = shallowRef('')
const query = shallowRef('')
const error = shallowRef('')
const notice = shallowRef('')
const verified = shallowRef(false)
const detailsName = shallowRef('')
const detailsReport = shallowRef('')
const detailsError = shallowRef('')
const detailsLoading = shallowRef(false)
let detailsRevision = 0
let detailsRequest: ReturnType<typeof backend.ReadClaudePluginDetails> | null = null
let revision = 0
let disposed = false
let readRequest: ReturnType<typeof backend.ReadClaudePluginCatalog> | null = null
const plugins = computed(() => (catalog.value?.plugins || []).filter(plugin =>
  `${plugin.name} ${scopeLabel(plugin.scope)} ${plugin.scope} ${plugin.version} ${plugin.folderVersion || ''} ${(plugin.mcpServerNames || []).join(' ')}`.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase()),
))
function clearDetails(): void {
  detailsRevision++
  detailsRequest?.cancel()
  detailsRequest = null
  detailsName.value = ''
  detailsReport.value = ''
  detailsError.value = ''
  detailsLoading.value = false
}
async function showDetails(name: string): Promise<void> {
  if (loading.value || mutation.value || !catalog.value?.native) return
  clearDetails()
  const token = detailsRevision
  const workspace = props.workspace
  detailsName.value = name
  detailsLoading.value = true
  const request = backend.ReadClaudePluginDetails(name, workspace)
  detailsRequest = request
  try {
    const result = await request
    if (disposed || token !== detailsRevision || workspace !== props.workspace) return
    if (result.name !== name || result.workspace !== workspace) throw new Error('插件或项目已变化，请重新检测后查看。')
    detailsReport.value = result.report
  } catch (reason) {
    if (disposed || token !== detailsRevision || workspace !== props.workspace) return
    detailsError.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    if (token === detailsRevision) { detailsLoading.value = false; detailsRequest = null }
  }
}
function pluginKey(plugin: ClaudePluginView): string {
  return JSON.stringify([plugin.name, plugin.scope, plugin.projectPath, plugin.path, plugin.version])
}
function scopeLabel(scope: string): string {
  return ({ user: '用户 · 所有项目', project: '项目 · 团队共享', local: '本地 · 当前项目', managed: '组织管理', synced: '账户同步', session: '当前会话' } as Record<string, string>)[scope] || scope
}
function writable(plugin: ClaudePluginView): boolean {
  return Boolean(catalog.value?.native && typeof plugin.enabled === 'boolean'
    && ['user', 'project', 'local'].includes(plugin.scope))
}
async function refresh(): Promise<void> {
  if (mutation.value) return
  clearDetails()
  const token = ++revision
  const workspace = props.workspace
  readRequest?.cancel()
  loading.value = true
  error.value = ''
  notice.value = ''
  const request = backend.ReadClaudePluginCatalog(workspace)
  readRequest = request
  try {
    const next = await request
    if (disposed || token !== revision || workspace !== props.workspace) return
    catalog.value = next
    emit('loaded', next.plugins || [])
  } catch (reason) {
    if (disposed || token !== revision) return
    catalog.value = null
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    if (token === revision) { loading.value = false; readRequest = null }
  }
}
async function toggle(plugin: ClaudePluginView): Promise<void> {
  if (mutation.value || loading.value || !writable(plugin)) return
  clearDetails()
  const workspace = props.workspace
  const token = ++revision
  mutation.value = pluginKey(plugin)
  error.value = ''
  notice.value = ''
  try {
    const result = await backend.SetClaudePluginEnabled({ name: plugin.name, scope: plugin.scope, workspace, enabled: !plugin.enabled })
    if (disposed || token !== revision || workspace !== props.workspace) return
    catalog.value = result.catalog
    verified.value = result.verified
    notice.value = result.message
    emit('loaded', result.catalog.plugins || [])
  } catch (reason) {
    if (disposed || token !== revision || workspace !== props.workspace) return
    // A CLI command may have written before failing. Keep old values read-only
    // until an explicit refresh confirms the actual state.
    if (catalog.value) catalog.value = { ...catalog.value, native: false }
    error.value = `${reason instanceof Error ? reason.message : String(reason)}。请重新检测插件状态。`
  } finally {
    mutation.value = ''
    if (!disposed && (workspace !== props.workspace || token !== revision)) void refresh()
  }
}
watch(() => [props.workspace, props.refreshKey], () => {
  clearDetails()
  revision++
  readRequest?.cancel()
  catalog.value = null
  notice.value = ''
  error.value = ''
  if (!mutation.value) void refresh()
}, { immediate: true })
onBeforeUnmount(() => { disposed = true; revision++; readRequest?.cancel(); clearDetails() })
</script>

<template>
  <div class="min-w-0 space-y-3">
    <div class="flex flex-wrap items-center gap-2">
      <Input v-model="query" class="min-w-0 flex-1" placeholder="搜索插件、版本或范围" aria-label="搜索 Claude 插件" />
      <Button type="button" variant="outline" :disabled="loading || !!mutation" @click="refresh"><RefreshCw :size="13" class="mr-2" />{{ loading ? '检测中…' : '重新检测' }}</Button>
    </div>
    <p class="text-xs leading-5 text-muted-foreground">只显示用户插件与当前项目的安装记录。启停操作由 Claude Code 保存到卡片标注的范围；状态以当前项目合并配置为准。</p>
    <p v-if="catalog?.warning" role="alert" class="break-words text-xs text-destructive">{{ catalog.warning }}</p>
    <p v-if="error" role="alert" class="break-words text-xs text-destructive">{{ error }}</p>
    <p v-if="notice" role="status" class="break-words text-xs" :class="verified ? 'text-muted-foreground' : 'text-destructive'">{{ notice }}</p>
    <section v-if="detailsName" class="min-w-0 space-y-3 rounded-lg border bg-muted/20 p-4" :aria-busy="detailsLoading" aria-label="插件能力与开销">
      <div class="flex min-w-0 flex-wrap items-start justify-between gap-2">
        <p class="min-w-0 flex-1 break-all text-[13px] font-medium">{{ detailsName }} · 能力与开销</p>
        <Button type="button" variant="ghost" size="sm" @click="clearDetails">关闭报告</Button>
      </div>
      <p class="text-xs leading-5 text-muted-foreground">Claude Code 按当前项目的合并配置解析生效插件；同名插件的报告不区分安装范围。组件清单和 Token 开销来自原生命令，开销是估算值，不是实际用量或账单。</p>
      <p v-if="detailsLoading" role="status" class="text-xs text-muted-foreground">正在读取原生组件清单与开销…</p>
      <div v-else-if="detailsError" class="space-y-2">
        <p role="alert" class="break-words text-xs text-destructive">{{ detailsError }}</p>
        <Button type="button" variant="outline" size="sm" :disabled="loading || !!mutation" @click="showDetails(detailsName)">重试报告</Button>
      </div>
      <pre v-else class="max-h-80 overflow-y-auto whitespace-pre-wrap break-words rounded-md border bg-background p-3 text-[11px] leading-5 [overflow-wrap:anywhere]" tabindex="0" aria-label="Claude 原生插件报告">{{ detailsReport }}</pre>
    </section>
    <p v-if="loading && !catalog" role="status" class="py-8 text-center text-xs text-muted-foreground">正在读取 Claude 原生插件状态…</p>
    <Card v-for="plugin in plugins" :key="pluginKey(plugin)" class="min-w-0 gap-3 p-4">
      <div class="flex min-w-0 flex-wrap items-start justify-between gap-3">
        <div class="min-w-0 flex-1 space-y-2">
          <p class="break-all text-[13px] font-medium">{{ plugin.name }}</p>
          <div class="flex flex-wrap gap-1.5">
            <Badge variant="secondary" class="text-[10px]">{{ scopeLabel(plugin.scope) }}</Badge>
            <Badge v-if="plugin.folderVersion" variant="outline" class="max-w-full whitespace-normal break-all text-[10px]">加载 {{ plugin.folderVersion }}</Badge>
            <Badge v-if="plugin.version && plugin.version !== plugin.folderVersion" variant="outline" class="max-w-full whitespace-normal break-all text-[10px]">{{ plugin.folderVersion ? '安装 ' : '' }}{{ plugin.version }}</Badge>
            <Badge v-if="plugin.hasUserConfig && catalog?.native" variant="secondary" class="text-[10px]">支持插件设置</Badge>
            <Badge variant="outline" class="text-[10px]">{{ typeof plugin.enabled === 'boolean' && catalog?.native ? (plugin.enabled ? (plugin.errors?.length ? '已启用 · 加载异常' : '原生已启用') : '原生已停用') : '状态未确认' }}</Badge>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Button v-if="catalog?.native && plugin.enabled && !plugin.errors?.length" type="button" variant="outline" size="sm" :disabled="loading || !!mutation || detailsLoading" :aria-label="`查看 ${plugin.name} 的生效能力与开销`" @click="showDetails(plugin.name)">能力与开销</Button>
          <Button v-if="writable(plugin)" type="button" variant="outline" size="sm" :disabled="loading || !!mutation" :aria-label="`${plugin.enabled ? '停用' : '启用'} ${plugin.name} · ${scopeLabel(plugin.scope)}`" @click="toggle(plugin)">
            {{ mutation === pluginKey(plugin) ? '正在保存…' : plugin.enabled ? '停用插件' : '启用插件' }}
          </Button>
          <span v-else class="text-xs text-muted-foreground">只读</span>
        </div>
      </div>
      <p v-if="plugin.mcpServerNames?.length && catalog?.native" class="break-words text-[11px] text-muted-foreground [overflow-wrap:anywhere]">MCP 服务（{{ plugin.mcpServerNames.length }}）：{{ plugin.mcpServerNames.join('、') }}</p>
      <p v-if="plugin.projectPath" class="break-all text-[11px] text-muted-foreground">项目：{{ plugin.projectPath }}</p>
      <details v-if="plugin.path || plugin.errors?.length || plugin.notes?.length" :open="!!plugin.errors?.length" class="min-w-0 text-xs text-muted-foreground">
        <summary class="cursor-pointer">加载位置与诊断{{ plugin.errors?.length ? ` · ${plugin.errors.length} 个错误` : '' }}</summary>
        <p class="mt-2 break-all font-mono text-[10px]">{{ plugin.path }}</p>
        <p v-for="(message, index) in plugin.errors" :key="`error-${index}`" class="mt-2 break-words text-destructive">{{ message }}</p>
        <p v-for="(message, index) in plugin.notes" :key="`note-${index}`" class="mt-2 break-words">{{ message }}</p>
      </details>
    </Card>
    <p v-if="!loading && catalog && !plugins.length" class="rounded-lg border border-dashed px-4 py-8 text-center text-xs text-muted-foreground">{{ query.trim() ? '没有匹配的插件，请调整搜索条件。' : '当前项目未发现已安装插件。可先在 Claude Code 的 /plugin 中安装，再点击重新检测。' }}</p>
  </div>
</template>

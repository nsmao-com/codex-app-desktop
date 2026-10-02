<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import SettingsCard from '@/components/SettingsCard.vue'
import { checkFastCtx, syncFastCtx, type FastCtxStatus } from '@/utils/fastctx'

const status = shallowRef<FastCtxStatus | null>(null)
const busy = shallowRef<'check' | 'sync' | 'apply' | ''>('')
const shellEnabled = shallowRef(false)
const error = shallowRef('')
const message = shallowRef('')
const actionOutput = shallowRef('')
const restartRequired = shallowRef(false)
const shellChanged = computed(() => status.value !== null && shellEnabled.value !== status.value.shellEnabled)
const stateLabel = computed(() => {
  if (!status.value) return '等待检测'
  if (shellChanged.value) return '工具设置待应用'
  return {
    not_installed: '未安装', not_applied: '未应用', needs_apply: '需要重新应用',
    needs_attention: '需要处理', applied: '已应用',
  }[status.value.state]
})
const primaryLabel = computed(() => {
  if (busy.value === 'sync') return '安装 / 同步更新中…'
  return status.value?.installed ? '同步上游更新并应用' : '安装并应用到 Codex'
})

async function refresh(checkUpdates = true): Promise<void> {
  if (busy.value) return
  busy.value = 'check'
  error.value = ''
  try {
    const next = await checkFastCtx(checkUpdates)
    // A status refresh must not discard a pending choice.
    if (!shellChanged.value) shellEnabled.value = next.shellEnabled
    status.value = next
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    busy.value = ''
  }
}

async function apply(update: boolean): Promise<void> {
  if (busy.value) return
  busy.value = update ? 'sync' : 'apply'
  error.value = ''
  message.value = ''
  actionOutput.value = ''
  try {
    const result = await syncFastCtx(update, shellEnabled.value)
    status.value = result.status
    actionOutput.value = result.output
    restartRequired.value ||= result.restartRequired
    if (result.ok) message.value = result.message
    else error.value = result.message
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    busy.value = ''
  }
}

void refresh()
</script>

<template>
  <SettingsCard title="FastCtx · 仅 Codex" description="为 Codex 提供文件读取、搜索、匹配和批量替换工具，减少工具输出占用的上下文。">
    <div class="space-y-4 p-4" :aria-busy="Boolean(busy)">
      <div class="flex flex-wrap items-center gap-2" role="status" aria-live="polite">
        <Badge variant="outline">{{ busy === 'check' ? '正在检测…' : stateLabel }}</Badge>
        <Badge v-if="status?.updateAvailable" variant="secondary">上游有新版本</Badge>
        <span v-if="status?.version" class="text-xs text-muted-foreground">安装 {{ status.version }} · Codex 使用 {{ status.managedVersion || '尚未应用' }}</span>
        <span v-if="status?.latestVersion" class="text-xs text-muted-foreground">上游 {{ status.latestVersion }}</span>
      </div>
      <p v-if="status" class="text-xs leading-6 text-muted-foreground">{{ status.message }}</p>
      <p v-else class="text-xs leading-6 text-muted-foreground">{{ busy === 'check' ? '正在检查本地安装、Codex 应用状态和上游版本，首次服务检查可能需要一些时间。' : '暂未取得检测结果，请点击“检测状态与更新”重试。' }}</p>
      <div class="flex items-center justify-between gap-4 rounded-lg border p-3">
        <div class="min-w-0 space-y-1">
          <label for="fastctx-shell" class="text-[13px] font-medium">命令执行与后台任务</label>
          <p class="text-xs leading-5 text-muted-foreground">增加命令执行、后台任务及日志工具。Windows 需要 Git Bash；更改后点击应用。</p>
        </div>
        <Switch id="fastctx-shell" :checked="shellEnabled" :disabled="Boolean(busy) || !status" @update:checked="shellEnabled = $event" />
      </div>
      <p class="text-xs leading-6 text-muted-foreground">应用会按 FastCtx 官方规则更新当前 Codex 的 MCP 配置、工具输出预算和全局 AGENTS 指令块。保留上游已有的档位设置；同步更新会安装官方最新发布包并重新应用。</p>
      <div class="flex flex-wrap gap-2">
        <Button type="button" :disabled="Boolean(busy) || !status?.canInstall" @click="apply(true)">
          <RefreshCw v-if="busy === 'sync'" :size="14" class="mr-1.5 animate-spin" aria-hidden="true" />{{ primaryLabel }}
        </Button>
        <Button v-if="status?.installed" type="button" variant="outline" :disabled="Boolean(busy)" @click="apply(false)">
          {{ busy === 'apply' ? '应用并复检中…' : shellChanged || status.state !== 'applied' ? '应用到 Codex' : '重新应用' }}
        </Button>
        <Button type="button" variant="outline" :disabled="Boolean(busy)" @click="refresh(true)">检测状态与更新</Button>
        <Button type="button" variant="ghost" :disabled="Boolean(busy)" @click="refresh(false)">仅检测本地</Button>
      </div>
      <p v-if="status && !status.canInstall" class="text-xs text-muted-foreground">安装与同步更新需要 Node.js 18 或更新版本及 pnpm；已安装的 FastCtx 仍可检测和重新应用。</p>
      <p v-if="status?.updateError" class="text-xs text-muted-foreground">{{ status.updateError }}</p>
      <p v-if="message" role="status" class="text-xs leading-6">{{ message }}</p>
      <p v-if="error" role="alert" class="break-words text-xs leading-6 text-destructive">{{ error }}</p>
      <p v-if="restartRequired || status?.state === 'applied'" class="text-xs leading-6 text-muted-foreground">应用后请重启 Codex 连接或 NiceCodex，并在新会话调用一次 FastCtx 工具确认可用。这里的检测验证本地配置与服务，不代表当前对话已加载工具。</p>
      <details v-if="status?.output || actionOutput || status?.codexHome" class="text-xs">
        <summary class="cursor-pointer py-1 text-muted-foreground">检测与操作详情</summary>
        <p class="mt-2 break-all text-muted-foreground">Codex 配置目录：{{ status?.codexHome }}</p>
        <pre v-if="status?.output" class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted p-3">{{ status.output }}</pre>
        <pre v-if="actionOutput" class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted p-3">{{ actionOutput }}</pre>
      </details>
    </div>
  </SettingsCard>
</template>

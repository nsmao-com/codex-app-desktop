<script setup lang="ts">
import { computed, onMounted, shallowRef } from 'vue'
import { Monitor, RefreshCw } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import SettingsCard from '@/components/SettingsCard.vue'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { normalizeExperimentalFeatures } from '@/utils/capabilities'

const router = useRouter()
const enabled = shallowRef(false)
const supported = shallowRef(false)
const busy = shallowRef(false)
const error = shallowRef('')
const saved = shallowRef(false)
const effectiveEnabled = shallowRef<boolean | null>(null)
const statusText = computed(() => {
  if (busy.value) return '正在检测或保存…'
  if (effectiveEnabled.value === null) return '尚未确认原生状态，请连接 Codex 后重新检测。'
  return effectiveEnabled.value ? 'CLI 已允许此能力，仍需插件与系统权限就绪。' : 'CLI 当前未允许此能力。'
})

async function readEffectiveState(): Promise<void> {
  const feature = normalizeExperimentalFeatures(await backend.ListExperimentalFeatures()).find(item => item.name === 'computer_use')
  supported.value = Boolean(feature && feature.stage !== 'removed')
  effectiveEnabled.value = supported.value && feature ? feature.enabled : null
}

async function refresh(): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await readEffectiveState()
    enabled.value = effectiveEnabled.value === true
    saved.value = false
  } catch {
    supported.value = false
    effectiveEnabled.value = null
    error.value = '无法读取 Codex 能力。请先连接 Codex，再重新检测。'
  } finally { busy.value = false }
}
async function toggle(value: boolean): Promise<void> {
  if (busy.value || !supported.value) return
  busy.value = true
  error.value = ''
  try {
    await backend.SaveComputerUseSetting(value)
    enabled.value = value
    saved.value = true
    try {
      await readEffectiveState()
    } catch {
      effectiveEnabled.value = null
      error.value = '配置已保存，但无法确认 CLI 状态。请重新检测。'
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = false }
}
onMounted(refresh)
</script>

<template>
  <SettingsCard title="Computer Use · 电脑操作" description="通过截图、点击和键盘操作应用。启用前需确认 Codex 支持、插件可用，以及系统权限就绪。">
    <div class="space-y-5 p-5">
      <div class="flex items-center justify-between gap-5">
        <div class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground"><Monitor :size="16" class="shrink-0 text-primary" />允许新会话使用电脑操作</div>
        <Switch :checked="enabled" :disabled="busy || !supported || effectiveEnabled === null" aria-label="允许新会话使用 Computer Use" @update:checked="toggle" />
      </div>
      <p v-if="error" class="text-xs text-destructive" role="alert">{{ error }}</p>
      <p v-if="saved" class="text-xs text-muted-foreground" role="status">配置已保存。已有任务请在结束后使用左下角“重新加载配置”。</p>
      <p class="text-xs text-muted-foreground" aria-live="polite">{{ statusText }}</p>
      <p v-if="saved && effectiveEnabled !== null && effectiveEnabled !== enabled" class="text-xs text-destructive" role="status">保存值与 CLI 状态不一致，可能被项目配置或管理策略覆盖。请检查配置后重新检测。</p>
      <div class="rounded-xl bg-muted/40 p-4 text-xs leading-6 space-y-2">
        <p>1. 在插件中安装并启用 Computer Use 的服务器与 Skill。</p>
        <p>2. Windows 需保持目标窗口可见，运行时会占用前台鼠标和键盘；macOS 需授予录屏与辅助功能权限。</p>
        <p>3. 应用授权与终端沙箱相互独立。不自动添加“始终允许”应用。</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="busy" @click="refresh"><RefreshCw :size="13" class="mr-2" />重新检测</Button>
        <Button type="button" variant="outline" @click="router.replace({ name: 'settings', query: { section: 'capabilities', tab: 'plugins' } })">管理插件</Button>
        <Button type="button" variant="ghost" @click="backend.OpenExternal('https://developers.openai.com/codex/computer-use')">官方设置说明</Button>
      </div>
    </div>
  </SettingsCard>
</template>

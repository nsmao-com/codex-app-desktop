<script setup lang="ts">
import { onMounted, shallowRef } from 'vue'
import { Monitor, RefreshCw } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { normalizeExperimentalFeatures } from '@/utils/capabilities'

const router = useRouter()
const enabled = shallowRef(false)
const supported = shallowRef(false)
const busy = shallowRef(false)
const error = shallowRef('')
const saved = shallowRef(false)
async function refresh(): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const feature = normalizeExperimentalFeatures(await backend.ListExperimentalFeatures()).find(item => item.name === 'computer_use')
    supported.value = Boolean(feature)
    enabled.value = await backend.ReadComputerUseSetting()
    saved.value = false
  } catch {
    supported.value = false
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
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = false }
}
onMounted(refresh)
</script>

<template>
  <section class="rounded-2xl border bg-card p-5 space-y-5">
    <div class="flex items-start justify-between gap-5">
      <div class="space-y-2"><Monitor :size="22" class="text-primary" /><h2 class="text-base font-semibold">Computer Use · 电脑操作</h2><p class="max-w-xl text-xs leading-6 text-muted-foreground">让 Codex 通过截图、点击和键盘操作应用。检测 CLI 支持后，仅保存 config.toml 中的 features.computer_use；不会自动安装插件或放行应用权限。</p></div>
      <Switch :checked="enabled" :disabled="busy || !supported" aria-label="启用 Computer Use" @update:checked="toggle" />
    </div>
    <p v-if="error" class="text-xs text-destructive" role="alert">{{ error }}</p>
    <p v-else-if="saved" class="text-xs text-primary" role="status">配置已保存。请结束当前任务后重新连接 Codex，使新开关生效。管理员策略可能限制此能力。</p>
    <p v-else class="text-xs text-muted-foreground" aria-live="polite">{{ busy ? '正在检测或保存…' : supported ? (enabled ? '原生能力已开启，仍需插件与系统权限就绪。' : '原生能力已关闭。') : '当前 CLI 未返回 Computer Use 能力，请检查版本和连接。' }}</p>
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
  </section>
</template>

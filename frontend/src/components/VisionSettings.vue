<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useAppStore } from '@/stores'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import SearchableSelect from '@/components/SearchableSelect.vue'
import SettingsCard from '@/components/SettingsCard.vue'
import * as backend from '../../bindings/nice_codex_desktop/appservice'

const app = useAppStore()
const draft = ref({ ...app.settings.vision, protocol: app.settings.vision?.protocol || 'chat' })
const busy = shallowRef(false)
const loading = shallowRef(false)
const saved = shallowRef(false)
const error = shallowRef('')
const models = shallowRef<string[]>([])
const manual = shallowRef(false)
let catalogRequest: ReturnType<typeof backend.ListVisionModels> | null = null
let catalogRevision = 0
const options = computed(() => [...new Set([draft.value.model, ...models.value].filter(Boolean))].map(value => ({ value, label: value })))
const protocols = [
  { value: 'chat', label: 'OpenAI 兼容 · Chat Completions' },
  { value: 'responses', label: 'OpenAI · Responses' },
  { value: 'anthropic', label: 'Anthropic · Messages' },
]
const valid = computed(() => !draft.value.defaultEnabled || Boolean(draft.value.baseURL.trim() && draft.value.model.trim()))
function cancelCatalog(): void {
  catalogRevision++
  catalogRequest?.cancel()
  catalogRequest = null
  loading.value = false
}
watch(() => [draft.value.baseURL, draft.value.apiKey, draft.value.protocol], () => {
  cancelCatalog()
  models.value = []
})
watch(draft, () => { saved.value = false }, { deep: true })
onBeforeUnmount(cancelCatalog)
async function loadModels(): Promise<void> {
  if (loading.value || !draft.value.baseURL.trim()) return
  error.value = ''
  loading.value = true
  const revision = ++catalogRevision
  const request = backend.ListVisionModels({ ...draft.value })
  catalogRequest = request
  try {
    const result = await request
    if (revision !== catalogRevision) return
    models.value = result || []
    if (!models.value.length) error.value = '接口未返回模型，可打开手动填写。模型目录不保证每个模型都支持图片。'
  } catch (reason) {
    if (revision === catalogRevision) error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    if (revision === catalogRevision) { loading.value = false; catalogRequest = null }
  }
}
async function save(): Promise<void> {
  if (busy.value || !valid.value) return
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    await app.savePreferences({ ...app.settings, vision: { ...draft.value } }, { silent: true })
    saved.value = true
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = false }
}
</script>

<template>
  <SettingsCard title="独立视觉模型" description="主模型不支持看图时，先用视觉模型分析图片，再把文字描述交给主模型继续任务。适用于所有服务商的消息输入框。">
    <div class="space-y-5 p-5">
      <label class="flex items-center justify-between gap-4 text-sm">
        <span>有图片时默认使用独立视觉模型</span>
        <Switch v-model:checked="draft.defaultEnabled" aria-label="默认使用独立视觉模型" :disabled="busy" />
      </label>
      <p class="text-xs leading-6 text-muted-foreground">默认关闭，直接发送原图。每次发送前都可以在图片下方更改；无需修改主模型或 CLI 登录。</p>
      <label class="block space-y-2 text-xs">接口协议<SearchableSelect v-model="draft.protocol" :disabled="busy" :options="protocols" aria-label="视觉接口协议" /></label>
      <label class="block space-y-2 text-xs">接口地址<Input v-model="draft.baseURL" autocomplete="off" placeholder="https://你的接口地址/v1" :disabled="busy" /></label>
      <label class="block space-y-2 text-xs">API Key<Input v-model="draft.apiKey" type="password" autocomplete="off" placeholder="本机免认证接口可留空" :disabled="busy" /></label>
      <div class="space-y-2">
        <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
          <span>视觉模型</span>
          <Button type="button" variant="outline" size="sm" :disabled="loading || busy || !draft.baseURL.trim()" @click="loadModels">{{ loading ? '读取中…' : '读取模型列表' }}</Button>
        </div>
        <SearchableSelect v-if="!manual" v-model="draft.model" :disabled="busy" :options="options" placeholder="读取列表后选择支持图片的模型" aria-label="视觉模型" />
        <Input v-else v-model="draft.model" autocomplete="off" placeholder="接口不提供目录时，填写完整模型 ID" aria-label="手动填写视觉模型 ID" :disabled="busy" />
        <Button type="button" variant="ghost" size="sm" @click="manual = !manual">{{ manual ? '返回模型选择' : '接口没有目录？手动填写' }}</Button>
        <p class="text-xs leading-5 text-muted-foreground">目录只用于选择，不代表模型已具备图片能力。请选择服务商明确支持图片输入的模型。</p>
      </div>
      <div class="rounded-lg bg-muted/40 p-3 text-xs leading-6 text-muted-foreground">
        <p>选择“视觉模型转文字”后，发送时会把本条问题和图片提交到上方接口，再将结果作为参考交给主模型。主模型收到描述，不会收到原图。</p>
        <p>每次最多 8 张、总计 20 MB。分析可能产生额外 API 费用；失败或取消会保留草稿，不会自动改发到其他接口。</p>
        <p>API Key 按现有偏好设置保存在本机配置文件中，不是系统密钥保险库。CLI/OAuth 登录凭据不会被复用。</p>
      </div>
      <div class="flex flex-wrap items-center gap-3"><Button type="button" :disabled="busy || !valid" @click="save">{{ busy ? '保存中…' : '保存视觉设置' }}</Button><span v-if="saved" role="status" class="text-xs text-muted-foreground">已保存</span></div>
      <p v-if="error" role="alert" class="break-words text-xs text-destructive">{{ error }}</p>
    </div>
  </SettingsCard>
</template>

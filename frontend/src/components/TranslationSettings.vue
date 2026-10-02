<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import { useAppStore } from '@/stores'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import SearchableSelect from '@/components/SearchableSelect.vue'
import SettingsCard from '@/components/SettingsCard.vue'

const app = useAppStore()
const draft = ref({
  translationProvider: app.settings.translationProvider || 'google',
  translationBaseURL: app.settings.translationBaseURL || '',
  translationModel: app.settings.translationModel || '',
  translationAPIKey: app.settings.translationAPIKey || '',
  translationGoogleKey: app.settings.translationGoogleKey || '',
})
const busy = shallowRef(false)
const error = shallowRef('')
const saved = shallowRef(false)
const options = [
  { value: 'google', label: 'Google 翻译（默认）' },
  { value: 'current', label: '使用当前对话的模型厂商配置' },
  { value: 'custom', label: '自定义模型接口' },
]
const valid = computed(() => draft.value.translationProvider !== 'custom' || (draft.value.translationBaseURL.trim() && draft.value.translationModel.trim()))
async function save(): Promise<void> {
  if (busy.value || !valid.value) return
  busy.value = true
  saved.value = false
  error.value = ''
  try {
    await app.savePreferences({ ...app.settings, ...draft.value }, { silent: true })
    saved.value = true
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = false }
}
</script>

<template>
  <SettingsCard title="消息翻译" description="只翻译你选择的一条消息，不修改原文，也不打断正在运行的任务。">
    <div class="space-y-5 p-5" @input="saved = false">
      <label class="block space-y-2 text-xs">翻译服务<SearchableSelect v-model="draft.translationProvider" :options="options" aria-label="翻译服务" @update:model-value="saved = false" /></label>
      <template v-if="draft.translationProvider === 'google'">
        <p class="rounded-lg border border-primary/20 bg-primary/5 px-3 py-2 text-xs leading-6 text-muted-foreground">Google 翻译开箱即用，无需填写 API Key。程序只会在点击翻译时发送选中的文本。</p>
      </template>
      <template v-else-if="draft.translationProvider === 'custom'">
        <label class="block space-y-2 text-xs">Base URL<Input v-model="draft.translationBaseURL" autocomplete="off" placeholder="https://你的接口地址/v1" /></label>
        <label class="block space-y-2 text-xs">模型<Input v-model="draft.translationModel" autocomplete="off" placeholder="服务商支持的完整模型 ID" /></label>
        <label class="block space-y-2 text-xs">API Key<Input v-model="draft.translationAPIKey" type="password" autocomplete="off" placeholder="仅本地免认证接口可留空" /></label>
        <p class="text-xs leading-6 text-muted-foreground">使用 OpenAI 兼容 Chat Completions 协议；程序在 Base URL 后追加 /chat/completions。远程接口须用 HTTPS，本机接口可用 HTTP。</p>
      </template>
      <p v-else class="text-xs leading-6 text-muted-foreground">跟随消息所属对话的厂商，读取其 API 地址、具体模型和 API Key。支持 Codex Responses、Claude Messages、Grok 及 OpenCode 已配置接口。CLI/OAuth 登录（包括 Antigravity 登录）无法自动转成 API Key；无可复用配置时会提示改用 Google 或自定义接口。</p>
      <p class="text-xs leading-6 text-muted-foreground">填写的 Key 按现有应用偏好设置保存在本机配置文件中（不是系统密钥保险库），请勿分享配置文件。点击消息的“翻译”才会发送文本，可能产生对应服务的 API 费用。</p>
      <div class="flex items-center gap-3"><Button type="button" :disabled="busy || !valid" @click="save">{{ busy ? '保存中…' : '保存翻译设置' }}</Button><span v-if="saved" role="status" class="text-xs text-muted-foreground">已保存</span></div>
      <p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p>
    </div>
  </SettingsCard>
</template>

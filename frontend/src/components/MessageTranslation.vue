<script setup lang="ts">
import { Languages, LoaderCircle } from '@lucide/vue'
import { shallowRef, watch } from 'vue'
import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import SearchableSelect from '@/components/SearchableSelect.vue'

const props = defineProps<{ text: string; disabled?: boolean }>()
const open = shallowRef(false)
const target = shallowRef('zh-CN')
const key = shallowRef('')
const busy = shallowRef(false)
const translated = shallowRef('')
const error = shallowRef('')
let request = 0
const languages = [
  ['zh-CN', '简体中文'], ['zh-TW', '繁體中文'], ['en', 'English'], ['ja', '日本語'], ['ko', '한국어'],
  ['fr', 'Français'], ['de', 'Deutsch'], ['es', 'Español'], ['pt', 'Português'], ['ru', 'Русский'], ['ar', 'العربية'],
].map(([value, label]) => ({ value: value!, label: label! }))
watch([() => props.text, target, open], () => { request++; busy.value = false; translated.value = ''; error.value = ''; if (!open.value) key.value = '' })
async function translate(): Promise<void> {
  if (busy.value) return
  const current = ++request
  busy.value = true
  error.value = ''
  translated.value = ''
  try {
    const result = await backend.TranslateMessage(props.text, target.value, key.value)
    if (current === request) translated.value = String(result)
  } catch (reason) {
    if (current === request) error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    if (current === request) busy.value = false
  }
}
</script>

<template>
  <Button variant="ghost" size="icon-xs" class="size-7 text-muted-foreground" aria-label="使用 Google 翻译" title="翻译到其他语言" :disabled="disabled || !text.trim()" @click="open = true">
    <Languages :size="13" />
  </Button>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[85dvh] overflow-y-auto sm:max-w-2xl">
      <DialogHeader>
        <DialogTitle>翻译消息</DialogTitle>
        <DialogDescription>由 Google Cloud Translation 提供。点击翻译将发送这一条消息到 Google，可能产生 API 费用；原始对话不会被修改。</DialogDescription>
      </DialogHeader>
      <label class="space-y-2 text-xs">目标语言<SearchableSelect v-model="target" :options="languages" aria-label="目标语言" /></label>
      <label class="space-y-2 text-xs">Google Translation API Key<Input v-model="key" type="password" autocomplete="off" placeholder="仅本次使用；已设置环境变量时可留空" /></label>
      <p class="text-xs text-muted-foreground">支持 GOOGLE_TRANSLATE_API_KEY 环境变量。长消息上限 30,000 字符；代码和 Markdown 原文会保留在对话中。</p>
      <Button :disabled="busy || !text.trim() || [...text].length > 30000" @click="translate"><LoaderCircle v-if="busy" :size="14" class="mr-2 animate-spin" />{{ busy ? '正在翻译…' : '翻译' }}</Button>
      <p v-if="[...text].length > 30000" role="alert" class="text-sm text-destructive">这条消息超过 30,000 字符，请选用较短消息。</p>
      <p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p>
      <div v-if="translated" class="rounded-xl border bg-muted/30 p-4 text-sm leading-7 whitespace-pre-wrap break-words" aria-live="polite">{{ translated }}</div>
    </DialogContent>
  </Dialog>
</template>

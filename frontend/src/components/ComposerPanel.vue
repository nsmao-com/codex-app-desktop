<script setup lang="ts">
import {
  AlertCircle,
  ArrowUp,
  Clock3,
  Command,
  Image as ImageIcon,
  ListOrdered,
  ListTodo,
  LoaderCircle,
  Octagon,
  Paperclip,
  RotateCcw,
  Shield,
  X,
  Zap,
} from '@lucide/vue'
import { computed, nextTick, onMounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useAppStore, useCodexStore } from '@/stores'
import { notify } from '@/utils/notify'
import {
  DEFAULT_CODEX_REASONING,
  formatModelLabel,
  modelsForRuntime,
} from '@/utils/runtimeProviders'

const appStore = useAppStore()
const codexStore = useCodexStore()
const router = useRouter()
const { t } = useI18n()

const modelValue = defineModel<string>({ required: true })
const composer = useTemplateRef<HTMLElement>('composer')
const composing = shallowRef(false)
const attachedImages = shallowRef<string[]>([])
const slashIndex = shallowRef(0)

type SlashCommand = {
  id: string
  label: string
  description: string
  run: () => void | Promise<void>
}

const slashCommands = computed<SlashCommand[]>(() => [
  {
    id: 'review',
    label: '/review',
    description: t('slash.review'),
    run: () => codexStore.startReview({ targetType: 'uncommittedChanges', delivery: 'inline' }),
  },
  {
    id: 'compact',
    label: '/compact',
    description: t('slash.compact'),
    run: () => codexStore.compactActiveThread(),
  },
  {
    id: 'fork',
    label: '/fork',
    description: t('slash.fork'),
    run: () => codexStore.forkActiveThread(),
  },
  {
    id: 'archive',
    label: '/archive',
    description: t('slash.archive'),
    run: () => codexStore.archiveActiveThread(),
  },
  {
    id: 'rename',
    label: '/rename',
    description: t('slash.rename'),
    run: () => codexStore.renameActiveThread(),
  },
  {
    id: 'delete',
    label: '/delete',
    description: t('slash.delete'),
    run: () => codexStore.deleteActiveThread(),
  },
  {
    id: 'mcp',
    label: '/mcp',
    description: t('slash.mcp'),
    run: () => { void router.push({ name: 'capabilities', query: { tab: 'mcp' } }) },
  },
  {
    id: 'plan',
    label: '/plan',
    description: t('chat.planModeToggleHint'),
    run: () => togglePlanMode(),
  },
])

const slashQuery = computed(() => {
  const text = modelValue.value
  if (!text.startsWith('/') || text.includes('\n') || text.includes(' ')) return ''
  return text.slice(1).toLocaleLowerCase()
})
const slashOpen = computed(() => modelValue.value.startsWith('/') && !modelValue.value.includes('\n') && !modelValue.value.slice(1).includes(' '))
const filteredSlashCommands = computed(() => {
  const query = slashQuery.value
  if (!query) return slashCommands.value
  return slashCommands.value.filter((command) =>
    command.id.includes(query) || command.label.toLocaleLowerCase().includes(query),
  )
})

watch(filteredSlashCommands, (commands) => {
  if (slashIndex.value >= commands.length) slashIndex.value = Math.max(0, commands.length - 1)
})
const sessionLocked = computed(() => Boolean(
  codexStore.activeThreadId
  && !codexStore.activeThreadId.startsWith('pending-thread-')
  && codexStore.activeThread,
))
const displayModel = computed(() => sessionLocked.value
  ? (codexStore.activeThread?.model || appStore.settings.model)
  : appStore.settings.model)
const displayEffort = computed(() => sessionLocked.value
  ? (codexStore.activeThread?.effort || appStore.settings.effort)
  : appStore.settings.effort)
const selectedModel = computed(() => appStore.models.find((model) => model.model === displayModel.value))
const selectableModels = computed(() => modelsForRuntime(appStore.models, appStore.settings.customModels ?? []))
const reasoningOptions = computed(() => {
  const fromModel = selectedModel.value?.supportedReasoningEfforts ?? []
  return fromModel.length ? fromModel : [...DEFAULT_CODEX_REASONING]
})
const permissionLabel = computed(() => {
  if (appStore.settings.sandbox === 'danger-full-access' && appStore.settings.approvalPolicy === 'never') return t('settings.permissionAuto')
  if (appStore.settings.sandbox === 'read-only') return t('settings.permissionStrict')
  return t('settings.permissionAsk')
})
const selectedModelLabel = computed(() => {
  const id = displayModel.value
  const hit = selectableModels.value.find((model) => model.model === id)
  return hit?.displayName || formatModelLabel(id || '')
})
const selectedEffortLabel = computed(() => {
  const effort = displayEffort.value
  const option = reasoningOptions.value.find((item) => item.effort === effort)
  if (option && 'displayName' in option && option.displayName) return String(option.displayName)
  if (!effort) return ''
  return effort.charAt(0).toUpperCase() + effort.slice(1)
})
/** Only show the queue strip when something is actually waiting / failed — not the in-flight send. */
const showQueueStrip = computed(() =>
  codexStore.activeQueuedMessages.some((message) => message.state === 'queued' || message.state === 'failed'),
)
const canSend = computed(() => Boolean(modelValue.value.trim()) && codexStore.isReady && !codexStore.creatingThread)
const canSteer = computed(() =>
  codexStore.isTurnRunning
  && Boolean(codexStore.activeTurnId)
  && !codexStore.activeThreadId.startsWith('pending-thread-'),
)
const composerPlaceholder = computed(() => {
  if (canSteer.value) return t('chat.steerPlaceholder')
  if (showQueueStrip.value || (codexStore.isTurnRunning && !canSteer.value)) return t('chat.queuePlaceholder')
  return t('chat.placeholder')
})

watch(modelValue, resize, { flush: 'post' })
watch(
  [selectableModels, displayModel],
  () => {
    const models = selectableModels.value
    if (!models.length) return
    const current = displayModel.value.trim()
    if (current && models.some((model) => model.model === current)) return
    const preferred = models.find((model) => model.isDefault)?.model || models[0]?.model
    if (preferred && preferred !== current) void applyModelSelection(preferred)
  },
  { flush: 'post' },
)
onMounted(resize)

function resize(): void {
  void nextTick(() => {
    const textarea = composer.value?.querySelector('textarea')
    if (!textarea) return
    textarea.style.height = '0px'
    textarea.style.height = `${Math.min(textarea.scrollHeight, 200)}px`
  })
}

function onKeydown(event: KeyboardEvent): void {
  if (slashOpen.value && filteredSlashCommands.value.length) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      slashIndex.value = (slashIndex.value + 1) % filteredSlashCommands.value.length
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      slashIndex.value = (slashIndex.value - 1 + filteredSlashCommands.value.length) % filteredSlashCommands.value.length
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      modelValue.value = ''
      return
    }
    if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
      event.preventDefault()
      void runSlashCommand(filteredSlashCommands.value[slashIndex.value])
      return
    }
  }
  // Official Codex: Shift+Tab toggles plan mode.
  if (event.key === 'Tab' && event.shiftKey) {
    event.preventDefault()
    void togglePlanMode()
    return
  }
  if (event.key !== 'Enter' || event.shiftKey || composing.value) return
  event.preventDefault()
  void send()
}

async function runSlashCommand(command?: SlashCommand): Promise<void> {
  if (!command) return
  modelValue.value = ''
  await command.run()
}

const collaborationMode = computed(() => {
  const sessionMode = codexStore.activeThread?.collaborationMode
  if (sessionMode === 'plan' || sessionMode === 'default') return sessionMode
  return appStore.settings.collaborationMode === 'plan' ? 'plan' : 'default'
})
const isPlanMode = computed(() => collaborationMode.value === 'plan')

async function togglePlanMode(): Promise<void> {
  await codexStore.setCollaborationMode(isPlanMode.value ? 'default' : 'plan')
}

async function send(): Promise<void> {
  const message = modelValue.value.trim()
  if (!message || !codexStore.isReady) return
  const images = [...attachedImages.value]
  modelValue.value = ''
  attachedImages.value = []
  const ok = await codexStore.sendMessage(message, images)
  if (!ok) {
    modelValue.value = message
    attachedImages.value = images
  }
}

function onStop(): void {
  void codexStore.interruptTurn()
}

async function attachImages(): Promise<void> {
  if (attachedImages.value.length >= 4) return
  try {
    const selected = await backend.SelectImages() ?? []
    if (selected.length) attachedImages.value = [...new Set([...attachedImages.value, ...selected])].slice(0, 4)
  } catch (error) {
    notify('error', t('notifications.imagesNotSelected'), error instanceof Error ? error.message : t('notifications.unexpected'))
  }
}

function removeAttachment(path: string): void {
  attachedImages.value = attachedImages.value.filter((item) => item !== path)
}

function attachmentName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function onModelChange(value: string): void {
  void applyModelSelection(value)
}

async function applyModelSelection(value: string): Promise<void> {
  const modelID = value.trim()
  if (!modelID) return

  let effort = appStore.settings.effort
  let serviceTier = appStore.settings.serviceTier
  const model = appStore.models.find((item) => item.model === modelID)
  if (model) {
    const supported = model.supportedReasoningEfforts
    effort = supported.some((option) => option.effort === effort)
      ? effort
      : model.defaultReasoningEffort || supported[0]?.effort || 'high'
    serviceTier = model.serviceTiers.some((tier) => tier.id === serviceTier)
      ? serviceTier
      : model.defaultServiceTier
  }

  if (sessionLocked.value && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(modelID, effort)
    try {
      await backend.UpdateSessionPreferences({
        sessionId: codexStore.activeThread.id,
        model: modelID,
        effort,
        collaborationMode: collaborationMode.value,
      })
    } catch {
      // Keep the in-memory session selection usable for this turn.
    }
    return
  }

  appStore.updateAgentPreferences(modelID, effort, serviceTier, appStore.settings.collaborationMode)
  if (codexStore.activeThreadId.startsWith('pending-thread-') && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(modelID, effort)
  }
}

function onEffortChange(value: string): void {
  if (sessionLocked.value && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(displayModel.value, value)
    void backend.UpdateSessionPreferences({
      sessionId: codexStore.activeThread.id,
      model: displayModel.value,
      effort: value,
      collaborationMode: collaborationMode.value,
    }).catch(() => undefined)
    return
  }
  appStore.updateAgentPreferences(displayModel.value || appStore.settings.model, value, appStore.settings.serviceTier, appStore.settings.collaborationMode)
  if (codexStore.activeThreadId.startsWith('pending-thread-') && codexStore.activeThread) {
    codexStore.patchActiveSessionPreferences(displayModel.value, value)
  }
}

function setPermission(mode: 'ask' | 'auto' | 'strict'): void {
  const values = mode === 'auto'
    ? { sandbox: 'danger-full-access', approvalPolicy: 'never' }
    : mode === 'strict'
      ? { sandbox: 'read-only', approvalPolicy: 'untrusted' }
      : { sandbox: 'workspace-write', approvalPolicy: 'on-request' }
  if (values.sandbox === appStore.settings.sandbox && values.approvalPolicy === appStore.settings.approvalPolicy) return
  appStore.patchSettings(values)
}
</script>

<template>
  <div class="shrink-0 px-4 pb-4 pt-1 sm:px-6">
    <div
      ref="composer"
      class="mx-auto flex max-w-[680px] flex-col gap-1.5 rounded-xl border bg-card p-2 transition-colors"
      :class="codexStore.isTurnRunning || codexStore.sendingMessage
        ? 'border-primary/35'
        : 'border-border'"
    >
      <div v-if="attachedImages.length" class="flex flex-wrap gap-1.5 px-1">
        <Badge
          v-for="path in attachedImages"
          :key="path"
          variant="secondary"
          class="h-6 gap-1.5 rounded-md pl-2 pr-1 text-[11px] font-normal"
        >
          <ImageIcon :size="12" />
          <span class="max-w-[140px] truncate">{{ attachmentName(path) }}</span>
          <Button
            variant="ghost"
            size="icon-xs"
            class="size-5"
            :aria-label="t('chat.removeAttachment')"
            @click="removeAttachment(path)"
          >
            <X :size="11" />
          </Button>
        </Badge>
      </div>

      <div
        v-if="showQueueStrip"
        class="flex items-center justify-between gap-2 rounded-md border border-border/50 bg-muted/40 px-2 py-1"
      >
        <Popover>
          <PopoverTrigger as-child>
            <Button
              variant="ghost"
              size="sm"
              class="h-6 min-w-0 gap-1.5 rounded-full bg-background/70 px-2 text-[11px] font-medium text-foreground/80 hover:text-foreground"
            >
              <ListOrdered :size="12" class="shrink-0" />
              <span class="truncate">{{ t('chat.queuedCount', { count: codexStore.activeQueuedMessages.filter((m) => m.state !== 'sending').length || codexStore.activeQueuedMessages.length }) }}</span>
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" side="top" class="w-96 max-w-[calc(100vw-2rem)] p-2">
            <div class="px-2 pb-2 pt-1">
              <p class="text-xs font-medium">{{ t('chat.queuedTitle') }}</p>
              <p class="mt-1 text-[11px] leading-4 text-muted-foreground">{{ t('chat.queuedHint') }}</p>
            </div>
            <div class="max-h-56 space-y-0.5 overflow-y-auto">
              <div
                v-for="message in codexStore.activeQueuedMessages"
                :key="message.id"
                class="group flex items-start gap-2 rounded-md px-2 py-1.5 hover:bg-muted/60"
              >
                <LoaderCircle
                  v-if="message.state === 'sending'"
                  :size="12"
                  class="mt-0.5 shrink-0 animate-spin text-muted-foreground"
                />
                <AlertCircle
                  v-else-if="message.state === 'failed'"
                  :size="12"
                  class="mt-0.5 shrink-0 text-destructive"
                />
                <Clock3 v-else :size="12" class="mt-0.5 shrink-0 text-muted-foreground" />

                <div class="min-w-0 flex-1">
                  <p class="line-clamp-2 text-[12px] leading-4 text-foreground/90">{{ message.text }}</p>
                  <div class="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
                    <span v-if="message.state === 'sending'">{{ t('chat.queuedSending') }}</span>
                    <span v-else-if="message.state === 'failed'" class="text-destructive">{{ t('chat.queuedFailed') }}</span>
                    <span v-else>{{ t('chat.queuedWaiting') }}</span>
                    <span v-if="message.images.length">· {{ t('chat.queuedAttachments', { count: message.images.length }) }}</span>
                  </div>
                  <p v-if="message.error" class="mt-0.5 line-clamp-2 text-[10px] text-destructive/90">
                    {{ message.error }}
                  </p>
                </div>

                <Button
                  v-if="message.state === 'failed'"
                  variant="ghost"
                  size="icon-xs"
                  class="size-6 shrink-0"
                  :aria-label="t('chat.retryQueued')"
                  @click="codexStore.retryQueuedMessage(message.id)"
                >
                  <RotateCcw :size="11" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="size-6 shrink-0 text-muted-foreground hover:text-destructive"
                  :aria-label="t('chat.removeQueued')"
                  :disabled="message.state === 'sending'"
                  @click="codexStore.removeQueuedMessage(message.id)"
                >
                  <X :size="11" />
                </Button>
              </div>
            </div>
          </PopoverContent>
        </Popover>
        <span class="hidden truncate pr-1 text-[10px] text-muted-foreground sm:block">{{ t('chat.queuedHint') }}</span>
      </div>

      <div v-if="slashOpen && filteredSlashCommands.length" class="rounded-md border border-border/60 bg-muted/30 p-1">
        <div class="flex items-center gap-1.5 px-2 py-1 text-[10px] text-muted-foreground">
          <Command :size="11" />
          {{ t('slash.title') }}
        </div>
        <button
          v-for="(command, index) in filteredSlashCommands"
          :key="command.id"
          type="button"
          class="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
          :class="index === slashIndex ? 'bg-background text-foreground' : 'text-foreground/85 hover:bg-background/70'"
          @mousedown.prevent="runSlashCommand(command)"
          @mouseenter="slashIndex = index"
        >
          <span class="shrink-0 font-mono text-[11px] font-medium">{{ command.label }}</span>
          <span class="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">{{ command.description }}</span>
        </button>
      </div>

      <Textarea
        v-model="modelValue"
        rows="1"
        :placeholder="composerPlaceholder"
        class="min-h-[44px] resize-none border-0 bg-transparent px-2 py-1.5 text-[13.5px] leading-6 shadow-none placeholder:text-muted-foreground/70 focus-visible:ring-0"
        @compositionend="composing = false"
        @compositionstart="composing = true"
        @keydown="onKeydown"
      />

      <div class="flex items-center justify-between gap-1">
        <div class="flex min-w-0 items-center gap-0.5">
          <Button
            variant="ghost"
            size="icon-sm"
            class="size-7 text-muted-foreground"
            :aria-label="t('chat.attachImages')"
            :disabled="attachedImages.length >= 4"
            @click="attachImages"
          >
            <Paperclip :size="14" />
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button
                variant="ghost"
                size="sm"
                class="hidden h-7 gap-1.5 px-2 text-[11px] font-normal text-muted-foreground sm:inline-flex"
              >
                <Shield :size="12" />
                {{ permissionLabel }}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" class="w-56">
              <DropdownMenuItem @click="setPermission('ask')">{{ t('settings.permissionAsk') }}</DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('auto')">{{ t('settings.permissionAuto') }}</DropdownMenuItem>
              <DropdownMenuItem @click="setPermission('strict')">{{ t('settings.permissionStrict') }}</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <Button
            type="button"
            variant="ghost"
            size="sm"
            class="h-7 gap-1.5 px-2 text-[11px] font-normal"
            :class="isPlanMode
              ? 'bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary'
              : 'text-muted-foreground'"
            :title="t('chat.planModeToggleHint')"
            :aria-pressed="isPlanMode"
            @click="togglePlanMode"
          >
            <ListTodo :size="12" />
            {{ isPlanMode ? t('chat.planModeOn') : t('chat.planModeOff') }}
          </Button>
        </div>

        <div class="flex min-w-0 items-center gap-0.5">
          <Select
            :model-value="displayModel"
            @update:model-value="(value) => onModelChange(String(value))"
          >
            <SelectTrigger
              :aria-label="t('chat.model')"
              class="h-7 w-auto max-w-40 gap-1 border-0 bg-transparent px-2 text-[11px] font-normal text-muted-foreground shadow-none"
            >
              <SelectValue :placeholder="t('chat.defaultModel')">{{ selectedModelLabel }}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="model in selectableModels" :key="model.model" :value="model.model">
                {{ model.displayName }}
              </SelectItem>
            </SelectContent>
          </Select>

          <Select
            :model-value="displayEffort"
            @update:model-value="(value) => onEffortChange(String(value))"
          >
            <SelectTrigger
              :aria-label="t('chat.reasoning')"
              class="hidden h-7 w-auto min-w-0 max-w-24 gap-1 border-0 bg-transparent px-2 text-[11px] font-normal text-muted-foreground shadow-none md:flex"
            >
              <Zap :size="11" class="shrink-0" />
              <SelectValue>{{ selectedEffortLabel }}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem
                v-for="option in reasoningOptions"
                :key="option.effort"
                :value="option.effort"
              >
                {{ 'displayName' in option ? option.displayName : option.effort }}
              </SelectItem>
            </SelectContent>
          </Select>

          <Button
            v-if="codexStore.isTurnRunning"
            type="button"
            variant="ghost"
            size="sm"
            class="h-7 px-2 text-[11px] text-destructive hover:bg-destructive/10 hover:text-destructive"
            :disabled="codexStore.interruptingTurn"
            :aria-label="t('chat.stopLabel')"
            @click.stop.prevent="onStop"
          >
            <Octagon :size="12" class="mr-1" fill="currentColor" />
            {{ codexStore.interruptingTurn ? t('chat.stopping') : t('chat.stop') }}
          </Button>
          <Button
            size="icon-sm"
            class="size-7 rounded-md transition-opacity"
            :class="canSend ? 'opacity-100' : 'opacity-40'"
            :aria-label="canSteer ? t('chat.steer') : t('chat.send')"
            :title="canSteer ? t('chat.steer') : t('chat.send')"
            :disabled="!canSend"
            @click="send"
          >
            <ArrowUp :size="15" stroke-width="2.5" />
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

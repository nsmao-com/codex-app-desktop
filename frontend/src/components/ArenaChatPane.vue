<script setup lang="ts">
import {
  CopyPlus,
  Folder,
  FolderOpen,
  GripVertical,
  MessageSquareText,
  Plus,
  SquareStack,
  X,
} from '@lucide/vue'
import { computed, onMounted, provide, toRef, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'

import ChatWorkspace from '@/components/ChatWorkspace.vue'
import ClaudeIcon from '@/components/icons/ClaudeIcon.vue'
import GeminiIcon from '@/components/icons/GeminiIcon.vue'
import GrokIcon from '@/components/icons/GrokIcon.vue'
import OpenAIIcon from '@/components/icons/OpenAIIcon.vue'
import OpenCodeIcon from '@/components/icons/OpenCodeIcon.vue'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { SimpleTooltip } from '@/components/ui/tooltip'
import { ArenaPaneIdKey, ArenaPaneRuntimeKey } from '@/composables/useRuntimeMode'
import { useArenaStore } from '@/stores/arena'
import { useAppStore, type WorkspaceRuntime } from '@/stores/app'
import { useClaudeStore, useCodexStore, useGrokStore } from '@/stores'
import { notify } from '@/utils/notify'

const props = defineProps<{
  paneId: string
  runtime: WorkspaceRuntime
  focused: boolean
  dragging?: boolean
}>()

const emit = defineEmits<{
  focus: []
  'show-inspector': []
  'drag-start-pane': [paneId: string]
  'drag-end-pane': []
  duplicate: []
}>()

const { t } = useI18n()
const arenaStore = useArenaStore()
const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()

const runtimeRef = toRef(props, 'runtime')
const paneIdRef = toRef(props, 'paneId')
provide(ArenaPaneRuntimeKey, computed(() => runtimeRef.value))
provide(ArenaPaneIdKey, computed(() => paneIdRef.value))

const runtimeOptions: Array<{ value: WorkspaceRuntime; label: string; icon: Component }> = [
  { value: 'codex', label: 'Codex', icon: OpenAIIcon },
  { value: 'claude', label: 'Claude', icon: ClaudeIcon },
  { value: 'grok', label: 'Grok', icon: GrokIcon },
  { value: 'gemini', label: 'Gemini', icon: GeminiIcon },
  { value: 'opencode', label: 'OpenCode', icon: OpenCodeIcon },
]

const currentOption = computed(() =>
  runtimeOptions.find((item) => item.value === props.runtime) || runtimeOptions[0]!,
)

type SessionOption = {
  value: string
  label: string
  preview: string
  meta: string
  taken: boolean
  tooltip: string
}

type SessionGroupOption = {
  key: string
  name: string
  path: string
  active: boolean
  tooltip: string
  sessions: SessionOption[]
}

const PER_GROUP_LIMIT = 30
const MAX_TOTAL_SESSIONS = 80

function leafName(path: string): string {
  const clean = (path || '').replace(/[\\/]+$/, '')
  const parts = clean.split(/[\\/]/).filter(Boolean)
  return parts[parts.length - 1] || path || '(unknown)'
}

function buildTooltip(parts: Array<string | undefined | null>): string {
  return parts.map((part) => String(part || '').trim()).filter(Boolean).join('\n')
}

const sessionGroups = computed<SessionGroupOption[]>(() => {
  let total = 0
  const groups: SessionGroupOption[] = []

  if (props.runtime === 'grok') {
    for (const group of grokStore.sessionGroups) {
      if (total >= MAX_TOTAL_SESSIONS) break
      const sessions = group.sessions
        .slice(0, PER_GROUP_LIMIT)
        .map((session) => {
          const label = session.name || session.preview || session.id
          const preview = session.preview && session.preview !== session.name
            ? session.preview
            : (session.model || session.backend || '')
          const taken = arenaStore.isSessionTakenByOtherPane(props.paneId, session.id, 'grok')
          return {
            value: session.id,
            label,
            preview,
            meta: taken ? t('arena.sessionTakenShort') : '',
            taken,
            tooltip: buildTooltip([
              label,
              preview && preview !== label ? preview : '',
              group.path,
              taken ? t('arena.sessionInUseHint') : '',
            ]),
          }
        })
        .filter((item) => item.value)
      if (!sessions.length) continue
      total += sessions.length
      groups.push({
        key: group.path || group.name,
        name: group.name || leafName(group.path),
        path: group.path,
        active: Boolean(group.active),
        tooltip: buildTooltip([group.name || leafName(group.path), group.path]),
        sessions,
      })
    }
    return groups
  }

  if (props.runtime === 'claude') {
    for (const group of claudeStore.sessionGroups) {
      if (total >= MAX_TOTAL_SESSIONS) break
      const sessions = group.sessions
        .slice(0, PER_GROUP_LIMIT)
        .map((session) => {
          const label = session.name || session.preview || session.id
          const preview = session.preview && session.preview !== session.name
            ? session.preview
            : ''
          const taken = arenaStore.isSessionTakenByOtherPane(props.paneId, session.id, 'claude')
          return {
            value: session.id,
            label,
            preview,
            meta: taken ? t('arena.sessionTakenShort') : '',
            taken,
            tooltip: buildTooltip([
              label,
              preview && preview !== label ? preview : '',
              group.path,
              taken ? t('arena.sessionInUseHint') : '',
            ]),
          }
        })
        .filter((item) => item.value)
      if (!sessions.length) continue
      total += sessions.length
      groups.push({
        key: group.path || group.name,
        name: group.name || leafName(group.path),
        path: group.path,
        active: Boolean(group.active),
        tooltip: buildTooltip([group.name || leafName(group.path), group.path]),
        sessions,
      })
    }
    return groups
  }

  // Codex / Gemini / OpenCode share thread groups by project.
  for (const group of codexStore.threadGroups) {
    if (total >= MAX_TOTAL_SESSIONS) break
    const sessions = group.threads
      .filter((thread) => codexStore.runtimeIDForThread(thread.id) === props.runtime)
      .slice(0, PER_GROUP_LIMIT)
      .map((thread) => {
        const label = thread.name || thread.preview || thread.id
        const preview = thread.preview && thread.preview !== thread.name
          ? thread.preview
          : ''
        const taken = arenaStore.isSessionTakenByOtherPane(props.paneId, thread.id, props.runtime)
        return {
          value: thread.id,
          label,
          preview,
          meta: taken ? t('arena.sessionTakenShort') : '',
          taken,
          tooltip: buildTooltip([
            label,
            preview && preview !== label ? preview : '',
            group.path,
            taken ? t('arena.sessionInUseHint') : '',
          ]),
        }
      })
      .filter((item) => item.value)
    if (!sessions.length) continue
    total += sessions.length
    groups.push({
      key: group.path || group.name,
      name: group.name || leafName(group.path),
      path: group.path,
      active: Boolean(group.active),
      tooltip: buildTooltip([group.name || leafName(group.path), group.path]),
      sessions,
    })
  }
  return groups
})

const selectedSessionId = computed(() => arenaStore.sessionForPane(props.paneId))

const selectedSession = computed(() => {
  const id = selectedSessionId.value
  if (!id) return null
  for (const group of sessionGroups.value) {
    const hit = group.sessions.find((item) => item.value === id)
    if (hit) return { ...hit, groupName: group.name, groupPath: group.path }
  }
  return {
    value: id,
    label: id,
    preview: '',
    meta: '',
    taken: false,
    tooltip: id,
    groupName: '',
    groupPath: '',
  }
})

const selectedSessionLabel = computed(() =>
  selectedSession.value?.label || t('arena.newOrSelectSession'),
)

const selectedSessionTooltip = computed(() => {
  if (!selectedSession.value) return t('arena.newOrSelectSession')
  return selectedSession.value.tooltip || selectedSession.value.label
})

function onRuntimeChange(value: unknown): void {
  if (typeof value !== 'string' || !value) return
  arenaStore.setPaneRuntime(props.paneId, value as WorkspaceRuntime)
  emit('focus')
}

async function onSessionChange(value: unknown): Promise<void> {
  if (typeof value !== 'string') return
  if (value === '__new__') {
    await createNewSession()
    return
  }
  if (!value) return
  const previousOwner = arenaStore.selectPaneSession(props.paneId, value)
  if (previousOwner) {
    notify('info', t('arena.sessionInUseTitle'), t('arena.sessionInUseHint'))
  }
  // Focus after binding so the layout opens the newly selected session, not the old one.
  emit('focus')
}

async function createNewSession(): Promise<void> {
  const previousSessionId = selectedSessionId.value
  arenaStore.setPaneSession(props.paneId, '')
  emit('focus')
  if (!await appStore.setActiveRuntime(props.runtime)) {
    if (previousSessionId) arenaStore.setPaneSession(props.paneId, previousSessionId)
    emit('focus')
    return
  }
  if (props.runtime === 'grok') {
    await grokStore.newSession()
    if (grokStore.activeSessionId) {
      arenaStore.setPaneSession(props.paneId, grokStore.activeSessionId)
    } else {
      arenaStore.setPaneSession(props.paneId, '')
    }
    return
  }
  if (props.runtime === 'claude') {
    claudeStore.newSession()
    if (claudeStore.activeSessionId) {
      arenaStore.setPaneSession(props.paneId, claudeStore.activeSessionId)
    } else {
      arenaStore.setPaneSession(props.paneId, '')
    }
    return
  }
  const thread = await codexStore.newThread(true)
  if (thread?.id) {
    arenaStore.setPaneSession(props.paneId, thread.id)
  }
}

function onGripDragStart(event: DragEvent): void {
  if (!event.dataTransfer) return
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', props.paneId)
  event.dataTransfer.setData('application/x-nice-arena-pane', props.paneId)
  try {
    const ghost = document.createElement('div')
    ghost.style.cssText = 'position:fixed;top:-1000px;left:-1000px;width:12px;height:12px;opacity:0.01'
    document.body.appendChild(ghost)
    event.dataTransfer.setDragImage(ghost, 6, 6)
    requestAnimationFrame(() => ghost.remove())
  } catch {
    // setDragImage may fail in some environments — native ghost is fine.
  }
  emit('drag-start-pane', props.paneId)
}

function onGripDragEnd(): void {
  emit('drag-end-pane')
}

onMounted(() => {
  if (props.runtime === 'grok') void grokStore.loadSessions()
  else if (props.runtime === 'claude') void claudeStore.loadSessions()
  else {
    void codexStore.loadThreads()
    void codexStore.loadRecentProjectThreads()
  }
})

watch(
  [selectedSessionId, () => props.runtime, () => codexStore.threadGroups],
  ([sessionId, runtime]) => {
    if (!sessionId || runtime === 'grok' || runtime === 'claude') return
    const knownRuntime = codexStore.knownRuntimeIDForThread(sessionId)
    if (knownRuntime && knownRuntime !== runtime) {
      arenaStore.setPaneSession(props.paneId, '')
    }
  },
  { immediate: true },
)

// Keep the owning pane aligned when a provider promotes its optimistic id.
watch(
  [
    selectedSessionId,
    () => props.runtime,
    () => codexStore.itemsByThread,
    () => grokStore.messagesBySession,
    () => claudeStore.itemsBySession,
  ],
  ([boundId, runtime]) => {
    if (!boundId) return
    const resolvedId = runtime === 'grok'
      ? grokStore.resolveSessionId(boundId)
      : runtime === 'claude'
        ? claudeStore.resolveSessionId(boundId)
        : codexStore.resolveThreadID(boundId)
    if (resolvedId && resolvedId !== boundId) {
      arenaStore.setPaneSession(props.paneId, resolvedId)
    }
  },
)

watch(
  () => props.runtime,
  (runtime) => {
    if (runtime === 'grok') void grokStore.loadSessions()
    else if (runtime === 'claude') void claudeStore.loadSessions()
    else {
      void codexStore.loadThreads()
      void codexStore.loadRecentProjectThreads()
    }
  },
)
</script>

<template>
  <section
    class="flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden border-border/70 bg-card transition-[opacity,box-shadow] duration-150"
    :class="[
      focused ? 'ring-1 ring-inset ring-primary/40' : 'opacity-[0.96]',
      dragging ? 'opacity-55 shadow-lg ring-1 ring-inset ring-primary/25' : '',
    ]"
    @pointerdown="emit('focus')"
  >
    <header class="flex shrink-0 flex-col gap-1 border-b border-border/70 px-1.5 py-1.5">
      <div class="flex items-center gap-1">
        <button
          type="button"
          class="grid size-7 shrink-0 cursor-grab place-items-center rounded-md text-muted-foreground hover:bg-muted active:cursor-grabbing"
          :aria-label="t('arena.dragPane')"
          draggable="true"
          @click.stop
          @dragstart.stop="onGripDragStart"
          @dragend.stop="onGripDragEnd"
        >
          <GripVertical :size="13" />
        </button>
        <component :is="currentOption.icon" :size="13" class="shrink-0 opacity-90" />
        <Select :model-value="runtime" @update:model-value="onRuntimeChange">
          <SelectTrigger class="h-7 min-w-0 w-full max-w-full flex-1 overflow-hidden border-0 bg-transparent px-1.5 text-[11px] shadow-none focus:ring-0">
            <SelectValue>
              <span class="block min-w-0 truncate font-medium">{{ currentOption.label }}</span>
            </SelectValue>
          </SelectTrigger>
          <SelectContent class="w-[200px] max-w-[min(200px,90vw)] min-w-[160px]">
            <SelectItem
              v-for="option in runtimeOptions"
              :key="option.value"
              :value="option.value"
            >
              <span class="flex min-w-0 items-center gap-2">
                <component :is="option.icon" :size="12" class="shrink-0" />
                <span class="truncate">{{ option.label }}</span>
              </span>
            </SelectItem>
          </SelectContent>
        </Select>
        <span
          v-if="focused"
          class="shrink-0 rounded bg-primary/10 px-1.5 py-0.5 text-[9px] font-medium text-primary"
        >
          {{ t('arena.focused') }}
        </span>
        <SimpleTooltip :content="t('arena.duplicatePane')">
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            class="shrink-0"
            :disabled="!arenaStore.canAddPane"
            :aria-label="t('arena.duplicatePane')"
            @click.stop="emit('duplicate')"
          >
            <CopyPlus :size="12" />
          </Button>
        </SimpleTooltip>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          class="shrink-0"
          :aria-label="t('arena.closePane')"
          @click.stop="arenaStore.closePane(paneId)"
        >
          <X :size="12" />
        </Button>
      </div>

      <div class="flex min-w-0 items-center gap-1 pl-7">
        <MessageSquareText :size="12" class="shrink-0 text-muted-foreground" />
        <Select
          :model-value="selectedSessionId || undefined"
          @update:model-value="(value) => void onSessionChange(value)"
        >
          <SimpleTooltip
            :content="selectedSessionTooltip"
            side="bottom"
            :delay-duration="350"
            content-class="z-[120] max-w-[320px] whitespace-pre-line break-words text-left"
          >
            <SelectTrigger
              class="h-8 min-w-0 w-full max-w-full flex-1 overflow-hidden border bg-background/60 px-1.5 text-[11px] shadow-none"
              :aria-label="selectedSessionLabel"
            >
              <SelectValue>
                <span class="flex min-w-0 max-w-full items-center gap-1.5 overflow-hidden">
                  <component
                    :is="currentOption.icon"
                    :size="11"
                    class="shrink-0 opacity-70"
                  />
                  <span class="min-w-0 flex-1 truncate text-left">
                    {{ selectedSessionLabel }}
                  </span>
                </span>
              </SelectValue>
            </SelectTrigger>
          </SimpleTooltip>

          <!-- Compact, icon-rich menu; fixed width so it never fills the page. -->
          <SelectContent
            class="arena-session-menu max-h-80 w-[300px] max-w-[min(300px,calc(100vw-1.25rem))] min-w-[240px] p-0"
          >
            <div class="p-1">
              <SelectItem value="__new__" class="rounded-md py-2">
                <span class="flex min-w-0 items-center gap-2">
                  <span class="grid size-6 shrink-0 place-items-center rounded-md border border-border/70 bg-muted/40 text-muted-foreground">
                    <Plus :size="12" />
                  </span>
                  <span class="min-w-0 truncate font-medium">{{ t('arena.newSession') }}</span>
                </span>
              </SelectItem>
            </div>

            <SelectSeparator class="my-0" />

            <div class="max-h-64 overflow-y-auto p-1">
              <SelectGroup
                v-for="group in sessionGroups"
                :key="group.key"
                class="mb-1 last:mb-0"
              >
                <SelectLabel
                  class="sticky top-0 z-[1] mb-0.5 flex min-w-0 items-center gap-1.5 rounded-md bg-muted/55 px-2 py-1.5 text-[10px] font-medium tracking-wide text-muted-foreground backdrop-blur-sm"
                >
                  <SimpleTooltip
                    :content="group.tooltip"
                    side="right"
                    :delay-duration="300"
                    content-class="z-[120] max-w-[320px] whitespace-pre-line break-words text-left"
                  >
                    <span class="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden">
                      <FolderOpen
                        v-if="group.active"
                        :size="12"
                        class="shrink-0 text-foreground/70"
                      />
                      <Folder
                        v-else
                        :size="12"
                        class="shrink-0 opacity-60"
                      />
                      <span class="min-w-0 flex-1 truncate">{{ group.name }}</span>
                      <span class="shrink-0 tabular-nums text-[9px] opacity-70">
                        {{ group.sessions.length }}
                      </span>
                    </span>
                  </SimpleTooltip>
                </SelectLabel>

                <SelectItem
                  v-for="option in group.sessions"
                  :key="option.value"
                  :value="option.value"
                  class="items-start rounded-md py-1.5"
                >
                  <SimpleTooltip
                    :content="option.tooltip"
                    side="right"
                    :delay-duration="350"
                    content-class="z-[120] max-w-[320px] whitespace-pre-line break-words text-left"
                  >
                    <span class="flex min-w-0 w-full items-start gap-2">
                      <span
                        class="mt-0.5 grid size-6 shrink-0 place-items-center rounded-md border border-border/60 bg-panel/80 text-muted-foreground"
                      >
                        <component :is="currentOption.icon" :size="12" />
                      </span>
                      <span class="min-w-0 flex-1 overflow-hidden">
                        <span class="flex min-w-0 items-center gap-1">
                          <span class="min-w-0 flex-1 truncate text-[12px] font-medium leading-4">
                            {{ option.label }}
                          </span>
                          <span
                            v-if="option.taken"
                            class="shrink-0 rounded bg-muted px-1 py-px text-[9px] text-muted-foreground"
                          >
                            {{ t('arena.sessionTakenShort') }}
                          </span>
                        </span>
                        <span
                          v-if="option.preview"
                          class="mt-0.5 block truncate text-[10px] leading-3.5 text-muted-foreground"
                        >
                          {{ option.preview }}
                        </span>
                      </span>
                    </span>
                  </SimpleTooltip>
                </SelectItem>
              </SelectGroup>

              <div
                v-if="!sessionGroups.length"
                class="flex flex-col items-center gap-1.5 px-3 py-6 text-center"
              >
                <SquareStack :size="18" class="opacity-40" />
                <p class="text-[11px] text-muted-foreground">
                  {{ t('arena.noSessions') }}
                </p>
              </div>
            </div>
          </SelectContent>
        </Select>
      </div>
    </header>
    <div class="min-h-0 min-w-0 flex-1 overflow-hidden">
      <ChatWorkspace hide-chrome @show-inspector="emit('show-inspector')" />
    </div>
  </section>
</template>

<style scoped>
/* Force ellipsis inside reka SelectItemText (span) for long session titles. */
:deep(.arena-session-menu [data-slot='select-item']) {
  max-width: 100%;
}

:deep(.arena-session-menu [data-slot='select-item-text']) {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
}

:deep(.arena-session-menu [data-slot='select-label']) {
  max-width: 100%;
}
</style>

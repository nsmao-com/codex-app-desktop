<script setup lang="ts">
import {
  Bot,
  CheckCircle2,
  CircleAlert,
  Clock3,
  Eraser,
  LoaderCircle,
  Radio,
} from '@lucide/vue'
import { computed, shallowRef } from 'vue'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useAppStore } from '@/stores/app'
import { useSubagentsStore } from '@/stores/subagents'
import type {
  SubagentActivity,
  SubagentCapability,
  SubagentCapabilityLevel,
  SubagentRuntime,
  SubagentStatus,
} from '@/types/subagents'

const props = withDefaults(defineProps<{
  /** Optional scope override for arena panes; empty means the active runtime. */
  runtime?: SubagentRuntime
  sessionId?: string
}>(), {
  runtime: undefined,
  sessionId: '',
})

const appStore = useAppStore()
const subagentsStore = useSubagentsStore()

const runtime = computed<SubagentRuntime>(() => props.runtime || subagentsStore.activeRuntime)
const sessionId = computed(() => props.sessionId || '')
const currentActivities = computed(() => subagentsStore.activitiesFor(runtime.value, sessionId.value))
const runningCount = computed(() => currentActivities.value.filter((item) => item.status === 'running' || item.status === 'pending').length)
const recentActivities = computed(() => currentActivities.value.slice(0, 100))
const hasAnyActivities = computed(() => recentActivities.value.length > 0)
const isChinese = computed(() => String(appStore.settings.language || '').toLowerCase().startsWith('zh'))
const expandedActivityIds = shallowRef<string[]>([])

const runtimeLabels: Record<SubagentRuntime, { zh: string; en: string }> = {
  codex: { zh: 'Codex / OpenAI', en: 'Codex / OpenAI' },
  claude: { zh: 'Claude Code / Anthropic', en: 'Claude Code / Anthropic' },
  gemini: { zh: 'Gemini CLI / Google', en: 'Gemini CLI / Google' },
  grok: { zh: 'Grok / xAI', en: 'Grok / xAI' },
  opencode: { zh: 'OpenCode', en: 'OpenCode' },
}

const levelLabels: Record<SubagentCapabilityLevel, { zh: string; en: string }> = {
  supported: { zh: '支持', en: 'Supported' },
  experimental: { zh: '实验性', en: 'Experimental' },
  unsupported: { zh: '不支持', en: 'Unsupported' },
  unknown: { zh: '未确认', en: 'Unconfirmed' },
}

const statusLabels: Record<SubagentStatus, { zh: string; en: string }> = {
  pending: { zh: '排队中', en: 'Queued' },
  running: { zh: '运行中', en: 'Running' },
  completed: { zh: '已完成', en: 'Completed' },
  failed: { zh: '失败', en: 'Failed' },
  interrupted: { zh: '已中断', en: 'Interrupted' },
  unknown: { zh: '状态未知', en: 'Unknown' },
}

const capabilityDescriptions: Record<SubagentRuntime, { zh: string; en: string }> = {
  codex: { zh: '原生协作线程与子代理工具事件，可持续查看各代理进度。', en: 'Native collaboration threads and sub-agent tool events expose ongoing progress.' },
  claude: { zh: '原生 Task / Agent 工具可提供子代理生命周期。', en: 'Native Task and Agent tools expose child-agent lifecycle updates.' },
  gemini: { zh: '原生子代理与 invoke_agent 事件；明细取决于已安装 CLI 版本。', en: 'Native sub-agents and invoke_agent events; detail depends on the installed CLI version.' },
  grok: { zh: 'Grok Build 支持 spawn_subagent；直连 API 模式可能没有子代理事件。', en: 'Grok Build supports spawn_subagent; direct API mode may not emit child-agent events.' },
  opencode: { zh: '原生 primary / subagent 与 Task 工具事件可被追踪。', en: 'Native primary/subagent and Task tool events can be tracked.' },
}

const activeRuntimeLabel = computed(() => {
  const label = runtimeLabels[runtime.value]
  return isChinese.value ? label.zh : label.en
})

function text(value: { zh: string; en: string }): string {
  return isChinese.value ? value.zh : value.en
}

function capabilityName(capability: SubagentCapability): string {
  return runtimeLabels[capability.runtime][isChinese.value ? 'zh' : 'en']
}

function capabilityStatus(capability: SubagentCapability): string {
  return text(levelLabels[capability.level])
}

function capabilityDescription(capability: SubagentCapability): string {
  return text(capabilityDescriptions[capability.runtime])
}

function capabilityClass(capability: SubagentCapability): string {
  if (capability.observed) return 'border-positive/30 bg-positive/5 text-positive'
  if (capability.level === 'supported') return 'border-primary/20 bg-primary/5 text-primary'
  if (capability.level === 'experimental') return 'border-warning/30 bg-warning/5 text-warning'
  return 'border-border/70 bg-muted/40 text-muted-foreground'
}

function activityIcon(status: SubagentStatus): typeof LoaderCircle {
  if (status === 'running') return LoaderCircle
  if (status === 'completed') return CheckCircle2
  if (status === 'failed' || status === 'interrupted') return CircleAlert
  return Clock3
}

function activityClass(status: SubagentStatus): string {
  if (status === 'running') return 'text-primary'
  if (status === 'completed') return 'text-positive'
  if (status === 'failed' || status === 'interrupted') return 'text-destructive'
  return 'text-muted-foreground'
}

function statusLabel(status: SubagentStatus): string {
  return text(statusLabels[status])
}

function formatTime(value: number): string {
  if (!value) return ''
  try {
    return new Intl.DateTimeFormat(isChinese.value ? 'zh-CN' : 'en-US', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).format(value)
  } catch {
    return ''
  }
}

function activityDetail(activity: SubagentActivity): string {
  const detail = activity.detail.trim()
  if (!detail) return ''
  if (expandedActivityIds.value.includes(activity.id)) return detail
  return detail.length > 280 ? `${detail.slice(0, 280)}...` : detail
}

function activityCanExpand(activity: SubagentActivity): boolean {
  return activity.detail.trim().length > 280
}

function toggleActivity(activity: SubagentActivity): void {
  expandedActivityIds.value = expandedActivityIds.value.includes(activity.id)
    ? expandedActivityIds.value.filter((id) => id !== activity.id)
    : [...expandedActivityIds.value, activity.id]
}

function clearCurrent(): void {
  expandedActivityIds.value = []
  subagentsStore.clear(runtime.value, sessionId.value)
}

function activityLabel(activity: SubagentActivity): string {
  const name = activity.agentName.trim() || (isChinese.value ? '子代理' : 'Sub-agent')
  const action = activity.action.trim()
  return action ? `${name} · ${action}` : name
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <ScrollArea class="h-full px-3 py-3">
      <div class="space-y-3 pb-2">
        <Card class="rounded-md shadow-none">
          <CardHeader class="pb-2">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <CardTitle class="flex items-center gap-2 text-xs">
                  <Bot :size="14" class="text-primary" />
                  {{ isChinese ? '子代理活动' : 'Sub-agent activity' }}
                </CardTitle>
                <p class="mt-1 text-[10px] leading-4 text-muted-foreground">
                  {{ isChinese
                    ? `${activeRuntimeLabel} 的最新子代理活动会优先显示。`
                    : `The newest child-agent activity from ${activeRuntimeLabel} appears first.` }}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon-xs"
                class="shrink-0 text-muted-foreground"
                :aria-label="isChinese ? '清除活动记录' : 'Clear activity'"
                :disabled="!hasAnyActivities"
                @click="clearCurrent"
              >
                <Eraser :size="13" />
              </Button>
            </div>
            <div class="mt-2 flex items-center gap-2 text-[10px] text-muted-foreground">
              <span class="inline-flex items-center gap-1">
                <Radio :size="11" :class="runningCount ? 'text-primary' : 'opacity-50'" />
                {{ runningCount
                  ? (isChinese ? `${runningCount} 个运行中` : `${runningCount} running`)
                  : (isChinese ? '当前无运行中的子代理' : 'No child agent is running') }}
              </span>
              <span v-if="sessionId" class="max-w-[140px] truncate font-mono opacity-60" :title="sessionId">
                {{ sessionId }}
              </span>
            </div>
          </CardHeader>
        </Card>

        <section aria-labelledby="subagent-capabilities-title">
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <h3 id="subagent-capabilities-title" class="text-[11px] font-medium text-muted-foreground">
              {{ isChinese ? '模型商能力' : 'Provider capability' }}
            </h3>
            <span class="text-[10px] text-muted-foreground/70">
              {{ isChinese ? '能力 / 实际事件' : 'Capability / live evidence' }}
            </span>
          </div>
          <div class="space-y-1.5">
            <div
              v-for="capability in subagentsStore.capabilities"
              :key="capability.runtime"
              class="rounded-md border px-2.5 py-2"
              :class="capabilityClass(capability)"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="min-w-0 truncate text-[11px] font-medium">{{ capabilityName(capability) }}</span>
                <Badge variant="outline" class="h-5 shrink-0 rounded px-1.5 text-[9px] font-normal">
                  {{ capabilityStatus(capability) }}
                </Badge>
              </div>
              <p class="mt-1 text-[10px] leading-4 opacity-75">{{ capabilityDescription(capability) }}</p>
              <p v-if="capability.observed" class="mt-1 inline-flex items-center gap-1 text-[9px] font-medium text-positive">
                <Radio :size="9" aria-hidden="true" />
                {{ isChinese ? '当前运行期间已收到原生事件' : 'Native events observed during this app run' }}
              </p>
            </div>
          </div>
        </section>

        <section aria-labelledby="subagent-timeline-title">
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <h3 id="subagent-timeline-title" class="text-[11px] font-medium text-muted-foreground">
              {{ isChinese ? '活动时间线' : 'Activity timeline' }}
            </h3>
            <span v-if="recentActivities.length" class="text-[10px] text-muted-foreground/70">
              {{ recentActivities.length }}{{ isChinese ? ' 项' : '' }}
            </span>
          </div>
          <div
            v-if="hasAnyActivities"
            class="relative space-y-1.5 pl-1"
            aria-live="polite"
            aria-relevant="additions text"
          >
            <div class="pointer-events-none absolute bottom-2 left-[9px] top-2 w-px bg-border/70" />
            <article
              v-for="activity in recentActivities"
              :key="activity.id"
              class="relative rounded-md border border-border/60 bg-card/70 px-2.5 py-2 pl-7"
            >
              <component
                :is="activityIcon(activity.status)"
                :size="14"
                class="absolute left-1.5 top-2.5"
                :class="[activityClass(activity.status), activity.status === 'running' ? 'animate-spin motion-reduce:animate-none' : '']"
                aria-hidden="true"
              />
              <div class="flex items-start justify-between gap-2">
                <p class="min-w-0 truncate text-[11px] font-medium text-foreground/90" :title="activityLabel(activity)">
                  {{ activityLabel(activity) }}
                </p>
                <span class="shrink-0 text-[9px] tabular-nums text-muted-foreground">{{ formatTime(activity.updatedAt) }}</span>
              </div>
              <div class="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
                <span>{{ statusLabel(activity.status) }}</span>
                <span v-if="activity.source" class="truncate opacity-60">· {{ activity.source }}</span>
              </div>
              <p v-if="activityDetail(activity)" class="mt-1 break-words text-[10px] leading-4 text-muted-foreground/90">
                {{ activityDetail(activity) }}
              </p>
              <button
                v-if="activityCanExpand(activity)"
                type="button"
                class="mt-1 text-[9px] font-medium text-primary hover:underline"
                :aria-expanded="expandedActivityIds.includes(activity.id)"
                @click="toggleActivity(activity)"
              >
                {{ expandedActivityIds.includes(activity.id)
                  ? (isChinese ? '收起详情' : 'Show less')
                  : (isChinese ? '展开完整详情' : 'Show full detail') }}
              </button>
              <p v-if="activity.parentAgentId" class="mt-1 truncate font-mono text-[9px] text-muted-foreground/60" :title="activity.parentAgentId">
                {{ isChinese ? '父代理' : 'Parent' }}: {{ activity.parentAgentId }}
              </p>
            </article>
          </div>
          <div v-else class="rounded-md border border-dashed border-border/70 px-3 py-5 text-center">
            <Bot :size="20" class="mx-auto text-muted-foreground/50" />
            <p class="mt-2 text-[11px] text-muted-foreground">
              {{ isChinese ? '还没有收到子代理事件' : 'No sub-agent events yet' }}
            </p>
            <p class="mt-1 text-[10px] leading-4 text-muted-foreground/70">
              {{ isChinese
                ? '当模型调用 Task、Agent 或协作工具时，这里会显示它正在做什么。'
                : 'When a provider calls Task, Agent, or a collaboration tool, its work appears here.' }}
            </p>
          </div>
        </section>
      </div>
    </ScrollArea>
  </div>
</template>

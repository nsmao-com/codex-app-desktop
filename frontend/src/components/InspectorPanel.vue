<script setup lang="ts">
import { Activity, Coins, FileDiff, Gauge, GitBranch, PanelRightClose, RefreshCw, ShieldCheck } from '@lucide/vue'
import { Motion } from 'motion-v'
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Tabs, TabsContent } from '@/components/ui/tabs'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { panelFromRight } from '@/lib/motion'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const { locale, t } = useI18n()

const emit = defineEmits<{
  collapse: []
}>()

const activeTab = shallowRef<'changes' | 'runtime'>('changes')

const changes = computed(() => workspaceStore.changes)
const contextUsedPercent = computed(() => {
  const usage = codexStore.activeTokenUsage
  if (!usage?.modelContextWindow) return 0
  return Math.min(100, Math.max(0, (usage.total.totalTokens / usage.modelContextWindow) * 100))
})

function formatTokens(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—'
  return new Intl.NumberFormat(locale.value, { notation: value >= 100_000 ? 'compact' : 'standard' }).format(value)
}

function statusClass(status: string): string {
  const first = status.trim().slice(0, 1).toUpperCase()
  switch (first) {
    case 'A': return 'text-green-500'
    case 'D': return 'text-destructive'
    case 'M': return 'text-warning'
    default: return 'text-muted-foreground'
  }
}
</script>

<template>
  <Motion
    as="aside"
    class="flex h-full w-80 shrink-0 flex-col border-l bg-panel max-lg:absolute max-lg:inset-y-0 max-lg:right-0 max-lg:z-30 max-lg:shadow-xl"
    :initial="panelFromRight.initial"
    :animate="panelFromRight.animate"
    :exit="panelFromRight.exit"
    :transition="panelFromRight.transition"
  >
    <Tabs v-model="activeTab" class="flex h-full flex-col">
      <div class="flex items-center justify-between border-b px-3 py-2">
        <TooltipProvider>
          <div class="flex items-center rounded-md border bg-muted/40 p-0.5">
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  :aria-label="t('inspector.changes')"
                  :class="activeTab === 'changes' ? 'bg-background text-foreground shadow-xs hover:bg-background' : 'text-muted-foreground'"
                  @click="activeTab = 'changes'"
                >
                  <FileDiff :size="13" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">{{ t('inspector.changes') }} · {{ changes.length }}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  :aria-label="t('inspector.runtime')"
                  :class="activeTab === 'runtime' ? 'bg-background text-foreground shadow-xs hover:bg-background' : 'text-muted-foreground'"
                  @click="activeTab = 'runtime'"
                >
                  <Activity :size="13" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">{{ t('inspector.runtime') }}</TooltipContent>
            </Tooltip>
          </div>
        </TooltipProvider>
        <Button variant="ghost" size="icon-xs" :aria-label="t('inspector.details')" @click="emit('collapse')">
          <PanelRightClose :size="14" />
        </Button>
      </div>

      <TabsContent value="changes" class="mt-0 flex-1 overflow-hidden">
        <ScrollArea class="h-full px-3 py-3">
          <Card class="mb-3 rounded-md shadow-none">
            <CardHeader class="pb-2">
              <CardTitle class="text-xs">{{ t('inspector.workspace') }}</CardTitle>
            </CardHeader>
            <CardContent>
              <div class="flex items-center gap-2 text-sm font-medium">
                <GitBranch :size="14" />
                {{ workspaceStore.branch || 'detached' }}
              </div>
              <p v-if="!workspaceStore.isGit" class="mt-1 text-[11px] text-muted-foreground">
                {{ workspaceStore.workspace?.gitError || t('inspector.gitHint') }}
              </p>
            </CardContent>
          </Card>

          <div class="flex items-center justify-between">
            <h3 class="text-xs font-medium text-muted-foreground">{{ t('inspector.changedFiles') }}</h3>
            <Button variant="ghost" size="icon-xs" :aria-label="t('inspector.refreshGit')" @click="workspaceStore.refreshWorkspace">
              <RefreshCw :size="12" />
            </Button>
          </div>
          <div v-if="changes.length" class="mt-2 space-y-1">
            <Button
              v-for="change in changes"
              :key="`${change.status}:${change.path}`"
              variant="ghost"
              class="h-auto w-full justify-start gap-2 px-2 py-1.5 text-left text-xs"
              :class="{ 'bg-accent/50': workspaceStore.inspectedDiffPath === change.path && workspaceStore.diffSidebarOpen }"
              :disabled="workspaceStore.diffInspectionLoading"
              @click="workspaceStore.inspectWorkspaceDiff(change.path)"
            >
              <span class="w-4 text-center font-mono text-[10px]" :class="statusClass(change.status)">{{ change.status || 'M' }}</span>
              <span class="min-w-0 flex-1 truncate text-[11px]" :title="change.path">{{ change.path }}</span>
            </Button>
          </div>
          <div v-else class="mt-4 flex flex-col items-center gap-2 text-center text-xs text-muted-foreground">
            <ShieldCheck :size="20" />
            <p>{{ t('inspector.clean') }}</p>
          </div>
        </ScrollArea>
      </TabsContent>

      <TabsContent value="runtime" class="mt-0 flex-1 overflow-hidden">
        <ScrollArea class="h-full px-3 py-3">
          <Card class="mb-3 rounded-md shadow-none">
            <CardHeader class="pb-2">
              <CardTitle class="text-xs">{{ t('inspector.appServer') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-1 text-xs">
              <div class="flex justify-between">
                <span class="text-muted-foreground">{{ t('inspector.version') }}</span>
                <span>{{ codexStore.connection.version || '—' }}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-muted-foreground">{{ t('inspector.approvals') }}</span>
                <span>{{ codexStore.pendingRequests.length ? t('inspector.waiting', { count: codexStore.pendingRequests.length }) : t('inspector.noWaiting') }}</span>
              </div>
            </CardContent>
          </Card>

          <Card v-if="codexStore.activeTokenUsage" class="mb-3 rounded-md shadow-none">
            <CardHeader class="pb-2">
              <CardTitle class="flex items-center gap-2 text-xs">
                <Gauge :size="14" />
                {{ t('inspector.tokenUsage') }}
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div class="mb-2 flex items-center justify-between text-xs">
                <span class="text-muted-foreground">{{ t('inspector.contextUsage') }}</span>
                <span class="tabular-nums">{{ contextUsedPercent.toFixed(1) }}%</span>
              </div>
              <Progress :model-value="contextUsedPercent" class="h-1.5" />
              <div class="mt-3 grid grid-cols-3 gap-2 text-center text-[10px]">
                <div class="rounded-md bg-muted p-2">
                  <p class="text-muted-foreground">{{ t('inspector.inputTokens') }}</p>
                  <p class="font-medium tabular-nums">{{ formatTokens(codexStore.activeTokenUsage.total.inputTokens) }}</p>
                </div>
                <div class="rounded-md bg-muted p-2">
                  <p class="text-muted-foreground">{{ t('inspector.outputTokens') }}</p>
                  <p class="font-medium tabular-nums">{{ formatTokens(codexStore.activeTokenUsage.total.outputTokens) }}</p>
                </div>
                <div class="rounded-md bg-muted p-2">
                  <p class="text-muted-foreground">{{ t('inspector.contextWindow') }}</p>
                  <p class="font-medium tabular-nums">{{ formatTokens(codexStore.activeTokenUsage.modelContextWindow) }}</p>
                </div>
              </div>
            </CardContent>
          </Card>

          <Card v-if="appStore.accountRateLimits?.primary || appStore.accountRateLimits?.secondary" class="mb-3 rounded-md shadow-none">
            <CardHeader class="pb-2">
              <CardTitle class="flex items-center gap-2 text-xs">
                <Coins :size="14" />
                {{ t('inspector.rateLimits') }}
              </CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div v-for="window in [appStore.accountRateLimits?.primary, appStore.accountRateLimits?.secondary].filter(Boolean)" :key="window?.usedPercent">
                <div class="mb-1 flex justify-between text-[10px]">
                  <span class="text-muted-foreground">{{ t('inspector.primaryLimit') }}</span>
                  <span class="tabular-nums">{{ window?.usedPercent }}%</span>
                </div>
                <Progress :model-value="window?.usedPercent" class="h-1.5" />
              </div>
            </CardContent>
          </Card>

          <div v-if="codexStore.lastTransportMessage" class="rounded-md bg-muted p-3 text-[11px] text-muted-foreground">
            <strong class="block text-foreground">{{ t('inspector.latestNote') }}</strong>
            {{ codexStore.lastTransportMessage }}
          </div>
        </ScrollArea>
      </TabsContent>
    </Tabs>
  </Motion>
</template>

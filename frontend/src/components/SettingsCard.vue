<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'

withDefaults(defineProps<{
  title: string
  description?: string
}>(), {
  description: '',
})
</script>

<template>
  <Collapsible :default-open="true" class="overflow-hidden rounded-xl border bg-card">
    <div class="flex items-start gap-3 border-b px-4 py-3">
      <CollapsibleTrigger as-child>
        <button
          type="button"
          class="group flex min-w-0 flex-1 items-start gap-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-2 focus-visible:ring-offset-card"
          :aria-label="title"
        >
          <span class="min-w-0 flex-1">
            <span class="block text-[13px] font-semibold">{{ title }}</span>
            <span v-if="description" class="mt-0.5 block text-[11px] text-muted-foreground">{{ description }}</span>
          </span>
          <ChevronDown :size="15" class="mt-0.5 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180" aria-hidden="true" />
        </button>
      </CollapsibleTrigger>
      <div v-if="$slots.actions" class="shrink-0">
        <slot name="actions" />
      </div>
    </div>
    <CollapsibleContent>
      <slot />
    </CollapsibleContent>
  </Collapsible>
</template>

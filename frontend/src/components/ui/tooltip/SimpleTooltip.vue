<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'

withDefaults(defineProps<{
  content: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  delayDuration?: number
  contentClass?: HTMLAttributes['class']
  disabled?: boolean
}>(), {
  side: 'top',
  delayDuration: 200,
  disabled: false,
})
</script>

<template>
  <slot v-if="disabled || !content" />
  <TooltipProvider v-else :delay-duration="delayDuration" :disable-hoverable-content="true">
    <Tooltip>
      <TooltipTrigger as-child>
        <slot />
      </TooltipTrigger>
      <TooltipContent :side="side" :class="contentClass">
        {{ content }}
      </TooltipContent>
    </Tooltip>
  </TooltipProvider>
</template>

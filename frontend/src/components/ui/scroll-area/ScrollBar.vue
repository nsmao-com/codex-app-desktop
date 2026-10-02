<script setup lang="ts">
import type { ScrollAreaScrollbarProps } from "reka-ui"
import type { HTMLAttributes } from "vue"
import { reactiveOmit } from "@vueuse/core"
import { ScrollAreaScrollbar, ScrollAreaThumb } from "reka-ui"
import { cn } from "@/lib/utils"

const props = withDefaults(defineProps<ScrollAreaScrollbarProps & { class?: HTMLAttributes["class"] }>(), {
  orientation: "vertical",
})

const delegatedProps = reactiveOmit(props, "class")
</script>

<template>
  <ScrollAreaScrollbar
    data-slot="scroll-area-scrollbar"
    v-bind="delegatedProps"
    :class="
      cn('flex touch-none p-px transition-colors select-none',
         orientation === 'vertical'
           && 'absolute right-0 top-0 z-10 h-full w-2.5 border-l border-l-transparent',
         orientation === 'horizontal'
           && 'absolute bottom-0 left-0 z-10 h-2.5 w-full flex-col border-t border-t-transparent',
         props.class)"
  >
    <ScrollAreaThumb
      data-slot="scroll-area-thumb"
      class="relative flex-1 rounded-full bg-transparent transition-colors hover:bg-black/25 dark:bg-transparent dark:hover:bg-white/35"
    />
  </ScrollAreaScrollbar>
</template>

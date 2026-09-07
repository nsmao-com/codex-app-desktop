<script setup lang="ts">
import { computed } from 'vue'
import { Brain } from '@lucide/vue'
const props = defineProps<{ modelValue: string; options: { effort: string; displayName?: string; description?: string }[]; label: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const index = computed(() => Math.max(0, props.options.findIndex(item => item.effort === props.modelValue)))
const current = computed(() => props.options[index.value])
const progress = computed(() => props.options.length > 1 ? index.value / (props.options.length - 1) * 100 : 0)
function change(event: Event): void {
  const value = props.options[Number((event.target as HTMLInputElement).value)]?.effort
  if (value) emit('update:modelValue', value)
}
</script>
<template>
  <div class="reasoning-control rounded-xl border border-border/60 bg-muted/20 p-4">
    <div class="mb-3 flex items-center justify-between gap-3 text-xs"><span class="flex items-center gap-2 text-muted-foreground"><Brain :size="14" />{{ label }}</span><strong class="rounded-md bg-primary/10 px-2 py-1 font-semibold capitalize text-primary">{{ current?.displayName || current?.effort || '—' }}</strong></div>
    <input class="policy-slider w-full" type="range" min="0" :max="Math.max(0, options.length - 1)" step="1" :value="index" :disabled="options.length < 2" :aria-label="label" :aria-valuetext="current?.displayName || current?.effort" :style="{ '--policy-progress': `${progress}%` }" @input="change" />
    <div class="mt-1 flex flex-wrap justify-between gap-1">
      <button v-for="option in options" :key="option.effort" type="button" class="rounded-md px-2 py-1.5 text-[10px] capitalize transition-colors focus-visible:outline focus-visible:outline-primary" :class="modelValue === option.effort ? 'bg-primary/10 text-primary font-semibold' : 'text-muted-foreground hover:bg-muted'" :aria-pressed="modelValue === option.effort" @click="emit('update:modelValue', option.effort)">{{ option.displayName || option.effort }}</button>
    </div>
    <p v-if="current?.description" class="mt-3 text-[11px] leading-5 text-muted-foreground">{{ current.description }}</p>
  </div>
</template>

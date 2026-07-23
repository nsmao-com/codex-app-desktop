<script setup lang="ts">
import { Check, ChevronsUpDown, Search } from '@lucide/vue'
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'
import type { SelectOption } from '@/types/codex'

const model = defineModel<string>({ required: true })

const props = withDefaults(defineProps<{
  options: SelectOption[]
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  ariaLabel?: string
  class?: string
  contentClass?: string
  /** Preview each option in its own font family (for system font pickers). */
  previewFont?: boolean
  align?: 'start' | 'center' | 'end'
}>(), {
  placeholder: '',
  searchPlaceholder: '',
  emptyText: '',
  ariaLabel: '',
  class: '',
  contentClass: '',
  previewFont: false,
  align: 'end',
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const { t } = useI18n()
const open = shallowRef(false)
const query = shallowRef('')
const searchInput = useTemplateRef<InstanceType<typeof Input>>('searchInput')

const selected = computed(() =>
  props.options.find((option) => option.value === model.value) ?? null,
)

const selectedLabel = computed(() =>
  selected.value?.label || props.placeholder || model.value || '',
)

const filteredOptions = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase()
  if (!needle) return props.options
  return props.options.filter((option) => {
    const haystack = `${option.label} ${option.value} ${option.description || ''} ${option.badge || ''}`
    return haystack.toLocaleLowerCase().includes(needle)
  })
})

const resolvedSearchPlaceholder = computed(() =>
  props.searchPlaceholder || t('common.searchPlaceholder'),
)

const resolvedEmptyText = computed(() =>
  props.emptyText || t('common.searchEmpty'),
)

watch(open, async (isOpen) => {
  if (!isOpen) {
    query.value = ''
    return
  }
  await nextTick()
  const el = searchInput.value?.$el as HTMLInputElement | undefined
  el?.focus?.()
  el?.select?.()
})

function optionStyle(option: SelectOption): Record<string, string> | undefined {
  if (!props.previewFont) return undefined
  if (option.value === 'manrope' || option.value === 'system' || option.value === 'mono') return undefined
  return { fontFamily: `"${option.value.replaceAll('"', '\\"')}"` }
}

function pick(option: SelectOption): void {
  if (option.disabled) return
  model.value = option.value
  emit('update:modelValue', option.value)
  open.value = false
  query.value = ''
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button
        type="button"
        variant="outline"
        role="combobox"
        :aria-expanded="open"
        :aria-label="ariaLabel || selectedLabel"
        :class="cn(
          'border-input h-8 w-full justify-between gap-2 px-3 text-xs font-normal shadow-xs',
          !selected && 'text-muted-foreground',
          props.class,
        )"
      >
        <span
          class="min-w-0 flex-1 truncate text-left"
          :style="selected ? optionStyle(selected) : undefined"
        >
          {{ selectedLabel }}
        </span>
        <ChevronsUpDown class="size-3.5 shrink-0 opacity-50" />
      </Button>
    </PopoverTrigger>
    <PopoverContent
      :align="align"
      :class="cn('w-[var(--reka-popover-trigger-width)] min-w-56 p-0', props.contentClass)"
      @open-auto-focus.prevent
    >
      <div class="flex items-center gap-2 border-b px-2">
        <Search class="size-3.5 shrink-0 text-muted-foreground" />
        <Input
          ref="searchInput"
          v-model="query"
          type="search"
          autocomplete="off"
          spellcheck="false"
          class="h-9 border-0 bg-transparent px-0 text-xs shadow-none focus-visible:border-0 focus-visible:ring-0"
          :placeholder="resolvedSearchPlaceholder"
          @keydown.escape.stop="open = false"
        />
      </div>
      <div class="max-h-64 overflow-y-auto p-1">
        <button
          v-for="option in filteredOptions"
          :key="option.value"
          type="button"
          class="flex w-full items-start gap-2 rounded-sm px-2 py-1.5 text-left text-xs outline-none"
          :class="option.disabled
            ? 'cursor-not-allowed opacity-40'
            : 'hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent'"
          :disabled="option.disabled"
          @click="pick(option)"
        >
          <Check
            class="mt-0.5 size-3.5 shrink-0"
            :class="option.value === model ? 'opacity-100' : 'opacity-0'"
          />
          <span class="min-w-0 flex-1">
            <span class="block truncate" :style="optionStyle(option)">{{ option.label }}</span>
            <span v-if="option.description" class="mt-0.5 block truncate text-[10px] text-muted-foreground">
              {{ option.description }}
            </span>
          </span>
          <span v-if="option.badge" class="shrink-0 text-[10px] text-muted-foreground">{{ option.badge }}</span>
        </button>
        <p v-if="!filteredOptions.length" class="px-2 py-6 text-center text-[11px] text-muted-foreground">
          {{ resolvedEmptyText }}
        </p>
      </div>
    </PopoverContent>
  </Popover>
</template>

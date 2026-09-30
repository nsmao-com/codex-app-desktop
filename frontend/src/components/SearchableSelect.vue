<script setup lang="ts">
import { Check, ChevronsUpDown, Search } from '@lucide/vue'
import { computed, nextTick, shallowRef, useId, useTemplateRef, watch } from 'vue'
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
  disabled?: boolean
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
  disabled: false,
  class: '',
  contentClass: '',
  previewFont: false,
  align: 'end',
})

const { t } = useI18n()
const open = shallowRef(false)
const query = shallowRef('')
const listId = `select-${useId()}`
const activeIndex = shallowRef(-1)
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

const activeOptionId = computed(() => activeIndex.value >= 0 ? `${listId}-${activeIndex.value}` : undefined)

function resetActiveOption(): void {
  const selectedIndex = filteredOptions.value.findIndex((option) => option.value === model.value && !option.disabled)
  activeIndex.value = selectedIndex >= 0 ? selectedIndex : filteredOptions.value.findIndex((option) => !option.disabled)
}

watch(filteredOptions, resetActiveOption)
watch(() => props.disabled, (disabled) => { if (disabled) open.value = false })
watch(activeOptionId, async (id) => {
  await nextTick()
  if (open.value && id) document.getElementById(id)?.scrollIntoView({ block: 'nearest' })
})

watch(open, async (isOpen) => {
  if (!isOpen) {
    query.value = ''
    return
  }
  if (props.disabled) { open.value = false; return }
  resetActiveOption()
  await nextTick()
  if (!open.value) return
  const el = searchInput.value?.$el as HTMLInputElement | undefined
  el?.focus?.()
  el?.select?.()
})

function onSearchKeydown(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (event.key === 'Tab') { open.value = false; return }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', 'Escape'].includes(event.key)) return
  event.preventDefault()
  event.stopPropagation()
  if (event.key === 'Escape') { open.value = false; return }
  if (event.key === 'Enter') {
    const option = filteredOptions.value[activeIndex.value]
    if (option) pick(option)
    return
  }
  const indices = filteredOptions.value.flatMap((option, index) => option.disabled ? [] : [index])
  if (!indices.length) return
  if (event.key === 'Home') activeIndex.value = indices[0]!
  else if (event.key === 'End') activeIndex.value = indices[indices.length - 1]!
  else {
    const current = indices.indexOf(activeIndex.value)
    const next = current < 0 ? (event.key === 'ArrowDown' ? 0 : indices.length - 1)
      : (current + (event.key === 'ArrowDown' ? 1 : -1) + indices.length) % indices.length
    activeIndex.value = indices[next]!
  }
}

function optionStyle(option: SelectOption): Record<string, string> | undefined {
  if (!props.previewFont) return undefined
  if (option.value === 'manrope' || option.value === 'system' || option.value === 'mono') return undefined
  return { fontFamily: `"${option.value.replaceAll('"', '\\"')}"` }
}

function pick(option: SelectOption): void {
  if (props.disabled || option.disabled) return
  model.value = option.value
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
        aria-haspopup="listbox"
        :aria-controls="open ? listId : undefined"
        :aria-label="ariaLabel || selectedLabel"
        :disabled="disabled"
        @keydown.down.prevent="open = true"
        @keydown.up.prevent="open = true"
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
          role="combobox"
          aria-autocomplete="list"
          :aria-expanded="open"
          :aria-controls="listId"
          :aria-activedescendant="activeOptionId"
          :aria-label="ariaLabel || resolvedSearchPlaceholder"
          @keydown="onSearchKeydown"
        />
      </div>
      <div :id="listId" role="listbox" :aria-label="ariaLabel || selectedLabel" class="max-h-64 overflow-y-auto p-1">
        <button
          v-for="(option, index) in filteredOptions"
          :key="option.value"
          :id="`${listId}-${index}`"
          type="button"
          role="option"
          :aria-selected="option.value === model"
          :aria-disabled="Boolean(option.disabled)"
          :tabindex="-1"
          class="flex w-full items-start gap-2 rounded-sm px-2 py-1.5 text-left text-xs outline-none"
          :class="option.disabled
            ? 'cursor-not-allowed opacity-40'
            : index === activeIndex ? 'bg-accent text-accent-foreground' : 'hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent'"
          :disabled="option.disabled"
          @pointermove="!option.disabled && (activeIndex = index)"
          @mousedown.prevent
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
        <p v-if="!filteredOptions.length" role="status" class="px-2 py-6 text-center text-[11px] text-muted-foreground">
          {{ resolvedEmptyText }}
        </p>
      </div>
    </PopoverContent>
  </Popover>
</template>

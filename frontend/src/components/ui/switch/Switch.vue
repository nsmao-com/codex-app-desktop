<script setup lang="ts">
import type { SwitchRootEmits, SwitchRootProps } from "reka-ui"
import type { HTMLAttributes } from "vue"
import { computed } from "vue"
import { reactiveOmit } from "@vueuse/core"
import {
  SwitchRoot,
  SwitchThumb,
  useForwardProps,
} from "reka-ui"
import { cn } from "@/lib/utils"

const props = defineProps<SwitchRootProps & {
  class?: HTMLAttributes["class"]
  /** Alias for modelValue (shadcn/radix checked API). */
  checked?: boolean
}>()

const emit = defineEmits<SwitchRootEmits & {
  "update:checked": [payload: boolean]
}>()

const delegatedProps = reactiveOmit(props, "class", "checked", "modelValue")
const forwarded = useForwardProps(delegatedProps)

const modelValue = computed(() => {
  if (props.modelValue !== undefined && props.modelValue !== null) return props.modelValue
  if (props.checked !== undefined) return props.checked
  return undefined
})

function onUpdate(value: boolean): void {
  emit("update:modelValue", value)
  emit("update:checked", value)
}
</script>

<template>
  <SwitchRoot
    data-slot="switch"
    v-bind="forwarded"
    :model-value="modelValue"
    :class="cn(
      'peer data-[state=checked]:bg-primary data-[state=unchecked]:bg-input focus-visible:border-ring focus-visible:ring-ring/50 dark:data-[state=unchecked]:bg-input/80 inline-flex h-[1.15rem] w-8 shrink-0 items-center rounded-full border border-transparent shadow-xs transition-all outline-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50',
      props.class,
    )"
    @update:model-value="onUpdate($event as boolean)"
  >
    <SwitchThumb
      data-slot="switch-thumb"
      :class="cn('bg-background dark:data-[state=unchecked]:bg-foreground dark:data-[state=checked]:bg-primary-foreground pointer-events-none block size-4 rounded-full ring-0 transition-transform data-[state=checked]:translate-x-[calc(100%-2px)] data-[state=unchecked]:translate-x-0')"
    />
  </SwitchRoot>
</template>

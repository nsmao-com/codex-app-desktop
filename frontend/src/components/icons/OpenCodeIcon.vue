<script setup lang="ts">
withDefaults(defineProps<{ size?: number | string }>(), { size: 16 })
</script>

<template>
  <svg :width="size" :height="size" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M22 24H2V0h20zM17 4.8H7v14.4h10z" />
  </svg>
</template>

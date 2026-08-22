<script setup lang="ts">
import {
  MODE_DRAWS,
  resolvePreset,
  type OrbSize,
  type OrbState,
} from 'thinking-orbs/engine'
import { onMounted, onUnmounted, shallowRef, watch } from 'vue'

type OrbTheme = 'auto' | 'dark' | 'light'

const props = withDefaults(defineProps<{
  state?: OrbState
  size?: OrbSize
  theme?: OrbTheme
  speed?: number
  paused?: boolean
  label?: string
}>(), {
  state: 'working',
  size: 20,
  theme: 'auto',
  speed: 1,
  paused: false,
  label: 'Thinking',
})

const canvas = shallowRef<HTMLCanvasElement | null>(null)
const dark = shallowRef(true)
const reducedMotion = shallowRef(false)

let frameId = 0
let mounted = false
let intersecting = true
let intersectionObserver: IntersectionObserver | null = null
let themeObserver: MutationObserver | null = null
let themeMedia: MediaQueryList | null = null
let motionMedia: MediaQueryList | null = null

function ancestorTheme(element: HTMLElement | null): boolean | null {
  let current = element
  while (current) {
    const theme = current.getAttribute('data-theme')
    if (theme === 'dark') return true
    if (theme === 'light') return false
    if (current.classList.contains('dark')) return true
    if (current.classList.contains('light')) return false
    current = current.parentElement
  }
  return null
}

function resolveDark(): boolean {
  if (props.theme === 'dark') return true
  if (props.theme === 'light') return false
  return ancestorTheme(canvas.value) ?? themeMedia?.matches ?? true
}

function stopAnimation(): void {
  cancelAnimationFrame(frameId)
  frameId = 0
}

function draw(seconds: number): void {
  const target = canvas.value
  if (!target) return

  const pixelRatio = Math.min(2, window.devicePixelRatio || 1)
  const canvasSize = Math.round(props.size * pixelRatio)
  if (target.width !== canvasSize || target.height !== canvasSize) {
    target.width = canvasSize
    target.height = canvasSize
  }

  const context = target.getContext('2d')
  if (!context) return

  const { mode, opts } = resolvePreset(props.state, props.size)
  context.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0)
  context.clearRect(0, 0, props.size, props.size)
  MODE_DRAWS[mode](context, props.size, seconds, dark.value, opts)
}

function animationFrame(timestamp: number): void {
  const { speed } = resolvePreset(props.state, props.size)
  draw(timestamp / 1000 * speed * props.speed)
  frameId = requestAnimationFrame(animationFrame)
}

function canAnimate(): boolean {
  return mounted
    && !props.paused
    && !reducedMotion.value
    && intersecting
    && document.visibilityState !== 'hidden'
}

function refreshAnimation(): void {
  stopAnimation()
  if (reducedMotion.value) {
    draw(0.6)
    return
  }

  const { speed } = resolvePreset(props.state, props.size)
  draw(performance.now() / 1000 * speed * props.speed)
  if (canAnimate()) frameId = requestAnimationFrame(animationFrame)
}

function refreshTheme(): void {
  dark.value = resolveDark()
}

function refreshReducedMotion(): void {
  reducedMotion.value = Boolean(
    motionMedia?.matches
    || document.documentElement.getAttribute('data-reduce-motion') === 'true',
  )
}

function refreshAppearance(): void {
  refreshTheme()
  refreshReducedMotion()
}

function onVisibilityChange(): void {
  refreshAnimation()
}

onMounted(() => {
  mounted = true
  themeMedia = window.matchMedia('(prefers-color-scheme: dark)')
  motionMedia = window.matchMedia('(prefers-reduced-motion: reduce)')
  refreshAppearance()

  themeMedia.addEventListener('change', refreshTheme)
  motionMedia.addEventListener('change', refreshReducedMotion)

  themeObserver = new MutationObserver(refreshAppearance)
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class', 'data-theme', 'data-reduce-motion'],
    subtree: true,
  })

  if (typeof IntersectionObserver !== 'undefined' && canvas.value) {
    intersectionObserver = new IntersectionObserver(([entry]) => {
      intersecting = entry?.isIntersecting ?? true
      refreshAnimation()
    })
    intersectionObserver.observe(canvas.value)
  }

  document.addEventListener('visibilitychange', onVisibilityChange)
  refreshAnimation()
})

watch(
  () => [props.state, props.size, props.speed, props.paused, dark.value, reducedMotion.value] as const,
  refreshAnimation,
)

watch(() => props.theme, refreshTheme)

onUnmounted(() => {
  mounted = false
  stopAnimation()
  intersectionObserver?.disconnect()
  themeObserver?.disconnect()
  themeMedia?.removeEventListener('change', refreshTheme)
  motionMedia?.removeEventListener('change', refreshReducedMotion)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>

<template>
  <canvas
    ref="canvas"
    role="img"
    :aria-label="label"
    class="block shrink-0"
    :style="{ width: `${size}px`, height: `${size}px` }"
  />
</template>

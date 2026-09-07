/** One delegated listener also covers teleported menus and plain icon buttons. */
export function installClickEffects(): () => void {
  const active = new Map<HTMLElement, ReturnType<typeof setTimeout>>()
  const reduced = () => document.documentElement.dataset.reduceMotion === 'true'
    || window.matchMedia('(prefers-reduced-motion: reduce)').matches
  function remove(layer: HTMLElement): void {
    clearTimeout(active.get(layer))
    active.delete(layer)
    layer.remove()
  }
  function ripple(event: MouseEvent): void {
    if (reduced() || event.button !== 0 || !(event.target instanceof Element)) return
    const target = event.target.closest<HTMLElement>('button, [role="button"], [role="menuitem"], [role="tab"]')
    if (!target || target.matches(':disabled, [aria-disabled="true"], [data-disabled]') || target.closest('[inert]')) return
    const rect = target.getBoundingClientRect()
    if (!rect.width || !rect.height) return
    const x = event.detail === 0 ? rect.width / 2 : event.clientX - rect.left
    const y = event.detail === 0 ? rect.height / 2 : event.clientY - rect.top
    const radius = Math.hypot(Math.max(x, rect.width - x), Math.max(y, rect.height - y))
    const layer = document.createElement('span')
    layer.className = 'nice-click-ripple'
    layer.setAttribute('aria-hidden', 'true')
    Object.assign(layer.style, {
      left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px`,
      borderRadius: getComputedStyle(target).borderRadius,
      color: getComputedStyle(target).color,
    })
    const wave = document.createElement('span')
    Object.assign(wave.style, { left: `${x - radius}px`, top: `${y - radius}px`, width: `${radius * 2}px`, height: `${radius * 2}px` })
    layer.append(wave)
    // Decorative fixed overlay never participates in focus or pointer handling.
    document.body.append(layer)
    if (active.size >= 12) remove(active.keys().next().value!)
    active.set(layer, setTimeout(() => remove(layer), 550))
    wave.animate([{ transform: 'scale(0)', opacity: 0.18 }, { transform: 'scale(1)', opacity: 0 }], {
      duration: 480, easing: 'cubic-bezier(.16,1,.3,1)',
    }).onfinish = () => remove(layer)
  }
  document.addEventListener('click', ripple, true)
  return () => {
    document.removeEventListener('click', ripple, true)
    for (const layer of active.keys()) remove(layer)
  }
}

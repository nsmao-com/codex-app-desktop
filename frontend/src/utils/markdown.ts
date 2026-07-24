import DOMPurify from 'dompurify'
import { marked, type Tokens } from 'marked'

import { escapeHTML, highlightCode } from './highlight'

const MAX_CACHE_ENTRIES = 320
const MAX_CACHE_WEIGHT = 8_000_000
const CODE_COLLAPSE_LINES = 18
const markdownCache = new Map<string, { html: string; weight: number }>()
let markdownCacheWeight = 0

let purifyHooksInstalled = false

function installPurifyHooks(): void {
  if (purifyHooksInstalled || typeof window === 'undefined') return
  purifyHooksInstalled = true

  // DOMPurify strips file:/sandbox:/C:\... hrefs. Move them to data-open-path first.
  DOMPurify.addHook('uponSanitizeAttribute', (node, data) => {
    if (node.nodeName !== 'A' || data.attrName !== 'href') return
    const value = String(data.attrValue || '').trim()
    if (!value || value === '#' || /^https?:\/\//i.test(value) || /^mailto:/i.test(value)) return
    if (!node.getAttribute('data-open-path')) {
      node.setAttribute('data-open-path', value)
    }
    data.attrValue = '#'
  })

  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.nodeName !== 'A') return
    const el = node as HTMLElement
    const openPath = el.getAttribute('data-open-path')?.trim() || ''
    const href = el.getAttribute('href')?.trim() || ''
    if (openPath && (!href || href === '#')) {
      el.setAttribute('href', '#')
      el.classList.add('markdown-local-link')
    }
    // Bare <a>download</a> with no href — still make it clickable.
    if (!href && !openPath) {
      el.setAttribute('href', '#')
      el.setAttribute('data-open-path', '')
      el.classList.add('markdown-local-link')
    }
  })
}

function renderLocalOrRemoteLink(href: string, title: string | null | undefined, text: string): string {
  const rawHref = (href || '').trim()
  const safeTitle = title ? ` title="${escapeHTML(title)}"` : ''
  if (/^https?:\/\//i.test(rawHref) || /^mailto:/i.test(rawHref)) {
    const safeHref = escapeHTML(rawHref)
    const external = /^https?:\/\//i.test(rawHref)
    const rel = external ? ' rel="noopener noreferrer"' : ''
    const target = external ? ' target="_blank"' : ''
    return `<a href="${safeHref}"${safeTitle}${target}${rel}>${text}</a>`
  }
  // Keep local / special protocols out of href so sanitizer cannot drop them.
  const safePath = escapeHTML(rawHref)
  return `<a href="#" class="markdown-local-link" data-open-path="${safePath}"${safeTitle}>${text}</a>`
}

const renderer = new marked.Renderer()
renderer.link = ({ href, title, text }: Tokens.Link): string =>
  renderLocalOrRemoteLink(href || '', title, text)
renderer.code = ({ text, lang }: Tokens.Code): string => {
  const language = (lang ?? '').trim().split(/\s+/)[0] ?? ''
  const highlighted = highlightCode(text, language)
  const langClass = language ? ` language-${escapeHTML(language)}` : ''
  const langLabel = language
    ? `<span class="markdown-code-lang">${escapeHTML(language)}</span>`
    : ''
  const lineCount = text.split('\n').length
  const tall = lineCount > CODE_COLLAPSE_LINES
  const tallClass = tall ? ' markdown-code--tall is-collapsed' : ''
  const expandButton = tall
    ? '<button type="button" class="markdown-code-expand" data-collapse-code aria-expanded="false">__EXPAND_LABEL__</button>'
    : ''
  return [
    `<div class="markdown-code${tallClass}">`,
    langLabel,
    '<button type="button" class="markdown-code-copy" data-copy-code aria-label="__COPY_LABEL__">__COPY_LABEL__</button>',
    `<pre><code class="tool-payload${langClass}">${highlighted}</code></pre>`,
    expandButton,
    '</div>',
  ].join('')
}

marked.use({
  breaks: true,
  gfm: true,
  renderer,
})

export type RenderMarkdownOptions = {
  /**
   * Live / in-progress bubble only.
   * Plain pre-wrap: Grok stream mid-turn is often incomplete or reordered; parsing
   * it as GFM causes tables/fences to explode. Completed history rows use full GFM.
   */
  lite?: boolean
}

export function renderMarkdownLite(source: string): string {
  return renderMarkdown(source, 'Copy', 'Show more', { lite: true })
}

const PURIFY_TAGS = [
  'p', 'br', 'strong', 'em', 'del', 'a', 'ul', 'ol', 'li', 'code', 'pre',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'blockquote', 'hr', 'table', 'thead',
  'tbody', 'tr', 'th', 'td', 'div', 'button', 'span',
] as const

const PURIFY_ATTR = [
  'href', 'target', 'rel', 'class', 'type', 'title',
  'aria-label', 'aria-expanded',
  'data-copy-code', 'data-collapse-code', 'data-open-path',
] as const

function sanitizeMarkdownHTML(html: string): string {
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: [...PURIFY_TAGS],
    ALLOWED_ATTR: [...PURIFY_ATTR],
  })
}

function renderMarkdownPlain(source: string): string {
  const key = `plain\0${source}`
  const cached = markdownCache.get(key)
  if (cached) {
    markdownCache.delete(key)
    markdownCache.set(key, cached)
    return cached.html
  }
  const escaped = escapeHTML(source)
  const html = sanitizeMarkdownHTML(
    `<p class="markdown-stream-plain whitespace-pre-wrap break-words">${escaped}</p>`,
  )
  cacheMarkdown(key, html)
  return html
}

export function renderMarkdown(
  source: string,
  copyLabel = 'Copy',
  expandLabel = 'Show more',
  options?: RenderMarkdownOptions,
): string {
  if (!source) return ''

  if (options?.lite) {
    // Live tail only (history segments render with full GFM as completed items).
    return renderMarkdownPlain(source)
  }

  const key = copyLabel + '\0' + expandLabel + '\0' + source
  const cached = markdownCache.get(key)
  if (cached) {
    markdownCache.delete(key)
    markdownCache.set(key, cached)
    return cached.html
  }

  installPurifyHooks()
  let raw = ''
  try {
    raw = marked.parse(source, { async: false }) as string
  } catch {
    return renderMarkdownPlain(source)
  }
  const withLabels = raw
    .replaceAll('__COPY_LABEL__', escapeHTML(copyLabel))
    .replaceAll('__EXPAND_LABEL__', escapeHTML(expandLabel))
  const sanitized = sanitizeMarkdownHTML(withLabels)
  cacheMarkdown(key, sanitized)
  return sanitized
}

/** Extract likely file paths from surrounding message text for bare download anchors. */
export function extractCandidateFilePaths(text: string): string[] {
  if (!text.trim()) return []
  const patterns = [
    /(?:file:\/\/\/?[^\s<>"']+)/gi,
    /(?:[a-zA-Z]:[\\/][^\s<>"'|]+)/g,
    /(?:\.{0,2}[\\/][^\s<>"'|]+\.[a-zA-Z0-9]{1,12})/g,
    /(?:[^\s<>"'|]+\.(?:docx?|xlsx?|pptx?|pdf|zip|csv|txt|md|json|png|jpe?g|webp|gif|html?))/gi,
  ]
  const found = new Set<string>()
  for (const pattern of patterns) {
    for (const match of text.matchAll(pattern)) {
      const value = (match[0] || '').trim().replace(/[.,;:!?)]+$/, '')
      if (value.length >= 3) found.add(value)
    }
  }
  return [...found]
}

function cacheMarkdown(key: string, html: string): void {
  const weight = key.length + html.length
  if (weight > MAX_CACHE_WEIGHT / 2) return
  markdownCache.set(key, { html, weight })
  markdownCacheWeight += weight
  while (markdownCache.size > MAX_CACHE_ENTRIES || markdownCacheWeight > MAX_CACHE_WEIGHT) {
    const oldestKey = markdownCache.keys().next().value as string | undefined
    if (!oldestKey) break
    const oldest = markdownCache.get(oldestKey)
    markdownCache.delete(oldestKey)
    markdownCacheWeight -= oldest?.weight ?? 0
  }
}

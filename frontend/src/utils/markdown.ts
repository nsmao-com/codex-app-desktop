import DOMPurify from 'dompurify'
import { marked, type Tokens } from 'marked'

import { escapeHTML, highlightCode } from './highlight'

const MAX_CACHE_ENTRIES = 320
const MAX_CACHE_WEIGHT = 8_000_000
const CODE_COLLAPSE_LINES = 18
const markdownCache = new Map<string, { html: string; weight: number }>()
let markdownCacheWeight = 0

const renderer = new marked.Renderer()
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
  /** Skip marked/highlight while streaming — escape + line breaks only. */
  lite?: boolean
}

/** Cheap streaming path: no marked/highlight, just escaped text with line breaks. */
export function renderMarkdownLite(source: string): string {
  const escaped = escapeHTML(source)
  return DOMPurify.sanitize(
    `<p class="markdown-lite whitespace-pre-wrap break-words">${escaped}</p>`,
    { ALLOWED_TAGS: ['p', 'br'], ALLOWED_ATTR: ['class'] },
  )
}

export function renderMarkdown(
  source: string,
  copyLabel = 'Copy',
  expandLabel = 'Show more',
  options?: RenderMarkdownOptions,
): string {
  if (options?.lite) return renderMarkdownLite(source)

  const key = copyLabel + '\0' + expandLabel + '\0' + source
  const cached = markdownCache.get(key)
  if (cached) {
    markdownCache.delete(key)
    markdownCache.set(key, cached)
    return cached.html
  }

  const raw = marked.parse(source, { async: false }) as string
  const withLabels = raw
    .replaceAll('__COPY_LABEL__', escapeHTML(copyLabel))
    .replaceAll('__EXPAND_LABEL__', escapeHTML(expandLabel))
  const sanitized = DOMPurify.sanitize(withLabels, {
    ALLOWED_TAGS: [
      'p', 'br', 'strong', 'em', 'del', 'a', 'ul', 'ol', 'li', 'code', 'pre',
      'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'blockquote', 'hr', 'table', 'thead',
      'tbody', 'tr', 'th', 'td', 'div', 'button', 'span',
    ],
    ALLOWED_ATTR: ['href', 'target', 'rel', 'class', 'type', 'aria-label', 'aria-expanded', 'data-copy-code', 'data-collapse-code'],
  })
  cacheMarkdown(key, sanitized)
  return sanitized
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

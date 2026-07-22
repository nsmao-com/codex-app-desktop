import DOMPurify from 'dompurify'
import { marked, type Tokens } from 'marked'

import { escapeHTML, highlightCode } from './highlight'

const MAX_CACHE_ENTRIES = 320
const MAX_CACHE_WEIGHT = 8_000_000
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
  return [
    '<div class="markdown-code">',
    langLabel,
    '<button type="button" class="markdown-code-copy" data-copy-code aria-label="__COPY_LABEL__">__COPY_LABEL__</button>',
    `<pre><code class="tool-payload${langClass}">${highlighted}</code></pre>`,
    '</div>',
  ].join('')
}

marked.use({
  breaks: true,
  gfm: true,
  renderer,
})

export function renderMarkdown(source: string, copyLabel = 'Copy'): string {
  const key = copyLabel + '\0' + source
  const cached = markdownCache.get(key)
  if (cached) {
    markdownCache.delete(key)
    markdownCache.set(key, cached)
    return cached.html
  }

  const raw = marked.parse(source, { async: false }) as string
  const withLabel = raw.replaceAll('__COPY_LABEL__', escapeHTML(copyLabel))
  const sanitized = DOMPurify.sanitize(withLabel, {
    ALLOWED_TAGS: [
      'p', 'br', 'strong', 'em', 'del', 'a', 'ul', 'ol', 'li', 'code', 'pre',
      'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'blockquote', 'hr', 'table', 'thead',
      'tbody', 'tr', 'th', 'td', 'div', 'button', 'span',
    ],
    ALLOWED_ATTR: ['href', 'target', 'rel', 'class', 'type', 'aria-label', 'data-copy-code'],
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

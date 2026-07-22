/** Lightweight fenced-code highlighting without external deps. */

export function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[character] ?? character)
}

function isCodey(lang: string): boolean {
  return [
    'ts', 'tsx', 'js', 'jsx', 'javascript', 'typescript',
    'go', 'rust', 'rs', 'py', 'python', 'java', 'c', 'cpp', 'csharp', 'cs',
    'ruby', 'rb', 'php', 'swift', 'kotlin', 'scala', 'vue', 'svelte',
    'html', 'css', 'scss', 'less', 'sql', 'yaml', 'yml', 'toml', 'xml',
    'graphql', 'proto', 'lua', 'r', 'dart', 'zig', 'nim',
  ].includes(lang)
}

function isPlainText(lang: string): boolean {
  return !lang || ['text', 'plain', 'plaintext', 'txt', 'output', 'log'].includes(lang)
}

export function highlightCode(code: string, language = ''): string {
  const lang = language.toLowerCase().replace(/^language-/, '')
  if (!code) return ''
  if (isPlainText(lang)) return escapeHTML(code)
  if (lang === 'json' || looksLikeJSON(code)) return highlightJSON(code)
  if (isDiff(lang, code)) return highlightDiff(code)
  if (isShell(lang)) return highlightShell(code)
  if (isCodey(lang)) return highlightGenericCode(code)
  return escapeHTML(code)
}

function looksLikeJSON(code: string): boolean {
  const t = code.trim()
  return (t.startsWith('{') || t.startsWith('[')) && /"\s*:/.test(t)
}

function isDiff(lang: string, code: string): boolean {
  return lang === 'diff' || lang === 'patch' || /^@@ |^diff --git |^[-+]{3} /m.test(code)
}

function isShell(lang: string): boolean {
  return ['bash', 'sh', 'shell', 'zsh', 'powershell', 'ps1', 'cmd'].includes(lang)
}

export function highlightJSON(source: string): string {
  let text = source
  try {
    const parsed = JSON.parse(source) as unknown
    text = JSON.stringify(parsed, null, 2)
  } catch {
    // keep original when not strict JSON
  }

  let out = ''
  let i = 0
  const len = text.length
  while (i < len) {
    const ch = text[i]!
    if (ch === ' ' || ch === '\n' || ch === '\r' || ch === '\t') {
      out += ch
      i += 1
      continue
    }
    if (ch === '"') {
      let j = i + 1
      while (j < len) {
        if (text[j] === '\\') { j += 2; continue }
        if (text[j] === '"') { j += 1; break }
        j += 1
      }
      const lit = text.slice(i, j)
      let k = j
      while (k < len && (text[k] === ' ' || text[k] === '\t')) k += 1
      out += text[k] === ':'
        ? `<span class="tok-key">${escapeHTML(lit)}</span>`
        : `<span class="tok-str">${escapeHTML(lit)}</span>`
      i = j
      continue
    }
    if (ch === '-' || (ch >= '0' && ch <= '9')) {
      let j = i + 1
      while (j < len && /[0-9.eE+-]/.test(text[j]!)) j += 1
      out += `<span class="tok-num">${escapeHTML(text.slice(i, j))}</span>`
      i = j
      continue
    }
    if (text.startsWith('true', i)) { out += '<span class="tok-kw">true</span>'; i += 4; continue }
    if (text.startsWith('false', i)) { out += '<span class="tok-kw">false</span>'; i += 5; continue }
    if (text.startsWith('null', i)) { out += '<span class="tok-kw">null</span>'; i += 4; continue }
    if ('{}[]:,'.includes(ch)) {
      out += `<span class="tok-punct">${escapeHTML(ch)}</span>`
      i += 1
      continue
    }
    out += escapeHTML(ch)
    i += 1
  }
  return out
}

function highlightDiff(code: string): string {
  return code.split('\n').map((line) => {
    const esc = escapeHTML(line)
    if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff ') || line.startsWith('index ')) {
      return `<span class="tok-meta">${esc}</span>`
    }
    if (line.startsWith('@@')) return `<span class="tok-meta">${esc}</span>`
    if (line.startsWith('+')) return `<span class="tok-add">${esc}</span>`
    if (line.startsWith('-')) return `<span class="tok-del">${esc}</span>`
    return esc
  }).join('\n')
}

function highlightShell(code: string): string {
  return code.split('\n').map((line) => {
    if (/^\s*#/.test(line)) return `<span class="tok-comment">${escapeHTML(line)}</span>`
    let out = ''
    let i = 0
    while (i < line.length) {
      const ch = line[i]!
      if (ch === '"' || ch === "'") {
        const quote = ch
        let j = i + 1
        while (j < line.length && line[j] !== quote) {
          if (line[j] === '\\') j += 1
          j += 1
        }
        j = Math.min(j + 1, line.length)
        out += `<span class="tok-str">${escapeHTML(line.slice(i, j))}</span>`
        i = j
        continue
      }
      if (ch === '$') {
        let j = i + 1
        if (line[j] === '{') {
          while (j < line.length && line[j] !== '}') j += 1
          j = Math.min(j + 1, line.length)
        } else {
          while (j < line.length && /[A-Za-z0-9_]/.test(line[j]!)) j += 1
        }
        out += `<span class="tok-key">${escapeHTML(line.slice(i, j))}</span>`
        i = j
        continue
      }
      out += escapeHTML(ch)
      i += 1
    }
    return out
  }).join('\n')
}

function highlightGenericCode(code: string): string {
  const keywords = new Set([
    'const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'do', 'switch', 'case',
    'break', 'continue', 'class', 'extends', 'implements', 'interface', 'type', 'enum', 'import', 'export',
    'from', 'as', 'default', 'async', 'await', 'try', 'catch', 'finally', 'throw', 'new', 'this', 'super',
    'typeof', 'instanceof', 'in', 'of', 'void', 'null', 'undefined', 'true', 'false', 'public', 'private',
    'protected', 'static', 'readonly', 'abstract', 'package', 'struct', 'fn', 'impl', 'trait', 'use',
    'mod', 'pub', 'mut', 'match', 'loop', 'def', 'elif', 'lambda', 'with', 'yield', 'pass', 'raise',
    'except', 'go', 'defer', 'chan', 'select', 'map', 'func', 'package',
  ])

  let out = ''
  let i = 0
  const len = code.length

  while (i < len) {
    // line/block comments
    if (code.startsWith('//', i) || code.startsWith('#', i)) {
      let j = i
      while (j < len && code[j] !== '\n') j += 1
      out += `<span class="tok-comment">${escapeHTML(code.slice(i, j))}</span>`
      i = j
      continue
    }
    if (code.startsWith('/*', i)) {
      const end = code.indexOf('*/', i + 2)
      const j = end === -1 ? len : end + 2
      out += `<span class="tok-comment">${escapeHTML(code.slice(i, j))}</span>`
      i = j
      continue
    }
    // strings
    if (code[i] === '"' || code[i] === "'" || code[i] === '`') {
      const quote = code[i]!
      let j = i + 1
      while (j < len) {
        if (code[j] === '\\') { j += 2; continue }
        if (code[j] === quote) { j += 1; break }
        j += 1
      }
      out += `<span class="tok-str">${escapeHTML(code.slice(i, j))}</span>`
      i = j
      continue
    }
    // numbers
    if (/[0-9]/.test(code[i]!) && (i === 0 || /[^\w$]/.test(code[i - 1]!))) {
      let j = i + 1
      while (j < len && /[0-9.xXa-fA-Fn_]/.test(code[j]!)) j += 1
      out += `<span class="tok-num">${escapeHTML(code.slice(i, j))}</span>`
      i = j
      continue
    }
    // identifiers / keywords
    if (/[A-Za-z_$]/.test(code[i]!)) {
      let j = i + 1
      while (j < len && /[A-Za-z0-9_$]/.test(code[j]!)) j += 1
      const word = code.slice(i, j)
      if (keywords.has(word)) out += `<span class="tok-kw">${escapeHTML(word)}</span>`
      else out += escapeHTML(word)
      i = j
      continue
    }
    out += escapeHTML(code[i]!)
    i += 1
  }
  return out
}

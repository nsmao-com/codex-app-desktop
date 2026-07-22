import type { DiffFileView, DiffHunkView, DiffLineView } from '../types/codex'

export function parseUnifiedDiff(diff: string): DiffFileView[] {
  if (!diff.trim()) return []

  const files: DiffFileView[] = []
  let file: DiffFileView | null = null
  let hunk: DiffHunkView | null = null
  let oldLine = 0
  let newLine = 0

  const ensureFile = (): DiffFileView => {
    if (file) return file
    file = createFile('', '')
    files.push(file)
    return file
  }

  for (const line of diff.split(/\r?\n/)) {
    const fileHeader = line.match(/^diff --git a\/(.+) b\/(.+)$/)
    if (fileHeader) {
      file = createFile(fileHeader[1] ?? '', fileHeader[2] ?? '')
      files.push(file)
      hunk = null
      continue
    }

    if (line.startsWith('--- ')) {
      const current = ensureFile()
      current.oldPath = cleanDiffPath(line.slice(4))
      continue
    }
    if (line.startsWith('+++ ')) {
      const current = ensureFile()
      current.newPath = cleanDiffPath(line.slice(4))
      current.displayPath = current.newPath === '/dev/null' ? current.oldPath : current.newPath
      continue
    }

    const hunkHeader = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/)
    if (hunkHeader) {
      oldLine = Number(hunkHeader[1] ?? 0)
      newLine = Number(hunkHeader[2] ?? 0)
      hunk = { header: line, lines: [] }
      ensureFile().hunks.push(hunk)
      continue
    }

    if (!hunk) continue
    const current = ensureFile()
    const view = toDiffLine(line, oldLine, newLine)
    hunk.lines.push(view)
    if (view.kind === 'add') {
      current.additions += 1
      newLine += 1
    } else if (view.kind === 'delete') {
      current.deletions += 1
      oldLine += 1
    } else if (view.kind === 'context') {
      oldLine += 1
      newLine += 1
    }
  }

  return files.filter((item) => item.hunks.length > 0 || item.displayPath)
}

function createFile(oldPath: string, newPath: string): DiffFileView {
  return {
    oldPath,
    newPath,
    displayPath: newPath || oldPath,
    additions: 0,
    deletions: 0,
    hunks: [],
  }
}

function cleanDiffPath(value: string): string {
  const path = value.split('\t')[0]?.trim() ?? ''
  if (path === '/dev/null') return path
  return path.replace(/^[ab]\//, '')
}

/** Extract a single file's unified-diff chunk from a multi-file turn diff. */
export function extractFileDiff(diff: string, path: string): string {
  const target = path.replace(/\\/g, '/').replace(/^\.\//, '').trim()
  if (!diff.trim() || !target) return ''

  const chunks = diff.split(/(?=^diff --git )/m)
  for (const chunk of chunks) {
    const trimmed = chunk.trim()
    if (!trimmed) continue
    const header = trimmed.match(/^diff --git a\/(.+?) b\/(.+)$/m)
    const oldPath = cleanDiffPath(header?.[1] ?? '')
    const newPath = cleanDiffPath(header?.[2] ?? '')
    const plus = trimmed.match(/^\+\+\+ (.+)$/m)?.[1]
    const minus = trimmed.match(/^--- (.+)$/m)?.[1]
    const candidates = [
      oldPath,
      newPath,
      cleanDiffPath(plus ?? ''),
      cleanDiffPath(minus ?? ''),
    ].filter((value) => value && value !== '/dev/null')

    if (candidates.some((candidate) => pathsMatch(candidate, target))) {
      return trimmed
    }
  }
  return ''
}

function pathsMatch(candidate: string, target: string): boolean {
  const left = candidate.replace(/\\/g, '/').replace(/^\.\//, '')
  const right = target
  return left === right || left.endsWith(`/${right}`) || right.endsWith(`/${left}`)
}

function toDiffLine(line: string, oldLine: number, newLine: number): DiffLineView {
  if (line.startsWith('+') && !line.startsWith('+++')) {
    return { kind: 'add', content: line.slice(1), oldLine: null, newLine }
  }
  if (line.startsWith('-') && !line.startsWith('---')) {
    return { kind: 'delete', content: line.slice(1), oldLine, newLine: null }
  }
  if (line.startsWith(' ')) {
    return { kind: 'context', content: line.slice(1), oldLine, newLine }
  }
  return { kind: 'meta', content: line, oldLine: null, newLine: null }
}

/** Shared view model for native and provider-emitted sub-agent activity. */
export type SubagentRuntime = 'codex' | 'claude' | 'gemini' | 'grok' | 'opencode'

export type SubagentStatus =
  | 'pending'
  | 'running'
  | 'completed'
  | 'failed'
  | 'interrupted'
  | 'unknown'

export type SubagentCapabilityLevel = 'supported' | 'experimental' | 'unsupported' | 'unknown'

export interface SubagentCapability {
  runtime: SubagentRuntime
  label: string
  level: SubagentCapabilityLevel
  /** Human-readable evidence, kept separate from raw provider error text. */
  evidence: string
  description: string
  observed: boolean
}

export interface SubagentActivity {
  /** Stable id used to merge start/update/complete events. */
  id: string
  runtime: SubagentRuntime
  sessionId: string
  turnId: string
  agentId: string
  parentAgentId: string
  agentName: string
  status: SubagentStatus
  /** Short task-oriented label such as "Started", "Reading files", or "Completed". */
  action: string
  detail: string
  source: string
  startedAt: number
  updatedAt: number
  completedAt?: number
}

/**
 * Baseline support claims are intentionally conservative. `observed` is raised
 * only after a real event arrives, so a provider without a documented native
 * event never appears to be running a child agent by accident.
 */
export const SUBAGENT_RUNTIME_META: ReadonlyArray<{
  runtime: SubagentRuntime
  label: string
  level: SubagentCapabilityLevel
  evidence: string
  description: string
}> = [
  {
    runtime: 'codex',
    label: 'Codex / OpenAI',
    level: 'supported',
    evidence: 'App Server collaboration items',
    description: 'Supports native collaboration and sub-agent tool events.',
  },
  {
    runtime: 'claude',
    label: 'Claude Code / Anthropic',
    level: 'supported',
    evidence: 'Task / Agent tool events',
    description: 'Task and Agent tools expose child-agent lifecycle updates.',
  },
  {
    runtime: 'gemini',
    label: 'Gemini CLI / Google',
    level: 'supported',
    evidence: 'invoke_agent / agent tool events',
    description: 'Native sub-agents are supported; event detail depends on the installed CLI version.',
  },
  {
    runtime: 'grok',
    label: 'Grok / xAI',
    level: 'supported',
    evidence: 'Grok Build spawn_subagent / Task events',
    description: 'Grok Build supports sub-agents; direct API mode may not emit child-agent events.',
  },
  {
    runtime: 'opencode',
    label: 'OpenCode',
    level: 'supported',
    evidence: 'Task / subtask tool parts',
    description: 'Task-style tool parts can be tracked as child-agent activity.',
  },
] as const

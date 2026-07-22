import { constants } from 'node:fs';
import { access } from 'node:fs/promises';
import type { AgentType } from './types.ts';

export type AgentResult =
  { isAgent: true; agent: { name: string } } | { isAgent: false; agent: undefined };

const DEVIN_LOCAL_PATH = '/opt/.devin';

let cachedResult: AgentResult | null = null;

/** Cursor trace IDs are also present in ordinary integrated terminals. */
function hasStrongCursorAgentSignal(): boolean {
  return (
    Boolean(process.env.CURSOR_AGENT?.trim()) ||
    process.env.CURSOR_EXTENSION_HOST_ROLE === 'agent-exec'
  );
}

function refineAgentResult(result: AgentResult): AgentResult {
  if (!result.isAgent) {
    return result;
  }

  if (result.agent.name === 'cursor' || result.agent.name === 'cursor-cli') {
    if (!hasStrongCursorAgentSignal()) {
      return { isAgent: false, agent: undefined };
    }

    return { isAgent: true, agent: { name: 'cursor-cli' } };
  }

  return result;
}

async function determineAgent(): Promise<AgentResult> {
  const explicitAgent = process.env.AI_AGENT?.trim();
  if (explicitAgent) {
    const name = explicitAgent === 'github-copilot-cli' ? 'github-copilot' : explicitAgent;
    return { isAgent: true, agent: { name } };
  }

  if (process.env.CURSOR_TRACE_ID) {
    return { isAgent: true, agent: { name: 'cursor' } };
  }
  if (process.env.CURSOR_AGENT || process.env.CURSOR_EXTENSION_HOST_ROLE === 'agent-exec') {
    return { isAgent: true, agent: { name: 'cursor-cli' } };
  }
  if (process.env.GEMINI_CLI) {
    return { isAgent: true, agent: { name: 'gemini' } };
  }
  if (process.env.CODEX_SANDBOX || process.env.CODEX_CI || process.env.CODEX_THREAD_ID) {
    return { isAgent: true, agent: { name: 'codex' } };
  }
  if (process.env.ANTIGRAVITY_AGENT) {
    return { isAgent: true, agent: { name: 'antigravity' } };
  }
  if (process.env.AUGMENT_AGENT) {
    return { isAgent: true, agent: { name: 'augment-cli' } };
  }
  if (process.env.OPENCODE_CLIENT) {
    return { isAgent: true, agent: { name: 'opencode' } };
  }
  if (process.env.CLAUDECODE || process.env.CLAUDE_CODE) {
    const name = process.env.CLAUDE_CODE_IS_COWORK ? 'cowork' : 'claude';
    return { isAgent: true, agent: { name } };
  }
  if (process.env.REPL_ID) {
    return { isAgent: true, agent: { name: 'replit' } };
  }
  if (
    process.env.COPILOT_MODEL ||
    process.env.COPILOT_ALLOW_ALL ||
    process.env.COPILOT_GITHUB_TOKEN
  ) {
    return { isAgent: true, agent: { name: 'github-copilot' } };
  }

  try {
    await access(DEVIN_LOCAL_PATH, constants.F_OK);
    return { isAgent: true, agent: { name: 'devin' } };
  } catch {
    return { isAgent: false, agent: undefined };
  }
}

/**
 * Map from detected execution-agent names to skills-cli AgentType identifiers.
 * Only includes agents that exist in both systems.
 */
const agentNameToType: Record<string, AgentType> = {
  cursor: 'cursor',
  'cursor-cli': 'cursor',
  claude: 'claude-code',
  cowork: 'claude-code',
  devin: 'universal', // Devin not in skills-cli agent list, use universal
  replit: 'replit',
  gemini: 'gemini-cli',
  codex: 'codex',
  antigravity: 'antigravity',
  'augment-cli': 'augment',
  opencode: 'opencode',
  'github-copilot': 'github-copilot',
};

/**
 * Detect if the CLI is being run inside an AI agent environment.
 * Results are cached after the first call.
 */
export async function detectAgent(): Promise<AgentResult> {
  if (cachedResult) return cachedResult;
  cachedResult = refineAgentResult(await determineAgent());
  return cachedResult;
}

/**
 * Returns true if the CLI is running inside a detected AI agent.
 * When true, the CLI should skip interactive prompts and use sensible defaults.
 */
export async function isRunningInAgent(): Promise<boolean> {
  const result = await detectAgent();
  return result.isAgent;
}

/**
 * Returns the name of the detected agent, or null if not running in an agent.
 */
export async function getAgentName(): Promise<string | null> {
  const result = await detectAgent();
  return result.isAgent ? result.agent.name : null;
}

/**
 * Maps a detected agent name to the corresponding skills-cli AgentType.
 * Returns null if the agent can't be mapped to a specific skills-cli agent.
 */
export function getAgentType(agentName: string): AgentType | null {
  return agentNameToType[agentName] ?? null;
}

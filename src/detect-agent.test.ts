import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const AGENT_ENV_VARS = [
  'AI_AGENT',
  'ANTIGRAVITY_AGENT',
  'AUGMENT_AGENT',
  'CLAUDE_CODE',
  'CLAUDE_CODE_IS_COWORK',
  'CLAUDECODE',
  'CODEX_CI',
  'CODEX_SANDBOX',
  'CODEX_THREAD_ID',
  'COPILOT_ALLOW_ALL',
  'COPILOT_GITHUB_TOKEN',
  'COPILOT_MODEL',
  'CURSOR_AGENT',
  'CURSOR_EXTENSION_HOST_ROLE',
  'CURSOR_TRACE_ID',
  'GEMINI_CLI',
  'OPENCODE_CLIENT',
  'REPL_ID',
] as const;

async function detectWith(environment: Record<string, string>) {
  for (const name of AGENT_ENV_VARS) {
    vi.stubEnv(name, '');
  }
  for (const [name, value] of Object.entries(environment)) {
    vi.stubEnv(name, value);
  }

  const { detectAgent } = await import('./detect-agent.ts');
  return detectAgent();
}

describe('detectAgent', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.unstubAllEnvs();
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it.each([
    ['AI_AGENT', { AI_AGENT: ' custom-agent ' }, 'custom-agent'],
    ['Copilot CLI AI_AGENT alias', { AI_AGENT: 'github-copilot-cli' }, 'github-copilot'],
    ['Cursor agent flag', { CURSOR_AGENT: '1' }, 'cursor-cli'],
    ['Cursor agent role', { CURSOR_EXTENSION_HOST_ROLE: 'agent-exec' }, 'cursor-cli'],
    ['Gemini', { GEMINI_CLI: '1' }, 'gemini'],
    ['Codex sandbox', { CODEX_SANDBOX: 'sandboxed' }, 'codex'],
    ['Codex CI', { CODEX_CI: '1' }, 'codex'],
    ['Codex thread', { CODEX_THREAD_ID: 'thread-123' }, 'codex'],
    ['Antigravity', { ANTIGRAVITY_AGENT: '1' }, 'antigravity'],
    ['Augment', { AUGMENT_AGENT: '1' }, 'augment-cli'],
    ['OpenCode', { OPENCODE_CLIENT: '1' }, 'opencode'],
    ['Claude Code legacy', { CLAUDECODE: '1' }, 'claude'],
    ['Claude Code', { CLAUDE_CODE: '1' }, 'claude'],
    ['Claude Cowork', { CLAUDE_CODE: '1', CLAUDE_CODE_IS_COWORK: '1' }, 'cowork'],
    ['Replit', { REPL_ID: 'repl-123' }, 'replit'],
    ['Copilot model', { COPILOT_MODEL: 'gpt-5' }, 'github-copilot'],
    ['Copilot allow-all', { COPILOT_ALLOW_ALL: '1' }, 'github-copilot'],
    ['Copilot token', { COPILOT_GITHUB_TOKEN: 'token' }, 'github-copilot'],
  ])('detects %s with its compatible name', async (_label, environment, expectedName) => {
    const result = await detectWith(environment);

    expect(result).toEqual({ isAgent: true, agent: { name: expectedName } });
  });

  it('gives the explicit AI_AGENT signal precedence', async () => {
    const result = await detectWith({ AI_AGENT: 'codex', GEMINI_CLI: '1' });

    expect(result).toEqual({ isAgent: true, agent: { name: 'codex' } });
  });

  it.each([
    ['trace only', { CURSOR_TRACE_ID: 'trace-123' }, { isAgent: false, agent: undefined }],
    [
      'trace before another agent signal',
      { CURSOR_TRACE_ID: 'trace-123', GEMINI_CLI: '1' },
      { isAgent: false, agent: undefined },
    ],
    [
      'trace with Cursor agent flag',
      { CURSOR_TRACE_ID: 'trace-123', CURSOR_AGENT: '1' },
      { isAgent: true, agent: { name: 'cursor-cli' } },
    ],
    [
      'trace with Cursor agent role',
      { CURSOR_TRACE_ID: 'trace-123', CURSOR_EXTENSION_HOST_ROLE: 'agent-exec' },
      { isAgent: true, agent: { name: 'cursor-cli' } },
    ],
  ])('refines Cursor detection for %s', async (_label, environment, expected) => {
    const result = await detectWith(environment);

    expect(result).toEqual(expected);
  });

  it('does not detect an agent in an ordinary shell', async () => {
    const result = await detectWith({});

    expect(result).toEqual({ isAgent: false, agent: undefined });
  });
});

describe('getAgentType', () => {
  it.each([
    ['cursor', 'cursor'],
    ['cursor-cli', 'cursor'],
    ['claude', 'claude-code'],
    ['cowork', 'claude-code'],
    ['devin', 'universal'],
    ['replit', 'replit'],
    ['gemini', 'gemini-cli'],
    ['codex', 'codex'],
    ['antigravity', 'antigravity'],
    ['augment-cli', 'augment'],
    ['opencode', 'opencode'],
    ['github-copilot', 'github-copilot'],
  ] as const)('maps %s to %s', async (agentName, expectedType) => {
    const { getAgentType } = await import('./detect-agent.ts');

    expect(getAgentType(agentName)).toBe(expectedType);
  });

  it('does not invent a target for an unknown detected agent', async () => {
    const { getAgentType } = await import('./detect-agent.ts');

    expect(getAgentType('custom-agent')).toBeNull();
  });
});

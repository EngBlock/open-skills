import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mkdtemp, mkdir, writeFile, rm, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

// Isolate side-effecting UI / network deps so runAdd can run headless in CI.
vi.mock('@clack/prompts', () => {
  const noop = () => {};
  return {
    intro: noop,
    outro: noop,
    note: noop,
    confirm: vi.fn().mockResolvedValue(true),
    cancel: noop,
    log: {
      info: noop,
      message: noop,
      warn: noop,
      error: (...a: unknown[]) => console.log('[clack.error]', ...a),
      step: noop,
      success: noop,
    },
    spinner: () => ({ start: noop, stop: noop }),
  };
});

vi.mock('picocolors', () => {
  const id = (s: string) => s;
  const colors = [
    'red',
    'green',
    'blue',
    'yellow',
    'cyan',
    'white',
    'black',
    'dim',
    'bold',
    'bgRed',
    'bgCyan',
    'bgBlack',
    'bgWhite',
    'underline',
    'inverse',
    'magenta',
    'gray',
    'reset',
  ];
  const obj: any = id;
  for (const c of colors) obj[c] = id;
  return { default: obj };
});

vi.mock('../src/detect-agent.ts', () => ({
  detectAgent: vi.fn().mockResolvedValue({ isAgent: false, agent: { name: 'none' } }),
  getAgentType: vi.fn(),
  ensureUniversalAgents: vi.fn((x: string[]) => x),
}));

// NOTE: do NOT mock ../src/agents.ts. installer.installSkillForAgent relies on the
// real `agents` map (e.g. codex.skillsDir / globalSkillsDir); a stubbed map with only
// `displayName` makes getAgentBaseDir() do join(baseDir, undefined) and throws
// "The \"path\" argument must be of type string. Received undefined". We pass
// `agent: ['codex']` to runAdd so the detectInstalledAgents() branch is skipped anyway.

// Simulate a GitHub clone without touching the network: cloneRepo returns a local
// fixture directory. cleanupTempDir is a no-op so we control teardown ourselves.
vi.mock('../src/git.ts', () => ({
  cloneRepo: vi.fn(),
  cleanupTempDir: vi.fn().mockResolvedValue(undefined),
  GitCloneError: class GitCloneError extends Error {},
}));

import { runAdd } from '../src/add.ts';
import { cloneRepo } from '../src/git.ts';
import * as installer from '../src/installer.ts';

describe('GitHub disk installation', () => {
  let base: string;
  let fixture: string; // a fake cloned repo (SKILL.md at the repo root)
  let project: string; // install target cwd
  let origCwd: string;
  let installSkillSpy: ReturnType<typeof vi.spyOn>;
  let fetchSpy: ReturnType<typeof vi.fn>;

  beforeEach(async () => {
    base = await mkdtemp(join(tmpdir(), 'root-disk-'));
    fixture = join(base, 'fixture-repo');
    await mkdir(fixture, { recursive: true });
    await writeFile(
      join(fixture, 'SKILL.md'),
      '---\nname: myrootskill\ndescription: root level skill\n---\n',
      'utf-8'
    );
    await mkdir(join(fixture, 'scripts'), { recursive: true });
    await writeFile(join(fixture, 'scripts', 'check-deps.mjs'), 'console.log("x")\n', 'utf-8');
    await mkdir(join(fixture, 'references'), { recursive: true });
    await writeFile(join(fixture, 'references', 'guide.md'), '# guide\n', 'utf-8');

    project = join(base, 'project');
    await mkdir(project, { recursive: true });
    origCwd = process.cwd();
    process.chdir(project);

    // Drive the GitHub-clone code path (tempDir === skill.path) without network.
    vi.mocked(cloneRepo).mockResolvedValue(fixture);
    fetchSpy = vi.fn().mockResolvedValue(new Response('', { status: 404 }));
    vi.stubGlobal('fetch', fetchSpy);

    installSkillSpy = vi.spyOn(installer, 'installSkillForAgent');
    vi.spyOn(process, 'exit').mockImplementation((() => {
      throw new Error('process.exit called');
    }) as never);
  });

  afterEach(async () => {
    process.chdir(origCwd);
    await rm(base, { recursive: true, force: true });
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('clones a formerly snapshot-eligible source and records the project install', async () => {
    await runAdd(['vercel-labs/agent-skills'], {
      yes: true,
      agent: ['codex'],
      global: false,
      mode: 'copy',
    });

    expect(cloneRepo).toHaveBeenCalledWith(
      'https://github.com/vercel-labs/agent-skills.git',
      undefined
    );
    expect(installSkillSpy).toHaveBeenCalled();
    expect(fetchSpy).not.toHaveBeenCalled();

    const installed = join(project, '.agents', 'skills', 'myrootskill');
    await expect(readFile(join(installed, 'SKILL.md'), 'utf-8')).resolves.toContain('myrootskill');
    await expect(readFile(join(installed, 'scripts', 'check-deps.mjs'), 'utf-8')).resolves.toBe(
      'console.log("x")\n'
    );
    await expect(readFile(join(installed, 'references', 'guide.md'), 'utf-8')).resolves.toBe(
      '# guide\n'
    );

    const lock = JSON.parse(await readFile(join(project, 'skills-lock.json'), 'utf-8')) as {
      version: number;
      skills: Record<string, Record<string, unknown>>;
    };
    expect(lock).toEqual({
      version: 1,
      skills: {
        myrootskill: {
          source: 'vercel-labs/agent-skills',
          sourceType: 'github',
          skillPath: 'SKILL.md',
          computedHash: expect.stringMatching(/^[a-f0-9]{64}$/),
        },
      },
    });
  });

  it('retains the global GitHub lock schema for cloned skills', async () => {
    const stateDir = join(base, 'state');
    vi.stubEnv('XDG_STATE_HOME', stateDir);
    fetchSpy.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          sha: 'root-tree-sha',
          tree: [{ path: 'SKILL.md', type: 'blob', sha: 'skill-blob-sha' }],
        })
      )
    );
    installSkillSpy.mockResolvedValue({
      success: true,
      path: join(base, 'global-skills', 'myrootskill'),
      mode: 'copy',
    });

    await runAdd(['vercel-labs/agent-skills'], {
      yes: true,
      agent: ['codex'],
      global: true,
      copy: true,
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(fetchSpy).toHaveBeenCalledWith(
      'https://api.github.com/repos/vercel-labs/agent-skills/git/trees/HEAD?recursive=1',
      expect.any(Object)
    );

    const lock = JSON.parse(
      await readFile(join(stateDir, 'skills', '.skill-lock.json'), 'utf-8')
    ) as { version: number; skills: Record<string, Record<string, unknown>> };
    expect(lock).toEqual({
      version: 3,
      skills: {
        myrootskill: {
          source: 'vercel-labs/agent-skills',
          sourceType: 'github',
          sourceUrl: 'https://github.com/vercel-labs/agent-skills.git',
          skillPath: 'SKILL.md',
          skillFolderHash: 'root-tree-sha',
          installedAt: expect.any(String),
          updatedAt: expect.any(String),
        },
      },
      dismissed: {},
    });
  });
});

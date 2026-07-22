import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

vi.mock('../src/git.ts', () => ({
  cloneRepo: vi.fn(),
  cleanupTempDir: vi.fn().mockResolvedValue(undefined),
  GitCloneError: class GitCloneError extends Error {},
}));

import { cloneRepo } from '../src/git.ts';
import { runUse } from '../src/use.ts';

describe('clone-backed use', () => {
  let repoDir: string;

  beforeEach(async () => {
    vi.clearAllMocks();
    repoDir = await mkdtemp(join(tmpdir(), 'skills-use-clone-'));
    await writeSkill(join(repoDir, 'skills', 'one'), 'one', 'Do one.');
    const selectedDir = join(repoDir, 'skills', 'selected');
    await writeSkill(selectedDir, 'selected', 'Use the supporting script.');
    await mkdir(join(selectedDir, 'scripts'), { recursive: true });
    await writeFile(join(selectedDir, 'scripts', 'run.sh'), 'echo selected\n', 'utf-8');
    await writeSkill(join(repoDir, 'examples', 'catalog', 'deep'), 'deep', 'Found deeply.');

    vi.mocked(cloneRepo).mockResolvedValue(repoDir);
    vi.spyOn(process.stdout, 'write').mockImplementation(() => true);
    vi.spyOn(process, 'exit').mockImplementation(((code?: string | number | null) => {
      throw new Error(`process.exit(${code})`);
    }) as never);
  });

  afterEach(async () => {
    for (const [output] of vi.mocked(process.stdout.write).mock.calls) {
      const supportDir = String(output)
        .split('Supporting files for this skill were downloaded to:\n')[1]
        ?.split('\n')[0];
      if (supportDir) await rm(join(supportDir, '..'), { recursive: true, force: true });
    }
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    await rm(repoDir, { recursive: true, force: true });
  });

  it('clones a formerly snapshot-eligible GitHub source without fetching hosted contents', async () => {
    const fetchSpy = vi.fn(() => {
      throw new Error('unexpected hosted fetch');
    });
    vi.stubGlobal('fetch', fetchSpy);

    await runUse(['vercel-labs/agent-skills@selected']);

    expect(cloneRepo).toHaveBeenCalledWith(
      'https://github.com/vercel-labs/agent-skills.git',
      undefined
    );
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(process.stdout.write).toHaveBeenCalledWith(
      expect.stringContaining('Use the supporting script.')
    );

    const prompt = vi.mocked(process.stdout.write).mock.calls[0]?.[0] as string;
    const supportDir = prompt
      .split('Supporting files for this skill were downloaded to:\n')[1]
      ?.split('\n')[0];
    expect(supportDir).toBeTruthy();
    await expect(
      import('node:fs/promises').then(({ readFile }) =>
        readFile(join(supportDir!, 'scripts', 'run.sh'), 'utf-8')
      )
    ).resolves.toBe('echo selected\n');
  });

  it('preserves clone-backed GitLab skill selection', async () => {
    await runUse(['https://gitlab.com/acme/skills'], { skill: 'selected' });

    expect(cloneRepo).toHaveBeenCalledWith('https://gitlab.com/acme/skills.git', undefined);
    expect(writtenPrompt()).toContain('Use the supporting script.');
  });

  it('preserves full-depth discovery for cloned repositories', async () => {
    await runUse(['vercel-labs/agent-skills'], { skill: 'deep', fullDepth: true });

    expect(cloneRepo).toHaveBeenCalledWith(
      'https://github.com/vercel-labs/agent-skills.git',
      undefined
    );
    expect(writtenPrompt()).toContain('Found deeply.');
  });

  it('preserves well-known skill materialization without cloning', async () => {
    const fetchSpy = vi.fn(async (url: string | URL | Request) => {
      const href = String(url);
      if (href === 'https://example.com/.well-known/agent-skills/index.json') {
        return jsonResponse({
          skills: [
            {
              name: 'remote-skill',
              description: 'Remote skill.',
              files: ['SKILL.md', 'references/guide.md'],
            },
          ],
        });
      }
      if (href === 'https://example.com/.well-known/agent-skills/remote-skill/SKILL.md') {
        return new Response(
          '---\nname: remote-skill\ndescription: Remote skill.\n---\n# Remote\n\nUse the guide.'
        );
      }
      if (
        href === 'https://example.com/.well-known/agent-skills/remote-skill/references/guide.md'
      ) {
        return new Response('Remote guide.');
      }
      return new Response('not found', { status: 404 });
    });
    vi.stubGlobal('fetch', fetchSpy);

    await runUse(['https://example.com']);

    expect(cloneRepo).not.toHaveBeenCalled();
    expect(writtenPrompt()).toContain('Use the guide.');
    expect(fetchSpy.mock.calls.map(([url]) => String(url))).not.toContainEqual(
      expect.stringContaining('skills.sh')
    );
  });
});

function writtenPrompt(): string {
  return vi.mocked(process.stdout.write).mock.calls.at(-1)?.[0] as string;
}

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    headers: { 'content-type': 'application/json' },
  });
}

async function writeSkill(skillDir: string, name: string, body: string): Promise<void> {
  await mkdir(skillDir, { recursive: true });
  await writeFile(
    join(skillDir, 'SKILL.md'),
    `---\nname: ${name}\ndescription: ${name} description\n---\n\n# ${name}\n\n${body}\n`,
    'utf-8'
  );
}

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  fetchRepoTree,
  fetchSkillFolderHash,
  getSkillFolderHashFromTree,
  type RepoTree,
} from '../src/github-tree.ts';

describe('GitHub tree capability', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('resolves root and nested skill paths from one repository tree', () => {
    const tree: RepoTree = {
      sha: 'root-tree-sha',
      branch: 'main',
      tree: [
        { path: 'skills', type: 'tree', sha: 'skills-tree-sha' },
        { path: 'skills/review', type: 'tree', sha: 'review-tree-sha' },
        { path: 'skills/review/SKILL.md', type: 'blob', sha: 'skill-blob-sha' },
      ],
    };

    expect(getSkillFolderHashFromTree(tree, 'SKILL.md')).toBe('root-tree-sha');
    expect(getSkillFolderHashFromTree(tree, 'skills/review/SKILL.md')).toBe('review-tree-sha');
  });

  it('fetches a nested skill folder hash through the focused capability', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          sha: 'root-tree-sha',
          tree: [{ path: 'skills/review', type: 'tree', sha: 'review-tree-sha' }],
        }),
        { status: 200 }
      )
    ) as unknown as typeof globalThis.fetch;

    await expect(
      fetchSkillFolderHash('owner/repo', 'skills/review/SKILL.md', undefined, 'release')
    ).resolves.toBe('review-tree-sha');
  });

  it('falls back from HEAD to main when no ref is supplied', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 403 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ sha: 'main-tree-sha', tree: [] }), { status: 200 })
      );
    globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch;

    const tree = await fetchRepoTree('owner/repo');

    expect(tree).toEqual({ sha: 'main-tree-sha', branch: 'main', tree: [] });
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toEqual([
      'https://api.github.com/repos/owner/repo/git/trees/HEAD?recursive=1',
      'https://api.github.com/repos/owner/repo/git/trees/main?recursive=1',
    ]);
  });
});

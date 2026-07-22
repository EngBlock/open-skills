const FETCH_TIMEOUT = 10_000;

export interface TreeEntry {
  path: string;
  type: 'blob' | 'tree';
  sha: string;
  size?: number;
}

export interface RepoTree {
  sha: string;
  branch: string;
  tree: TreeEntry[];
}

/**
 * Within-process memo: once GitHub rate-limits this IP, later calls skip
 * requests guaranteed to fail and go directly to the authenticated fallback.
 */
let _rateLimitedThisSession = false;

/** For tests only. */
export function resetRepoTreeAuthState(): void {
  _rateLimitedThisSession = false;
}

interface BranchFetchResult {
  tree: RepoTree | null;
  rateLimited: boolean;
  authRetryable: boolean;
}

async function fetchTreeBranch(
  ownerRepo: string,
  branch: string,
  token: string | null
): Promise<BranchFetchResult> {
  try {
    const url = `https://api.github.com/repos/${ownerRepo}/git/trees/${encodeURIComponent(branch)}?recursive=1`;
    const headers: Record<string, string> = {
      Accept: 'application/vnd.github.v3+json',
      'User-Agent': 'skills-cli',
    };
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }

    const response = await fetch(url, {
      headers,
      signal: AbortSignal.timeout(FETCH_TIMEOUT),
    });

    if (response.ok) {
      const data = (await response.json()) as { sha: string; tree: TreeEntry[] };
      return {
        tree: { sha: data.sha, branch, tree: data.tree },
        rateLimited: false,
        authRetryable: false,
      };
    }

    const rateLimited =
      response.status === 403 && response.headers.get('x-ratelimit-remaining') === '0';
    const authRetryable = response.status === 401 || response.status === 404;
    return { tree: null, rateLimited, authRetryable };
  } catch {
    return { tree: null, rateLimited: false, authRetryable: false };
  }
}

async function fetchTreeWithToken(
  ownerRepo: string,
  branches: string[],
  getToken: () => string | null
): Promise<RepoTree | null> {
  const token = getToken();
  if (!token) return null;
  for (const branch of branches) {
    const result = await fetchTreeBranch(ownerRepo, branch, token);
    if (result.tree) return result.tree;
  }
  return null;
}

/**
 * Fetch the full recursive tree for a user-supplied or lock-recorded GitHub repository.
 * Authentication is resolved lazily after rate limits or anonymous private-repository
 * responses so normal public-repository requests do not access local credentials.
 */
export async function fetchRepoTree(
  ownerRepo: string,
  ref?: string,
  getToken?: () => string | null
): Promise<RepoTree | null> {
  const branches = ref ? [ref] : ['HEAD', 'main', 'master'];

  if (_rateLimitedThisSession && getToken) {
    return fetchTreeWithToken(ownerRepo, branches, getToken);
  }

  let rateLimited = false;
  let authRetryable = false;
  for (const branch of branches) {
    const result = await fetchTreeBranch(ownerRepo, branch, null);
    if (result.tree) return result.tree;
    if (result.rateLimited) {
      rateLimited = true;
      break;
    }
    if (result.authRetryable) {
      authRetryable = true;
      break;
    }
  }

  if (!getToken || !(rateLimited || authRetryable)) return null;

  if (rateLimited) _rateLimitedThisSession = true;

  return fetchTreeWithToken(ownerRepo, branches, getToken);
}

/**
 * Fetch the tree SHA for a skill folder in a user-supplied or lock-recorded
 * GitHub repository.
 */
export async function fetchSkillFolderHash(
  ownerRepo: string,
  skillPath: string,
  getToken?: (() => string | null) | null,
  ref?: string
): Promise<string | null> {
  const tree = await fetchRepoTree(ownerRepo, ref, getToken ?? undefined);
  if (!tree) return null;
  return getSkillFolderHashFromTree(tree, skillPath);
}

/**
 * Extract the folder hash (tree SHA) for a specific skill path from a repo tree.
 */
export function getSkillFolderHashFromTree(tree: RepoTree, skillPath: string): string | null {
  let folderPath = skillPath.replace(/\\/g, '/');

  if (folderPath.toLowerCase().endsWith('/skill.md')) {
    folderPath = folderPath.slice(0, -9);
  } else if (folderPath.toLowerCase().endsWith('skill.md')) {
    folderPath = folderPath.slice(0, -8);
  }
  if (folderPath.endsWith('/')) {
    folderPath = folderPath.slice(0, -1);
  }

  if (!folderPath) {
    return tree.sha;
  }

  const entry = tree.tree.find(
    (candidate) => candidate.type === 'tree' && candidate.path === folderPath
  );
  return entry?.sha ?? null;
}

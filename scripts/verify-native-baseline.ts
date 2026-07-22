import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import manifestJson from '../compatibility/npm-0.1.2/oracle.json' with { type: 'json' };

export interface OracleManifest {
  schemaVersion: number;
  package: {
    name: string;
    version: string;
    publishedAt: string;
    metadataUrl: string;
    publicationMetadataUrl: string;
  };
  artifact: {
    url: string;
    integrity: string;
    sha1: string;
    sha256: string;
    sha512: string;
    size: number;
    fileCount: number;
    unpackedSize: number;
    npmSignature: {
      keyId: string;
      signature: string;
    };
  };
  source: {
    repository: string;
    tag: string;
    tagRefUrl: string;
    tagObject: string;
    tagObjectUrl: string;
    commit: string;
    commitUrl: string;
    tree: string;
    tagSigned: boolean;
    protection: {
      rulesetId: number;
      rulesetName: string;
      rulesetUrl: string;
    };
  };
}

type Fetcher = typeof fetch;
type JsonObject = Record<string, any>;

function expectEqual(actual: unknown, expected: unknown, field: string): void {
  if (actual !== expected) {
    throw new Error(
      `${field} mismatch: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`
    );
  }
}

function digest(algorithm: string, data: Uint8Array, encoding: 'hex' | 'base64' = 'hex'): string {
  return createHash(algorithm).update(data).digest(encoding);
}

async function fetchJson(fetcher: Fetcher, url: string, headers: HeadersInit): Promise<JsonObject> {
  const response = await fetcher(url, { headers });
  if (!response.ok) {
    throw new Error(`GET ${url} failed: ${response.status} ${response.statusText}`);
  }
  return (await response.json()) as JsonObject;
}

export async function verifyBaseline(
  manifest: OracleManifest,
  fetcher: Fetcher = fetch,
  githubToken?: string
): Promise<void> {
  expectEqual(manifest.schemaVersion, 1, 'schemaVersion');

  const githubHeaders: HeadersInit = {
    Accept: 'application/vnd.github+json',
    'User-Agent': 'open-skills-baseline-verifier',
    ...(githubToken ? { Authorization: `Bearer ${githubToken}` } : {}),
  };

  const [metadata, publicationMetadata, tagRef, tagObject, commit, ruleset, artifactResponse] =
    await Promise.all([
      fetchJson(fetcher, manifest.package.metadataUrl, {}),
      fetchJson(fetcher, manifest.package.publicationMetadataUrl, {}),
      fetchJson(fetcher, manifest.source.tagRefUrl, githubHeaders),
      fetchJson(fetcher, manifest.source.tagObjectUrl, githubHeaders),
      fetchJson(fetcher, manifest.source.commitUrl, githubHeaders),
      fetchJson(fetcher, manifest.source.protection.rulesetUrl, githubHeaders),
      fetcher(manifest.artifact.url),
    ]);

  if (!artifactResponse.ok) {
    throw new Error(
      `GET ${manifest.artifact.url} failed: ${artifactResponse.status} ${artifactResponse.statusText}`
    );
  }

  expectEqual(metadata.name, manifest.package.name, 'package.name');
  expectEqual(metadata.version, manifest.package.version, 'package.version');
  const npmRepository = metadata.repository?.url?.replace(/^git\+/, '').replace(/\.git$/, '');
  expectEqual(npmRepository, manifest.source.repository, 'source.repository');

  const repository = new URL(manifest.source.repository);
  expectEqual(repository.origin, 'https://github.com', 'source.repository host');
  const repositoryPath = repository.pathname.replace(/^\//, '').replace(/\/$/, '');
  const apiPrefix = `https://api.github.com/repos/${repositoryPath}/`;
  for (const [field, url] of [
    ['source.tagRefUrl', manifest.source.tagRefUrl],
    ['source.tagObjectUrl', manifest.source.tagObjectUrl],
    ['source.commitUrl', manifest.source.commitUrl],
    ['source.protection.rulesetUrl', manifest.source.protection.rulesetUrl],
  ] as const) {
    if (!url.startsWith(apiPrefix)) {
      throw new Error(`${field} mismatch: expected URL under ${apiPrefix}, got ${url}`);
    }
  }
  expectEqual(
    publicationMetadata.time?.[manifest.package.version],
    manifest.package.publishedAt,
    'package.publishedAt'
  );
  expectEqual(metadata.dist?.tarball, manifest.artifact.url, 'artifact.url');
  expectEqual(metadata.dist?.integrity, manifest.artifact.integrity, 'artifact.integrity');
  expectEqual(metadata.dist?.shasum, manifest.artifact.sha1, 'artifact.sha1 metadata');
  expectEqual(metadata.dist?.fileCount, manifest.artifact.fileCount, 'artifact.fileCount');
  expectEqual(metadata.dist?.unpackedSize, manifest.artifact.unpackedSize, 'artifact.unpackedSize');

  const signatures = Array.isArray(metadata.dist?.signatures) ? metadata.dist.signatures : [];
  expectEqual(
    signatures[0]?.keyid,
    manifest.artifact.npmSignature.keyId,
    'artifact.npmSignature.keyId'
  );
  expectEqual(
    signatures[0]?.sig,
    manifest.artifact.npmSignature.signature,
    'artifact.npmSignature.signature'
  );

  const artifact = new Uint8Array(await artifactResponse.arrayBuffer());
  expectEqual(artifact.byteLength, manifest.artifact.size, 'artifact.size');
  expectEqual(digest('sha1', artifact), manifest.artifact.sha1, 'artifact.sha1 bytes');
  expectEqual(digest('sha256', artifact), manifest.artifact.sha256, 'artifact.sha256 bytes');
  expectEqual(digest('sha512', artifact), manifest.artifact.sha512, 'artifact.sha512 bytes');
  expectEqual(
    `sha512-${digest('sha512', artifact, 'base64')}`,
    manifest.artifact.integrity,
    'artifact SRI'
  );

  expectEqual(tagRef.object?.type, 'tag', 'source tag ref type');
  expectEqual(tagRef.object?.sha, manifest.source.tagObject, 'source tag ref');
  expectEqual(tagObject.sha, manifest.source.tagObject, 'source tag object');
  expectEqual(tagObject.tag, manifest.source.tag, 'source tag name');
  expectEqual(tagObject.object?.type, 'commit', 'source tag target type');
  expectEqual(tagObject.object?.sha, manifest.source.commit, 'source tag target');
  expectEqual(
    tagObject.verification?.verified,
    manifest.source.tagSigned,
    'source tag signature status'
  );
  expectEqual(commit.sha, manifest.source.commit, 'source commit');
  expectEqual(commit.tree?.sha, manifest.source.tree, 'source tree');

  expectEqual(ruleset.id, manifest.source.protection.rulesetId, 'source protection ruleset ID');
  expectEqual(
    ruleset.name,
    manifest.source.protection.rulesetName,
    'source protection ruleset name'
  );
  expectEqual(ruleset.target, 'tag', 'source protection target');
  expectEqual(ruleset.enforcement, 'active', 'source protection enforcement');
  expectEqual(
    JSON.stringify(ruleset.conditions?.ref_name?.include),
    JSON.stringify([`refs/tags/${manifest.source.tag}`]),
    'source protection include'
  );
  expectEqual(
    JSON.stringify(ruleset.conditions?.ref_name?.exclude),
    '[]',
    'source protection exclude'
  );
  const ruleTypes = Array.isArray(ruleset.rules)
    ? ruleset.rules.map((rule: JsonObject) => rule.type)
    : [];
  for (const requiredRule of ['update', 'deletion']) {
    if (!ruleTypes.includes(requiredRule)) {
      throw new Error(`source protection rules mismatch: missing ${requiredRule}`);
    }
  }
  expectEqual(JSON.stringify(ruleset.bypass_actors), '[]', 'source protection bypass actors');
  expectEqual(ruleset.current_user_can_bypass, 'never', 'source protection current-user bypass');
}

function getGitHubToken(): string {
  const environmentToken = process.env.GITHUB_TOKEN || process.env.GH_TOKEN;
  if (environmentToken) return environmentToken;

  try {
    return execFileSync('gh', ['auth', 'token'], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
  } catch {
    throw new Error(
      'GitHub authentication is required to verify that the protected tag has no bypass actors; set GITHUB_TOKEN/GH_TOKEN or run gh auth login'
    );
  }
}

async function main(): Promise<void> {
  const manifest = manifestJson as OracleManifest;
  await verifyBaseline(manifest, fetch, getGitHubToken());
  console.log(`Verified ${manifest.package.name}@${manifest.package.version}`);
  console.log(`  artifact sha512: ${manifest.artifact.sha512}`);
  console.log(
    `  ${manifest.source.tag}: ${manifest.source.tagObject} -> ${manifest.source.commit}`
  );
}

const entrypoint = process.argv[1];
if (entrypoint && import.meta.url === pathToFileURL(resolve(entrypoint)).href) {
  main().catch((error: unknown) => {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  });
}

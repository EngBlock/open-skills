import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import recordedManifestJson from '../compatibility/npm-0.1.2/oracle.json' with { type: 'json' };
import { verifyBaseline, type OracleManifest } from '../scripts/verify-native-baseline.ts';

const recordedManifest = recordedManifestJson as OracleManifest;

function hash(algorithm: string, data: Uint8Array, encoding: 'hex' | 'base64' = 'hex'): string {
  return createHash(algorithm).update(data).digest(encoding);
}

function fixture(): {
  manifest: OracleManifest;
  fetcher: typeof fetch;
  artifact: Uint8Array;
} {
  const artifact = new TextEncoder().encode('frozen npm oracle');
  const manifest = structuredClone(recordedManifest);
  manifest.package.metadataUrl = 'https://registry.test/version';
  manifest.package.publicationMetadataUrl = 'https://registry.test/package';
  manifest.artifact.url = 'https://registry.test/artifact.tgz';
  manifest.artifact.sha1 = hash('sha1', artifact);
  manifest.artifact.sha256 = hash('sha256', artifact);
  manifest.artifact.sha512 = hash('sha512', artifact);
  manifest.artifact.integrity = `sha512-${hash('sha512', artifact, 'base64')}`;
  manifest.artifact.size = artifact.byteLength;
  manifest.source.tagRefUrl =
    'https://api.github.com/repos/EngBlock/open-skills/git/refs/tags/v0.1.2';
  manifest.source.tagObjectUrl =
    'https://api.github.com/repos/EngBlock/open-skills/git/tags/test-tag-object';
  manifest.source.commitUrl =
    'https://api.github.com/repos/EngBlock/open-skills/git/commits/test-commit';
  manifest.source.protection.rulesetUrl =
    'https://api.github.com/repos/EngBlock/open-skills/rulesets/test-ruleset';

  const json = (body: unknown): Response =>
    new Response(JSON.stringify(body), {
      headers: { 'content-type': 'application/json' },
    });

  const responses = new Map<string, () => Response>([
    [
      manifest.package.metadataUrl,
      () =>
        json({
          name: manifest.package.name,
          version: manifest.package.version,
          repository: {
            url: 'git+https://github.com/EngBlock/open-skills.git',
          },
          dist: {
            tarball: manifest.artifact.url,
            integrity: manifest.artifact.integrity,
            shasum: manifest.artifact.sha1,
            fileCount: manifest.artifact.fileCount,
            unpackedSize: manifest.artifact.unpackedSize,
            signatures: [
              {
                keyid: manifest.artifact.npmSignature.keyId,
                sig: manifest.artifact.npmSignature.signature,
              },
            ],
          },
        }),
    ],
    [
      manifest.package.publicationMetadataUrl,
      () => json({ time: { [manifest.package.version]: manifest.package.publishedAt } }),
    ],
    [
      manifest.source.tagRefUrl,
      () => json({ object: { type: 'tag', sha: manifest.source.tagObject } }),
    ],
    [
      manifest.source.tagObjectUrl,
      () =>
        json({
          sha: manifest.source.tagObject,
          tag: manifest.source.tag,
          object: { type: 'commit', sha: manifest.source.commit },
          verification: { verified: manifest.source.tagSigned },
        }),
    ],
    [
      manifest.source.commitUrl,
      () => json({ sha: manifest.source.commit, tree: { sha: manifest.source.tree } }),
    ],
    [
      manifest.source.protection.rulesetUrl,
      () =>
        json({
          id: manifest.source.protection.rulesetId,
          name: manifest.source.protection.rulesetName,
          target: 'tag',
          enforcement: 'active',
          conditions: {
            ref_name: { include: [`refs/tags/${manifest.source.tag}`], exclude: [] },
          },
          rules: [{ type: 'update' }, { type: 'deletion' }],
          bypass_actors: [],
          current_user_can_bypass: 'never',
        }),
    ],
    [manifest.artifact.url, () => new Response(artifact)],
  ]);

  const fetcher = (async (input: string | URL | Request) => {
    const url = input instanceof Request ? input.url : input.toString();
    return responses.get(url)?.() ?? new Response('not found', { status: 404 });
  }) as typeof fetch;

  return { manifest, fetcher, artifact };
}

function withJsonMutation(
  fetcher: typeof fetch,
  targetUrl: string,
  mutate: (body: Record<string, any>) => void
): typeof fetch {
  return (async (input: string | URL | Request, init?: RequestInit) => {
    const url = input instanceof Request ? input.url : input.toString();
    if (url !== targetUrl) return fetcher(input, init);

    const response = await fetcher(input, init);
    const body = (await response.json()) as Record<string, any>;
    mutate(body);
    return new Response(JSON.stringify(body), {
      headers: { 'content-type': 'application/json' },
    });
  }) as typeof fetch;
}

describe('native compatibility baseline', () => {
  it('pins npm 0.1.2 to exact artifact and Git object identities', () => {
    expect(recordedManifest).toMatchObject({
      schemaVersion: 1,
      package: {
        name: '@engblock/open-skills',
        version: '0.1.2',
        publishedAt: '2026-07-22T14:13:45.715Z',
      },
      artifact: {
        integrity:
          'sha512-TTD/WemKLYiem5bM+vEtuxXORSZExvP4wdDxCELP1xpSqlSnEzMa2xe61JO7xBMFEbSrtBqEyys+cHVEDyCMFg==',
        sha1: '159fb3c760ea72b731674c6a93059a37040a0c1f',
        sha256: '2871993290bb28ae40d3a1c59f64b1e29564de4145a62bd3c9bdf7b85aef39c9',
      },
      source: {
        repository: 'https://github.com/EngBlock/open-skills',
        tag: 'v0.1.2',
        tagObject: 'b3117c12d841b5fdfc3c2fead72c39d01e148ab2',
        commit: 'a91eb79d035d7a33300d2cc60b18db3f81a94621',
        tree: 'f766eaf80048c8f5232eaa981bfd1fa45485fc70',
        protection: {
          rulesetId: 19578936,
          rulesetName: 'Protect npm 0.1.2 compatibility baseline',
        },
      },
    });
  });

  it('verifies registry metadata, artifact bytes, and the annotated tag chain', async () => {
    const { manifest, fetcher } = fixture();
    await expect(verifyBaseline(manifest, fetcher)).resolves.toBeUndefined();
  });

  it.each([
    {
      name: 'registry metadata',
      url: (manifest: OracleManifest) => manifest.package.metadataUrl,
      mutate: (body: Record<string, any>) => {
        body.dist.integrity = 'sha512-drifted';
      },
      error: 'artifact.integrity mismatch',
    },
    {
      name: 'source repository metadata',
      url: (manifest: OracleManifest) => manifest.package.metadataUrl,
      mutate: (body: Record<string, any>) => {
        body.repository.url = 'git+https://github.com/other/repository.git';
      },
      error: 'source.repository mismatch',
    },
    {
      name: 'tag ref',
      url: (manifest: OracleManifest) => manifest.source.tagRefUrl,
      mutate: (body: Record<string, any>) => {
        body.object.sha = 'drifted';
      },
      error: 'source tag ref mismatch',
    },
    {
      name: 'tag target',
      url: (manifest: OracleManifest) => manifest.source.tagObjectUrl,
      mutate: (body: Record<string, any>) => {
        body.object.sha = 'drifted';
      },
      error: 'source tag target mismatch',
    },
    {
      name: 'commit',
      url: (manifest: OracleManifest) => manifest.source.commitUrl,
      mutate: (body: Record<string, any>) => {
        body.sha = 'drifted';
      },
      error: 'source commit mismatch',
    },
    {
      name: 'tree',
      url: (manifest: OracleManifest) => manifest.source.commitUrl,
      mutate: (body: Record<string, any>) => {
        body.tree.sha = 'drifted';
      },
      error: 'source tree mismatch',
    },
    {
      name: 'tag protection rules',
      url: (manifest: OracleManifest) => manifest.source.protection.rulesetUrl,
      mutate: (body: Record<string, any>) => {
        body.rules = [{ type: 'update' }];
      },
      error: 'source protection rules mismatch: missing deletion',
    },
    {
      name: 'tag protection exclusions',
      url: (manifest: OracleManifest) => manifest.source.protection.rulesetUrl,
      mutate: (body: Record<string, any>) => {
        body.conditions.ref_name.exclude = ['refs/tags/v0.1.2'];
      },
      error: 'source protection exclude mismatch',
    },
    {
      name: 'missing tag protection bypass data',
      url: (manifest: OracleManifest) => manifest.source.protection.rulesetUrl,
      mutate: (body: Record<string, any>) => {
        delete body.bypass_actors;
      },
      error: 'source protection bypass actors mismatch',
    },
    {
      name: 'tag protection bypass permission',
      url: (manifest: OracleManifest) => manifest.source.protection.rulesetUrl,
      mutate: (body: Record<string, any>) => {
        body.current_user_can_bypass = 'always';
      },
      error: 'source protection current-user bypass mismatch',
    },
  ])('fails closed when $name drifts', async ({ url, mutate, error }) => {
    const { manifest, fetcher } = fixture();
    const changedFetcher = withJsonMutation(fetcher, url(manifest), mutate);
    await expect(verifyBaseline(manifest, changedFetcher)).rejects.toThrow(error);
  });

  it('fails closed when a pinned resource is unavailable', async () => {
    const { manifest, fetcher } = fixture();
    const unavailableFetcher = (async (input: string | URL | Request, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (url === manifest.source.tagRefUrl) {
        return new Response('unavailable', { status: 503, statusText: 'Unavailable' });
      }
      return fetcher(input, init);
    }) as typeof fetch;

    await expect(verifyBaseline(manifest, unavailableFetcher)).rejects.toThrow(
      `GET ${manifest.source.tagRefUrl} failed: 503 Unavailable`
    );
  });

  it('fails closed when artifact bytes differ', async () => {
    const { manifest, fetcher, artifact } = fixture();
    const changed = artifact.slice();
    changed[0] = changed[0]! ^ 1;
    const tamperedFetcher = (async (input: string | URL | Request, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (url === manifest.artifact.url) return new Response(changed);
      return fetcher(input, init);
    }) as typeof fetch;

    await expect(verifyBaseline(manifest, tamperedFetcher)).rejects.toThrow(
      'artifact.sha1 bytes mismatch'
    );
  });
});

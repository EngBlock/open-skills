import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';

type Step = {
  env?: Record<string, string>;
  id?: string;
  name?: string;
  run?: string;
  uses?: string;
  with?: Record<string, string>;
};

const rootDir = join(import.meta.dirname, '..');
const workflowsDir = join(rootDir, '.github/workflows');
const workflow = parse(readFileSync(join(workflowsDir, 'native-preview.yml'), 'utf-8'));
const workflows = readdirSync(workflowsDir)
  .filter((filename) => filename.endsWith('.yml') || filename.endsWith('.yaml'))
  .map((filename) => parse(readFileSync(join(workflowsDir, filename), 'utf-8')));
const buildJob = workflow.jobs.build;
const buildSteps = buildJob.steps as Step[];
const releaseJob = workflow.jobs.release;
const releaseSteps = releaseJob.steps as Step[];
const homebrewJob = workflow.jobs.homebrew;
const homebrewSteps = homebrewJob?.steps as Step[] | undefined;

describe('native preview release workflow', () => {
  it('runs only for canonical prerelease tags with release signing permissions', () => {
    expect(workflow.on).toEqual({ push: { tags: ['v0.2.0-*'] } });
    expect(workflow.permissions).toEqual({
      contents: 'read',
      'id-token': 'write',
      attestations: 'write',
    });
    expect(buildJob['runs-on']).toBe('ubuntu-latest');
    expect(buildSteps[0]?.name).toBe('Checkout signed tag');
    expect(buildSteps[1]?.name).toBe('Verify immutable signed tag');
    expect(buildSteps[1]?.run).toBe('scripts/verify-native-release-tag.sh');
  });

  it('pins every repository workflow action to an immutable commit', () => {
    const actionSteps = workflows.flatMap((candidate) =>
      Object.values(candidate.jobs).flatMap((job) => (job as { steps?: Step[] }).steps ?? [])
    );
    for (const step of actionSteps.filter((candidate) => candidate.uses)) {
      expect(step.uses, step.name).toMatch(/^[^@]+@[0-9a-f]{40}$/);
    }
  });

  it('builds from the tag and produces checksums, signatures, and provenance before publishing', () => {
    const packageStep = buildSteps.find((step) => step.name?.startsWith('Build CGO-disabled'));
    const attestStep = buildSteps.find((step) => step.name === 'Attest archive build provenance');
    const signStep = buildSteps.find(
      (step) => step.name === 'Attach provenance and keyless signatures'
    );
    const verifyStep = buildSteps.find((step) => step.name?.startsWith('Verify canonical targets'));
    const publishStep = releaseSteps.find((step) =>
      step.name?.startsWith('Revalidate tag and publish')
    );

    expect(packageStep?.env?.CGO_ENABLED).toBe('0');
    expect(packageStep?.run).toContain('go run ./internal/release/cmd/native-preview');
    expect(packageStep?.run).toContain('--version "${GITHUB_REF_NAME#v}"');
    expect(packageStep?.run).toContain('--output native-dist');
    expect(packageStep?.run).toContain('--homebrew-formula homebrew/open-skills.rb');
    expect(packageStep?.run).not.toContain('--skip-linux-smoke');
    expect(attestStep?.with?.['subject-checksums']).toBe('native-dist/checksums.txt');
    expect(signStep?.run).toContain('cosign sign-blob --yes --bundle');
    expect(signStep?.run).toContain('native-dist/provenance.sigstore.json');
    expect(verifyStep?.run).toContain('go run ./internal/release/cmd/verify-native-release');
    expect(verifyStep?.run).toContain('sha256sum --check checksums.txt');
    expect(verifyStep?.run).toContain('cosign verify-blob');
    expect(verifyStep?.run).toContain('gh attestation verify');
    expect(verifyStep?.run).toContain('--signer-workflow "${workflow}"');
    expect(verifyStep?.run).toContain('@refs/tags/${GITHUB_REF_NAME}');
    expect(publishStep?.run).toContain('scripts/verify-native-release-tag.sh');
    expect(publishStep?.run).toContain('gh release create "${GITHUB_REF_NAME}"');
    expect(publishStep?.run).toContain('native-dist/*');
    expect(publishStep?.run).toContain('--prerelease');
    expect(publishStep?.run).toContain('--verify-tag');
    expect(publishStep?.run).toContain('--target "${GITHUB_SHA}"');
  });

  it('validates and smoke-tests the checked Homebrew formula on macOS ARM64 before publication', () => {
    const verifyFormulaStep = buildSteps.find(
      (step) => step.name === 'Verify checked Homebrew formula'
    );
    const uploadStep = buildSteps.find(
      (step) => step.name === 'Stage verified release and Homebrew smoke inputs'
    );
    const architectureStep = homebrewSteps?.find((step) => step.name === 'Require macOS ARM64');
    const smokeStep = homebrewSteps?.find(
      (step) => step.name === 'Smoke-test Homebrew install and upgrade'
    );

    expect(verifyFormulaStep?.run).toBe('cmp Formula/open-skills.rb homebrew/open-skills.rb');
    expect(uploadStep?.uses).toMatch(/^actions\/upload-artifact@[0-9a-f]{40}$/);
    expect(uploadStep?.with?.path).toContain('native-dist');
    expect(uploadStep?.with?.path).toContain('homebrew/open-skills.rb');
    expect(homebrewJob?.needs).toBe('build');
    expect(homebrewJob?.['runs-on']).toBe('macos-15');
    expect(architectureStep?.run).toBe('test "$(uname -m)" = arm64');
    expect(smokeStep?.run).toContain('OPEN_SKILLS_HOMEBREW_ARTIFACT="$artifact"');
    expect(smokeStep?.run).toContain('scripts/homebrew-smoke.sh homebrew/open-skills.rb');
    expect(releaseJob?.needs).toEqual(['build', 'homebrew']);
  });
});

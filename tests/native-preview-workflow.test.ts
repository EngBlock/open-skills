import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';

type Step = {
  env?: Record<string, string>;
  name?: string;
  run?: string;
  uses?: string;
};

const rootDir = join(import.meta.dirname, '..');
const workflow = parse(
  readFileSync(join(rootDir, '.github/workflows/native-preview.yml'), 'utf-8')
);
const releaseJob = workflow.jobs.release;
const steps = releaseJob.steps as Step[];

describe('native preview release workflow', () => {
  it('is an explicit prerelease operation with release permissions', () => {
    expect(workflow.on.workflow_dispatch.inputs.version).toMatchObject({
      required: true,
      type: 'string',
    });
    expect(workflow.permissions).toEqual({ contents: 'write' });
    expect(releaseJob['runs-on']).toBe('ubuntu-latest');
  });

  it('uses the tested packager and publishes all generated archives as a GitHub prerelease', () => {
    const packageStep = steps.find((step) => step.name?.startsWith('Build CGO-disabled'));
    const publishStep = steps.find((step) => step.name === 'Publish GitHub prerelease');

    expect(packageStep?.env?.CGO_ENABLED).toBe('0');
    expect(packageStep?.run).toContain('go run ./internal/release/cmd/native-preview');
    expect(packageStep?.run).toContain('--output native-dist');
    expect(packageStep?.run).toContain('--notes native-preview-notes.md');
    expect(publishStep?.run).toContain('gh release create');
    expect(publishStep?.run).toContain('native-dist/*');
    expect(publishStep?.run).toContain('--prerelease');
    expect(publishStep?.run).toContain('--notes-file native-preview-notes.md');
  });
});

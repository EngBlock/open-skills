import { describe, it, expect } from 'vitest';
import { execSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { stripTerminalEscapes } from '../src/sanitize.ts';

const rootDir = join(import.meta.dirname, '..');

describe('dist build', () => {
  it('builds and runs without errors', { timeout: 30000 }, () => {
    // Build the project
    execSync('pnpm build', { cwd: rootDir, stdio: 'pipe' });

    // Run the CLI - should exit cleanly with help output
    const result = execSync('node bin/cli.mjs --help', {
      cwd: rootDir,
      stdio: 'pipe',
      encoding: 'utf-8',
    });

    expect(stripTerminalEscapes(result)).toContain('Usage: open-skills <command> [options]');

    const dependencyArtifacts = [
      'package.json',
      'pnpm-lock.yaml',
      'dist/cli.mjs',
      'dist/THIRD-PARTY-LICENSES.md',
      'ThirdPartyNoticeText.txt',
    ].map((path) => readFileSync(join(rootDir, path), 'utf-8'));

    const artifacts = dependencyArtifacts.join('\n');
    expect(artifacts).toContain('Open Skills CLI ThirdPartyNotices');
    expect(artifacts).not.toContain('@vercel/detect-agent');
  });
});

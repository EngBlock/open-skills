import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { stripTerminalEscapes } from '../src/sanitize.ts';

const rootDir = join(import.meta.dirname, '..');
const packageJson = JSON.parse(readFileSync(join(rootDir, 'package.json'), 'utf-8'));

describe('@engblock/open-skills package identity', () => {
  it('publishes the independent package metadata', () => {
    expect(packageJson).toMatchObject({
      name: '@engblock/open-skills',
      publishConfig: {
        access: 'public',
      },
      version: '0.1.1',
      author: 'Nathan Beddoe',
      repository: {
        type: 'git',
        url: 'git+https://github.com/EngBlock/open-skills.git',
      },
      homepage: 'https://github.com/EngBlock/open-skills#readme',
      bugs: {
        url: 'https://github.com/EngBlock/open-skills/issues',
      },
    });
  });

  it('packs the release files under the open-skills identity', () => {
    const packOutput = execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], {
      cwd: rootDir,
      encoding: 'utf-8',
    });
    const [packed] = JSON.parse(packOutput);
    const files = packed.files.map((file: { path: string }) => file.path);
    const notices = readFileSync(join(rootDir, 'ThirdPartyNoticeText.txt'), 'utf-8');

    expect(packed.name).toBe('@engblock/open-skills');
    expect(packed.version).toBe('0.1.1');
    expect(files).toEqual(
      expect.arrayContaining([
        'bin/cli.mjs',
        'dist/cli.mjs',
        'README.md',
        'ThirdPartyNoticeText.txt',
      ])
    );
    expect(notices).toContain('Open Skills CLI ThirdPartyNotices');
    expect(notices).not.toContain('@vercel/detect-agent');
  });

  it('provides compatible open-skills, skills, and add-skill executables', () => {
    expect(packageJson.bin).toEqual({
      'open-skills': 'bin/cli.mjs',
      skills: 'bin/cli.mjs',
      'add-skill': 'bin/cli.mjs',
    });

    for (const executable of ['open-skills', 'skills', 'add-skill']) {
      const binPath = join(rootDir, packageJson.bin[executable]);
      const help = execFileSync(process.execPath, [binPath, '--help'], {
        cwd: rootDir,
        encoding: 'utf-8',
      });
      const version = execFileSync(process.execPath, [binPath, '--version'], {
        cwd: rootDir,
        encoding: 'utf-8',
      });

      expect(stripTerminalEscapes(help)).toContain('Usage: open-skills <command> [options]');
      expect(version.trim()).toBe('0.1.1');
    }
  });
});

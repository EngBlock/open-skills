import { describe, it, expect } from 'vitest';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { runCli, runCliOutput, stripLogo, hasLogo } from './test-utils.ts';

const FAIL_ON_FETCH =
  '--import=data:text/javascript,globalThis.fetch%3D()%3D%3E%7Bprocess.stdout.write(%22UNEXPECTED_FETCH%5Cn%22)%3Bthrow%20new%20Error(%22fetch%20disabled%22)%7D';

function expectSuccessfulWithoutFetch(result: ReturnType<typeof runCli>): void {
  expect(result.exitCode).toBe(0);
  expect(result.stdout).not.toContain('UNEXPECTED_FETCH');
}

function withTemporaryDirectory(prefix: string, test: (directory: string) => void): void {
  const directory = mkdtempSync(join(tmpdir(), prefix));
  try {
    test(directory);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

describe('skills CLI', () => {
  describe('--help', () => {
    it('should display help message', () => {
      const output = runCliOutput(['--help']);
      expect(output).toContain('Usage: open-skills <command> [options]');
      expect(output).toContain('npx open-skills add NathanBeddoeWebDev/open-skills');
      expect(output).not.toContain('npx skills');
      expect(output).not.toContain('vercel-labs');
      expect(output).toContain('Manage Skills:');
      expect(output).toContain('init [name]');
      expect(output).toContain('add <package>');
      expect(output).toContain('use <package>@<skill>');
      expect(output).toContain('update');
      expect(output).toContain('Add Options:');
      expect(output).toContain('Use Options:');
      expect(output).toContain('-g, --global');
      expect(output).toContain('-a, --agent');
      expect(output).toContain('-s, --skill');
      expect(output).toContain('-l, --list');
      expect(output).toContain('-y, --yes');
      expect(output).toContain('--all');
      expect(output).not.toContain('--metadata');
      expect(output).toContain(
        'find, search, f, s  Show migration guidance for decentralized discovery'
      );
      expect(output).not.toContain('Find Options:');
      expect(output).not.toContain('Search for skills');
      expect(output).not.toContain('interactive search');
      expect(output).not.toContain('search by keyword');
      expect(output).not.toContain('skills.sh');
    });

    it('should show same output for -h alias', () => {
      const helpOutput = runCliOutput(['--help']);
      const hOutput = runCliOutput(['-h']);
      expect(hOutput).toBe(helpOutput);
    });
  });

  describe('--version', () => {
    it('should display version number', () => {
      const output = runCliOutput(['--version']);
      expect(output.trim()).toMatch(/^\d+\.\d+\.\d+$/);
    });

    it('should match package.json version', () => {
      const output = runCliOutput(['--version']);
      const pkg = JSON.parse(
        readFileSync(join(import.meta.dirname, '..', 'package.json'), 'utf-8')
      );
      expect(output.trim()).toBe(pkg.version);
    });
  });

  describe('no arguments', () => {
    it('should display banner', () => {
      const result = runCli([]);
      const output = stripLogo(result.stdout);
      expect(output).toContain('The open agent skills ecosystem');
      expect(output).toContain('npx open-skills add');
      expect(output).toContain('npx open-skills use');
      expect(output).toContain('npx open-skills update');
      expect(output).toContain('npx open-skills init');
      expect(output).toContain('NathanBeddoeWebDev/open-skills');
      expect(output).not.toContain('npx skills');
      expect(output).not.toContain('Search for skills');
      expect(output).not.toContain('skills.sh');
    });
  });

  describe('offline local commands', () => {
    it.each([
      ['help', ['--help']],
      ['version', ['--version']],
    ])('runs %s without fetching', (_label, args) => {
      expectSuccessfulWithoutFetch(runCli(args, undefined, { NODE_OPTIONS: FAIL_ON_FETCH }));
    });

    it('initializes a skill without fetching', () => {
      withTemporaryDirectory('skills-offline-init-', (cwd) => {
        const result = runCli(['init', 'demo'], cwd, { NODE_OPTIONS: FAIL_ON_FETCH });

        expectSuccessfulWithoutFetch(result);
        expect(result.stdout).not.toContain('skills.sh');
        expect(readFileSync(join(cwd, 'demo', 'SKILL.md'), 'utf-8')).toContain('name: demo');
      });
    });

    it('lists local skills without fetching', () => {
      withTemporaryDirectory('skills-offline-list-', (cwd) => {
        expectSuccessfulWithoutFetch(runCli(['list'], cwd, { NODE_OPTIONS: FAIL_ON_FETCH }));
      });
    });

    it('installs a local source without fetching', () => {
      withTemporaryDirectory('skills-offline-add-', (base) => {
        const source = join(base, 'source');
        const cwd = join(base, 'project');
        mkdirSync(source, { recursive: true });
        mkdirSync(cwd, { recursive: true });
        writeFileSync(join(source, 'SKILL.md'), '---\nname: demo\ndescription: Demo\n---\n');

        const result = runCli(['add', source, '--agent', 'universal', '--copy', '--yes'], cwd, {
          NODE_OPTIONS: FAIL_ON_FETCH,
        });

        expectSuccessfulWithoutFetch(result);
        expect(readFileSync(join(cwd, '.agents', 'skills', 'demo', 'SKILL.md'), 'utf-8')).toContain(
          'name: demo'
        );
      });
    });

    it('syncs a local node_modules skill without fetching', () => {
      withTemporaryDirectory('skills-offline-sync-', (cwd) => {
        const skillDir = join(cwd, 'node_modules', 'demo-package');
        mkdirSync(skillDir, { recursive: true });
        writeFileSync(join(skillDir, 'SKILL.md'), '---\nname: demo\ndescription: Demo\n---\n');

        const result = runCli(['experimental_sync', '--agent', 'universal', '--yes'], cwd, {
          NODE_OPTIONS: FAIL_ON_FETCH,
        });
        expectSuccessfulWithoutFetch(result);
      });
    });

    it('updates an empty local installation without fetching', () => {
      withTemporaryDirectory('skills-offline-update-', (cwd) => {
        const result = runCli(['update', '--yes'], cwd, { NODE_OPTIONS: FAIL_ON_FETCH });
        expectSuccessfulWithoutFetch(result);
      });
    });

    it('removes a local skill without fetching', () => {
      withTemporaryDirectory('skills-offline-remove-', (cwd) => {
        const skillDir = join(cwd, '.agents', 'skills', 'demo');
        mkdirSync(skillDir, { recursive: true });
        writeFileSync(join(skillDir, 'SKILL.md'), '---\nname: demo\ndescription: Demo\n---\n');

        const result = runCli(['remove', 'demo', '--yes'], cwd, {
          NODE_OPTIONS: FAIL_ON_FETCH,
        });
        expectSuccessfulWithoutFetch(result);
      });
    });
  });

  describe('legacy find commands', () => {
    const migrationGuidance = `Hosted skill search is no longer available.\nDiscover skills by searching GitHub and the web for SKILL.md files, then install one with:\n  npx open-skills add <owner>/<repo>@<skill>`;

    it.each(['find', 'search', 'f', 's'])(
      '%s returns stable offline migration guidance',
      (command) => {
        const result = runCli([command, 'react', '--owner', 'example'], undefined, {
          NODE_OPTIONS: FAIL_ON_FETCH,
        });

        expect(result.exitCode).toBe(1);
        expect(result.stdout.trim()).toBe(migrationGuidance);
        expect(result.stdout).not.toContain('UNEXPECTED_FETCH');
        expect(result.stderr).toBe('');
      }
    );

    it.each(['find', 'search', 'f', 's'])(
      '%s handles legacy help flags consistently',
      (command) => {
        const result = runCli([command, '--help'], undefined, { NODE_OPTIONS: FAIL_ON_FETCH });

        expect(result.exitCode).toBe(1);
        expect(result.stdout.trim()).toBe(migrationGuidance);
        expect(result.stdout).not.toContain('UNEXPECTED_FETCH');
        expect(result.stderr).toBe('');
      }
    );
  });

  describe('removed options', () => {
    it('rejects the removed telemetry metadata option as unknown', () => {
      const result = runCli(['add', 'local-skill', '--metadata', '{}']);

      expect(result.exitCode).toBe(1);
      expect(result.stderr).toContain('Error: Unknown option: --metadata');
    });
  });

  describe('unknown command', () => {
    it('should show error for unknown command', () => {
      const output = runCliOutput(['unknown-command']);
      expect(output).toMatchInlineSnapshot(`
        "Unknown command: unknown-command
        Run open-skills --help for usage.
        "
      `);
    });

    it('should exit with code 1 for unknown command', () => {
      const result = runCli(['unknown-command']);
      expect(result.exitCode).toBe(1);
    });

    it('should exit with code 0 for top-level --help', () => {
      const result = runCli(['--help']);
      expect(result.exitCode).toBe(0);
    });

    it('should exit with code 0 for --version', () => {
      const result = runCli(['--version']);
      expect(result.exitCode).toBe(0);
    });
  });

  describe('subcommand --help', () => {
    // Each subcommand invoked with --help/-h must short-circuit to help output
    // before the subcommand handler runs, so no side effects (network calls
    // or lock-file writes) can happen.
    const cases: Array<[string, string]> = [
      ['add --help routes to top-level help', 'add'],
      ['update --help routes to top-level help', 'update'],
      ['check --help routes to top-level help', 'check'],
      ['list --help routes to top-level help', 'list'],
      ['init --help routes to top-level help', 'init'],
      ['experimental_install --help routes to top-level help', 'experimental_install'],
      ['experimental_sync --help routes to top-level help', 'experimental_sync'],
    ];

    for (const [label, command] of cases) {
      it(label, () => {
        const result = runCli([command, '--help']);
        expect(result.exitCode).toBe(0);
        expect(result.stdout).toContain('Usage: open-skills <command> [options]');
      });

      it(`${label} (-h alias)`, () => {
        const result = runCli([command, '-h']);
        expect(result.exitCode).toBe(0);
        expect(result.stdout).toContain('Usage: open-skills <command> [options]');
      });
    }

    it('remove --help routes to remove-specific help', () => {
      const result = runCli(['remove', '--help']);
      expect(result.exitCode).toBe(0);
      // remove has its own help screen distinct from the top-level usage banner
      expect(result.stdout).toContain('open-skills remove');
      expect(result.stdout).not.toContain('skills.sh');
    });

    it('update --help does not run the update flow', () => {
      const result = runCli(['update', '--help']);
      expect(result.exitCode).toBe(0);
      // The update flow prints this banner; it must not appear when --help is
      // passed, otherwise the side-effecting check is being executed.
      expect(result.stdout).not.toContain('Checking for skill updates');
      expect(result.stderr).not.toContain('Checking for skill updates');
    });
  });

  describe('logo display', () => {
    it('should not display logo for list command', () => {
      const output = runCliOutput(['list']);
      expect(hasLogo(output)).toBe(false);
    });

    it('should not display logo for check command', () => {
      // Note: check command makes GitHub API calls, so we just verify initial output
      const output = runCliOutput(['check']);
      expect(hasLogo(output)).toBe(false);
    }, 60000);

    it('should not display logo for update command', () => {
      // Note: update command makes GitHub API calls, so we just verify initial output
      const output = runCliOutput(['update']);
      expect(hasLogo(output)).toBe(false);
    }, 60000);
  });
});

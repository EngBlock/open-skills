import { describe, it, expect } from 'vitest';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { runCli, runCliOutput, stripLogo, hasLogo } from './test-utils.ts';

const FAIL_ON_FETCH =
  '--import=data:text/javascript,globalThis.fetch%3D()%3D%3E%7Bprocess.stdout.write(%22UNEXPECTED_FETCH%5Cn%22)%3Bthrow%20new%20Error(%22fetch%20disabled%22)%7D';
const RECORD_EMPTY_SEARCH_FETCHES =
  '--import=data:text/javascript,globalThis.fetch%3D(u)%3D%3E%7Bprocess.stdout.write(%22FETCH%20%22%2Bu%2B%22%5Cn%22)%3Breturn%20Promise.resolve(%7Bok%3Atrue%2Cjson%3Aasync()%3D%3E(%7Bskills%3A%5B%5D%7D)%7D)%7D';

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
      expect(output).toContain('Usage: skills <command> [options]');
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
      expect(output).toContain('npx skills add');
      expect(output).toContain('npx skills use');
      expect(output).toContain('npx skills update');
      expect(output).toContain('npx skills init');
      expect(output).toContain('skills.sh');
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

  describe('remote commands', () => {
    it('sends only the requested search fetch for find', () => {
      const result = runCli(['find', 'react'], undefined, {
        NODE_OPTIONS: RECORD_EMPTY_SEARCH_FETCHES,
        SKILLS_API_URL: 'https://search.example.test',
      });
      const fetches = result.stdout.split('\n').filter((line) => line.startsWith('FETCH '));

      expect(result.exitCode).toBe(0);
      expect(fetches).toEqual(['FETCH https://search.example.test/api/search?q=react&limit=10']);
    });
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
        Run skills --help for usage.
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
      ['find --help routes to top-level help', 'find'],
      ['experimental_install --help routes to top-level help', 'experimental_install'],
      ['experimental_sync --help routes to top-level help', 'experimental_sync'],
    ];

    for (const [label, command] of cases) {
      it(label, () => {
        const result = runCli([command, '--help']);
        expect(result.exitCode).toBe(0);
        expect(result.stdout).toContain('Usage: skills <command> [options]');
      });

      it(`${label} (-h alias)`, () => {
        const result = runCli([command, '-h']);
        expect(result.exitCode).toBe(0);
        expect(result.stdout).toContain('Usage: skills <command> [options]');
      });
    }

    it('remove --help routes to remove-specific help', () => {
      const result = runCli(['remove', '--help']);
      expect(result.exitCode).toBe(0);
      // remove has its own help screen distinct from the top-level usage banner
      expect(result.stdout).toContain('skills remove');
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

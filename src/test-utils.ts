import { execFileSync } from 'child_process';
import { mkdtempSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { stripTerminalEscapes } from './sanitize.ts';

// const PROJECT_ROOT = join(import.meta.dirname, '..');
const CLI_PATH = join(import.meta.dirname, 'cli.ts');

export function stripAnsi(str: string): string {
  return stripTerminalEscapes(str);
}

export function stripLogo(str: string): string {
  return str
    .split('\n')
    .filter((line) => !line.includes('███') && !line.includes('╔') && !line.includes('╚'))
    .join('\n')
    .replace(/^\n+/, '');
}

export function hasLogo(str: string): boolean {
  return str.includes('███') || str.includes('╔') || str.includes('╚');
}

export function createTestHomeEnvironment(home: string): Record<string, string> {
  return {
    HOME: home,
    USERPROFILE: home,
    XDG_CONFIG_HOME: join(home, '.config'),
    XDG_DATA_HOME: join(home, '.local', 'share'),
    XDG_STATE_HOME: join(home, '.local', 'state'),
    XDG_CACHE_HOME: join(home, '.cache'),
    APPDATA: join(home, 'AppData', 'Roaming'),
    LOCALAPPDATA: join(home, 'AppData', 'Local'),
    CODEX_HOME: join(home, '.codex'),
    CLAUDE_CONFIG_DIR: join(home, '.claude'),
    VIBE_HOME: join(home, '.vibe'),
    HERMES_HOME: join(home, '.hermes'),
    AUTOHAND_HOME: join(home, '.autohand'),
    FLATPAK_XDG_CONFIG_HOME: join(home, '.var', 'app'),
    DISABLE_TELEMETRY: '1',
  };
}

function createIsolatedTestEnvironment(overrides?: Record<string, string>): {
  env: NodeJS.ProcessEnv;
  temporaryHome: string;
} {
  const temporaryHome = mkdtempSync(join(tmpdir(), 'skills-cli-test-home-'));
  const home = overrides?.HOME || overrides?.USERPROFILE || temporaryHome;

  return {
    temporaryHome,
    env: {
      ...process.env,
      ...createTestHomeEnvironment(home),
      ...overrides,
    },
  };
}

export function runCli(
  args: string[],
  cwd?: string,
  env?: Record<string, string>,
  timeout?: number
): { stdout: string; stderr: string; exitCode: number } {
  const { env: testEnv, temporaryHome } = createIsolatedTestEnvironment(env);

  try {
    const output = execFileSync(process.execPath, [CLI_PATH, ...args], {
      encoding: 'utf-8',
      cwd,
      stdio: ['pipe', 'pipe', 'pipe'],
      env: testEnv,
      timeout: timeout ?? 30000,
    });
    return { stdout: stripAnsi(output), stderr: '', exitCode: 0 };
  } catch (error: any) {
    return {
      stdout: stripAnsi(error.stdout || ''),
      stderr: stripAnsi(error.stderr || ''),
      exitCode: error.status || 1,
    };
  } finally {
    rmSync(temporaryHome, { recursive: true, force: true });
  }
}

export function runCliOutput(args: string[], cwd?: string): string {
  const result = runCli(args, cwd);
  return result.stdout || result.stderr;
}

export function runCliWithInput(
  args: string[],
  input: string,
  cwd?: string
): { stdout: string; stderr: string; exitCode: number } {
  const { env, temporaryHome } = createIsolatedTestEnvironment();

  try {
    const output = execFileSync(process.execPath, [CLI_PATH, ...args], {
      encoding: 'utf-8',
      cwd,
      input: input + '\n',
      stdio: ['pipe', 'pipe', 'pipe'],
      env,
    });
    return { stdout: stripAnsi(output), stderr: '', exitCode: 0 };
  } catch (error: any) {
    return {
      stdout: stripAnsi(error.stdout || ''),
      stderr: stripAnsi(error.stderr || ''),
      exitCode: error.status || 1,
    };
  } finally {
    rmSync(temporaryHome, { recursive: true, force: true });
  }
}

import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const rootDir = join(import.meta.dirname, '..');
const formula = readFileSync(join(rootDir, 'Formula/open-skills.rb'), 'utf8');
const readme = readFileSync(join(rootDir, 'README.md'), 'utf8');
const smokeScript = join(rootDir, 'scripts/homebrew-smoke.sh');

function formulaValue(name: string): string {
  const match = formula.match(new RegExp(`^  ${name} "([^"]+)"$`, 'm'));
  if (!match) throw new Error(`missing ${name} in Homebrew formula`);
  return match[1];
}

describe('supported Homebrew distribution', () => {
  it('pins the macOS ARM64 package to one checksummed canonical release archive', () => {
    const version = formulaValue('version');
    const expectedUrl = `https://github.com/EngBlock/open-skills/releases/download/v${version}/open-skills_${version}_darwin_arm64.tar.gz`;

    expect(formulaValue('url')).toBe(expectedUrl);
    expect(formulaValue('sha256')).toMatch(/^[0-9a-f]{64}$/);
    expect(formula).toContain('depends_on :macos');
    expect(formula).toContain('depends_on arch: :arm64');
    expect(formula.toLowerCase()).not.toMatch(/npm|node|go build/);
  });

  it('installs and tests only the open-skills executable', () => {
    expect(formula.match(/bin\.install /g)).toEqual(['bin.install ']);
    expect(formula).toContain('bin.install "open-skills"');
    expect(formula).not.toMatch(/bin\.install "(?:skills|add-skill)"/);
    expect(formula).toContain('shell_output("#{bin}/open-skills --version")');
    expect(formula).toContain('shell_output("#{bin}/open-skills --help")');
    if (process.platform !== 'win32') {
      expect(statSync(smokeScript).mode & 0o111).not.toBe(0);
    }
  });

  it('documents Homebrew install and upgrade without curl-pipe-shell', () => {
    expect(readme).toContain(
      'brew tap EngBlock/open-skills https://github.com/EngBlock/open-skills'
    );
    expect(readme).toContain('brew install EngBlock/open-skills/open-skills');
    expect(readme).toContain('brew upgrade EngBlock/open-skills/open-skills');
    expect(readme).not.toMatch(/curl[^\n|]*\|[^\n]*(?:sh|bash)/i);
  });
});

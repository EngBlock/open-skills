import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const readme = readFileSync(join(import.meta.dirname, '..', 'README.md'), 'utf-8');

describe('README identity', () => {
  it('documents open-skills as the preferred independent CLI', () => {
    expect(readme).toMatch(/^# open-skills$/m);
    expect(readme).toContain(
      '[![CI](https://github.com/NathanBeddoeWebDev/open-skills/actions/workflows/ci.yml/badge.svg)](https://github.com/NathanBeddoeWebDev/open-skills/actions/workflows/ci.yml)'
    );
    expect(readme).toContain(
      '[![npm version](https://img.shields.io/npm/v/@engblock/open-skills.svg)](https://www.npmjs.com/package/@engblock/open-skills)'
    );
    expect(readme).toContain(
      'npx @engblock/open-skills add NathanBeddoeWebDev/open-skills@find-skills'
    );
    expect(readme).not.toContain('npx skills');
  });

  it('documents decentralized discovery and preserves fork attribution', () => {
    expect(readme).toContain('Hosted skill search has been removed.');
    expect(readme).toContain(
      'Discover skills by searching GitHub and the web for relevant `SKILL.md` files'
    );
    expect(readme).toContain(
      'Open Skills originated as a fork of [vercel-labs/skills](https://github.com/vercel-labs/skills).'
    );
    expect(readme).toMatch(/## License\n\nMIT/);
  });
});

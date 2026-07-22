import { describe, expect, it } from 'vitest';
import { readFileSync } from 'fs';
import { join } from 'path';

const findSkillsMarkdown = readFileSync(
  join(import.meta.dirname, '..', 'skills', 'find-skills', 'SKILL.md'),
  'utf-8'
);

describe('bundled find-skills guidance', () => {
  it('describes a decentralized, evidence-based recommendation workflow', () => {
    expect(findSkillsMarkdown).toMatch(/search GitHub and the web/i);
    expect(findSkillsMarkdown).toMatch(/GitHub CLI[^\n]*optional/i);
    expect(findSkillsMarkdown).toMatch(/inspect[^\n]*SKILL\.md/i);

    for (const signal of [
      'relevance',
      'maintainer reputation',
      'maintenance activity',
      'license',
      'documentation',
    ]) {
      expect(findSkillsMarkdown.toLowerCase()).toContain(signal);
    }

    expect(findSkillsMarkdown).toMatch(/stars and forks[^\n]*secondary/i);
    expect(findSkillsMarkdown).toMatch(/(?:not|never)[^\n]*proof of (?:safety|quality)/i);
  });
});

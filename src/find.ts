import { CLI_RUN_COMMAND } from './constants.ts';

const MIGRATION_GUIDANCE = `Hosted skill search is no longer available.
Discover skills by searching GitHub and the web for SKILL.md files, then install one with:
  ${CLI_RUN_COMMAND} add <owner>/<repo>@<skill>`;

export function showFindMigrationGuidance(): void {
  console.log(MIGRATION_GUIDANCE);
  process.exitCode = 1;
}

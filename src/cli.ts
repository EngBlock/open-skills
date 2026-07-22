#!/usr/bin/env node

import { writeFileSync, readFileSync, existsSync, mkdirSync } from 'fs';
import { basename, join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { runAdd, parseAddOptions } from './add.ts';
import { showFindMigrationGuidance } from './find.ts';
import { runInstallFromLock } from './install.ts';
import { runList } from './list.ts';
import { removeCommand, parseRemoveOptions } from './remove.ts';
import { runSync, parseSyncOptions } from './sync.ts';
import { isRunningInAgent } from './detect-agent.ts';
import { runUpdate } from './update.ts';
import { runUse, parseUseOptions } from './use.ts';
import { CLI_NAME, CLI_RUN_COMMAND, PROJECT_REPOSITORY } from './constants.ts';

const __dirname = dirname(fileURLToPath(import.meta.url));

function getVersion(): string {
  try {
    const pkgPath = join(__dirname, '..', 'package.json');
    const pkg = JSON.parse(readFileSync(pkgPath, 'utf-8'));
    return pkg.version;
  } catch {
    return '0.0.0';
  }
}

const VERSION = getVersion();
const LEGACY_FIND_COMMANDS = new Set(['find', 'search', 'f', 's']);

const RESET = '\x1b[0m';
const BOLD = '\x1b[1m';
// 256-color grays - visible on both light and dark backgrounds
const DIM = '\x1b[38;5;102m'; // darker gray for secondary text
const TEXT = '\x1b[38;5;145m'; // lighter gray for primary text

const LOGO_LINES = [
  '███████╗██╗  ██╗██╗██╗     ██╗     ███████╗',
  '██╔════╝██║ ██╔╝██║██║     ██║     ██╔════╝',
  '███████╗█████╔╝ ██║██║     ██║     ███████╗',
  '╚════██║██╔═██╗ ██║██║     ██║     ╚════██║',
  '███████║██║  ██╗██║███████╗███████╗███████║',
  '╚══════╝╚═╝  ╚═╝╚═╝╚══════╝╚══════╝╚══════╝',
];

// 256-color middle grays - visible on both light and dark backgrounds
const GRAYS = [
  '\x1b[38;5;250m', // lighter gray
  '\x1b[38;5;248m',
  '\x1b[38;5;245m', // mid gray
  '\x1b[38;5;243m',
  '\x1b[38;5;240m',
  '\x1b[38;5;238m', // darker gray
];

function showLogo(): void {
  console.log();
  LOGO_LINES.forEach((line, i) => {
    console.log(`${GRAYS[i]}${line}${RESET}`);
  });
}

function showBanner(): void {
  showLogo();
  console.log();
  console.log(`${DIM}The open agent skills ecosystem${RESET}`);
  console.log();
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} add ${DIM}<package>${RESET}        ${DIM}Add a new skill${RESET}`
  );
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} use ${DIM}<package>@<skill>${RESET} ${DIM}Use a skill without installing${RESET}`
  );
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} remove${RESET}               ${DIM}Remove installed skills${RESET}`
  );
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} list${RESET}                 ${DIM}List installed skills${RESET}`
  );
  console.log();
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} update${RESET}               ${DIM}Update installed skills${RESET}`
  );
  console.log();
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} experimental_install${RESET} ${DIM}Restore from skills-lock.json${RESET}`
  );
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} init ${DIM}[name]${RESET}          ${DIM}Create a new skill${RESET}`
  );
  console.log(
    `  ${DIM}$${RESET} ${TEXT}${CLI_RUN_COMMAND} experimental_sync${RESET}    ${DIM}Sync skills from node_modules${RESET}`
  );
  console.log();
  console.log(`${DIM}try:${RESET} ${CLI_RUN_COMMAND} add ${PROJECT_REPOSITORY}@find-skills`);
  console.log();
}

function showHelp(): void {
  console.log(`
${BOLD}Usage:${RESET} ${CLI_NAME} <command> [options]

${BOLD}Manage Skills:${RESET}
  add <package>        Add a skill package (alias: a)
                       e.g. ${PROJECT_REPOSITORY}
                            https://github.com/${PROJECT_REPOSITORY}
  use <package>@<skill>
                       Generate a prompt for using one skill without installing it
  remove [skills]      Remove installed skills
  list, ls             List installed skills
  find, search, f, s  Show migration guidance for decentralized discovery

${BOLD}Updates:${RESET}
  update [skills...]   Update skills to latest versions (alias: upgrade)

${BOLD}Update Options:${RESET}
  -g, --global           Update global skills only
  -p, --project          Update project skills only
  -y, --yes              Skip scope prompt (auto-detect: project if in a project, else global)

${BOLD}Project:${RESET}
  experimental_install Restore skills from skills-lock.json
  init [name]          Initialize a skill (creates <name>/SKILL.md or ./SKILL.md)
  experimental_sync    Sync skills from node_modules into agent directories

${BOLD}Add Options:${RESET}
  -g, --global           Install skill globally (user-level) instead of project-level
  -a, --agent <agents>   Specify agents to install to (use '*' for all agents)
  -s, --skill <skills>   Specify skill names to install (use '*' for all skills)
  -l, --list             List available skills in the repository without installing
  -y, --yes              Skip confirmation prompts
  --copy                 Copy files instead of symlinking to agent directories
  --subagent <names>     Install to Eve subagents (use 'root' for the root agent)
  --all                  Shorthand for --skill '*' --agent '*' -y
  --full-depth           Search all subdirectories even when a root SKILL.md exists

${BOLD}Use Options:${RESET}
  -s, --skill <skill>    Specify the skill to use
  -a, --agent <agent>    Start one supported agent interactively
  --full-depth           Search all subdirectories even when a root SKILL.md exists
  --dangerously-accept-openclaw-risks
                         Allow unverified OpenClaw community skills

${BOLD}Remove Options:${RESET}
  -g, --global           Remove from global scope
  -a, --agent <agents>   Remove from specific agents (use '*' for all agents)
  -s, --skill <skills>   Specify skills to remove (use '*' for all skills)
  -y, --yes              Skip confirmation prompts
  --all                  Shorthand for --skill '*' --agent '*' -y
  
${BOLD}Experimental Sync Options:${RESET}
  -a, --agent <agents>   Specify agents to install to (use '*' for all agents)
  -y, --yes              Skip confirmation prompts

${BOLD}List Options:${RESET}
  -g, --global           List global skills (default: project)
  -a, --agent <agents>   Filter by specific agents
  --json                 Output as JSON (machine-readable, no ANSI codes)

${BOLD}Options:${RESET}
  --help, -h        Show this help message
  --version, -v     Show version number

${BOLD}Examples:${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} add ${PROJECT_REPOSITORY}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} use ${PROJECT_REPOSITORY}@find-skills | claude
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} use ${PROJECT_REPOSITORY} --skill find-skills --agent claude-code
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} add ${PROJECT_REPOSITORY} -g
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} add ${PROJECT_REPOSITORY} --agent claude-code cursor
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} add ${PROJECT_REPOSITORY} --skill find-skills
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove                        ${DIM}# interactive remove${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove web-design             ${DIM}# remove by name${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} rm --global frontend-design
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} list                          ${DIM}# list project skills${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} ls -g                         ${DIM}# list global skills${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} ls -a claude-code             ${DIM}# filter by agent${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} ls --json                      ${DIM}# JSON output${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} update
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} update my-skill             ${DIM}# update a single skill${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} update -g                    ${DIM}# update global skills only${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} experimental_install            ${DIM}# restore from skills-lock.json${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} init my-skill
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} experimental_sync              ${DIM}# sync from node_modules${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} experimental_sync -y           ${DIM}# sync without prompts${RESET}
`);
}

function showRemoveHelp(): void {
  console.log(`
${BOLD}Usage:${RESET} ${CLI_NAME} remove [skills...] [options]

${BOLD}Description:${RESET}
  Remove installed skills from agents. If no skill names are provided,
  an interactive selection menu will be shown.

${BOLD}Arguments:${RESET}
  skills            Optional skill names to remove (space-separated)

${BOLD}Options:${RESET}
  -g, --global       Remove from global scope (~/) instead of project scope
  -a, --agent        Remove from specific agents (use '*' for all agents)
  -s, --skill        Specify skills to remove (use '*' for all skills)
  -y, --yes          Skip confirmation prompts
  --all              Shorthand for --skill '*' --agent '*' -y

${BOLD}Examples:${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove                           ${DIM}# interactive selection${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove my-skill                   ${DIM}# remove specific skill${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove skill1 skill2 -y           ${DIM}# remove multiple skills${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove --global my-skill          ${DIM}# remove from global scope${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} rm --agent claude-code my-skill   ${DIM}# remove from specific agent${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove --all                      ${DIM}# remove all skills${RESET}
  ${DIM}$${RESET} ${CLI_RUN_COMMAND} remove --skill '*' -a cursor      ${DIM}# remove all skills from cursor${RESET}
`);
}

function runInit(args: string[]): void {
  const cwd = process.cwd();
  const skillName = args[0] || basename(cwd);
  const hasName = args[0] !== undefined;

  const skillDir = hasName ? join(cwd, skillName) : cwd;
  const skillFile = join(skillDir, 'SKILL.md');
  const displayPath = hasName ? `${skillName}/SKILL.md` : 'SKILL.md';

  if (existsSync(skillFile)) {
    console.log(`${TEXT}Skill already exists at ${DIM}${displayPath}${RESET}`);
    return;
  }

  if (hasName) {
    mkdirSync(skillDir, { recursive: true });
  }

  const skillContent = `---
name: ${skillName}
description: A brief description of what this skill does
---

# ${skillName}

Instructions for the agent to follow when this skill is activated.

## When to use

Describe when this skill should be used.

## Instructions

1. First step
2. Second step
3. Additional steps as needed
`;

  writeFileSync(skillFile, skillContent);

  console.log(`${TEXT}Initialized skill: ${DIM}${skillName}${RESET}`);
  console.log();
  console.log(`${DIM}Created:${RESET}`);
  console.log(`  ${displayPath}`);
  console.log();
  console.log(`${DIM}Next steps:${RESET}`);
  console.log(`  1. Edit ${TEXT}${displayPath}${RESET} to define your skill instructions`);
  console.log(
    `  2. Update the ${TEXT}name${RESET} and ${TEXT}description${RESET} in the frontmatter`
  );
  console.log();
  console.log(`${DIM}Publishing:${RESET}`);
  console.log(
    `  ${DIM}GitHub:${RESET}  Push to a repo, then ${TEXT}${CLI_RUN_COMMAND} add <owner>/<repo>${RESET}`
  );
  console.log(
    `  ${DIM}URL:${RESET}     Host the file, then ${TEXT}${CLI_RUN_COMMAND} add https://example.com/${displayPath}${RESET}`
  );
  console.log();
}

// ============================================
// Main
// ============================================

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const inAgent = await isRunningInAgent();

  if (args.length === 0) {
    if (!inAgent) {
      showBanner();
    }
    return;
  }

  const command = args[0]!;
  const restArgs = args.slice(1);

  if (LEGACY_FIND_COMMANDS.has(command)) {
    showFindMigrationGuidance();
    return;
  }

  // Subcommand --help / -h must short-circuit before dispatch so that running
  // e.g. `skills update --help` prints help instead of executing the update
  // flow. Without this pre-check, every subcommand handler that doesn't
  // inspect `--help` itself ends up running its side-effecting work.
  if (
    command !== '--help' &&
    command !== '-h' &&
    command !== '--version' &&
    command !== '-v' &&
    (restArgs.includes('--help') || restArgs.includes('-h'))
  ) {
    if (command === 'remove' || command === 'rm' || command === 'r') {
      showRemoveHelp();
    } else {
      showHelp();
    }
    return;
  }

  switch (command) {
    case 'init':
      if (!inAgent) showLogo();
      console.log();
      runInit(restArgs);
      break;
    case 'experimental_install': {
      if (!inAgent) showLogo();
      await runInstallFromLock(restArgs);
      break;
    }
    case 'i':
    case 'install':
    case 'a':
    case 'add': {
      if (!inAgent) showLogo();
      const { source: addSource, options: addOpts, errors } = parseAddOptions(restArgs);
      if (errors.length > 0) {
        for (const error of errors) console.error(`Error: ${error}`);
        process.exitCode = 1;
        break;
      }
      await runAdd(addSource, addOpts);
      break;
    }
    case 'use': {
      const {
        source: useSource,
        options: useOptions,
        errors: useErrors,
      } = parseUseOptions(restArgs);
      await runUse(useSource, useOptions, useErrors);
      break;
    }
    case 'remove':
    case 'rm':
    case 'r': {
      const { skills, options: removeOptions } = parseRemoveOptions(restArgs);
      await removeCommand(skills, removeOptions);
      break;
    }
    case 'experimental_sync': {
      if (!inAgent) showLogo();
      const { options: syncOptions } = parseSyncOptions(restArgs);
      await runSync(restArgs, syncOptions);
      break;
    }
    case 'list':
    case 'ls':
      await runList(restArgs);
      break;
    case 'check':
    case 'update':
    case 'upgrade':
      await runUpdate(restArgs);
      break;
    case '--help':
    case '-h':
      showHelp();
      break;
    case '--version':
    case '-v':
      console.log(VERSION);
      break;

    default:
      console.log(`Unknown command: ${command}`);
      console.log(`Run ${BOLD}${CLI_NAME} --help${RESET} for usage.`);
      process.exitCode = 1;
  }
}

void main();

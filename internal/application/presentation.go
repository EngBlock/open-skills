package application

const logo = `
███████╗██╗  ██╗██╗██╗     ██╗     ███████╗
██╔════╝██║ ██╔╝██║██║     ██║     ██╔════╝
███████╗█████╔╝ ██║██║     ██║     ███████╗
╚════██║██╔═██╗ ██║██║     ██║     ╚════██║
███████║██║  ██╗██║███████╗███████╗███████║
╚══════╝╚═╝  ╚═╝╚═╝╚══════╝╚══════╝╚══════╝
`

const banner = logo + `
The open agent skills ecosystem

  $ open-skills add <package>        Add a new skill
  $ open-skills use <package>@<skill> Use a skill without installing
  $ open-skills remove               Remove installed skills
  $ open-skills list                 List installed skills

  $ open-skills update               Update installed skills

  $ open-skills experimental_install Restore from skills-lock.json
  $ open-skills init [name]          Create a new skill
  $ open-skills experimental_sync    Sync skills from node_modules

try: open-skills add EngBlock/open-skills@find-skills

`

const help = `
Usage: open-skills <command> [options]

Manage Skills:
  add <package>        Add a skill package (alias: a)
                       e.g. EngBlock/open-skills
                            https://github.com/EngBlock/open-skills
  use <package>@<skill>
                       Generate a prompt for using one skill without installing it
  remove [skills]      Remove installed skills
  list, ls             List installed skills
  trust                Audit and revoke remote instruction trust
  find, search, f, s  Show migration guidance for decentralized discovery

Updates:
  check [skills...]    Inspect available skill updates
  update [skills...]   Apply available skill updates (alias: upgrade)

Update Options:
  -g, --global           Update global skills only
  -p, --project          Update project skills only
  -y, --yes              Skip scope prompt (auto-detect: project if in a project, else global)

Project:
  experimental_install Restore skills from skills-lock.json
  init [name]          Initialize a skill (creates <name>/SKILL.md or ./SKILL.md)
  experimental_sync    Sync skills from node_modules into agent directories

Add Options:
  -g, --global           Install skill globally (user-level) instead of project-level
  -a, --agent <agents>   Specify agents to install to (use '*' for all agents)
  -s, --skill <skills>   Specify skill names to install (use '*' for all skills)
  --skill-path <paths>   Select exact repository-relative skill directories
  -l, --list             List available skills in the repository without installing
  -y, --yes              Skip confirmation prompts
  --copy                 Copy files instead of symlinking to agent directories
  --subagent <names>     Install to Eve subagents (use 'root' for the root agent)
  --all                  Shorthand for --skill '*' --agent '*' -y
  --full-depth           Search all subdirectories even when a root SKILL.md exists

Use Options:
  -s, --skill <skill>    Specify the skill to use
  --skill-path <path>    Select an exact repository-relative skill directory
  -a, --agent <agent>    Start one supported agent interactively
  --full-depth           Search all subdirectories even when a root SKILL.md exists
  --trust                Approve one exact remote source commit for agent use
  --dangerously-accept-openclaw-risks
                         Allow unverified OpenClaw community skills

Trust Commands:
  trust list [--json]
  trust revoke <source> [--commit <commit>] [--yes]
  trust clear [--yes]

Remove Options:
  -g, --global           Remove from global scope
  -a, --agent <agents>   Remove from specific agents (use '*' for all agents)
  -s, --skill <skills>   Specify skills to remove (use '*' for all skills)
  -y, --yes              Skip confirmation prompts
  --all                  Shorthand for --skill '*' --agent '*' -y
` + "  \n" + `Experimental Sync Options:
  -a, --agent <agents>   Specify agents to install to (use '*' for all agents)
  -y, --yes              Skip confirmation prompts

List Options:
  -g, --global           List global skills (default: project)
  -a, --agent <agents>   Filter by specific agents
  --json                 Output as JSON (machine-readable, no ANSI codes)

Options:
  --help, -h        Show this help message
  --version, -v     Show version number

Examples:
  $ open-skills add EngBlock/open-skills
  $ open-skills use EngBlock/open-skills@find-skills | claude
  $ open-skills use EngBlock/open-skills --skill find-skills --agent claude-code
  $ open-skills add EngBlock/open-skills -g
  $ open-skills add EngBlock/open-skills --agent claude-code cursor
  $ open-skills add EngBlock/open-skills --skill find-skills
  $ open-skills remove                        # interactive remove
  $ open-skills remove web-design             # remove by name
  $ open-skills rm --global frontend-design
  $ open-skills list                          # list project skills
  $ open-skills ls -g                         # list global skills
  $ open-skills ls -a claude-code             # filter by agent
  $ open-skills ls --json                      # JSON output
  $ open-skills check
  $ open-skills update
  $ open-skills update my-skill             # update a single skill
  $ open-skills update -g                    # update global skills only
  $ open-skills experimental_install            # restore from skills-lock.json
  $ open-skills init my-skill
  $ open-skills experimental_sync              # sync from node_modules
  $ open-skills experimental_sync -y           # sync without prompts

`

const removeHelp = `
Usage: open-skills remove [skills...] [options]

Description:
  Remove installed skills from agents. If no skill names are provided,
  an interactive selection menu will be shown.

Arguments:
  skills            Optional skill names to remove (space-separated)

Options:
  -g, --global       Remove from global scope (~/) instead of project scope
  -a, --agent        Remove from specific agents (use '*' for all agents)
  -s, --skill        Specify skills to remove (use '*' for all skills)
  -y, --yes          Skip confirmation prompts
  --all              Shorthand for --skill '*' --agent '*' -y

Examples:
  $ open-skills remove                           # interactive selection
  $ open-skills remove my-skill                   # remove specific skill
  $ open-skills remove skill1 skill2 -y           # remove multiple skills
  $ open-skills remove --global my-skill          # remove from global scope
  $ open-skills rm --agent claude-code my-skill   # remove from specific agent
  $ open-skills remove --all                      # remove all skills
  $ open-skills remove --skill '*' -a cursor      # remove all skills from cursor

`

const findMigrationGuidance = `Hosted skill search is no longer available.
Discover skills by searching GitHub and the web for SKILL.md files, then install one with:
  open-skills add <owner>/<repo>@<skill>
`

---
name: find-skills
description: Helps users discover and install agent skills when they ask questions like "how do I do X", "find a skill for X", "is there a skill that can...", or express interest in extending capabilities. This skill should be used when the user is looking for functionality that might exist as an installable skill.
---

# Find Skills

Discover agent skills from their source repositories. Search GitHub and the web, inspect each candidate, and give the user enough evidence to decide whether to install it. Do not rely on a centralized registry or treat a search result as a recommendation.

## 1. Understand the request

Identify:

- the task the skill should perform
- the user's language, framework, and tool constraints
- whether they want instructions, automation, or a reusable workflow

Turn that into a few specific search phrases. Prefer task terms such as `playwright accessibility SKILL.md` over broad categories such as `testing`.

## 2. Search GitHub and the web

Use the web-search tools available to you. Useful queries include:

- `site:github.com "SKILL.md" <task terms>`
- `site:github.com inurl:skills <task terms> agent`
- `<task terms> agent skill GitHub`

Search repository pages as well as code results. A skill might be at the repository root or in a directory such as `skills/<name>/SKILL.md`.

The GitHub CLI is an optional technique, not a requirement. If `gh` is already installed and authenticated, GitHub code search can help:

```bash
gh search code 'filename:SKILL.md "<task terms>"' --limit 20
gh search repos '<task terms> agent skills' --limit 20
```

Do not install or require `gh` just to perform discovery. Fall back to web search or the user's available search tools.

## 3. Inspect candidates

Do not recommend a candidate from its title, snippet, stars, or repository description alone.

For every candidate you might recommend:

1. Open and inspect the actual `SKILL.md` contents.
2. Follow referenced files needed to understand the workflow, such as scripts, templates, or supporting documentation.
3. Confirm that the instructions address the user's task and constraints.
4. Record the source repository and exact skill path so the user can verify it.
5. Look for suspicious, destructive, irrelevant, or unexpectedly broad instructions. Skills run with the agent's permissions, so uncertainty must be disclosed.

Discard candidates whose contents cannot be inspected or whose behavior does not match the request.

## 4. Evaluate the evidence

Evaluate each remaining candidate using all of these signals:

- **Relevance:** how directly the inspected instructions solve the user's task
- **Maintainer reputation:** whether the author or organization has a credible, verifiable history in the area
- **Maintenance activity:** recent meaningful commits, issue handling, and whether the skill still matches current tools
- **License:** whether a license exists and is compatible with the user's intended use
- **Documentation:** setup requirements, examples, limitations, and supporting files
- **Repository context:** reviewable history, focused scope, and unresolved security or maintenance concerns

Stars and forks are secondary discovery signals only. They can help locate projects worth inspecting, but they are never proof of safety or quality. A popular repository still requires content review, and a new or niche repository may be useful when its contents and provenance are clear.

## 5. Present recommendations

Present a short list, best match first. For each recommendation include:

- skill name and source repository
- exact path or link to its `SKILL.md`
- what the inspected instructions do and why they are relevant
- evidence about maintainer reputation, maintenance activity, license, and documentation
- important caveats or unresolved concerns
- an install command using the explicit source, for example:

```bash
npx open-skills add <owner>/<repository>@<skill-name>
```

If the skill is selected by a repository subpath instead, use that exact GitHub tree URL. Never invent a package name or imply that popularity provides a security review.

Ask the user before installing a recommendation. If they approve, install only the source and skill they selected.

## When no suitable skill is found

Say what you searched and why the candidates were rejected or remained unverifiable. Offer to help with the task directly. If the task recurs, suggest creating a local skill with:

```bash
npx open-skills init <skill-name>
```

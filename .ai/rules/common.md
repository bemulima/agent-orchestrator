# Common Agent Rules

These rules are shared delivery policy. Repository-local business, architecture, contract, and command rules take precedence when they are stricter.

## Source of truth and task state

- Read the current issue, `AGENTS.md`, `.ai/service.yaml`, and relevant repository documentation before changing code.
- Use the issue and its linked plan or pull/merge request as the durable task record. Do not create a parallel journal inside another repository.
- Derive runtime facts from code, manifests, migrations, and versioned contracts. Never invent endpoints, commands, dependencies, or behavior from a prompt.
- Keep canonical business rules, contracts, runbooks, and architecture decisions in the repository that owns them.

## Coding

- Follow existing project boundaries and naming. Prefer the smallest change that satisfies the acceptance criteria.
- Do not introduce a new framework, abstraction, dependency, or directory layout without repository evidence and a concrete need.
- Keep secrets out of source, logs, fixtures, prompts, and examples. Use placeholders in documented configuration.
- Use repository-local `.cache` for disposable tool output when the project has no stricter cache rule.

## Bugfix

- Reproduce or characterize the failure before changing behavior.
- Identify the root cause and affected contract or invariant; do not stop at masking the symptom.
- Add or update a regression test that fails before the fix when practical.
- Run the narrowest relevant checks first, then the repository verification required for handoff.

## Feature

- Confirm acceptance criteria, write scope, affected services, and compatibility requirements before implementation.
- Update tests and owned documentation in the same change when behavior, configuration, or a contract changes.
- For cross-repository work, split ownership by repository and make dependencies explicit in issues; do not copy canonical contracts between repositories.

## Refactor

- State the behavior that must remain unchanged and establish a passing baseline.
- Keep behavior changes separate from structural cleanup unless the issue explicitly combines them.
- Preserve public contracts, migrations, and configuration compatibility, or document and approve the breaking change.

## Review

- Review the real diff independently, with findings before summary.
- Prioritize correctness, security, data integrity, compatibility, migration reversibility, missing tests, and scope violations.
- Cite exact files and lines. Do not edit the implementation during review unless the owner asks for a fix.

## Git, issue, and pull/merge request delivery

- Use one issue, repository, worktree, branch, and agent thread per independently deliverable task.
- Inspect status and diff before committing. Preserve unrelated user changes and never rewrite shared history.
- Keep commits focused and use the repository's commit convention; use Conventional Commits only when no stricter convention exists.
- A pull/merge request must link its issue, describe scope and risk, list verification, and call out contracts, migrations, configuration, and follow-up work.
- Do not push, publish an issue or pull/merge request, merge, deploy, or perform destructive Git operations without the required owner authorization.

## Commands, tests, migrations, and contracts

- Run only commands discovered in `.ai/commands.yaml` or documented by the repository. Request approval for commands marked `requires_approval: true`.
- Treat migrations and contracts as versioned interfaces. Prefer reversible, data-preserving migrations and explicit compatibility checks.
- Verify producers and consumers before changing shared HTTP, event, database, file, or environment contracts.
- Report exactly what was and was not verified; never claim completion while required checks are failing.

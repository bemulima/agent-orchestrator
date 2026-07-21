# Repository onboarding runbook

Inventory snapshot: 2026-07-21. The three-repository read-only pilot in this
runbook was owner-approved and completed on the same date. No onboarding,
GitLab, Telegram, or Codex command has been executed.

## Safety boundary

- The host repository root is mounted into the API and worker at `/projects`.
  Set `PROJECTS_HOST_ROOT=/Users/marat/Developments` only in the ignored `.env`
  file before recreating those two services.
- Use container paths in every persisted project record. Do not connect the
  same checkout once by `/Users/...` and again by `/projects/...`.
- Connecting and discovery are read-only. Do not prepare or apply onboarding
  until the discovery report and proposed role have been reviewed.
- A dirty checkout can be discovered, but it is not a valid execution base.
  Issue worktrees are never separate projects.
- The branch/divergence observations below use local Git refs only. No fetch,
  pull, checkout, reset, clean, or file deletion was performed.

## Inventory conclusions

`microservices/*` currently contains 49 top-level directories: 35 primary Git
roots, 13 linked issue worktrees, and the non-Git parent `infra`. The nested
`infra/messaging` directory is another primary Git root, producing 36 primary
repositories in the microservices tree.

The following clean `main` checkouts are eligible for a read-only pilot now:

- `infra/messaging` (`infrastructure`);
- `ms-go-cache-search-validator`, `ms-go-docker-validator`,
  `ms-go-git-validator`, `ms-go-http-runtime-validator`,
  `ms-go-linux-validator`, `ms-go-php-framework-validator`,
  `ms-go-statistic`, `ms-py-validator`,
  `ms-ts-browser-runtime-validator`, and `ms-ts-nextjs-validator` (`service`);
- `journal` (`archive`) and `prompts` (`policy`).

The completed pilot connected `ms-go-http-runtime-validator`,
`ms-ts-nextjs-validator`, and `infra/messaging`. The remaining clean service
wave is `ms-go-cache-search-validator`, `ms-go-docker-validator`,
`ms-go-git-validator`, `ms-go-linux-validator`,
`ms-go-php-framework-validator`, `ms-go-statistic`, `ms-py-validator`, and
`ms-ts-browser-runtime-validator`.

`journal`, `prompts`, and `wiki` are not loose generated directories. They are
clean repositories with remotes and contain respectively 149, 20, and 32
tracked files with no untracked files. Keep them independent and connect them
as `archive`, `policy`, and `documentation`; do not copy their files into
service repositories. `wiki` first needs its non-default branch reviewed.

The parent `microservices/infra` is not a repository and must not be connected.
Its `messaging` child is a clean repository with ten tracked NATS configuration,
stream, script, prompt, and documentation files and should be connected using
the nested path with role `infrastructure`.

`/Users/marat/Developments/knowlege` does not exist. The likely intended path is
`/Users/marat/Developments/knowledge-tree`, but that assumption requires owner
confirmation. `knowledge-tree` belongs to role `content`; it is currently on
`fix/issue-159`, 55 commits behind the locally recorded `origin/main`, with one
modified registry file and 29 untracked `.DS_Store` files. Do not connect it
until the intended branch and dirty state are resolved.

## Deferred primary checkouts

These clean repositories are on a non-default branch or have a locally stale
default checkout. Review the intended branch and synchronize it outside the
orchestrator before connection. The values are a local-ref snapshot, not a
network freshness claim.

| Repository | Current state |
| --- | --- |
| `go-ms-ai-summary` | `feat/issue-1`; remote default not recorded locally |
| `ms-gateway` | `refactor/issue-27`; one commit behind local `origin/main` |
| `ms-go-ai-prompt` | `refactor/issue-3`; remote default not recorded locally |
| `ms-go-auth` | rename branch; one commit ahead of local `origin/main` |
| `ms-go-code-validator` | `docs/agent-prompts-5`; diverged 7 behind/1 ahead |
| `ms-go-course` | `refactor/issue-41`; 76 commits behind local `origin/main` |
| `ms-go-filestorage` | rename branch; one commit ahead |
| `ms-go-image-processor` | rename branch; one commit ahead |
| `ms-go-pet-project-orchestrator` | `refactor/issue-6`; one commit behind |
| `ms-go-php-validator` | rename branch; one commit behind |
| `ms-go-rbac` | rename branch; one commit ahead |
| `ms-go-sandbox` | rename branch; one commit ahead |
| `ms-go-student` | `refactor/issue-17`; 19 commits behind |
| `ms-go-tarantool` | rename branch; one commit ahead |
| `ms-go-user` | rename branch; one commit ahead |
| `ms-go-validation-orchestrator` | clean `main`; 16 commits behind local `origin/main` |
| `ms-node-validator` | rename branch; one commit behind |
| `ms-ts-css-validator` | rename branch; one commit ahead |
| `ms-ts-html-validator` | `fix/issue-9`; one commit ahead |
| `ms-ts-react-validator` | rename branch; one commit ahead |
| `wiki` | `agent/rename-practice-task-docs`; one commit ahead |
| `nextjs` | `refactor/issue-49`; one commit behind; role `frontend` |
| `admin-nextjs` | `fix/admin-practice-task-nav`; one commit ahead; role `frontend` |

`ms-go-db-validator` is additionally dirty because `.cache/` contains 1,793
untracked files. Discovery would exclude that cache content, but execution
would reject the checkout as a dirty base, so defer it until repository hygiene
is handled by the owner.

`course-dev-orchestrator` itself is intentionally excluded from the first
pilot. Managing the orchestrator with itself needs a separate self-management
policy and should not be mixed into the course-platform topology by default.

## Never connect as separate projects

The following 13 directories are linked worktrees of primary repositories.
Connecting them would at best resolve to the same source identity and at worst
make a temporary issue branch look like an independent service:

- `ms-go-code-validator-issue-7` and `ms-go-code-validator-issue-9`;
- `ms-go-course-issue-64`, `ms-go-course-issue-65`,
  `ms-go-course-issue-66`, and `ms-go-course-issue-68`;
- `ms-go-php-validator-issue-67-audit`;
- `ms-go-validation-orchestrator-issue-11`,
  `ms-go-validation-orchestrator-issue-13`,
  `ms-go-validation-orchestrator-issue-15`, and
  `ms-go-validation-orchestrator-issue-19`;
- `ms-node-validator-issue-7`;
- `ms-ts-browser-runtime-validator-issue-6`.

The same rule applies to the `knowledge-tree-issue-*` worktrees outside the
requested microservices tree: only the primary `knowledge-tree` checkout can
become a project.

## Recommended connection order

Steps 1–3 are complete:

1. Recreated only the API and worker with the reviewed host-root mount.
2. Ran the three-repository discovery pilot: one Go service, one TypeScript
   service, and messaging infrastructure.
3. Reviewed each discovery report and confirmed bounded inventory, roles,
   clean source state, and evidence paths. Two orchestrator defects found by
   the review were corrected and the reports were regenerated as schema v2.

Continue only with a new owner command:

4. Connect the remaining clean `main` validators.
5. After branch hygiene, connect platform anchors (`ms-gateway`, course,
   authentication, user/RBAC, storage, sandbox, student, statistic and related
   services), then rebuild topology and review contract drift.
6. Connect `nextjs` and `admin-nextjs` as `frontend` after their branch state is
   approved.
7. Connect `prompts`, `wiki`, and `journal` as non-runtime policy,
   documentation, and archive repositories. These must not become topology
   services.
8. Connect `knowledge-tree` as `content` only after the path assumption,
   branch, modified registry, and `.DS_Store` state are resolved.
9. Only after all reports are reviewed, prepare onboarding proposals one
   repository at a time. Proposal approval and apply remain separate commands.

## Reviewed commands

First edit the ignored `.env` file manually:

```dotenv
PROJECTS_HOST_ROOT=/Users/marat/Developments
REPOSITORY_ALLOWED_ROOTS=/projects
```

Then expose the root to the already configured stack. This does not connect a
project or write a project row:

```sh
docker compose up -d --build --force-recreate orchestrator worker
docker compose exec -T orchestrator /app/course-dev-orchestrator config-check
docker compose exec -T orchestrator git -C /projects/microservices/ms-go-http-runtime-validator status --short
docker compose exec -T orchestrator git -C /projects/microservices/ms-ts-nextjs-validator status --short
docker compose exec -T orchestrator git -C /projects/microservices/infra/messaging status --short
```

The first read-only pilot is intentionally small:

```sh
docker compose exec -T orchestrator /app/course-dev-orchestrator project-connect --path /projects/microservices/ms-go-http-runtime-validator --role service
docker compose exec -T orchestrator /app/course-dev-orchestrator project-connect --path /projects/microservices/ms-ts-nextjs-validator --role service
docker compose exec -T orchestrator /app/course-dev-orchestrator project-connect --path /projects/microservices/infra/messaging --role infrastructure

docker compose exec -T orchestrator /app/course-dev-orchestrator project-list
docker compose exec -T orchestrator /app/course-dev-orchestrator project-report --service ms-go-http-runtime-validator
docker compose exec -T orchestrator /app/course-dev-orchestrator project-report --service ms-ts-nextjs-validator
docker compose exec -T orchestrator /app/course-dev-orchestrator project-report --service ms-infra-messaging
docker compose exec -T orchestrator /app/course-dev-orchestrator topology
docker compose exec -T orchestrator /app/course-dev-orchestrator contract-drift
```

Pilot result after corrections:

- all three projects are `analyzed`, on clean `main` checkouts, with unchanged
  source HEADs;
- name-based `project-report` and `project-scan` work for all three projects;
- discovery schema v2 removed the false `server.py`, `workspace.root_path`, and
  `nextjs.app` event capabilities;
- repeated scans reused snapshot version 2, and repeated topology rebuilds
  reused the corrected revision;
- the topology has three services, nine capabilities, two HTTP contracts, no
  relations, and no contract drift; the seven remaining event capabilities all
  originate from `infra/messaging/streams/*.json`.

Stop after the reports if a role, branch, dirty flag, source path, ownership,
contract, or evidence path is unexpected. Do not continue by guessing.

After an individual report is accepted, onboarding remains proposal-first:

```sh
docker compose exec -T orchestrator /app/course-dev-orchestrator project-onboard --service SERVICE
docker compose exec -T orchestrator /app/course-dev-orchestrator project-diff --run-id RUN_ID
docker compose exec -T orchestrator /app/course-dev-orchestrator project-apply --run-id RUN_ID --dry-run
docker compose exec -T orchestrator /app/course-dev-orchestrator project-approve --run-id RUN_ID --actor owner --comment "reviewed proposal"
docker compose exec -T orchestrator /app/course-dev-orchestrator project-apply --run-id RUN_ID
```

The approval and final apply commands are examples only. They must not be run
in bulk and are not authorized merely by completing the discovery pilot.

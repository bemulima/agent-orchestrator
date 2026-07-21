# Repository onboarding runbook

Inventory snapshot: 2026-07-21, last operational update: 2026-07-22. The
three-repository pilot, eight-repository validator wave, and two-repository
non-runtime wave in this runbook were owner-approved and completed read-only.
No onboarding, GitLab, Telegram, or Codex command has been executed.

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
- The initial branch observations used local Git refs. The 2026-07-21
  follow-up audit queried live default/branch refs with `git ls-remote` and
  used the read-only GitHub compare API where the default commit was absent
  from the local object database. No fetch, pull, checkout, reset, clean, or
  file deletion was performed.

## Inventory conclusions

`microservices/*` currently contains 49 top-level directories: 35 primary Git
roots, 13 linked issue worktrees, and the non-Git parent `infra`. The nested
`infra/messaging` directory is another primary Git root, producing 36 primary
repositories in the microservices tree.

The following clean `main` checkouts have already been connected read-only:

- `infra/messaging` (`infrastructure`);
- `ms-go-cache-search-validator`, `ms-go-docker-validator`,
  `ms-go-git-validator`, `ms-go-http-runtime-validator`,
  `ms-go-linux-validator`, `ms-go-php-framework-validator`,
  `ms-go-statistic`, `ms-py-validator`,
  `ms-ts-browser-runtime-validator`, and `ms-ts-nextjs-validator` (`service`);
- `journal` (`archive`) and `prompts` (`policy`).

No additional repository currently satisfies all connection preconditions.
Every remaining candidate needs an owner decision about branch/checkout
hygiene first.

The completed first pilot connected `ms-go-http-runtime-validator`,
`ms-ts-nextjs-validator`, and `infra/messaging`. The completed second wave
connected `ms-go-cache-search-validator`, `ms-go-docker-validator`,
`ms-go-git-validator`, `ms-go-linux-validator`,
`ms-go-php-framework-validator`, `ms-go-statistic`, `ms-py-validator`, and
`ms-ts-browser-runtime-validator`.
The completed third wave connected local `prompts` and `journal` using their
canonical remote names `ms-course-promts` and `ms-course-journal`.

`journal`, `prompts`, and `wiki` are not loose generated directories. They are
clean repositories with remotes and contain respectively 149, 20, and 32
tracked files with no untracked files. Keep them independent and connect them
as `archive`, `policy`, and `documentation`; do not copy their files into
service repositories. `wiki` remains deferred: its current non-default branch
is one commit behind the live remote `main`.

The parent `microservices/infra` is not a repository and must not be connected.
Its `messaging` child is a clean repository with ten tracked NATS configuration,
stream, script, prompt, and documentation files and should be connected using
the nested path with role `infrastructure`.

`/Users/marat/Developments/knowlege` does not exist. The likely intended path is
`/Users/marat/Developments/knowledge-tree`, but that assumption requires owner
confirmation. `knowledge-tree` belongs to role `content`; it is currently on
`fix/issue-159`, 55 commits behind the live remote `main`, and that current
branch is absent from the remote. It also has one modified registry file and
29 untracked `.DS_Store` files. Do not connect it until the intended path,
branch, and dirty state are resolved.

## Deferred primary checkouts

These repositories are on a non-default branch, are behind/diverged from the
live remote default, or are dirty. Review the intended branch and synchronize
them outside the orchestrator before connection. Ahead/behind values below
compare the local checkout HEAD to the live remote `main` without fetching.

| Repository | Current state |
| --- | --- |
| `go-ms-ai-summary` | `feat/issue-1`; 5 ahead; branch exists remotely |
| `ms-gateway` | `refactor/issue-27`; 1 behind; branch exists remotely |
| `ms-go-ai-prompt` | `refactor/issue-3`; 1 behind; branch exists remotely |
| `ms-go-auth` | rename branch; 1 behind; branch absent remotely |
| `ms-go-code-validator` | `docs/agent-prompts-5`; diverged 11 behind/1 ahead; branch absent remotely |
| `ms-go-course` | `refactor/issue-41`; 76 behind; branch exists remotely |
| `ms-go-filestorage` | rename branch; 1 behind; branch absent remotely |
| `ms-go-image-processor` | rename branch; 1 behind; branch absent remotely |
| `ms-go-pet-project-orchestrator` | `refactor/issue-6`; 1 behind; branch exists remotely |
| `ms-go-php-validator` | rename branch; 1 behind; branch absent remotely |
| `ms-go-rbac` | rename branch; 1 behind; branch absent remotely |
| `ms-go-sandbox` | rename branch; 1 behind; branch absent remotely |
| `ms-go-student` | `refactor/issue-17`; 19 behind; branch exists remotely |
| `ms-go-tarantool` | rename branch; 1 behind; branch absent remotely |
| `ms-go-user` | rename branch; 1 behind; branch absent remotely |
| `ms-go-validation-orchestrator` | `main`; 16 behind the live remote `main` |
| `ms-node-validator` | rename branch; 5 behind; branch absent remotely |
| `ms-ts-css-validator` | rename branch; 1 behind; branch absent remotely |
| `ms-ts-html-validator` | `fix/issue-9`; 1 ahead; branch exists remotely |
| `ms-ts-react-validator` | rename branch; 1 behind; branch absent remotely |
| `wiki` | `agent/rename-practice-task-docs`; 1 behind; branch exists remotely |
| `nextjs` | `refactor/issue-49`; 1 behind; branch exists remotely; role `frontend` |
| `admin-nextjs` | `fix/admin-practice-task-nav`; 1 behind; branch exists remotely; role `frontend` |

`ms-go-db-validator` is on `docs/agent-prompts-3`, diverged 1 behind/1 ahead
from the live remote `main`, and that branch is absent remotely. It is also
dirty because `.cache/` contains 1,793 untracked files. Discovery would exclude
that cache content, but execution would reject the checkout as a dirty base,
so defer it until repository and branch hygiene are handled by the owner.

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

Steps 1–5 are complete:

1. Recreated only the API and worker with the reviewed host-root mount.
2. Ran the three-repository discovery pilot: one Go service, one TypeScript
   service, and messaging infrastructure.
3. Reviewed each pilot report and confirmed bounded inventory, roles, clean
   source state, and evidence paths. Two orchestrator defects found by the
   review were corrected.
4. Connected and reviewed the remaining eight clean `main` validators. Their
   Go/Python route evidence exposed two additional discovery gaps; the reports
   were regenerated as schema v4 and verified idempotent.
5. Connected `prompts` and `journal` as non-runtime `policy` and `archive`
   repositories. Discovery schema v6 suppresses runtime evidence from their
   documentation/examples, records root policy Markdown by checksum, and
   leaves runtime topology at 11 services.

The follow-up branch-hygiene audit is also complete. Continue only with a new
owner command:

6. After branch hygiene, connect platform anchors (`ms-gateway`, course,
   authentication, user/RBAC, storage, sandbox, student and related services),
   then rebuild topology and review contract drift. `ms-go-statistic` is
   already connected.
7. Connect `nextjs` and `admin-nextjs` as `frontend` after their branch state is
   approved.
8. Connect `wiki` as `documentation` after its branch state is approved. It
   must not become a topology service.
9. Connect `knowledge-tree` as `content` only after the path assumption,
   branch, modified registry, and `.DS_Store` state are resolved.
10. Only after all reports are reviewed, prepare onboarding proposals one
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

The second wave used the same read-only command boundary:

```sh
validators=(
  ms-go-cache-search-validator
  ms-go-docker-validator
  ms-go-git-validator
  ms-go-linux-validator
  ms-go-php-framework-validator
  ms-go-statistic
  ms-py-validator
  ms-ts-browser-runtime-validator
)
for validator in "${validators[@]}"; do
  docker compose exec -T orchestrator /app/course-dev-orchestrator project-connect \
    --path "/projects/microservices/${validator}" --role service
  docker compose exec -T orchestrator /app/course-dev-orchestrator project-report \
    --service "${validator}"
done
docker compose exec -T orchestrator /app/course-dev-orchestrator topology
docker compose exec -T orchestrator /app/course-dev-orchestrator contract-drift
```

Connected-catalog result after the validator waves and corrections:

- all eleven projects are `analyzed`, on clean `main` checkouts, with unchanged
  source HEADs;
- name-based `project-report` and `project-scan` work for every project;
- discovery schema v4 excludes false generic request values, recognizes
  Python as a backend runtime, and extracts Go `net/http` and Python
  `BaseHTTPRequestHandler` contracts;
- repeated scans reuse snapshot version 4 for the original three projects and
  version 3 for the second wave; repeated topology rebuilds reuse the same
  revision and fingerprint;
- the topology has 11 services, 31 capabilities, one ownership record, 25
  contracts, no relations, and no contract drift;
- the database contains no onboarding runs, commands, GitLab links/events,
  Telegram updates, plans, or plan runs.

The third wave used these read-only commands. Report lookup uses canonical
remote names rather than local directory names:

```sh
docker compose exec -T orchestrator /app/course-dev-orchestrator project-connect \
  --path /projects/microservices/prompts --role policy
docker compose exec -T orchestrator /app/course-dev-orchestrator project-connect \
  --path /projects/microservices/journal --role archive

docker compose exec -T orchestrator /app/course-dev-orchestrator project-report --service ms-course-promts
docker compose exec -T orchestrator /app/course-dev-orchestrator project-report --service ms-course-journal
docker compose exec -T orchestrator /app/course-dev-orchestrator topology
docker compose exec -T orchestrator /app/course-dev-orchestrator contract-drift
```

Current result after the non-runtime wave and discovery schema v6:

- all 13 projects are `analyzed`, on clean `main` checkouts, with unchanged
  source HEADs;
- `ms-course-promts` has 19 checksum-only policy instruction facts and no
  runtime evidence; `ms-course-journal` has classification/purpose evidence
  and no runtime evidence;
- repeated scans reuse snapshot version 6 for the original pilot, version 5
  for the validator wave, and version 3 for the non-runtime wave;
- repeated topology rebuilds reuse the same revision and fingerprint. The
  revision covers 13 projects but materializes only 11 runtime services, 31
  capabilities, one ownership record, 25 contracts, no relations, and no
  contract drift;
- neither non-runtime project has rows in topology service, capability,
  ownership, contract, or relation tables;
- the database contains no onboarding runs, commands, GitLab links/events,
  Telegram updates, plans, or plan runs.

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
in bulk and are not authorized merely by completing the discovery waves.

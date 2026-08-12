#!/bin/sh
set -eu
required_files="AGENTS.md .ai/manifest.yaml .ai/template.yaml .ai/rules/common.md .ai/service.yaml .ai/architecture.yaml .ai/commands.yaml .ai/contracts/project-lifecycle.yaml .ai/contracts/onboarding.yaml .ai/contracts/runtime.yaml .ai/agents/coder.md .ai/agents/backend-coder.md .ai/agents/migration-agent.md .ai/agents/reviewer.md .ai/workflows/bugfix.yaml .ai/workflows/implement-feature.yaml .ai/workflows/change-contract.yaml .ai/workflows/refactor.yaml .ai/workflows/review.yaml .ai/workflows/issue-delivery.yaml .ai/workflows/test.yaml docs/README.md docs/architecture-conventions.md docs/implementation-plan.md docs/progress.md docs/knowledge-source-retirement.md"
for policy_file in $required_files; do test -f "$policy_file" || { echo "agent-policy: missing $policy_file" >&2; exit 1; }; done
test ! -e .ai/discovery || { echo "agent-policy: generated discovery must not be canonical policy" >&2; exit 1; }
cmp -s .ai/rules/common.md internal/onboarding/templates/v1/common-rules.md || { echo "agent-policy: root common rules differ from canonical embedded template" >&2; exit 1; }
test "$(shasum -a 256 .ai/rules/common.md | awk '{print $1}')" = "38b32bff2396d3fbee28055e40596962f19dd35f4c8511be279ce31757b7ad9d" || { echo "agent-policy: common rules checksum mismatch" >&2; exit 1; }
obsolete_paths='prom''pts/git-workflow|prom''pts/codex-shared|\.\./prom''pts|microservices/prom''pts|microservices/jour''nal|microservices/wi''ki|/Users/marat/Developments'
if grep -n -E "$obsolete_paths" AGENTS.md .ai/* .ai/agents/* .ai/contracts/* .ai/rules/* .ai/workflows/* docs/README.md docs/architecture-conventions.md README.md 2>/dev/null; then echo "agent-policy: obsolete canonical knowledge reference found" >&2; exit 1; fi
echo "agent-policy: ok"

# Signup completion migration CI correction

The Auth service run [37647312293](https://github.com/bemulima/ms-go-auth/actions/runs/37647312293), job [112881343480](https://github.com/bemulima/ms-go-auth/actions/runs/37647312293/job/112881343480), at commit `95687e6dda1c7a87e1fa71cad70099891ababb53` failed the canonical `task migration-integration-test` on 2026-10-07 at 15:52:43 UTC:

```text
expected '3', got '4'
```

This was the first local `auth_schema_migration` count assertion. The published baseline `ee03c3b209241cc8871f61c74b3e9b37b70a2774` already includes `0004_signup_completion`. Four migrations apply locally, while production excludes the local seed and applies three schema migrations. The harness still expected three and two respectively.

The same exact baseline failure is independently recorded in [run 37508441512](https://github.com/bemulima/ms-go-auth/actions/runs/37508441512) at 2026-10-06 18:05:03 UTC during the policy migration command and again at 18:05:24 UTC during the standalone canonical migration task. Both report the same count mismatch. The later baseline [run 37595966645](https://github.com/bemulima/ms-go-auth/actions/runs/37595966645) stopped at Task setup due to an API rate limit; its migration step was skipped and supplies no migration execution evidence.

Byte comparison against that baseline confirms these files were unchanged before the correction:

| File | SHA256 |
| --- | --- |
| `test/integration/migration_lifecycle_test.sh` | `5723bc8b8073058c45ea6178ec185ec155f404ca6e0bec9f576b0e528a5ec450` |
| `scripts/migrate.sh` | `90bebeafa294854a3340fabf8b065abc93ad1bf943239332dde734368b9b8588` |
| `migrations/0004_signup_completion.up.sql` | `a2511527f0a81eb1c77a1b81b7aea2c4df210ae62e137506bfb9f1f5d3e926a3` |
| `migrations/0004_signup_completion.down.sql` | `f0091a3de15d4cacaa1007da40f681b88f253bbcbdf49d0aded7b4dd56459fef` |

The correction changes only migration verification: local ledger counts become four, production counts become three, and explicit signup-completion schema/status/idempotency assertions cover the existing migration. Rollback acceptance exercises one retained pending operation, then its verified and completed states, and requires failure without removing the record or ledger before verifying empty-table rollback. Application code, migration SQL, runner, fixture seed policy, and disposable provisioner remain unchanged.

Worker validation: shell syntax, policy validation, and catalog/count consistency checks. Actual PostgreSQL lifecycle execution and subsequent normal CI are coordinator-owned acceptance and must be recorded from their results; static validation alone is not a passing runtime result. Raw GitHub logs, environment values, credentials, and fixture hashes are intentionally absent from this evidence file.

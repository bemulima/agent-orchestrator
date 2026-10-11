# Shared boundary checklist

Use only entries shared by independent routed workers.

| Area | Examples to freeze when shared |
|---|---|
| Backend domain/application | Domain types and invariants; command/result and error semantics |
| Backend persistence/client | Repository or client port; query, transaction, retry and error contract |
| Backend transport/messaging | Request/response mapping; event/message envelope and compatibility |
| Database | Schema meaning, migration ordering and compatibility window |
| Frontend module | Model/schema, API adapter, usecase input/output, UI props/behavior |
| Frontend shared/BFF | Request/response shape, shared component props, cache/error behavior |
| Localization | Required keys, interpolation inputs, locale fallback behavior |

Private helpers, local variables, file layout, and implementation choices are
not contracts unless independent workers demonstrably share that boundary.

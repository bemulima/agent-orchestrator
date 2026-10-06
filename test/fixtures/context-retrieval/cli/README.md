# Offline CLI smoke fixture

This synthetic historical route intentionally lacks an R1 acquisition companion.
Prepare and Expand succeed with PARTIAL status and ROUTING_COVERAGE_UNKNOWN,
while returning exact current evidence. Current-companion COMPLETE behavior is
covered by CLI tests using the actual R1 collector, contract plan and coverage.

Run the built context executable with request/route/expand files from this
directory and explicit `--root fixture=<absolute source directory>`. Save stdout
pack and delta under disposable `.cache/agent-context` for round-trip testing.
No environment, model, network or external repository is required.

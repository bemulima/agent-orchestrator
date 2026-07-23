# First live concurrent coding plan

Add focused Go unit tests for the existing `/health` handler in each explicitly
selected repository.

For every selected service, assert that a GET request returns HTTP 200, uses
`application/json`, and decodes to the existing `status=ok` and configured
service-name fields. Keep the tests in `internal/**`, follow the repository's
current test conventions, and do not change existing runtime behavior or
response fields. Do not edit `AGENTS.md` or `.ai/**`.

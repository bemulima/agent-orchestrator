# E2E (Gateway) — ms-go-user

Тесты проверяют флоу из `wiki/USER_PROFILE.md`, выполняя реальные запросы только через `ms-gateway`.

## Требования
- Запущены контейнеры (gateway + user + auth + зависимости).
- Доступен student/guest gateway: `${GATEWAY_URL}` (по умолчанию `http://localhost:8080`).

## Переменные окружения
- `GATEWAY_URL` — base URL gateway.
- `HTTP_TIMEOUT` — таймаут curl (сек), по умолчанию `30`.
- `DEBUG=1` — подробный вывод.
- `MISMATCHES_OUT` — путь к файлу, куда дописывать найденные несоответствия (markdown).

## Запуск
```bash
cd ms-go-user
bash test/e2e/gateway/run-tests.sh
```

## Isolated verification delivery fixture

Set `USER_E2E_ISOLATED_FIXTURE=true` and
`USER_E2E_VERIFICATION_CODE_COMMAND=/absolute/path/to/mailbox-reader`.
The executable receives the newly registered email as its only argument and
returns exactly its four-digit Auth verification code on stdout. It must read
only the isolated fixture's delivery mailbox. The runner rejects missing fixture
attestation or a missing executable before contacting Gateway. Hook stderr and
response bodies are omitted from reports; keep shell tracing disabled.

Signup start and verify still call the canonical Auth routes through Gateway.
This hook is test-only and does not add a production endpoint or alter verification
state. The suite requires an already running isolated platform and starts no
services or sibling checkouts.

Safe hermetic observer regression (starts no runtime):

```bash
bash test/e2e/gateway/test-fixture-verification-code.sh
```

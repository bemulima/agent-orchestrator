# E2E (Gateway) — ms-go-auth

Тесты проверяют флоу из `wiki/AUTHENTICATION.md`, выполняя реальные запросы только через `ms-gateway`.
The password-reset scenario uses the verification service's integration-only
code hook, as does the signup scenario.

## Требования
- Запущены контейнеры (gateway + auth + зависимости).
- Доступен student/guest gateway: `${GATEWAY_URL}` (по умолчанию `http://localhost:8080`).

## Переменные окружения
- `GATEWAY_URL` — base URL для student/guest gateway.
- `ADMIN_GATEWAY_URL` — base URL для admin gateway (не используется в этом наборе).
- `HTTP_TIMEOUT` — таймаут curl (сек), по умолчанию `30`.
- `DEBUG=1` — подробный вывод.
- `MISMATCHES_OUT` — путь к файлу, куда дописывать найденные несоответствия (markdown).

## Запуск
```bash
cd ms-go-auth
bash test/e2e/gateway/run-tests.sh
```

Verification fixtures require `AUTH_E2E_ISOLATED_FIXTURE=true` and an absolute executable `AUTH_E2E_VERIFICATION_CODE_COMMAND`. The runner calls it with one synthetic `e2e-…@example.com|test` email and `AUTH_E2E_VERIFICATION_FLOW=signup|password-reset`; stdout must contain exactly four decimal digits. The helper uses separately owned disposable-store fixture credentials. It must reject unrelated identities/stores and avoid logs. Gateway public Auth HTTP remains the boundary under test; there is no verification HTTP route. Mismatch evidence omits request/response secret material.

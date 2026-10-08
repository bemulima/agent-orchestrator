# HTTP contract

All /api/v1/users routes require AuthMiddleware. /admin/v1/users additionally requires admin or moderator via RBAC middleware. Self responses may include private status fields; other-user responses omit status/is_active and mask email.

PATCH /me accepts only display_name because its decoder rejects unknown fields. Avatar upload is limited to 5 MB and supports EAGER, LAZY, or DISABLED; the default is DISABLED. Remove identity returns HTTP 200 with status detached, not the 204 shown in the former wiki.

For EAGER avatar generation, User uploads the source first and asks FileStorage for a processing delegation bound to the returned `file_id` and authenticated `owner_id`. Upload, signed-URL, and delegation calls use `X-Internal-Token` from `FILESTORAGE_INTERNAL_TOKEN`. The delegation request uses `delegate_service=image_processor` and `scope=read_source`; User omits `ttl_seconds` so FileStorage applies its 300-second default. User sends the opaque result to Image Processor as JSON `processing_delegation` and authenticates with `X-Internal-Token` from `IMAGE_PROCESSOR_INTERNAL_TOKEN`. The grant is never included in the public avatar response. Configure the service URLs with `MS_FILESTORAGE_URL` and `IMAGE_PROCESSOR_SERVICE_BASE_URL`.

POST /internal/v1/users/active/resolve requires exact X-Internal-Token, a single strict JSON object, 1..1000 unique non-nil UUIDs, and a body at most 64 KiB. ACTIVE and NEW_USER rows with is_active true are active; result arrays preserve request order. The configured token currently defaults to change-me and must be overridden outside local development.

Outbound RBAC HTTP uses `MS_RBAC` as an origin (`scheme://host[:port]`). The
adapter supplies `/api/v1` for GET principal-role/get, principal-role/get-by-role,
principal-permission/list, principal-permission/get-by-permission, and PATCH
principal-role/update. Role updates retain the `{ "value": { "user_id", "role" } }`
payload; query parameters and authentication behavior are unchanged.

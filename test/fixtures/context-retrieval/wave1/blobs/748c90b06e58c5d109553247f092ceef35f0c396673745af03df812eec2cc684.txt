# Messaging contract

user.create-user is idempotent Core NATS request/reply. Request requires id and may include email, source, type, and oauth_profile. It creates missing user/profile records. OAuth import fills only missing first_name, last_name, birth_year, gender, and avatar; import failure is non-blocking and returns warning oauth_profile_import_failed.

HTTP authentication calls auth.verifyJWT and role validation calls rbac.checkRole. These are RPC contracts, not durable events. internal/events/UserEvent is currently unused and is not an active broker contract.

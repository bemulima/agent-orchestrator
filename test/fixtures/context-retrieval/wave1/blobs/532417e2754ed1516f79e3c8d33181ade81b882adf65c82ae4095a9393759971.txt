# Database contract

The user table owns identity-independent user ID, optional email, status, active flag, and timestamps. Password storage was removed in migration 0006. user_profile owns display and OAuth-derived profile fields plus avatar_file_id; avatar_url is computed, not persisted after migration 0007.

user_identity and user_provider remain persisted and user HTTP endpoints operate on user_identity, while ms-go-auth separately owns auth_identity for login. Consolidation requires an explicit data/API migration and consumer audit, not silent deletion.

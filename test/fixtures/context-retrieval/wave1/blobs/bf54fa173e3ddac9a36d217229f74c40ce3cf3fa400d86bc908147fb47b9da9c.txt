# Messaging contract

auth.verifyJWT is a queue-group Core NATS request/reply endpoint. Request is {"token":"..."}. Response contains ok, optional user_id and email, optional claims, and an error code. Role and expiry remain inside claims; there are no dedicated role or expires_at response fields.

The service calls user.create-user, rbac.assign-role, and rbac.checkRole. These are RPC subjects, not durable events. Payload changes require producer/consumer tests in both owning repositories.

Signup completion treats User creation and RBAC assignment as required
synchronous acknowledgements. The frozen success, failure, and retry semantics
are defined in `.ai/contracts/auth-user-rbac-provisioning-v1.md`.

Signup role mutation uses a dedicated Auth-owned Ed25519 proof on the existing
assignment subject. The signed envelope binds the persisted signup operation,
canonical principal, exact `student` role and existing default scope; no outer
caller/header fields establish identity. See the authenticated transport addendum
in `.ai/contracts/auth-user-rbac-provisioning-v1.md`. Configure
`AUTH_RBAC_SIGNUP_PRIVATE_KEY` without a source/default secret; absent keys fail
closed for signup, and invalid nonempty keys fail composition. User access or
refresh tokens cannot replace this proof. Generic unsigned role assignment
remains a compatibility method without signup authority; OAuth and disposable
fixture policy require a human decision.

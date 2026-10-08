# AUTH_USER_RBAC_PROVISIONING_CONTRACT_V1

Status: frozen for signup completion.

## Owners and identity

Auth owns orchestration. User owns the user and profile projection. RBAC owns
the principal-role assignment.

The Auth user identifier is the canonical principal identifier. Auth passes
that unchanged as the User projection identifier and the RBAC principal
identifier.

The canonical signup role key is `student`. It is the RBAC key and is accepted
by Student through its normal role normalization.

## Successful signup invariant

Auth may report a successful final signup only after all of these operations
have succeeded synchronously:

1. identity verification has succeeded;
2. the Auth user exists;
3. `user.create-user` has acknowledged durable User projection creation; and
4. `rbac.assign-role` has acknowledged the canonical `student` assignment and
   `rbac.checkRole` confirms it.

Token and session issuance happen only after that invariant holds. The User
creation acknowledgement is the User service's durable read-after-create
boundary; the RBAC check is the normal production role-read boundary.

## Failure invariant

An error, timeout, negative acknowledgement, or failed role check from either
provisioning dependency prevents successful signup completion and token/session
issuance. Auth may retain the verified identity and local Auth user as a
recoverable partial state. It must not compensate by deleting remote state or
silently treat an incomplete actor as successful.

## Idempotency and recovery

Signup completion retries use the same canonical principal identifier. User
creation and role assignment are idempotent at their owning services.

* If User creation succeeded and RBAC assignment failed, retrying repeats
  User creation safely and repairs the role assignment.
* If RBAC assignment succeeded while User creation did not become visible,
  retrying creates the User projection and repeats the role assignment safely.
* Before the terminal CAS, retrying converges on the same Auth user, User
  projection, and role assignment. After that CAS, code replay is denied and
  cannot attempt token issuance again. Use ordinary password signin after a
  lost terminal/token response.

No distributed transaction, manual database repair, fabricated actor, or
Teacher-specific bypass is part of this contract.

## Durable proof and completion boundary

Auth persists the immutable operation and reserved principal before proof consume.
Only Auth's direct authenticated Tarantool same-operation receipt can verify it;
normalized email, operation, proof fingerprint, frozen credential, and original
hard expiry must agree. Missing recovery ports/configuration fail closed.
Principal creation and completion ownership commit atomically; arbitrary existing
email identity reuse is forbidden. User and RBAC commands retain the canonical
principal and `student` role. No new delivery or role policy is introduced.

A terminal verified-to-completed CAS commits before exactly one signup token
issuance attempt. Code completion requires a fresh receipt at the CAS. All
issueTokens callers fail closed for known pending principals and on persistence
lookup error. Password-authenticated signin for the exact owned verified
credential may retry the same required User/RBAC operations and terminal CAS
after receipt expiry; Refresh and OAuth cannot repair implicitly. Completed
accounts retain ordinary password signin behavior. Migrations retain operations
and terminal tombstones; rollback refuses a nonempty completion table.

## Authenticated signup role transport

The RBAC-owned application boundary is
[AUTH_RBAC_STUDENT_PROVISIONING_V1](../../../ms-go-rbac/.ai/contracts/auth-rbac-student-provisioning-v1.md).
It retains this signup operation's canonical principal, fixed student role and
default scope. Exact replay returns the same success receipt without writes;
incompatible principal/role/scope bindings conflict. Role and scope are never
chosen by the frontend request or passed as arguments to the signup-only port.

The accepted canonical `student` signup assignment is authenticated with a
separate `SignupRoleProvisioner.AssignSignupRole` port. Verified completion and
exact password-owned repair pass their persisted operation ID and reserved
principal unchanged, only after durable User acknowledgement. The NATS adapter
signs a byte-bound Ed25519 envelope (`key_id`, standard-base64 `payload`,
standard-base64 `signature`) with key ID `auth-signup-v1`. Signed purpose is
`signup-student-provisioning-v1`, issuer `ms-go-auth`, audience `ms-go-rbac`, and
subject is the actual assignment RPC subject. Payload binds version 1, the
canonical non-nil operation and principal UUIDs, role `student`, principal kind
`user`, and the existing default tenant/service/global/resource tuple. Integer
issued/expiry timestamps have a 60-second lifetime. RBAC verifies the dedicated
public key and all bindings before receipt lookup or assignment.

`AUTH_RBAC_SIGNUP_PRIVATE_KEY` is standard-base64 Ed25519 private key material
(64 bytes), held only by Auth. The matched public key is configured in RBAC.
Missing configuration leaves signup provisioning unavailable; malformed nonempty
configuration fails composition. There is no unsigned signup fallback or shared
internal-token authority. Keys and raw proofs are never logged or persisted.
Deployment requires an operator-supplied matched dedicated key pair.

RBAC atomically retains the signup operation receipt with its immutable
principal/student/default-scope binding. Auth recovery signs a fresh short-lived
proof for that same operation; a receipt does not bypass authentication or
revive expired code completion. The existing role read, terminal CAS and no-token
invariants remain required. Different existing default roles are not replaced.
This establishes transport identity for existing signup authority only.
Administrative grant rules and OAuth/verification-fixture provisioning authority
remain `HUMAN_POLICY_DECISION_REQUIRED`; their generic role calls gain no rights.

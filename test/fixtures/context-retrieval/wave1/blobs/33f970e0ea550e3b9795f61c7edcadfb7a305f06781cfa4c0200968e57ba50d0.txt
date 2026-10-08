package domain

import "context"

// VerificationRepository executes the closed Auth-owned storage operations.
// The adapter cannot evaluate Lua or administer the shared identity store.
type VerificationRepository interface {
	Execute(context.Context, string, []interface{}) ([]interface{}, error)
}

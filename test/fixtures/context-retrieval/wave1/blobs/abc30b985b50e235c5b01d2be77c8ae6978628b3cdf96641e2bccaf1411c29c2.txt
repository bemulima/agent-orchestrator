// Package domain defines user data and the lifecycle rules owned by this service.
package domain

import (
	"fmt"
	"time"
)

// UserStatus is a persisted lifecycle state for a user account.
type UserStatus string

// UserStatus values represent the lifecycle states accepted by the user domain.
const (
	UserStatusNew      UserStatus = "NEW_USER"
	UserStatusActive   UserStatus = "ACTIVE"
	UserStatusInactive UserStatus = "INACTIVE"
	UserStatusBlocked  UserStatus = "BLOCKED"
)

// User stores identity-independent account state; profile details live in UserProfile.
// Credential persistence belongs to ms-go-auth and is excluded from this model.
type User struct {
	ID           string     `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Email        string     `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash *string    `gorm:"-" json:"-"`
	Status       UserStatus `gorm:"column:status;type:text;default:NEW_USER" json:"status"`
	IsActive     bool       `gorm:"column:is_active;default:true" json:"is_active"`
	CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
	Profile      *UserProfile
}

// TableName binds the GORM model to the legacy singular user table.
func (User) TableName() string {
	return "user"
}

// HasPassword reports false because this service does not persist credentials.
func (u *User) HasPassword() bool {
	return false
}

// SetPasswordHash is a compatibility no-op; ms-go-auth owns credential writes.
func (u *User) SetPasswordHash(_ string) {
	// password hash is managed by ms-go-auth
}

// IsValid reports whether s is one of the lifecycle states supported by the service.
func (s UserStatus) IsValid() bool {
	return s == UserStatusActive || s == UserStatusInactive || s == UserStatusBlocked || s == UserStatusNew
}

// SetStatus validates and applies a lifecycle state, synchronizing the active flag.
func (u *User) SetStatus(status UserStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("invalid user status")
	}
	u.Status = status
	u.IsActive = status == UserStatusActive || status == UserStatusNew
	return nil
}

// StatusOrDefault returns the stored valid status or derives a legacy default.
func (u *User) StatusOrDefault() UserStatus {
	if u.Status.IsValid() {
		return u.Status
	}
	if u.IsActive {
		return UserStatusNew
	}
	return UserStatusInactive
}

// Activate marks the user active.
func (u *User) Activate() {
	_ = u.SetStatus(UserStatusActive)
}

// Deactivate marks the user inactive.
func (u *User) Deactivate() {
	_ = u.SetStatus(UserStatusInactive)
}

// Block marks the user blocked.
func (u *User) Block() {
	_ = u.SetStatus(UserStatusBlocked)
}

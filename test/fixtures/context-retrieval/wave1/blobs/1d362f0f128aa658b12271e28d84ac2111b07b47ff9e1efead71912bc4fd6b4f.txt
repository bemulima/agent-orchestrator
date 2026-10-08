package domain

import "time"

// IdentityProvider identifies a supported external identity provider.
type IdentityProvider string

// ProviderGoogle and ProviderGitHub name the providers accepted by the user API.
const (
	ProviderGoogle IdentityProvider = "google"
	ProviderGitHub IdentityProvider = "github"
)

// UserIdentity records a provider identity linked to a user for the retained user API.
type UserIdentity struct {
	ID             string           `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID         string           `gorm:"type:uuid;not null;index" json:"user_id"`
	Provider       IdentityProvider `gorm:"column:provider;not null" json:"provider"`
	ProviderUserID string           `gorm:"column:provider_user_id;not null" json:"provider_user_id"`
	Email          string           `gorm:"column:email;not null" json:"email"`
	DisplayName    *string          `gorm:"column:display_name" json:"display_name"`
	AvatarURL      *string          `gorm:"column:avatar_url" json:"avatar_url"`
	CreatedAt      time.Time        `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time        `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// TableName maps provider links to the persisted user_identity table.
func (UserIdentity) TableName() string {
	return "user_identity"
}

// IsValid reports whether p is supported by this service.
func (p IdentityProvider) IsValid() bool {
	return p == ProviderGoogle || p == ProviderGitHub
}

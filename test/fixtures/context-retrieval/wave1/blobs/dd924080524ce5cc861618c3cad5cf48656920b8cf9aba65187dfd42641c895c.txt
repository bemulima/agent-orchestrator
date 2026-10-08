package domain

import "time"

type AuthUser struct {
	ID                string         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Email             string         `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash      *string        `json:"-"`
	PasswordUpdatedAt *time.Time     `json:"password_updated_at"`
	LastLoginAt       *time.Time     `json:"last_login_at"`
	CreatedAt         time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	Identities        []AuthIdentity `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

func (AuthUser) TableName() string { return "auth_user" }

type AuthIdentity struct {
	ID             string                 `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID         string                 `gorm:"type:uuid;index;not null;uniqueIndex:idx_auth_identity_user_provider,priority:1" json:"user_id"`
	Provider       string                 `gorm:"type:text;not null;uniqueIndex:idx_auth_identity_user_provider,priority:2;uniqueIndex:idx_auth_identity_provider_subject,priority:1" json:"provider"`
	ProviderUserID string                 `gorm:"type:text;not null;uniqueIndex:idx_auth_identity_provider_subject,priority:2" json:"provider_user_id"`
	Email          string                 `gorm:"type:text" json:"email"`
	RawProfile     map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"raw_profile"`
	CreatedAt      time.Time              `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time              `gorm:"autoUpdateTime" json:"updated_at"`
}

func (AuthIdentity) TableName() string { return "auth_identity" }

type OAuthTransaction struct {
	ID           string     `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	StateHash    string     `gorm:"type:text;uniqueIndex;not null" json:"-"`
	Provider     string     `gorm:"type:text;index;not null" json:"provider"`
	CodeVerifier string     `gorm:"type:text;not null" json:"-"`
	ReturnTo     string     `gorm:"type:text;not null" json:"return_to"`
	ExpiresAt    time.Time  `gorm:"index;not null" json:"expires_at"`
	ConsumedAt   *time.Time `json:"consumed_at"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (OAuthTransaction) TableName() string { return "auth_oauth_transaction" }

// RefreshToken represents a persisted refresh session.
type RefreshToken struct {
	ID               string     `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID           string     `gorm:"type:uuid;index;not null" json:"user_id"`
	RefreshTokenHash string     `gorm:"type:text;not null" json:"-"`
	ExpiresAt        time.Time  `gorm:"not null" json:"expires_at"`
	RevokedAt        *time.Time `json:"revoked_at"`
	CreatedAt        time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

func (RefreshToken) TableName() string { return "auth_refresh_token" }

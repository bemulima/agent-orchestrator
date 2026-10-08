package domain

import "time"

const (
	SignupPending   = "pending"
	SignupVerified  = "verified"
	SignupCompleted = "completed"
)

// SignupCompletion binds one proof to a reserved principal before consumption.
// Its credential and receipt are private persistence facts, never responses.
type SignupCompletion struct {
	OperationID      string     `gorm:"type:uuid;primaryKey" json:"-"`
	PrincipalID      string     `gorm:"type:uuid;uniqueIndex;not null" json:"-"`
	Email            string     `gorm:"not null;uniqueIndex:idx_signup_email_proof,priority:1" json:"-"`
	ProofFingerprint string     `gorm:"not null;uniqueIndex:idx_signup_email_proof,priority:2" json:"-"`
	PasswordHash     string     `json:"-"`
	State            string     `gorm:"not null" json:"-"`
	PrincipalCreated bool       `gorm:"not null;default:false" json:"-"`
	ReceiptExpiresAt *time.Time `json:"-"`
	CreatedAt        time.Time  `gorm:"autoCreateTime" json:"-"`
	UpdatedAt        time.Time  `gorm:"autoUpdateTime" json:"-"`
}

func (SignupCompletion) TableName() string { return "auth_signup_completion" }

type SignupProofReceipt struct {
	OperationID  string    `json:"operation_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password"`
	ExpiresAt    time.Time `json:"expires_at"`
}

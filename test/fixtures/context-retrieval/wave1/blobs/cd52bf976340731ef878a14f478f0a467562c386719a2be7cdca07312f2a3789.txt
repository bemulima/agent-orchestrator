package domain

import (
	"strings"
	"time"
)

// UserProfile stores display and imported profile fields separately from account state.
// AvatarURL is computed for responses and is not persisted.
type UserProfile struct {
	ID           string    `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID       string    `gorm:"type:uuid;not null;uniqueIndex" json:"user_id"`
	DisplayName  *string   `gorm:"column:display_name" json:"display_name"`
	FirstName    *string   `gorm:"column:first_name" json:"first_name"`
	LastName     *string   `gorm:"column:last_name" json:"last_name"`
	BirthYear    *int      `gorm:"column:birth_year" json:"birth_year"`
	Gender       *string   `gorm:"column:gender" json:"gender"`
	AvatarFileID *string   `gorm:"column:avatar_file_id" json:"avatar_file_id"`
	AvatarURL    *string   `gorm:"-" json:"avatar_url,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// FillOAuthData fills only missing fields with normalized, bounded provider values.
func (p *UserProfile) FillOAuthData(firstName, lastName string, birthYear *int, gender string, currentYear int) {
	if p.FirstName == nil {
		p.FirstName = normalizedProfileText(firstName, 128)
	}
	if p.LastName == nil {
		p.LastName = normalizedProfileText(lastName, 128)
	}
	if p.BirthYear == nil && birthYear != nil && *birthYear >= 1900 && *birthYear <= currentYear {
		year := *birthYear
		p.BirthYear = &year
	}
	if p.Gender == nil {
		p.Gender = normalizedProfileText(strings.ToLower(gender), 64)
	}
}

func normalizedProfileText(value string, maxLength int) *string {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > maxLength {
		return nil
	}
	return &value
}

// TableName maps profiles to the persisted user_profile table.
func (UserProfile) TableName() string {
	return "user_profile"
}

// Update applies supplied display-name and avatar-reference changes.
func (p *UserProfile) Update(displayName, avatarFileID *string) {
	if displayName != nil {
		name := strings.TrimSpace(*displayName)
		if name == "" {
			p.DisplayName = nil
		} else {
			p.DisplayName = &name
		}
	}

	if avatarFileID != nil {
		fileID := strings.TrimSpace(*avatarFileID)
		if fileID == "" {
			p.AvatarFileID = nil
			p.AvatarURL = nil
		} else {
			p.AvatarFileID = &fileID
			p.AvatarURL = nil
		}
	}
}

// WithAvatarURL derives a response URL from the stored avatar file reference.
func (p *UserProfile) WithAvatarURL(urlResolver func(string) string) {
	if p == nil || p.AvatarFileID == nil || urlResolver == nil {
		return
	}

	fileID := strings.TrimSpace(*p.AvatarFileID)
	if fileID == "" {
		return
	}

	avatarURL := urlResolver(fileID)
	p.AvatarURL = &avatarURL
}

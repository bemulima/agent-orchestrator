package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// UserProvider stores external identity provider linkage for a user.
type UserProvider struct {
	ID             string    `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	ProviderType   string    `gorm:"type:text;not null" json:"provider_type"`
	ProviderUserID string    `gorm:"type:text;not null" json:"provider_user_id"`
	UserID         string    `gorm:"type:uuid;not null;index" json:"user_id"`
	Metadata       JSONMap   `gorm:"type:jsonb" json:"metadata"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// JSONMap provides database marshaling helpers for JSONB columns.
type JSONMap map[string]interface{}

// Value encodes JSONMap as JSON text for database/sql drivers.
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan decodes a database JSON value into the map.
func (m *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*m = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case string:
		bytes = []byte(v)
	case []byte:
		bytes = v
	default:
		return fmt.Errorf("unsupported type %T for JSONMap", value)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	*m = data
	return nil
}

// TableName maps provider links to the persisted user_provider table.
func (UserProvider) TableName() string {
	return "user_provider"
}

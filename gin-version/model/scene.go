package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Scene struct {
	SID       string    `gorm:"primaryKey;type:varchar(36);column:sid" json:"sid"`
	Name      string    `gorm:"type:varchar(100);not null" json:"name"`
	Describe  string    `gorm:"type:varchar(100);not null" json:"describe"`
	View      []string  `gorm:"type:text;not null;serializer:json" json:"view"`
	Location  string    `gorm:"type:varchar(30);not null" json:"location"`
	Article   string    `gorm:"type:text;not null" json:"article"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Scene) BeforeCreate(tx *gorm.DB) error {
	if s.SID == "" {
		s.SID = uuid.New().String()
	}
	return nil
}

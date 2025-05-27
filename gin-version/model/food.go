package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Food struct {
	FID       string    `gorm:"primaryKey;type:varchar(36);column:fid" json:"fid"`
	View      []string  `gorm:"type:text;not null;serializer:json" json:"view"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Describe  string    `gorm:"type:text" json:"describe"`
	Recipe    string    `gorm:"type:text" json:"recipe"`
	Article   string    `gorm:"type:text" json:"article"`
	Location  string    `gorm:"type:varchar(30);not null" json:"location"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (f *Food) BeforeCreate(tx *gorm.DB) error {
	if f.FID == "" {
		f.FID = uuid.New().String()
	}
	return nil
}

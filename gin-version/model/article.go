package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Article struct {
	AID       string    `gorm:"primaryKey;type:varchar(36);column:aid" json:"aid"`
	UID       string    `gorm:"type:varchar(36);column:uid" json:"uid"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string    `gorm:"type:varchar(100)" json:"avatar"`
	Title     string    `gorm:"type:varchar(100);not null" json:"title"`
	View      []string  `gorm:"type:text;not null;serializer:json" json:"view"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Likes     int       `gorm:"default:0" json:"likes"`
	Comments  int       `gorm:"default:0" json:"comments"`
	Favorite  int       `gorm:"default:0" json:"favorite"`
	Video     bool      `gorm:"default:false" json:"video"`
	Videoid   int       `gorm:"default:0" json:"videoid"`
	Height    int       `gorm:"default:0" json:"height"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	// ES字段
	// ESTitle   string    `json:"es_title,omitempty"`
	// ESContent string    `json:"es_content,omitempty"`
}

func (a *Article) BeforeCreate(tx *gorm.DB) error {
	if a.AID == "" {
		a.AID = uuid.New().String()
	}
	return nil
}

package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AudiobookDetail struct {
	DID       string    `gorm:"primaryKey;type:varchar(36);column:did;not null" json:"did"`
	BID       string    `gorm:"type:varchar(36);column:bid;index" json:"bid"`
	Chapter   int       `gorm:"default:1;not null" json:"chapter"`
	Audio     string    `gorm:"type:varchar(255);not null" json:"audio"`
	Name      string    `gorm:"type:varchar(255);not null" json:"name"`
	Duration  int       `gorm:"not null" json:"duration"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (d *AudiobookDetail) BeforeCreate(tx *gorm.DB) error {
	if d.DID == "" {
		d.DID = uuid.New().String()
	}
	return nil
}

type Audiobook struct {
	BID         string    `gorm:"primaryKey;type:varchar(36);column:bid" json:"bid"`
	View        string    `gorm:"type:varchar(255)" json:"view"`
	Author      string    `gorm:"type:varchar(50);not null" json:"author"`
	Name        string    `gorm:"type:varchar(50);not null" json:"name"`
	Playcount   int       `gorm:"default:0" json:"playcount"`
	Chapternum  int       `gorm:"default:0" json:"chapternum"`
	Rating      float64   `gorm:"default:9.4" json:"rating"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (b *Audiobook) BeforeCreate(tx *gorm.DB) error {
	if b.BID == "" {
		b.BID = uuid.New().String()
	}
	return nil
}

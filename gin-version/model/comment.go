package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Comment struct {
	CID       string    `gorm:"primaryKey;type:varchar(36);column:cid" json:"cid"`
	Target    string    `gorm:"type:varchar(36)" json:"target"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Likes     int       `gorm:"default:0" json:"likes"`
	UID       string    `gorm:"type:varchar(36);column:uid" json:"uid"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string    `gorm:"type:varchar(100)" json:"avatar"`
	Replycid  string    `gorm:"type:varchar(36)" json:"replycid"`
	Replyname string    `gorm:"type:varchar(36)" json:"replyname"`
	Time      string    `gorm:"type:varchar(36)" json:"time"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c *Comment) BeforeCreate(tx *gorm.DB) error {
	if c.CID == "" {
		c.CID = uuid.New().String()
	}
	return nil
}

package model

import (
	"time"
)

type Like struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid;index:idx_uid_aid,priority:1" json:"uid"`
	AID       string    `gorm:"type:varchar(36);column:aid;index:idx_uid_aid,priority:2" json:"aid"`
	CreatedAt time.Time `json:"created_at"`
}

type Favorite struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid;index:idx_uid_aid,priority:1" json:"uid"`
	AID       string    `gorm:"type:varchar(36);column:aid;index:idx_uid_aid,priority:2" json:"aid"`
	CreatedAt time.Time `json:"created_at"`
}

type Commentlike struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid;index:idx_uid_cid,priority:1" json:"uid"`
	CID       string    `gorm:"type:varchar(36);column:cid;index:idx_uid_cid,priority:2" json:"cid"`
	CreatedAt time.Time `json:"created_at"`
}

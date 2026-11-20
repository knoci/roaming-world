package data

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestCommentCountUsesCurrentActiveRows(t *testing.T) {
	dsn := os.Getenv("ROAMING_TEST_ARTICLE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ROAMING_TEST_ARTICLE_POSTGRES_DSN to a migrated test database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	d := &Data{db: db, log: log.NewHelper(log.NewStdLogger(io.Discard))}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	row := Article{AID: uuid.NewString(), Name: "Fixture", Title: "Fixture", View: []string{"fixture"}, Content: "Fixture"}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DELETE FROM comments WHERE target = ?", row.AID).Error; err != nil {
			t.Error(err)
		}
		if err := db.Delete(&row).Error; err != nil {
			t.Error(err)
		}
	})
	commentID := uuid.NewString()
	insert := `INSERT INTO comments (cid, target, name, content, replycid, status)
		VALUES (?, ?, 'Fixture', 'Fixture', ?, 'active')`
	if err := db.WithContext(ctx).Exec(insert, commentID, row.AID, "").Error; err != nil {
		t.Fatal(err)
	}
	checkCount := func(want int) {
		t.Helper()
		var updated Article
		if err := db.WithContext(ctx).Where("aid = ?", row.AID).Take(&updated).Error; err != nil {
			t.Fatal(err)
		}
		if updated.Comments != want {
			t.Fatalf("got %d comments, want %d", updated.Comments, want)
		}
	}
	event := func(oldCount int) []byte {
		return []byte(fmt.Sprintf(`{"aid":%q,"count":%d}`, row.AID, oldCount))
	}

	if err := d.handleCommentCountEvent(ctx, event(99)); err != nil {
		t.Fatal(err)
	}
	checkCount(1)

	writer := db.WithContext(ctx).Begin()
	if writer.Error != nil {
		t.Fatal(writer.Error)
	}
	defer writer.Rollback()
	var locked Article
	if err := writer.Clauses(clause.Locking{Strength: "NO KEY UPDATE"}).
		Select("aid").Where("aid = ?", row.AID).Take(&locked).Error; err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- d.handleCommentCountEvent(ctx, event(0))
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		err := db.WithContext(ctx).Raw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query LIKE '%"articles"%'
		)`).Scan(&blocked).Error
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the consumer never waited on the article lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := writer.Exec(insert, uuid.NewString(), row.AID, commentID).Error; err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	checkCount(2)

	if err := db.WithContext(ctx).Exec("UPDATE comments SET status = 'deleted' WHERE cid = ?", commentID).Error; err != nil {
		t.Fatal(err)
	}
	for _, oldCount := range []int{1, 0, 2, 2} {
		if err := d.handleCommentCountEvent(ctx, event(oldCount)); err != nil {
			t.Fatal(err)
		}
		checkCount(1)
	}
}

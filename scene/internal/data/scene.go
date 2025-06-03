package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"
	"gorm.io/gorm"

	"github.com/knoci/roaming-world/scene/internal/biz"
)

// Scene 场景模型
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

type sceneRepo struct {
	data *Data
	log  *log.Helper
}

// NewSceneRepo 创建场景仓库实例
func NewSceneRepo(data *Data, logger log.Logger) biz.SceneRepo {
	return &sceneRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

// CreateScene 创建场景
func (r *sceneRepo) CreateScene(ctx context.Context, s *biz.Scene) (*biz.Scene, error) {
	scene := &Scene{
		Name:     s.Name,
		Describe: s.Describe,
		View:     s.View,
		Location: s.Location,
		Article:  s.Article,
	}

	result := r.data.db.Create(scene)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("create scene error: %v", result.Error)
		error := r.data.SendErrorLog(ctx, "scene", result.Error.Error(), "db.Create", scene)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return nil, result.Error
	}

	viewJSON, _ := json.Marshal(scene.View)
	sql := `INSERT INTO scenes (sid, name, describe, view, location, article) VALUES ($1, $2, $3, $4, $5, $6)`
	params := []any{scene.SID, scene.Name, scene.Describe, string(viewJSON), scene.Location, scene.Article}
	error := r.data.SendSqlLog(ctx, "scene", sql, params)
	if error != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: kafka send sqllog error: %v", error)
	}

	// 添加到MeiliSearch索引
	r.updateMeiliSearchIndex(ctx, scene)

	// 保存到Redis
	r.saveSceneToRedis(ctx, scene)

	return &biz.Scene{
		SID:       scene.SID,
		Name:      scene.Name,
		Describe:  scene.Describe,
		View:      scene.View,
		Location:  scene.Location,
		Article:   scene.Article,
		CreatedAt: scene.CreatedAt,
		UpdatedAt: scene.UpdatedAt,
	}, nil
}

// DeleteScene 删除场景
func (r *sceneRepo) DeleteScene(ctx context.Context, sid string) error {
	result := r.data.db.Delete(&Scene{}, "sid = ?", sid)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("delete scene error: %v", result.Error)
		error := r.data.SendErrorLog(ctx, "scene", result.Error.Error(), "db.Delete", sid)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return result.Error
	}

	sql := `DELETE FROM scenes WHERE sid = $1`
	params := []any{sid}
	error := r.data.SendSqlLog(ctx, "scene", sql, params)
	if error != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: kafka send sqllog error: %v", error)
	}

	// 从MeiliSearch索引中删除
	r.deleteMeiliSearchDocument(ctx, sid)

	return nil
}

// UpdateScene 更新场景
func (r *sceneRepo) UpdateScene(ctx context.Context, s *biz.Scene) (*biz.Scene, error) {
	// 先查找现有的场景记录
	var existingScene Scene
	if err := r.data.db.Where("sid = ?", s.SID).First(&existingScene).Error; err != nil {
		r.log.WithContext(ctx).Errorf("find scene error: %v", err)
		return nil, err
	}

	// 只更新提供的字段
	updates := map[string]interface{}{}
	if s.Name != "" {
		updates["name"] = s.Name
		existingScene.Name = s.Name
	}
	if s.Describe != "" {
		updates["describe"] = s.Describe
		existingScene.Describe = s.Describe
	}
	if len(s.View) > 0 {
		updates["view"] = s.View
		existingScene.View = s.View
	}
	if s.Location != "" {
		updates["location"] = s.Location
		existingScene.Location = s.Location
	}
	if s.Article != "" {
		updates["article"] = s.Article
		existingScene.Article = s.Article
	}

	// 更新记录
	result := r.data.db.Model(&existingScene).Updates(updates)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("update scene error: %v", result.Error)
		error := r.data.SendErrorLog(ctx, "scene", result.Error.Error(), "db.Updates", updates)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return nil, result.Error
	}

	viewJSON, _ := json.Marshal(existingScene.View)
	sql := `UPDATE scenes SET name = $1, describe = $2, view = $3, location = $4, article = $5 WHERE sid = $6`
	params := []any{existingScene.Name, existingScene.Describe, string(viewJSON), existingScene.Location, existingScene.Article, existingScene.SID}
	error := r.data.SendSqlLog(ctx, "scene", sql, params)
	if error != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: kafka send sqllog error: %v", error)
	}

	// 更新MeiliSearch索引
	r.updateMeiliSearchIndex(ctx, &existingScene)

	return &biz.Scene{
		SID:       existingScene.SID,
		Name:      existingScene.Name,
		Describe:  existingScene.Describe,
		View:      existingScene.View,
		Location:  existingScene.Location,
		Article:   existingScene.Article,
		CreatedAt: existingScene.CreatedAt,
		UpdatedAt: existingScene.UpdatedAt,
	}, nil
}

// SearchScene 搜索场景 - 优先使用MeiliSearch，其次Redis，最后数据库
func (r *sceneRepo) SearchScene(ctx context.Context, keyword string, page int, pagesize int) ([]*biz.Scene, int, error) {
	// 如果关键词为空，返回错误
	if strings.TrimSpace(keyword) == "" {
		return nil, 0, biz.ErrInvalidArgument
	}

	r.log.WithContext(ctx).Infof("searching scenes with keyword: %s, page: %d, pagesize: %d", keyword, page, pagesize)

	// 计算偏移量
	offset := (page - 1) * pagesize
	if offset < 0 {
		offset = 0
	}

	// 1. 首先尝试使用MeiliSearch进行搜索
	scenes, totalHits, err := r.searchWithMeiliSearch(ctx, keyword, offset, pagesize)
	if err == nil {
		return scenes, totalHits, nil
	}

	// 2. 如果MeiliSearch搜索失败，尝试从Redis获取
	r.log.WithContext(ctx).Infof("MeiliSearch search failed, trying Redis: %v", err)
	scenes, totalHits, err = r.searchWithRedis(ctx, keyword, offset, pagesize)
	if err == nil {
		return scenes, totalHits, nil
	}

	// 3. 如果Redis也失败，最后回退到数据库搜索
	r.log.WithContext(ctx).Infof("Redis search failed, falling back to database: %v", err)
	return r.fallbackDBSearch(ctx, keyword, page, pagesize)
}

// fallbackDBSearch 数据库搜索回退方法
func (r *sceneRepo) fallbackDBSearch(ctx context.Context, keyword string, page int, pagesize int) ([]*biz.Scene, int, error) {
	r.log.WithContext(ctx).Infof("Falling back to database search: %s, page: %d, pagesize: %d", keyword, page, pagesize)

	// 按空格分割关键词
	keywords := strings.Fields(keyword)

	// 构建查询条件
	query := r.data.db.Model(&Scene{})
	var conditions []string
	var values []interface{}

	// 对每个关键词构建查询条件
	for _, kw := range keywords {
		condition := "(name LIKE ? OR describe LIKE ? OR location LIKE ?)"
		value := fmt.Sprintf("%%%s%%", kw)
		conditions = append(conditions, condition)
		values = append(values, value, value, value)
	}

	// 组合所有条件（OR关系）
	if len(conditions) > 0 {
		query = query.Where(strings.Join(conditions, " OR "), values...)
	}

	// 获取总记录数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		r.log.WithContext(ctx).Errorf("Count scenes error: %v", err)
		return nil, 0, err
	}

	// 计算分页
	offset := (page - 1) * pagesize
	if offset < 0 {
		offset = 0
	}

	// 获取分页数据
	var scenes []*Scene
	result := query.Offset(offset).Limit(pagesize).Find(&scenes)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("Search scene error: %v", result.Error)
		error := r.data.SendErrorLog(ctx, "scene", result.Error.Error(), "query.Offset(offset).Limit(pagesize).Find", nil)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return nil, 0, result.Error
	}

	r.log.WithContext(ctx).Infof("Database search found %d results (total: %d) for '%s'", len(scenes), total, keyword)

	return convertToBizScenes(scenes), int(total), nil
}

// ListScenes 获取场景列表
func (r *sceneRepo) ListScenes(ctx context.Context, page int, pagesize int) ([]*biz.Scene, int, error) {
	r.log.WithContext(ctx).Infof("ListScenes: page=%d, pagesize=%d", page, pagesize)

	// 计算偏移量
	offset := (page - 1) * pagesize
	if offset < 0 {
		offset = 0
	}

	// 尝试从Redis获取场景列表
	scenes, total, err := r.getScenesFromRedis(ctx, offset, pagesize)
	if err == nil && len(scenes) > 0 {
		r.log.WithContext(ctx).Infof("Retrieved %d scenes from Redis (total: %d)", len(scenes), total)
		return scenes, total, nil
	}

	// 如果Redis获取失败，从数据库获取
	r.log.WithContext(ctx).Infof("Falling back to database for scene listing")

	// 获取总记录数
	var total64 int64
	if err := r.data.db.Model(&Scene{}).Count(&total64).Error; err != nil {
		r.log.WithContext(ctx).Errorf("Count scenes error: %v", err)
		return nil, 0, err
	}

	// 获取分页数据
	var dbScenes []*Scene
	result := r.data.db.Offset(offset).Limit(pagesize).Find(&dbScenes)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("list scenes error: %v", result.Error)
		return nil, 0, result.Error
	}

	// 将结果保存到Redis
	go r.saveScenesListToRedis(context.Background(), dbScenes, int(total64))

	return convertToBizScenes(dbScenes), int(total64), nil
}

// GetSceneByID 根据ID获取场景
func (r *sceneRepo) GetSceneByID(ctx context.Context, sid string) (*biz.Scene, error) {
	var scene Scene
	result := r.data.db.Where("sid = ?", sid).First(&scene)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		r.log.WithContext(ctx).Errorf("get scene by id error: %v", result.Error)
		return nil, result.Error
	}
	return &biz.Scene{
		SID:       scene.SID,
		Name:      scene.Name,
		Describe:  scene.Describe,
		View:      scene.View,
		Location:  scene.Location,
		Article:   scene.Article,
		CreatedAt: scene.CreatedAt,
		UpdatedAt: scene.UpdatedAt,
	}, nil
}

// 转换为业务层场景列表
func convertToBizScenes(scenes []*Scene) []*biz.Scene {
	result := make([]*biz.Scene, 0, len(scenes))
	for _, s := range scenes {
		result = append(result, &biz.Scene{
			SID:       s.SID,
			Name:      s.Name,
			Describe:  s.Describe,
			View:      s.View,
			Location:  s.Location,
			Article:   s.Article,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
		})
	}
	return result
}

// updateMeiliSearchIndex 更新MeiliSearch索引
func (r *sceneRepo) updateMeiliSearchIndex(ctx context.Context, scene *Scene) {
	// 确保索引存在
	index := r.data.meili.Index("scenes")

	// 准备索引文档
	document := map[string]interface{}{
		"sid":        scene.SID,
		"name":       scene.Name,
		"describe":   scene.Describe,
		"location":   scene.Location,
		"article":    scene.Article,
		"view":       scene.View,
		"created_at": scene.CreatedAt.Format(time.RFC3339),
		"updated_at": scene.UpdatedAt.Format(time.RFC3339),
	}

	// 添加或更新文档
	_, err := index.AddDocuments([]map[string]interface{}{document}, "sid")
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to update MeiliSearch index: %v", scene.SID)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "index.AddDocuments", scene.SID)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return
	}

	r.log.WithContext(ctx).Infof("Successfully updated MeiliSearch index for scene: %s", scene.SID)
}

// deleteMeiliSearchDocument 从MeiliSearch索引中删除文档
func (r *sceneRepo) deleteMeiliSearchDocument(ctx context.Context, sid string) {
	// 获取索引
	index := r.data.meili.Index("scenes")

	// 删除文档
	_, err := index.DeleteDocument(sid)
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to delete document from MeiliSearch: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "index.DeleteDocument", sid)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return
	}

	r.log.WithContext(ctx).Infof("Successfully deleted document from MeiliSearch: %s", sid)
}

// saveSceneToRedis 将场景保存到Redis
func (r *sceneRepo) saveSceneToRedis(ctx context.Context, scene *Scene) {
	// 将场景转换为JSON
	sceneJSON, err := json.Marshal(scene)
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to marshal scene to JSON: %v", err)
		return
	}

	// 使用场景ID作为键，保存到Redis
	key := fmt.Sprintf("scene:%s", scene.SID)
	err = r.data.redis.Set(ctx, key, sceneJSON, 0).Err()
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to save scene to Redis: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "redis.Set", sceneJSON)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return
	}

	// 将场景ID添加到场景列表集合
	listKey := "scenes:list"
	err = r.data.redis.SAdd(ctx, listKey, scene.SID).Err()
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to add scene ID to Redis set: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "redis.SAdd", scene.SID)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return
	}

	// 更新场景总数
	r.updateSceneCountInRedis(ctx)

	r.log.WithContext(ctx).Infof("Successfully saved scene to Redis: %s", scene.SID)
}

// updateSceneCountInRedis 更新Redis中的场景总数
func (r *sceneRepo) updateSceneCountInRedis(ctx context.Context) {
	// 获取数据库中的场景总数
	var count int64
	if err := r.data.db.Model(&Scene{}).Count(&count).Error; err != nil {
		r.log.WithContext(ctx).Errorf("Failed to count scenes in database: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "db.Model(&Scene{}).Count", nil)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return
	}

	// 保存到Redis
	countKey := "scenes:count"
	err := r.data.redis.Set(ctx, countKey, count, 24*time.Hour).Err()
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to save scene count to Redis: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "redis.Set", count)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return
	}

	r.log.WithContext(ctx).Infof("Updated scene count in Redis: %d", count)
}

// getScenesFromRedis 从Redis获取场景列表
func (r *sceneRepo) getScenesFromRedis(ctx context.Context, offset int, limit int) ([]*biz.Scene, int, error) {
	// 获取场景总数
	countKey := "scenes:count"
	countCmd := r.data.redis.Get(ctx, countKey)
	if countCmd.Err() != nil {
		r.log.WithContext(ctx).Warnf("Failed to get scene count from Redis: %v", countCmd.Err())
		error := r.data.SendErrorLog(ctx, "scene", countCmd.Err().Error(), "redis.Get", countKey)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return nil, 0, countCmd.Err()
	}

	total, err := countCmd.Int()
	if err != nil {
		r.log.WithContext(ctx).Warnf("Failed to convert scene count to int: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "countCmd.Int()", nil)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return nil, 0, err
	}

	// 获取所有场景ID
	listKey := "scenes:list"
	sceneIDs, err := r.data.redis.SMembers(ctx, listKey).Result()
	if err != nil {
		r.log.WithContext(ctx).Warnf("Failed to get scene IDs from Redis: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "redis.SMembers", listKey)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return nil, 0, err
	}

	// 如果没有场景，返回空列表
	if len(sceneIDs) == 0 {
		return []*biz.Scene{}, 0, nil
	}

	// 应用分页
	end := offset + limit
	if end > len(sceneIDs) {
		end = len(sceneIDs)
	}
	if offset >= len(sceneIDs) {
		return []*biz.Scene{}, total, nil
	}

	pagedIDs := sceneIDs[offset:end]

	// 获取每个场景的详细信息
	var scenes []*biz.Scene
	for _, sid := range pagedIDs {
		key := fmt.Sprintf("scene:%s", sid)
		sceneJSON, err := r.data.redis.Get(ctx, key).Result()
		if err != nil {
			r.log.WithContext(ctx).Warnf("Failed to get scene from Redis: %v", err)
			error := r.data.SendErrorLog(ctx, "scene", err.Error(), "redis.Get", key)
			if error != nil {
				r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
			}
			continue
		}

		var scene Scene
		if err := json.Unmarshal([]byte(sceneJSON), &scene); err != nil {
			r.log.WithContext(ctx).Warnf("Failed to unmarshal scene JSON: %v", err)
			continue
		}

		scenes = append(scenes, &biz.Scene{
			SID:       scene.SID,
			Name:      scene.Name,
			Describe:  scene.Describe,
			View:      scene.View,
			Location:  scene.Location,
			Article:   scene.Article,
			CreatedAt: scene.CreatedAt,
			UpdatedAt: scene.UpdatedAt,
		})
	}

	return scenes, total, nil
}

// saveScenesListToRedis 将场景列表保存到Redis
func (r *sceneRepo) saveScenesListToRedis(ctx context.Context, scenes []*Scene, total int) {
	// 保存每个场景
	for _, scene := range scenes {
		r.saveSceneToRedis(ctx, scene)
	}

	// 保存场景总数
	countKey := "scenes:count"
	err := r.data.redis.Set(ctx, countKey, total, 24*time.Hour).Err()
	if err != nil {
		r.log.WithContext(ctx).Errorf("Failed to save scene count to Redis: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "redis.Set", countKey)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: : kafka send errorlog error: %v", error)
		}
		return
	}

	r.log.WithContext(ctx).Infof("Successfully saved %d scenes to Redis (total: %d)", len(scenes), total)
}

// searchWithMeiliSearch 使用MeiliSearch进行搜索
func (r *sceneRepo) searchWithMeiliSearch(ctx context.Context, keyword string, offset int, pagesize int) ([]*biz.Scene, int, error) {
	r.log.WithContext(ctx).Infof("Searching with MeiliSearch: %s", keyword)

	// 使用MeiliSearch进行搜索
	index := r.data.meili.Index("scenes")
	searchRes, err := index.Search(keyword, &meilisearch.SearchRequest{
		Limit:  int64(pagesize),
		Offset: int64(offset),
	})

	if err != nil {
		r.log.WithContext(ctx).Errorf("MeiliSearch search error: %v", err)
		error := r.data.SendErrorLog(ctx, "scene", err.Error(), "index.Search", keyword)
		if error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: kafka send errorlog error: %v", error)
		}
		return nil, 0, err
	}

	// 获取命中总数
	totalHits := searchRes.EstimatedTotalHits
	r.log.WithContext(ctx).Infof("MeiliSearch found %d results for '%s'", totalHits, keyword)

	// 处理搜索结果
	var bizScenes []*biz.Scene
	for _, hit := range searchRes.Hits {
		// 将搜索结果转换为Scene对象
		sceneMap, ok := hit.(map[string]interface{})
		if !ok {
			r.log.WithContext(ctx).Warnf("Invalid search result format")
			continue
		}

		// 获取场景ID和其他字段
		sid, ok := sceneMap["sid"].(string)
		if !ok || sid == "" {
			continue
		}

		// 直接从MeiliSearch结果构建场景对象
		name, _ := sceneMap["name"].(string)
		describe, _ := sceneMap["describe"].(string)
		location, _ := sceneMap["location"].(string)
		article, _ := sceneMap["article"].(string)
		updatedAtStr, _ := sceneMap["updated_at"].(string)
		createdAtStr, _ := sceneMap["created_at"].(string)

		// 解析时间
		var updatedAt, createdAt time.Time
		if updatedAtStr != "" {
			updatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
		}
		if createdAtStr != "" {
			createdAt, _ = time.Parse(time.RFC3339, createdAtStr)
		}

		// 获取View字段
		var viewSlice []string
		if viewInterface, ok := sceneMap["view"]; ok {
			// 尝试将view字段转换为字符串切片
			if viewArray, ok := viewInterface.([]interface{}); ok {
				for _, v := range viewArray {
					if strVal, ok := v.(string); ok {
						viewSlice = append(viewSlice, strVal)
					}
				}
			}
		}

		// 创建场景对象
		scene := &biz.Scene{
			SID:       sid,
			Name:      name,
			Describe:  describe,
			Location:  location,
			Article:   article,
			View:      viewSlice,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}

		// 如果MeiliSearch中缺少某些字段，尝试从Redis补充
		if len(viewSlice) == 0 || createdAt.IsZero() || updatedAt.IsZero() {
			key := fmt.Sprintf("scene:%s", sid)
			sceneJSON, err := r.data.redis.Get(ctx, key).Result()
			if err == nil {
				// 从Redis成功获取
				var redisScene Scene
				if err := json.Unmarshal([]byte(sceneJSON), &redisScene); err != nil {
					r.log.WithContext(ctx).Warnf("Failed to unmarshal scene JSON from Redis: %v", err)
				} else {
					// 只补充MeiliSearch中没有的字段
					if len(viewSlice) == 0 {
						scene.View = redisScene.View
					}
					if createdAt.IsZero() {
						scene.CreatedAt = redisScene.CreatedAt
					}
					if updatedAt.IsZero() {
						scene.UpdatedAt = redisScene.UpdatedAt
					}
				}
			} else if len(viewSlice) == 0 || createdAt.IsZero() || updatedAt.IsZero() {
				// 如果Redis获取失败且仍有缺失字段，从数据库获取
				var dbScene Scene
				if err := r.data.db.Where("sid = ?", sid).First(&dbScene).Error; err != nil {
					r.log.WithContext(ctx).Warnf("Failed to get scene from DB: %v", err)
				} else {
					// 只补充MeiliSearch中没有的字段
					if len(viewSlice) == 0 {
						scene.View = dbScene.View
					}
					if createdAt.IsZero() {
						scene.CreatedAt = dbScene.CreatedAt
					}
					if updatedAt.IsZero() {
						scene.UpdatedAt = dbScene.UpdatedAt
					}

					// 将获取到的场景保存到Redis以便下次使用
					go r.saveSceneToRedis(context.Background(), &dbScene)

					// 更新MeiliSearch索引，确保下次能直接从MeiliSearch获取完整信息
					go r.updateMeiliSearchIndex(context.Background(), &dbScene)
				}
			}
		}

		bizScenes = append(bizScenes, scene)
	}

	return bizScenes, int(totalHits), nil
}

// searchWithRedis 使用Redis进行搜索
func (r *sceneRepo) searchWithRedis(ctx context.Context, keyword string, offset int, pagesize int) ([]*biz.Scene, int, error) {
	r.log.WithContext(ctx).Infof("Searching with Redis: %s", keyword)

	// 获取所有场景ID
	listKey := "scenes:list"
	sceneIDs, err := r.data.redis.SMembers(ctx, listKey).Result()
	if err != nil {
		r.log.WithContext(ctx).Warnf("Failed to get scene IDs from Redis: %v", err)
		return nil, 0, err
	}

	// 如果没有场景，返回空列表
	if len(sceneIDs) == 0 {
		return []*biz.Scene{}, 0, fmt.Errorf("no scenes found in Redis")
	}

	// 获取每个场景的详细信息并进行关键词过滤
	var matchedScenes []*biz.Scene
	var totalMatches int

	for _, sid := range sceneIDs {
		key := fmt.Sprintf("scene:%s", sid)
		sceneJSON, err := r.data.redis.Get(ctx, key).Result()
		if err != nil {
			r.log.WithContext(ctx).Warnf("Failed to get scene from Redis: %v", err)
			continue
		}

		var scene Scene
		if err := json.Unmarshal([]byte(sceneJSON), &scene); err != nil {
			r.log.WithContext(ctx).Warnf("Failed to unmarshal scene JSON: %v", err)
			continue
		}

		// 检查场景是否匹配关键词
		keywordLower := strings.ToLower(keyword)
		if strings.Contains(strings.ToLower(scene.Name), keywordLower) ||
			strings.Contains(strings.ToLower(scene.Describe), keywordLower) ||
			strings.Contains(strings.ToLower(scene.Location), keywordLower) ||
			strings.Contains(strings.ToLower(scene.Article), keywordLower) {

			totalMatches++

			// 应用分页
			if totalMatches > offset && len(matchedScenes) < pagesize {
				matchedScenes = append(matchedScenes, &biz.Scene{
					SID:       scene.SID,
					Name:      scene.Name,
					Describe:  scene.Describe,
					View:      scene.View,
					Location:  scene.Location,
					Article:   scene.Article,
					CreatedAt: scene.CreatedAt,
					UpdatedAt: scene.UpdatedAt,
				})
			}
		}
	}

	if len(matchedScenes) == 0 {
		return nil, 0, fmt.Errorf("no matching scenes found in Redis for keyword: %s", keyword)
	}

	r.log.WithContext(ctx).Infof("Redis search found %d results (total: %d) for '%s'", len(matchedScenes), totalMatches, keyword)
	return matchedScenes, totalMatches, nil
}

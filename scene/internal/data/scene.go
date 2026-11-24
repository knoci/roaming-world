package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/knoci/roaming-world/scene/internal/biz"
)

const (
	sceneListVersionKey     = "scene:list:ver"
	defaultSceneListVersion = "1"
	sceneDetailKeyPrefix    = "scene:detail:"
	sceneNotFoundSentinel   = "__NOT_FOUND__"

	sceneListCacheMinTTL   = 10 * time.Minute
	sceneListCacheMaxTTL   = 15 * time.Minute
	sceneDetailCacheMinTTL = 15 * time.Minute
	sceneDetailCacheMaxTTL = 20 * time.Minute
	sceneNotFoundTTL       = 60 * time.Second
)

// Scene 场景模型
type Scene struct {
	SID       string    `gorm:"primaryKey;type:varchar(36);column:sid" json:"sid"`
	Name      string    `gorm:"type:varchar(100);not null" json:"name"`
	Describe  string    `gorm:"type:varchar(400);not null" json:"describe"`
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

type cachedScenePage struct {
	Items []*Scene `json:"items"`
	Total int      `json:"total"`
}

type sceneRepo struct {
	data *Data
	log  *log.Helper
	sf   singleflight.Group
}

// NewSceneRepo 创建场景仓库实例
func NewSceneRepo(data *Data, logger log.Logger) biz.SceneRepo {
	return &sceneRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

func sceneListCacheKey(ver string, page, pageSize int) string {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		ver = defaultSceneListVersion
	}
	return fmt.Sprintf("scene:list:v%s:p%d:s%d", ver, page, pageSize)
}

func sceneDetailCacheKey(sid string) string {
	return sceneDetailKeyPrefix + sid
}

// escapeLikePattern escapes PostgreSQL LIKE/ILIKE metacharacters (\, %, _).
func escapeLikePattern(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func jitteredTTL(minTTL, maxTTL time.Duration) time.Duration {
	if maxTTL <= minTTL {
		return minTTL
	}
	spreadSec := int((maxTTL - minTTL) / time.Second)
	if spreadSec <= 0 {
		return minTTL
	}
	return minTTL + time.Duration(rand.Intn(spreadSec+1))*time.Second
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

	result := r.data.db.WithContext(ctx).Create(scene)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("create scene error: %v", result.Error)
		if logErr := r.data.LogError(ctx, "scene", result.Error.Error(), "db.Create", scene); logErr != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
		}
		return nil, result.Error
	}

	viewJSON, _ := json.Marshal(scene.View)
	sql := `INSERT INTO scenes (sid, name, describe, view, location, article) VALUES ($1, $2, $3, $4, $5, $6)`
	params := []any{scene.SID, scene.Name, scene.Describe, string(viewJSON), scene.Location, scene.Article}
	if logErr := r.data.LogSQLAudit(ctx, "scene", sql, params); logErr != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: log sql audit failed: %v", logErr)
	}

	// 添加到 MeiliSearch 索引
	r.updateMeiliSearchIndex(ctx, scene)

	// O(1) 递增分页列表缓存版本，并清理可能存在的同 SID 负缓存
	r.invalidateSceneCache(ctx, scene.SID)

	return convertToBizScene(scene), nil
}

// DeleteScene 删除场景
func (r *sceneRepo) DeleteScene(ctx context.Context, sid string) error {
	result := r.data.db.WithContext(ctx).Delete(&Scene{}, "sid = ?", sid)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: delete scene error: %v", result.Error)
		if logErr := r.data.LogError(ctx, "scene", result.Error.Error(), "db.Delete", sid); logErr != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrSceneNotFound
	}

	sql := `DELETE FROM scenes WHERE sid = $1`
	params := []any{sid}
	if logErr := r.data.LogSQLAudit(ctx, "scene", sql, params); logErr != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: log sql audit failed: %v", logErr)
	}

	// 从 MeiliSearch 索引中删除
	r.deleteMeiliSearchDocument(ctx, sid)
	// 删除详情缓存并递增列表版本号
	r.invalidateSceneCache(ctx, sid)

	return nil
}

// UpdateScene 更新场景
func (r *sceneRepo) UpdateScene(ctx context.Context, s *biz.Scene) (*biz.Scene, error) {
	var existingScene Scene
	if err := r.data.db.WithContext(ctx).Where("sid = ?", s.SID).First(&existingScene).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSceneNotFound
		}
		r.log.WithContext(ctx).Errorf("sceneRepo: find scene error: %v", err)
		return nil, err
	}

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

	if len(updates) > 0 {
		result := r.data.db.WithContext(ctx).Model(&existingScene).Updates(updates)
		if result.Error != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: update scene error: %v", result.Error)
			if logErr := r.data.LogError(ctx, "scene", result.Error.Error(), "db.Updates", updates); logErr != nil {
				r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
			}
			return nil, result.Error
		}
	}

	viewJSON, _ := json.Marshal(existingScene.View)
	sql := `UPDATE scenes SET name = $1, describe = $2, view = $3, location = $4, article = $5 WHERE sid = $6`
	params := []any{existingScene.Name, existingScene.Describe, string(viewJSON), existingScene.Location, existingScene.Article, existingScene.SID}
	if logErr := r.data.LogSQLAudit(ctx, "scene", sql, params); logErr != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: log sql audit failed: %v", logErr)
	}

	// 更新 MeiliSearch 索引
	r.updateMeiliSearchIndex(ctx, &existingScene)
	// 删除详情缓存并递增列表版本号
	r.invalidateSceneCache(ctx, existingScene.SID)

	return convertToBizScene(&existingScene), nil
}

// SearchScene 搜索场景 - 优先使用 MeiliSearch，失败时回退到 PostgreSQL ILIKE 搜索
func (r *sceneRepo) SearchScene(ctx context.Context, keyword string, page int, pagesize int) ([]*biz.Scene, int, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, 0, biz.ErrInvalidArgument
	}

	page, pagesize = biz.NormalizePagination(page, pagesize)
	r.log.WithContext(ctx).Infof("sceneRepo: searching scenes with keyword: %s, page: %d, pagesize: %d", keyword, page, pagesize)

	offset := (page - 1) * pagesize
	if offset < 0 {
		offset = 0
	}

	// 1. 优先尝试使用 MeiliSearch 搜索
	if r.data.meili != nil {
		scenes, totalHits, err := r.searchWithMeiliSearch(ctx, keyword, offset, pagesize)
		if err == nil {
			return scenes, totalHits, nil
		}
		r.log.WithContext(ctx).Infof("sceneRepo: MeiliSearch search failed, falling back to database: %v", err)
	}

	// 2. 回退到数据库搜索
	return r.fallbackDBSearch(ctx, keyword, page, pagesize)
}

// fallbackDBSearch 数据库搜索回退方法（使用 PostgreSQL ILIKE 并转义通配符）
func (r *sceneRepo) fallbackDBSearch(ctx context.Context, keyword string, page int, pagesize int) ([]*biz.Scene, int, error) {
	r.log.WithContext(ctx).Infof("sceneRepo: Falling back to database search: %s, page: %d, pagesize: %d", keyword, page, pagesize)

	keywords := strings.Fields(keyword)
	query := r.data.db.WithContext(ctx).Model(&Scene{})
	var conditions []string
	var values []interface{}

	for _, kw := range keywords {
		condition := `(name ILIKE ? ESCAPE '\' OR describe ILIKE ? ESCAPE '\' OR location ILIKE ? ESCAPE '\' OR article ILIKE ? ESCAPE '\')`
		value := fmt.Sprintf("%%%s%%", escapeLikePattern(kw))
		conditions = append(conditions, condition)
		values = append(values, value, value, value, value)
	}

	if len(conditions) > 0 {
		query = query.Where(strings.Join(conditions, " OR "), values...)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: Count scenes error: %v", err)
		return nil, 0, err
	}

	offset := (page - 1) * pagesize
	if offset < 0 {
		offset = 0
	}

	var scenes []*Scene
	result := query.Order("created_at DESC, sid DESC").Offset(offset).Limit(pagesize).Find(&scenes)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: Search scene error: %v", result.Error)
		if logErr := r.data.LogError(ctx, "scene", result.Error.Error(), "query.Offset(offset).Limit(pagesize).Find", nil); logErr != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
		}
		return nil, 0, result.Error
	}

	r.log.WithContext(ctx).Infof("sceneRepo: Database search found %d results (total: %d) for '%s'", len(scenes), total, keyword)
	return convertToBizScenes(scenes), int(total), nil
}

// ListScenes 获取场景列表（版本化分页缓存 + singleflight 防击穿）
func (r *sceneRepo) ListScenes(ctx context.Context, page int, pagesize int) ([]*biz.Scene, int, error) {
	page, pagesize = biz.NormalizePagination(page, pagesize)
	r.log.WithContext(ctx).Infof("sceneRepo: ListScenes: page=%d, pagesize=%d", page, pagesize)

	ver := r.getSceneListVersion(ctx)
	cacheKey := sceneListCacheKey(ver, page, pagesize)

	// 1. 尝试从版本化分页缓存获取
	if r.data.redis != nil {
		if raw, err := r.data.redis.Get(ctx, cacheKey).Result(); err == nil {
			var cached cachedScenePage
			if unmarshalErr := json.Unmarshal([]byte(raw), &cached); unmarshalErr == nil {
				return convertToBizScenes(cached.Items), cached.Total, nil
			}
		}
	}

	// 2. 缓存未命中：使用 singleflight 合并并发回源请求
	val, err, _ := r.sf.Do(cacheKey, func() (interface{}, error) {
		sfCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		var total64 int64
		if err := r.data.db.WithContext(sfCtx).Model(&Scene{}).Count(&total64).Error; err != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: Count scenes error: %v", err)
			return nil, err
		}

		offset := (page - 1) * pagesize
		var dbScenes []*Scene
		if err := r.data.db.WithContext(sfCtx).
			Order("created_at DESC, sid DESC").
			Offset(offset).
			Limit(pagesize).
			Find(&dbScenes).Error; err != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: list scenes error: %v", err)
			return nil, err
		}

		pagePayload := cachedScenePage{
			Items: dbScenes,
			Total: int(total64),
		}
		if r.data.redis != nil {
			if encoded, marshalErr := json.Marshal(pagePayload); marshalErr == nil {
				ttl := jitteredTTL(sceneListCacheMinTTL, sceneListCacheMaxTTL)
				if setErr := r.data.redis.Set(sfCtx, cacheKey, encoded, ttl).Err(); setErr != nil {
					r.log.WithContext(ctx).Warnf("sceneRepo: failed to cache scene page %s: %v", cacheKey, setErr)
				}
			}
		}

		return pagePayload, nil
	})
	if err != nil {
		return nil, 0, err
	}

	pagePayload := val.(cachedScenePage)
	return convertToBizScenes(pagePayload.Items), pagePayload.Total, nil
}

// GetSceneByID 根据 ID 获取场景（Cache-Aside + singleflight + 空值负缓存防穿透）
func (r *sceneRepo) GetSceneByID(ctx context.Context, sid string) (*biz.Scene, error) {
	detailKey := sceneDetailCacheKey(sid)

	// 1. 先查 Redis 详情缓存
	if r.data.redis != nil {
		if raw, err := r.data.redis.Get(ctx, detailKey).Result(); err == nil {
			if raw == sceneNotFoundSentinel {
				return nil, nil
			}
			var cached Scene
			if unmarshalErr := json.Unmarshal([]byte(raw), &cached); unmarshalErr == nil {
				return convertToBizScene(&cached), nil
			}
		}
	}

	// 2. 缓存未命中：使用 singleflight 回源查询数据库
	val, err, _ := r.sf.Do(detailKey, func() (interface{}, error) {
		sfCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		var scene Scene
		err := r.data.db.WithContext(sfCtx).Where("sid = ?", sid).First(&scene).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if r.data.redis != nil {
					if setErr := r.data.redis.Set(sfCtx, detailKey, sceneNotFoundSentinel, sceneNotFoundTTL).Err(); setErr != nil {
						r.log.WithContext(ctx).Warnf("sceneRepo: failed to cache negative sentinel for %s: %v", sid, setErr)
					}
				}
				return (*Scene)(nil), nil
			}
			r.log.WithContext(ctx).Errorf("sceneRepo: get scene by id error: %v", err)
			return nil, err
		}

		if r.data.redis != nil {
			if encoded, marshalErr := json.Marshal(&scene); marshalErr == nil {
				ttl := jitteredTTL(sceneDetailCacheMinTTL, sceneDetailCacheMaxTTL)
				if setErr := r.data.redis.Set(sfCtx, detailKey, encoded, ttl).Err(); setErr != nil {
					r.log.WithContext(ctx).Warnf("sceneRepo: failed to cache scene detail %s: %v", sid, setErr)
				}
			}
		}

		return &scene, nil
	})
	if err != nil {
		return nil, err
	}

	scene, ok := val.(*Scene)
	if !ok || scene == nil {
		return nil, nil
	}
	return convertToBizScene(scene), nil
}

func (r *sceneRepo) getSceneListVersion(ctx context.Context) string {
	if r.data.redis == nil {
		return defaultSceneListVersion
	}
	ver, err := r.data.redis.Get(ctx, sceneListVersionKey).Result()
	if err != nil || strings.TrimSpace(ver) == "" {
		return defaultSceneListVersion
	}
	return strings.TrimSpace(ver)
}

func (r *sceneRepo) invalidateSceneCache(ctx context.Context, sid string) {
	if r.data.redis == nil {
		return
	}
	pipe := r.data.redis.Pipeline()
	if sid != "" {
		pipe.Del(ctx, sceneDetailCacheKey(sid))
	}
	pipe.Incr(ctx, sceneListVersionKey)
	if _, err := pipe.Exec(ctx); err != nil {
		r.log.WithContext(ctx).Warnf("sceneRepo: invalidate scene cache failed: %v", err)
	}
}

func convertToBizScene(s *Scene) *biz.Scene {
	if s == nil {
		return nil
	}
	var viewCopy []string
	if s.View != nil {
		viewCopy = make([]string, len(s.View))
		copy(viewCopy, s.View)
	}
	return &biz.Scene{
		SID:       s.SID,
		Name:      s.Name,
		Describe:  s.Describe,
		View:      viewCopy,
		Location:  s.Location,
		Article:   s.Article,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// 转换为业务层场景列表
func convertToBizScenes(scenes []*Scene) []*biz.Scene {
	result := make([]*biz.Scene, 0, len(scenes))
	for _, s := range scenes {
		result = append(result, convertToBizScene(s))
	}
	return result
}

// updateMeiliSearchIndex 更新 MeiliSearch 索引
func (r *sceneRepo) updateMeiliSearchIndex(ctx context.Context, scene *Scene) {
	if r.data.meili == nil {
		return
	}
	index := r.data.meili.Index("scenes")
	document := buildMeiliSceneDocument(scene)

	_, err := index.AddDocuments([]map[string]interface{}{document}, "sid")
	if err != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: Failed to update MeiliSearch index: %s (%v)", scene.SID, err)
		if logErr := r.data.LogError(ctx, "scene", err.Error(), "index.AddDocuments", scene.SID); logErr != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
		}
		return
	}

	r.log.WithContext(ctx).Infof("sceneRepo: Successfully updated MeiliSearch index for scene: %s", scene.SID)
}

// deleteMeiliSearchDocument 从 MeiliSearch 索引中删除文档
func (r *sceneRepo) deleteMeiliSearchDocument(ctx context.Context, sid string) {
	if r.data.meili == nil {
		return
	}
	index := r.data.meili.Index("scenes")

	_, err := index.DeleteDocument(sid)
	if err != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: Failed to delete document from MeiliSearch: %v", err)
		if logErr := r.data.LogError(ctx, "scene", err.Error(), "index.DeleteDocument", sid); logErr != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
		}
		return
	}

	r.log.WithContext(ctx).Infof("sceneRepo: Successfully deleted document from MeiliSearch: %s", sid)
}

// searchWithMeiliSearch 使用 MeiliSearch 进行只读搜索（无读路径回写副作用）
func (r *sceneRepo) searchWithMeiliSearch(ctx context.Context, keyword string, offset int, pagesize int) ([]*biz.Scene, int, error) {
	r.log.WithContext(ctx).Infof("sceneRepo: Searching with MeiliSearch: %s", keyword)

	index := r.data.meili.Index("scenes")
	searchRes, err := index.Search(keyword, &meilisearch.SearchRequest{
		Limit:  int64(pagesize),
		Offset: int64(offset),
	})
	if err != nil {
		r.log.WithContext(ctx).Errorf("sceneRepo: MeiliSearch search error: %v", err)
		if logErr := r.data.LogError(ctx, "scene", err.Error(), "index.Search", keyword); logErr != nil {
			r.log.WithContext(ctx).Errorf("sceneRepo: log error failed: %v", logErr)
		}
		return nil, 0, err
	}

	totalHits := searchRes.EstimatedTotalHits
	r.log.WithContext(ctx).Infof("sceneRepo: MeiliSearch found %d results for '%s'", totalHits, keyword)

	bizScenes := make([]*biz.Scene, 0, len(searchRes.Hits))
	for _, hit := range searchRes.Hits {
		sceneMap, ok := hit.(map[string]interface{})
		if !ok {
			r.log.WithContext(ctx).Warnf("sceneRepo: Invalid search result format")
			continue
		}

		sid, ok := sceneMap["sid"].(string)
		if !ok || sid == "" {
			continue
		}

		name, _ := sceneMap["name"].(string)
		describe, _ := sceneMap["describe"].(string)
		location, _ := sceneMap["location"].(string)
		article, _ := sceneMap["article"].(string)
		updatedAtStr, _ := sceneMap["updated_at"].(string)
		createdAtStr, _ := sceneMap["created_at"].(string)

		var updatedAt, createdAt time.Time
		if updatedAtStr != "" {
			updatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
		}
		if createdAtStr != "" {
			createdAt, _ = time.Parse(time.RFC3339, createdAtStr)
		}

		var viewSlice []string
		if viewInterface, ok := sceneMap["view"]; ok {
			if viewArray, ok := viewInterface.([]interface{}); ok {
				for _, v := range viewArray {
					if strVal, ok := v.(string); ok {
						viewSlice = append(viewSlice, strVal)
					}
				}
			}
		}

		bizScenes = append(bizScenes, &biz.Scene{
			SID:       sid,
			Name:      name,
			Describe:  describe,
			Location:  location,
			Article:   article,
			View:      viewSlice,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	return bizScenes, int(totalHits), nil
}

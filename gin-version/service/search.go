package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"travel-world/initialize"
	"travel-world/model"

	"github.com/elastic/go-elasticsearch/v8"
)

type SearchService struct{}

type ArticleES struct {
	AID     string `json:"aid"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Name    string `json:"name"`
}

type SceneES struct {
	SID      string `json:"sid"`
	Name     string `json:"name"`
	Describe string `json:"describe"`
	Location string `json:"location"`
}

// 搜索文章返回结构化结果
func (s *SearchService) SearchArticles(es *elasticsearch.Client, query string) ([]model.Article, error) {
	var buf bytes.Buffer
	searchQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  query,
				"fields": []string{"title^3", "content^2", "name^1"},
				"type":   "most_fields",
			},
		},
	}

	if err := json.NewEncoder(&buf).Encode(searchQuery); err != nil {
		return nil, err
	}

	res, err := es.Search(
		es.Search.WithContext(context.Background()),
		es.Search.WithIndex("articles_index"),
		es.Search.WithBody(&buf),
		es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("搜索错误: %s", res.String())
	}

	var response struct {
		Hits struct {
			Hits []struct {
				Source ArticleES `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, err
	}

	// 收集所有文章ID
	aids := make([]string, len(response.Hits.Hits))
	for i, hit := range response.Hits.Hits {
		aids[i] = hit.Source.AID
	}

	// 如果没有搜索结果，直接返回空数组
	if len(aids) == 0 {
		return []model.Article{}, nil
	}

	// 从数据库中批量查询文章
	var articles []model.Article
	if err := initialize.DB.Where("aid IN ?", aids).Find(&articles).Error; err != nil {
		return nil, err
	}

	return articles, nil
}

// 场景搜索
func (s *SearchService) SearchScenes(es *elasticsearch.Client, query string) ([]model.Scene, error) {
	var buf bytes.Buffer
	searchQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": map[string]interface{}{
					"multi_match": map[string]interface{}{
						"query":  query,
						"fields": []string{"name^3", "describe^2", "location^1"},
						"type":   "best_fields",
					},
				},
			},
		},
	}

	if err := json.NewEncoder(&buf).Encode(searchQuery); err != nil {
		return nil, err
	}

	res, err := es.Search(
		es.Search.WithContext(context.Background()),
		es.Search.WithIndex("scenes_index"),
		es.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("场景搜索错误: %s", res.String())
	}

	var response struct {
		Hits struct {
			Hits []struct {
				Source SceneES `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, err
	}

	// 收集所有场景ID
	sids := make([]string, len(response.Hits.Hits))
	for i, hit := range response.Hits.Hits {
		sids[i] = hit.Source.SID
	}

	// 如果没有搜索结果，直接返回空数组
	if len(sids) == 0 {
		return []model.Scene{}, nil
	}

	// 从数据库中批量查询场景
	var scenes []model.Scene
	if err := initialize.DB.Where("sid IN ?", sids).Find(&scenes).Error; err != nil {
		return nil, err
	}

	return scenes, nil
}

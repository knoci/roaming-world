package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"travel-world/model"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

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

// 插入单篇文章到ES
func InsertArticle(es *elasticsearch.Client, art model.Article) error {
	article := ArticleES{
		AID:     art.AID,
		Title:   art.Title,
		Content: art.Content,
		Name:    art.Name,
	}
	body, err := json.Marshal(article)
	if err != nil {
		return fmt.Errorf("序列化错误: %w", err)
	}

	req := esapi.IndexRequest{
		Index:      "articles_index",
		DocumentID: fmt.Sprintf("%s", article.AID),
		Body:       bytes.NewReader(body),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return fmt.Errorf("插入请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("插入错误: %s", res.String())
	}
	return nil
}

// 插入场景到ES
func InsertScene(es *elasticsearch.Client, sce model.Scene) error {
	scene := SceneES{
		SID:      sce.SID,
		Name:     sce.Name,
		Describe: sce.Describe,
		Location: sce.Location,
	}

	body, err := json.Marshal(scene)
	if err != nil {
		return fmt.Errorf("场景序列化错误: %w", err)
	}

	req := esapi.IndexRequest{
		Index:      "scenes_index",
		DocumentID: fmt.Sprintf("%s", scene.SID),
		Body:       bytes.NewReader(body),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return fmt.Errorf("场景插入请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("场景插入错误: %s", res.String())
	}
	return nil
}

// DeleteArticle 从ES中删除文章
func DeleteArticle(es *elasticsearch.Client, aid string) error {
	req := esapi.DeleteRequest{
		Index:      "articles_index",
		DocumentID: aid,
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return fmt.Errorf("删除文章请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("删除文章错误: %s", res.String())
	}
	return nil
}

// DeleteScene 从ES中删除场景
func DeleteScene(es *elasticsearch.Client, sid string) error {
	req := esapi.DeleteRequest{
		Index:      "scenes_index",
		DocumentID: sid,
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return fmt.Errorf("删除场景请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("删除场景错误: %s", res.String())
	}
	return nil
}

// UpdateArticle 更新ES中的文章
func UpdateArticle(es *elasticsearch.Client, art model.Article) error {
	article := ArticleES{
		AID:     art.AID,
		Title:   art.Title,
		Content: art.Content,
		Name:    art.Name,
	}
	body, err := json.Marshal(article)
	if err != nil {
		return fmt.Errorf("序列化错误: %w", err)
	}

	req := esapi.UpdateRequest{
		Index:      "articles_index",
		DocumentID: fmt.Sprintf("%s", article.AID),
		Body:       bytes.NewReader(bytes.NewBuffer([]byte(fmt.Sprintf(`{"doc":%s}`, body))).Bytes()),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return fmt.Errorf("更新文章请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("更新文章错误: %s", res.String())
	}
	return nil
}

// UpdateScene 更新ES中的场景
func UpdateScene(es *elasticsearch.Client, sce model.Scene) error {
	scene := SceneES{
		SID:      sce.SID,
		Name:     sce.Name,
		Describe: sce.Describe,
		Location: sce.Location,
	}

	body, err := json.Marshal(scene)
	if err != nil {
		return fmt.Errorf("场景序列化错误: %w", err)
	}

	req := esapi.UpdateRequest{
		Index:      "scenes_index",
		DocumentID: fmt.Sprintf("%s", scene.SID),
		Body:       bytes.NewReader(bytes.NewBuffer([]byte(fmt.Sprintf(`{"doc":%s}`, body))).Bytes()),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return fmt.Errorf("更新场景请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("更新场景错误: %s", res.String())
	}
	return nil
}

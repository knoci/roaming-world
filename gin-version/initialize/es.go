package initialize

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/spf13/viper"

	"github.com/elastic/go-elasticsearch/v8"
	"go.uber.org/zap"
)

var ESClient *elasticsearch.Client

func InitES() error {
	hosts := fmt.Sprintf("http://%s:%d", viper.GetString("es.host"), viper.GetInt("es.port"))

	// ES客户端配置
	cfg := elasticsearch.Config{
		Addresses:     []string{hosts},
		Username:      "elastic",
		Password:      "123456",
		RetryOnStatus: []int{502, 503, 504},
		MaxRetries:    3,
		RetryBackoff: func(i int) time.Duration {
			return time.Duration(i) * time.Second
		},
	}

	// 创建ES客户端
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		Logger.Error("创建ES客户端失败", zap.Error(err))
		return fmt.Errorf("创建ES客户端失败: %v", err)
	}

	// 测试ES连接
	res, err := client.Info()
	if err != nil {
		Logger.Error("ES连接测试失败", zap.Error(err))
		return fmt.Errorf("ES连接测试失败: %v", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		Logger.Error("ES连接异常", zap.String("status", res.Status()))
		return fmt.Errorf("ES连接异常: %s", res.Status())
	}

	ESClient = client

	// 创建文章索引
	if err := CreateIndex(ESClient); err != nil {
		Logger.Error("创建文章索引失败", zap.Error(err))
		return fmt.Errorf("创建文章索引失败: %v", err)
	}

	// 创建景点索引
	if err := CreateSceneIndex(ESClient); err != nil {
		Logger.Error("创建景点索引失败", zap.Error(err))
		return fmt.Errorf("创建景点索引失败: %v", err)
	}

	Logger.Info("ES初始化成功")
	return nil
}

// 创建场景索引
func CreateSceneIndex(es *elasticsearch.Client) error {
	mapping := `{
		"settings": {
			"analysis": {
				"analyzer": {
					"default": {
						"type": "ik_smart"
					}
				}
			}
		},
		"mappings": {
			"properties": {
				"name": {
					"type": "text",
					"analyzer": "ik_smart",
					"search_analyzer": "ik_smart"
				},
				"describe": {
					"type": "text",
					"analyzer": "ik_smart",
					"search_analyzer": "ik_smart"
				},
				"location": {
					"type": "text",
					"analyzer": "ik_smart",
					"search_analyzer": "ik_smart"
				}
			}
		}
	}`

	req := esapi.IndicesCreateRequest{
		Index: "scenes_index",
		Body:  bytes.NewReader([]byte(mapping)),
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() && !strings.Contains(res.String(), "resource_already_exists_exception") {
		return fmt.Errorf("创建场景索引错误: %s", res.String())
	}
	return nil
}

// 创建索引（带IK分词器）
func CreateIndex(es *elasticsearch.Client) error {
	mapping := `{
		"settings": {
			"analysis": {
				"analyzer": {
					"default": {
						"type": "ik_smart"
					}
				}
			}
		},
		"mappings": {
			"properties": {
				"title": {
					"type": "text",
					"analyzer": "ik_smart",
					"search_analyzer": "ik_smart"
				},
				"content": {
					"type": "text",
					"analyzer": "ik_smart",
					"search_analyzer": "ik_smart"
				},
				"name": {
					"type": "text",
					"analyzer": "ik_smart",
					"search_analyzer": "ik_smart"
				}
			}
		}
	}`

	req := esapi.IndicesCreateRequest{
		Index: "articles_index",
		Body:  bytes.NewReader([]byte(mapping)),
	}

	res, err := req.Do(context.Background(), es)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() && !strings.Contains(res.String(), "resource_already_exists_exception") {
		return fmt.Errorf("创建索引错误: %s", res.String())
	}
	return nil
}

package initialize

import (
	"fmt"

	"net/http"
	"net/url"

	"github.com/spf13/viper"
	"github.com/tencentyun/cos-go-sdk-v5"
	"go.uber.org/zap"
)

var COSClient *cos.Client

// InitCOS 初始化腾讯云对象存储客户端
func InitCOS() error {
	// 从配置中获取腾讯云COS配置
	bucketURL := viper.GetString("cos.bucket_url")
	secretID := viper.GetString("cos.secret_id")
	secretKey := viper.GetString("cos.secret_key")

	// 检查配置是否存在
	if bucketURL == "" || secretID == "" || secretKey == "" {
		Logger.Error("腾讯云COS配置不完整")
		return fmt.Errorf("腾讯云COS配置不完整")
	}

	// 解析bucket URL
	u, err := url.Parse(bucketURL)
	if err != nil {
		Logger.Error("解析腾讯云COS Bucket URL失败", zap.Error(err))
		return fmt.Errorf("解析腾讯云COS Bucket URL失败: %v", err)
	}

	// 创建COS客户端
	b := &cos.BaseURL{BucketURL: u}
	COSClient = cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})

	Logger.Info("腾讯云COS初始化成功")
	return nil
}

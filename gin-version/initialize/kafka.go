package initialize

import (
	"fmt"
	"sync"

	"github.com/IBM/sarama"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	KafkaClient   sarama.Client
	KafkaProducer sarama.SyncProducer
	kafkaMutex    sync.Mutex
)

func InitKafka() error {
	kafkaMutex.Lock()
	defer kafkaMutex.Unlock()

	// 创建 Kafka 配置
	config := sarama.NewConfig()
	config.Version = sarama.V2_8_0_0
	// 设置生产者配置
	config.Producer.Return.Successes = true

	// 从配置文件获取 Kafka 服务器地址
	brokers := viper.GetStringSlice("kafka.brokers")
	if len(brokers) == 0 {
		Logger.Error("未配置 Kafka 服务器地址")
		return fmt.Errorf("未配置 Kafka 服务器地址")
	}

	// 创建 Kafka 客户端
	client, err := sarama.NewClient(brokers, config)
	if err != nil {
		Logger.Error("创建 Kafka 客户端失败", zap.Error(err))
		return fmt.Errorf("创建 Kafka 客户端失败: %v", err)
	}

	KafkaClient = client

	// 创建全局生产者实例
	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		Logger.Error("创建Kafka生产者失败", zap.Error(err))
		client.Close()
		return fmt.Errorf("创建Kafka生产者失败: %v", err)
	}
	KafkaProducer = producer

	Logger.Info("Kafka初始化成功")
	return nil
}

func CloseKafka() {
	kafkaMutex.Lock()
	defer kafkaMutex.Unlock()

	if KafkaProducer != nil {
		if err := KafkaProducer.Close(); err != nil {
			Logger.Error("关闭Kafka生产者失败", zap.Error(err))
		}
		KafkaProducer = nil
	}

	if KafkaClient != nil {
		if err := KafkaClient.Close(); err != nil {
			Logger.Error("关闭Kafka客户端失败", zap.Error(err))
		}
		KafkaClient = nil
	}
}

func ReconnectKafka() error {
	CloseKafka()
	return InitKafka()
}

func SendSQLLog(format string, args ...interface{}) error {
	if KafkaProducer == nil {
		Logger.Error("Kafka生产者未初始化")
		return fmt.Errorf("Kafka生产者未初始化")
	}

	// 格式化SQL语句
	sqlLog := fmt.Sprintf(format, args...)

	// 创建消息
	msg := &sarama.ProducerMessage{
		Topic: viper.GetString("kafka.topics.sql_log"),
		Value: sarama.StringEncoder(sqlLog),
	}

	// 发送消息
	_, _, err := KafkaProducer.SendMessage(msg)
	if err != nil {
		Logger.Error("发送SQL日志到Kafka失败", zap.Error(err), zap.String("sql", sqlLog))
		return fmt.Errorf("发送SQL日志到Kafka失败: %v", err)
	}

	Logger.Info("SQL日志已发送到Kafka", zap.String("sql", sqlLog))
	return nil
}

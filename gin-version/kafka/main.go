package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IBM/sarama"

	"github.com/gin-gonic/gin"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/spf13/viper"
)

func main() {
	// 初始化Nacos配置
	if err := initNacosConfig(); err != nil {
		log.Fatalf("初始化Nacos配置失败: %v", err)
	}

	// 初始化Gin路由
	router := gin.Default()
	// 注册日志查询接口
	router.GET("/log", GetLogs)

	// 启动HTTP服务器
	go func() {
		if err := router.Run(":31337"); err != nil {
			log.Fatalf("启动HTTP服务器失败: %v", err)
		}
	}()

	// 初始化消费者组
	groupID := viper.GetString("kafka.consumer.group_id")
	if groupID == "" {
		log.Fatalf("未配置消费者组ID")
	}
	group, err := InitConsumerGroup(groupID)
	if err != nil {
		log.Fatalf("初始化消费者组失败: %v", err)
	}
	defer func() {
		if err := group.Close(); err != nil {
			log.Printf("关闭消费者组失败: %v", err)
		}
	}()

	// 创建上下文和取消函数
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建等待组
	wg := &sync.WaitGroup{}
	wg.Add(1)

	// 创建消费者处理器
	handler := &ConsumerGroupHandler{
		ready:    make(chan bool),
		messages: make(map[string]*MessageStatus),
	}

	// 启动消费者循环
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// 从配置中获取主题
				topic := viper.GetString("kafka.topics.sql_log")
				if topic == "" {
					log.Fatal("未配置Kafka主题")
				}

				// 消费消息
				if err := group.Consume(ctx, []string{topic}, handler); err != nil {
					log.Printf("消费消息时发生错误: %v", err)
				}

				// 检查上下文是否已取消
				if ctx.Err() != nil {
					return
				}

				// 重置ready通道
				handler.ready = make(chan bool)
			}
		}
	}()

	// 等待消费者就绪
	<-handler.ready
	log.Println("消费者已就绪，开始接收消息...")

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// 收到中断信号后取消上下文
	log.Println("收到中断信号，正在优雅关闭...")
	cancel()

	// 等待消费者循环结束
	wg.Wait()
}

func initNacosConfig() error {
	// Nacos服务器配置
	sc := []constant.ServerConfig{
		{
			IpAddr: "",   // Nacos服务器地址
			Port:   8848, // Nacos服务器端口
		},
	}

	// Nacos客户端配置
	cc := constant.ClientConfig{
		NamespaceId:         "e9a52cee-a797-4323-b773-05a243bed322", // 命名空间ID
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "/tmp/nacos/log",
		CacheDir:            "/tmp/nacos/cache",
		LogLevel:            "debug",
	}

	// 创建Nacos配置客户端
	client, err := clients.CreateConfigClient(map[string]interface{}{
		"serverConfigs": sc,
		"clientConfig":  cc,
	})
	if err != nil {
		return fmt.Errorf("创建Nacos配置客户端失败: %v", err)
	}

	// 获取配置
	content, err := client.GetConfig(vo.ConfigParam{
		DataId: "travel-world.yaml",
		Group:  "DEFAULT_GROUP",
	})
	if err != nil {
		return fmt.Errorf("获取Nacos配置失败: %v", err)
	}

	// 使用viper解析配置
	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(strings.NewReader(content)); err != nil {
		return fmt.Errorf("解析配置失败: %v", err)
	}

	return nil
}

// LogResponse 定义日志接口的响应结构
type LogResponse struct {
	Code  int    `json:"code"`
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// GetLogs 处理获取SQL日志的请求
func GetLogs(c *gin.Context) {
	// 获取查询参数
	year := c.Query("year")
	month := c.Query("month")

	// 构造查询日期
	var date time.Time
	if year != "" && month != "" {
		// 构造年月格式的日期字符串
		dateStr := year + month
		parsedDate, err := time.Parse("200601", dateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, LogResponse{
				Code:  http.StatusBadRequest,
				Error: "日期格式无效",
			})
			return
		}
		date = parsedDate
	}

	// 获取日志目录配置
	logDir := viper.GetString("kafka.log_dir")
	if logDir == "" {
		logDir = "./kafka_logs" // 使用默认目录
	}

	// 读取日志
	logs, err := ReadSQLLogs(logDir, date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, LogResponse{
			Code:  http.StatusInternalServerError,
			Error: err.Error(),
		})
		return
	}

	// 返回结果
	c.JSON(http.StatusOK, LogResponse{
		Code: http.StatusOK,
		Data: logs,
	})
}

// MessageStatus 用于跟踪消息的消费状态
type MessageStatus struct {
	Partition int32
	Offset    int64
	Consumed  bool
	Error     error
	Message   *sarama.ConsumerMessage
}

// SQLLogEntry 表示一条SQL日志记录
type SQLLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	SQL       string    `json:"sql"`
}

// SQLLogWriter 处理SQL日志的写入
type SQLLogWriter struct {
	logFile        *os.File
	writer         *bufio.Writer
	logDir         string
	currentWeek    string   // 当前日志文件的周期（每7天一个周期）
	manifestFile   *os.File // manifest文件句柄
	manifestWriter *bufio.Writer
}

// ManifestEntry 表示一条manifest记录
type ManifestEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Operation string    `json:"operation"`
	FileName  string    `json:"file_name"`
}

// ConsumerGroupHandler 处理Kafka消费者组的消息
type ConsumerGroupHandler struct {
	ready     chan bool
	messages  map[string]*MessageStatus
	logWriter *SQLLogWriter
}

// NewConsumerGroupHandler 创建一个新的消费者组处理器
func NewConsumerGroupHandler() *ConsumerGroupHandler {
	return &ConsumerGroupHandler{
		ready:    make(chan bool),
		messages: make(map[string]*MessageStatus),
	}
}

// NewSQLLogWriter 创建一个新的SQL日志写入器
func NewSQLLogWriter(logDir string) (*SQLLogWriter, error) {
	// 确保日志目录存在
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %v", err)
	}

	// 获取当前周期
	currentWeek := time.Now().Format("2006-01-02")

	// 创建或打开manifest文件
	manifestPath := filepath.Join(logDir, "manifest.json")
	manifestFile, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开manifest文件失败: %v", err)
	}

	// 创建或打开日志文件
	logPath := filepath.Join(logDir, fmt.Sprintf("sql_log_%s.json", currentWeek))
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		manifestFile.Close()
		return nil, fmt.Errorf("打开日志文件失败: %v", err)
	}

	writer := &SQLLogWriter{
		logFile:        file,
		writer:         bufio.NewWriter(file),
		logDir:         logDir,
		currentWeek:    currentWeek,
		manifestFile:   manifestFile,
		manifestWriter: bufio.NewWriter(manifestFile),
	}

	// 记录新日志文件创建的操作
	manifestEntry := ManifestEntry{
		Timestamp: time.Now(),
		Operation: "create",
		FileName:  fmt.Sprintf("sql_log_%s.json", currentWeek),
	}
	if err := writer.writeManifest(&manifestEntry); err != nil {
		file.Close()
		manifestFile.Close()
		return nil, fmt.Errorf("写入manifest失败: %v", err)
	}

	return writer, nil
}

// writeManifest 写入一条manifest记录
func (w *SQLLogWriter) writeManifest(entry *ManifestEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("序列化manifest记录失败: %v", err)
	}

	if _, err := w.manifestWriter.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("写入manifest记录失败: %v", err)
	}

	return w.manifestWriter.Flush()
}

// WriteLog 写入一条SQL日志
func (w *SQLLogWriter) WriteLog(entry *SQLLogEntry) error {
	// 检查是否需要切换到新的周期日志文件
	currentWeek := time.Now().Format("2006-01-02")
	if currentWeek != w.currentWeek {
		// 关闭当前日志文件
		if err := w.writer.Flush(); err != nil {
			return fmt.Errorf("刷新缓冲区失败: %v", err)
		}
		if err := w.logFile.Close(); err != nil {
			return fmt.Errorf("关闭旧日志文件失败: %v", err)
		}

		// 记录旧文件关闭操作
		closeEntry := ManifestEntry{
			Timestamp: time.Now(),
			Operation: "close",
			FileName:  fmt.Sprintf("sql_log_%s.json", w.currentWeek),
		}
		if err := w.writeManifest(&closeEntry); err != nil {
			return fmt.Errorf("写入manifest失败: %v", err)
		}

		// 清理旧版本文件
		if err := w.cleanOldVersions(); err != nil {
			log.Printf("清理旧版本文件失败: %v", err)
		}

		// 创建新的日志文件
		logPath := filepath.Join(w.logDir, fmt.Sprintf("sql_log_%s.json", currentWeek))
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("创建新日志文件失败: %v", err)
		}

		// 更新写入器
		w.logFile = file
		w.writer = bufio.NewWriter(file)
		w.currentWeek = currentWeek

		// 记录新文件创建操作
		createEntry := ManifestEntry{
			Timestamp: time.Now(),
			Operation: "create",
			FileName:  fmt.Sprintf("sql_log_%s.json", currentWeek),
		}
		if err := w.writeManifest(&createEntry); err != nil {
			return fmt.Errorf("写入manifest失败: %v", err)
		}
	}

	// 将日志条目序列化为JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("序列化日志失败: %v", err)
	}

	// 写入日志文件
	if _, err := w.writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("写入日志失败: %v", err)
	}

	// 刷新缓冲区
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("刷新日志失败: %v", err)
	}

	return nil
}

// cleanOldVersions 清理旧版本的日志文件，只保留最近15个版本
func (w *SQLLogWriter) cleanOldVersions() error {
	files, err := os.ReadDir(w.logDir)
	if err != nil {
		return fmt.Errorf("读取日志目录失败: %v", err)
	}

	var logFiles []string
	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(file.Name(), "sql_log_") && strings.HasSuffix(file.Name(), ".json") {
			logFiles = append(logFiles, file.Name())
		}
	}

	// 按文件名排序（文件名包含日期信息）
	sort.Strings(logFiles)

	// 如果文件数量超过15个，删除最旧的文件
	if len(logFiles) > 15 {
		for _, file := range logFiles[:len(logFiles)-15] {
			filePath := filepath.Join(w.logDir, file)
			if err := os.Remove(filePath); err != nil {
				log.Printf("删除旧日志文件失败: %v", err)
				continue
			}

			// 记录文件删除操作
			deleteEntry := ManifestEntry{
				Timestamp: time.Now(),
				Operation: "delete",
				FileName:  file,
			}
			if err := w.writeManifest(&deleteEntry); err != nil {
				log.Printf("写入manifest失败: %v", err)
			}
		}
	}

	return nil
}

// ReadSQLLogs 从指定目录读取SQL日志并返回可执行的PostgreSQL语句
func ReadSQLLogs(logDir string, date time.Time) (string, error) {
	// 如果date为空，使用当前时间
	if date.IsZero() {
		date = time.Now()
	}

	// 构建日志文件路径
	logPath := filepath.Join(logDir, fmt.Sprintf("sql_log_%s.json", date.Format("200601")))

	// 打开日志文件
	file, err := os.Open(logPath)
	if err != nil {
		return "", fmt.Errorf("打开日志文件失败: %v", err)
	}
	defer file.Close()

	// 读取日志文件
	var logs []SQLLogEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry SQLLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return "", fmt.Errorf("解析日志失败: %v", err)
		}
		logs = append(logs, entry)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取日志失败: %v", err)
	}

	// 按时间戳排序
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp.Before(logs[j].Timestamp)
	})

	// 构建可执行的SQL语句
	var sqlStatements strings.Builder
	sqlStatements.WriteString("BEGIN; ")
	for _, entry := range logs {
		// 添加SQL语句，确保每条语句都以分号结尾且用空格分隔
		sql := strings.TrimSpace(entry.SQL)
		if !strings.HasSuffix(sql, ";") {
			sql += ";"
		}
		sqlStatements.WriteString(sql + " ")
	}
	sqlStatements.WriteString("COMMIT;")

	return sqlStatements.String(), nil
}

// InitConsumerGroup 初始化消费者组
func InitConsumerGroup(groupID string) (sarama.ConsumerGroup, error) {
	// 创建 Kafka 配置
	config := sarama.NewConfig()
	config.Version = sarama.V2_8_0_0
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Offsets.Initial = sarama.OffsetOldest

	// 设置消费者组配置
	config.Consumer.Group.Session.Timeout = 20 * time.Second
	config.Consumer.Group.Heartbeat.Interval = 6 * time.Second
	config.Consumer.MaxProcessingTime = 500 * time.Millisecond

	// 从配置文件获取 Kafka 服务器地址
	brokers := viper.GetStringSlice("kafka.brokers")
	if len(brokers) == 0 {
		return nil, fmt.Errorf("未配置 Kafka 服务器地址")
	}

	// 创建消费者组
	group, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, fmt.Errorf("创建消费者组失败: %v", err)
	}

	// 监听消费者组错误
	go func() {
		for err := range group.Errors() {
			log.Printf("消费者组错误: %v", err)
		}
	}()

	return group, nil
}

// Setup 在消费者组启动时调用
func (h *ConsumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

// Cleanup 在消费者组关闭时调用
func (h *ConsumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	h.ready = make(chan bool)
	return nil
}

// ConsumeClaim 处理消息消费
func (h *ConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	// 初始化日志写入器
	if h.logWriter == nil {
		logDir := viper.GetString("kafka.log_dir")
		if logDir == "" {
			logDir = "./kafka_logs" // 默认日志目录
		}
		writer, err := NewSQLLogWriter(logDir)
		if err != nil {
			log.Printf("初始化日志写入器失败: %v", err)
			return err
		}
		h.logWriter = writer
		log.Printf("成功初始化日志写入器，日志目录: %s", logDir)
	}

	for message := range claim.Messages() {
		// 记录消息状态
		status := &MessageStatus{
			Partition: message.Partition,
			Offset:    message.Offset,
			Consumed:  false,
			Message:   message,
		}

		// 处理消息
		log.Printf("收到SQL日志 - Topic: %s, Partition: %d, Offset: %d, 时间: %s",
			message.Topic, message.Partition, message.Offset,
			message.Timestamp.Format("2006-01-02 15:04:05"))

		// 将SQL日志写入本地文件
		entry := &SQLLogEntry{
			Timestamp: message.Timestamp,
			SQL:       string(message.Value),
		}
		if err := h.logWriter.WriteLog(entry); err != nil {
			log.Printf("写入SQL日志失败 - Topic: %s, Partition: %d, Offset: %d, 错误: %v",
				message.Topic, message.Partition, message.Offset, err)
			status.Error = err
			// 不要因为写入失败就中断整个消费过程
			continue
		}

		// 标记消息为已消费
		status.Consumed = true
		h.messages[fmt.Sprintf("%s-%d-%d", message.Topic, message.Partition, message.Offset)] = status

		// 手动提交偏移量
		session.MarkMessage(message, "")
		log.Printf("成功处理SQL日志 - Topic: %s, Partition: %d, Offset: %d",
			message.Topic, message.Partition, message.Offset)
	}

	return nil
}

package email

import (
	"fmt"

	"gopkg.in/gomail.v2"

	"github.com/spf13/viper"
)

type EmailConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

var config EmailConfig

// InitEmailConfig 初始化邮件配置
func InitEmailConfig() {
	config = EmailConfig{
		Host:     viper.GetString("email.host"),
		Port:     viper.GetInt("email.port"),
		Username: viper.GetString("email.username"),
		Password: viper.GetString("email.password"),
		From:     viper.GetString("email.from"),
	}
}

// SendVerificationCode 发送验证码邮件
func SendVerificationCode(to string, code string) error {
	// 创建新的邮件消息
	m := gomail.NewMessage()
	m.SetHeader("From", config.From)
	m.SetHeader("To", to)
	m.SetHeader("Subject", "看看这世界- See The World - 邮箱验证码")
	m.SetBody("text/plain", fmt.Sprintf("您的验证码是：%s\n该验证码将在10分钟后过期。", code))

	// 创建SMTP拨号器
	d := gomail.NewDialer(config.Host, config.Port, config.Username, config.Password)
	d.SSL = true // 显式启用SSL

	// 发送邮件
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("发送邮件失败: %v", err)
	}

	return nil
}

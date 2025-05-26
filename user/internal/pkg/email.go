package pkg

import (
	"fmt"

	"gopkg.in/gomail.v2"

	nacos "github.com/knoci/roaming-world/user/internal/conf/nacos"
)

type EmailConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}


// SendVerificationCode 发送验证码邮件
func SendVerificationCode(to string, code string) error {
	cfg := nacos.GetConfig()
	config := EmailConfig{
		Host:     nacos.GetConfigString(cfg, "email.host"),
		Port:     nacos.GetConfigInt(cfg, "email.port"),
		Username: nacos.GetConfigString(cfg, "email.username"),
		Password: nacos.GetConfigString(cfg, "email.password"),
		From:     nacos.GetConfigString(cfg, "email.from"),
	}
	// 创建新的邮件消息
	m := gomail.NewMessage()
	m.SetHeader("From", config.From)
	m.SetHeader("To", to)
	m.SetHeader("Subject", "世界漫游记- Roaming World - 邮箱验证码")
	m.SetBody("text/plain", fmt.Sprintf("您的验证码是：%s\n该验证码将在10分钟后过期。", code))

	// 创建SMTP拨号器
	d := gomail.NewDialer(config.Host, config.Port, config.Username, config.Password)
	d.SSL = true // 显式启用SSL

	// 发送邮件
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("send verify code failed: %v", err)
	}

	return nil
}
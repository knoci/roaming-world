package pkg

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/knoci/roaming-world/pkg/auth"
	"github.com/segmentio/kafka-go"
)

func TestParseBrokers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single broker",
			in:   "kafka:9092",
			want: []string{"kafka:9092"},
		},
		{
			name: "multi broker with spaces and empty segments",
			in:   " kafka-1:9092 , kafka-2:9092, ,kafka-3:9092 ",
			want: []string{"kafka-1:9092", "kafka-2:9092", "kafka-3:9092"},
		},
		{
			name: "empty input",
			in:   " ,  , ",
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBrokers(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseBrokers(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if got := ParseBrokers(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseBrokers(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewKafkaSyncSenderUsesHashBalancerAndMultiBrokers(t *testing.T) {
	sender, err := NewKafkaSyncSender([]string{"broker-1:9092, broker-2:9092", "broker-3:9092"}, "comment-event")
	if err != nil {
		t.Fatalf("NewKafkaSyncSender unexpected error: %v", err)
	}
	defer sender.Close()

	if _, ok := sender.writer.Balancer.(*kafka.Hash); !ok {
		t.Fatalf("expected *kafka.Hash balancer for partition ordering, got %T", sender.writer.Balancer)
	}
	if sender.writer.Async {
		t.Fatal("expected synchronous Kafka writer (Async=false)")
	}
	if sender.writer.RequiredAcks != kafka.RequireAll {
		t.Fatalf("expected RequiredAcks=RequireAll, got %v", sender.writer.RequiredAcks)
	}
	if sender.writer.BatchSize != 1 {
		t.Fatalf("expected BatchSize=1, got %d", sender.writer.BatchSize)
	}
	addrStr := sender.writer.Addr.String()
	for _, host := range []string{"broker-1:9092", "broker-2:9092", "broker-3:9092"} {
		if !strings.Contains(addrStr, host) {
			t.Fatalf("expected writer.Addr %q to include %q", addrStr, host)
		}
	}
}

func TestNewKafkaSyncSenderRejectsEmptyBrokers(t *testing.T) {
	if _, err := NewKafkaSyncSender([]string{"", " , "}, "comment-event"); err == nil {
		t.Fatal("expected error for empty broker list")
	}
}

func TestJWTWrapperDelegatesToPkgAuth(t *testing.T) {
	auth.ResetKeyCacheForTest()
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Cleanup(auth.ResetKeyCacheForTest)

	if err := ValidateJWTSigningKey(); err != nil {
		t.Fatalf("ValidateJWTSigningKey: %v", err)
	}
	token, _, err := auth.GenerateAccessToken("u-1", "Bob", "bob@example.com", "", "user", 1, time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UID != "u-1" || claims.Name != "Bob" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

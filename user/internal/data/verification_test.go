package data

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/knoci/roaming-world/user/internal/biz"
	"github.com/redis/go-redis/v9"
)

type mockRedisEntry struct {
	value     string
	expiresAt time.Time
}

type mockRedisServer struct {
	ln       net.Listener
	mu       sync.Mutex
	kv       map[string]mockRedisEntry
	closed   chan struct{}
	wg       sync.WaitGroup
	issueSHA string
	checkSHA string
}

func startMockRedisServer(t *testing.T) *mockRedisServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock redis: %v", err)
	}
	s := &mockRedisServer{
		ln:       ln,
		kv:       make(map[string]mockRedisEntry),
		closed:   make(chan struct{}),
		issueSHA: issueVerificationScript.Hash(),
		checkSHA: checkVerificationScript.Hash(),
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.closed:
					return
				default:
					return
				}
			}
			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				defer c.Close()
				s.serveConn(c)
			}(conn)
		}
	}()
	t.Cleanup(func() {
		close(s.closed)
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *mockRedisServer) addr() string {
	return s.ln.Addr().String()
}

func (s *mockRedisServer) serveConn(conn net.Conn) {
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		args, err := readRESPCommand(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		resp := s.handleCommand(args)
		if _, err := w.WriteString(resp); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return strings.Fields(line), nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 {
		return nil, io.EOF
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lenLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		lenLine = strings.TrimRight(lenLine, "\r\n")
		if !strings.HasPrefix(lenLine, "$") {
			return nil, fmt.Errorf("expected bulk string, got %q", lenLine)
		}
		sz, err := strconv.Atoi(lenLine[1:])
		if err != nil || sz < 0 {
			return nil, fmt.Errorf("invalid bulk length %q", lenLine)
		}
		buf := make([]byte, sz+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:sz]))
	}
	return args, nil
}

func (s *mockRedisServer) getLocked(key string) (mockRedisEntry, bool) {
	e, ok := s.kv[key]
	if !ok {
		return mockRedisEntry{}, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(s.kv, key)
		return mockRedisEntry{}, false
	}
	return e, true
}

func (s *mockRedisServer) handleCommand(args []string) string {
	cmd := strings.ToUpper(args[0])
	switch cmd {
	case "HELLO":
		return "-ERR unknown command 'HELLO'\r\n"
	case "CLIENT":
		return "+OK\r\n"
	case "PING":
		return "+PONG\r\n"
	case "DEL":
		s.mu.Lock()
		defer s.mu.Unlock()
		var removed int64
		for _, k := range args[1:] {
			if _, ok := s.getLocked(k); ok {
				delete(s.kv, k)
				removed++
			}
		}
		return fmt.Sprintf(":%d\r\n", removed)
	case "EVALSHA", "EVAL":
		if len(args) < 3 {
			return "-ERR wrong number of arguments\r\n"
		}
		scriptOrSHA := args[1]
		numKeys, err := strconv.Atoi(args[2])
		if err != nil || len(args) < 3+numKeys {
			return "-ERR invalid numkeys\r\n"
		}
		keys := args[3 : 3+numKeys]
		argv := args[3+numKeys:]

		isIssue := scriptOrSHA == s.issueSHA || strings.Contains(scriptOrSHA, "EXISTS")
		isCheck := scriptOrSHA == s.checkSHA || strings.Contains(scriptOrSHA, "KEEPTTL")

		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case isIssue:
			if len(keys) < 2 || len(argv) < 3 {
				return "-ERR bad issue args\r\n"
			}
			cooldownKey, codeKey := keys[0], keys[1]
			if _, ok := s.getLocked(cooldownKey); ok {
				return ":0\r\n"
			}
			cdSec, _ := strconv.Atoi(argv[0])
			codeSec, _ := strconv.Atoi(argv[2])
			now := time.Now()
			s.kv[cooldownKey] = mockRedisEntry{value: "1", expiresAt: now.Add(time.Duration(cdSec) * time.Second)}
			s.kv[codeKey] = mockRedisEntry{value: argv[1], expiresAt: now.Add(time.Duration(codeSec) * time.Second)}
			return ":1\r\n"
		case isCheck:
			if len(keys) < 1 || len(argv) < 3 {
				return "-ERR bad check args\r\n"
			}
			codeKey := keys[0]
			entry, ok := s.getLocked(codeKey)
			if !ok {
				return ":-1\r\n"
			}
			stored, failures, parsed := parseVerificationValue(entry.value)
			if !parsed {
				return ":-1\r\n"
			}
			maxFailures, _ := strconv.Atoi(argv[2])
			if maxFailures <= 0 {
				maxFailures = verificationMaxFailures
			}
			if failures >= maxFailures {
				delete(s.kv, codeKey)
				return ":-1\r\n"
			}
			if stored == argv[0] {
				if argv[1] == "1" {
					delete(s.kv, codeKey)
				}
				return ":1\r\n"
			}
			failures++
			if failures >= maxFailures {
				delete(s.kv, codeKey)
			} else {
				entry.value = formatVerificationValue(stored, failures)
				s.kv[codeKey] = entry // preserves expiresAt (KEEPTTL)
			}
			return ":0\r\n"
		default:
			return "-NOSCRIPT No matching script\r\n"
		}
	default:
		return "-ERR unsupported command " + cmd + "\r\n"
	}
}

func TestEmailVerificationRedisLua(t *testing.T) {
	srv := startMockRedisServer(t)
	rdb := redis.NewClient(&redis.Options{
		Addr:     srv.addr(),
		Protocol: 2,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := &userRepo{data: &Data{redis: rdb}}

	t.Run("scoped and consumed once concurrently", func(t *testing.T) {
		target := biz.VerificationTarget{
			Email: uuid.NewString() + "@example.test", Purpose: biz.VerificationChangeEmail, UID: "account-one",
		}
		const code = "018723"
		discard, err := repo.IssueVerificationCode(ctx, target, code)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = discard(ctx) }()

		otherPurpose := biz.VerificationTarget{Email: target.Email, Purpose: biz.VerificationResetPassword}
		if _, err := repo.IssueVerificationCode(ctx, otherPurpose, "286974"); !errors.Is(err, biz.ErrVerificationThrottled) {
			t.Fatalf("shared email cooldown returned %v", err)
		}
		if err := repo.CheckVerificationCode(ctx, otherPurpose, code, true); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("verification for a different purpose returned %v", err)
		}
		otherAccount := target
		otherAccount.UID = "account-two"
		if err := repo.CheckVerificationCode(ctx, otherAccount, code, true); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("verification for a different account returned %v", err)
		}
		if err := repo.CheckVerificationCode(ctx, target, code, false); err != nil {
			t.Fatalf("precheck failed: %v", err)
		}

		const workers = 12
		results := make(chan error, workers)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			go func() {
				<-start
				results <- repo.CheckVerificationCode(ctx, target, code, true)
			}()
		}
		close(start)
		successes := 0
		for i := 0; i < workers; i++ {
			err := <-results
			switch {
			case err == nil:
				successes++
			case errors.Is(err, biz.ErrVerificationExpired):
			default:
				t.Errorf("unexpected verification error: %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("expected a single successful consumption, got %d", successes)
		}
	})

	t.Run("precheck failures share max attempt budget", func(t *testing.T) {
		target := biz.VerificationTarget{Email: uuid.NewString() + "@example.test", Purpose: biz.VerificationRegister}
		const code = "018723"
		discard, err := repo.IssueVerificationCode(ctx, target, code)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = discard(ctx) }()

		for attempt := 0; attempt < verificationMaxFailures; attempt++ {
			if err := repo.CheckVerificationCode(ctx, target, "286974", false); !errors.Is(err, biz.ErrVerificationExpired) {
				t.Fatalf("precheck attempt %d returned %v", attempt, err)
			}
		}
		if err := repo.CheckVerificationCode(ctx, target, code, true); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("consuming an exhausted code returned %v", err)
		}
	})

	t.Run("discard revokes both cooldown and code keys", func(t *testing.T) {
		target := biz.VerificationTarget{Email: uuid.NewString() + "@example.test", Purpose: biz.VerificationRegister}
		discard, err := repo.IssueVerificationCode(ctx, target, "111222")
		if err != nil {
			t.Fatal(err)
		}
		if err := discard(ctx); err != nil {
			t.Fatalf("discard failed: %v", err)
		}
		if err := repo.CheckVerificationCode(ctx, target, "111222", false); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("discarded code should be expired, got %v", err)
		}
		// Should be able to immediately re-issue without cooldown throttling
		discard2, err := repo.IssueVerificationCode(ctx, target, "333444")
		if err != nil {
			t.Fatalf("re-issue after discard should succeed, got %v", err)
		}
		defer func() { _ = discard2(ctx) }()
		if err := repo.CheckVerificationCode(ctx, target, "333444", true); err != nil {
			t.Fatalf("re-issued code check failed: %v", err)
		}
	})
}

package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP — минимальный SMTP-сервер для тестов: EHLO, MAIL, RCPT, DATA, QUIT.
// Опционально требует AUTH LOGIN.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	messages []string // тела полученных писем
	rcpts    []string
	require  bool // требовать AUTH
}

func newFakeSMTP(t *testing.T, auth bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, require: auth}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	write := func(s string) {
		_, _ = rw.WriteString(s + "\r\n")
		_ = rw.Flush()
	}
	write("220 fake ESMTP")
	var data strings.Builder
	inData := false
	authed := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				f.mu.Lock()
				f.messages = append(f.messages, data.String())
				f.mu.Unlock()
				data.Reset()
				write("250 OK queued")
				continue
			}
			data.WriteString(line)
			data.WriteString("\n")
			continue
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if f.require {
				write("250-fake")
				write("250 AUTH PLAIN LOGIN")
			} else {
				write("250 fake")
			}
		case strings.HasPrefix(cmd, "AUTH"):
			// Принимаем любой AUTH (механизм и данные не разбираем).
			if strings.EqualFold(line, "AUTH LOGIN") || strings.HasPrefix(cmd, "AUTH PLAIN") {
				if strings.HasPrefix(cmd, "AUTH PLAIN") {
					authed = true
					write("235 2.7.0 Authentication successful")
				} else {
					write("334 VXNlcm5hbWU6")
				}
				// Для LOGIN: два шага с base64.
				if strings.HasPrefix(cmd, "AUTH LOGIN") {
					if _, err := rw.ReadString('\n'); err != nil {
						return
					}
					write("334 UGFzc3dvcmQ6")
					if _, err := rw.ReadString('\n'); err != nil {
						return
					}
					authed = true
					authed = true
					write("235 2.7.0 Authentication successful")
				}
			} else {
				write("504 Unrecognized authentication type")
			}
		case strings.HasPrefix(cmd, "MAIL FROM"):
			if f.require && !authed {
				write("530 5.7.0 Authentication required")
				continue
			}
			write("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			f.mu.Lock()
			f.rcpts = append(f.rcpts, line)
			f.mu.Unlock()
			write("250 OK")
		case cmd == "DATA":
			inData = true
			write("354 End data with <CR><LF>.<CR><LF>")
		case cmd == "QUIT":
			write("221 Bye")
			return
		case cmd == "RSET":
			write("250 OK")
		default:
			write("250 OK")
		}
	}
}

func (f *fakeSMTP) hostPort() (string, int) {
	addr := f.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func TestClientSendPlain(t *testing.T) {
	f := newFakeSMTP(t, false)
	host, port := f.hostPort()
	c := &Client{Timeout: 5 * time.Second}

	s := Settings{
		Enabled: true, Host: host, Port: port, Security: SecurityNone,
		FromAddr: "lib@example.com", FromName: "Библиотека",
	}
	msg := Message{To: "reader@example.com", Subject: "Сброс пароля",
		Text: "Текст письма", HTML: "<p>Текст</p>", Template: TemplatePasswordReset, Lang: "ru"}
	res := c.Send(context.Background(), s, msg)
	if res.Err != nil {
		t.Fatalf("send: %v", res.Err)
	}
	if res.MessageID == "" {
		t.Fatal("expected message id")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(f.messages))
	}
	body := f.messages[0]
	if !strings.Contains(strings.ToLower(body), "reader@example.com") {
		t.Fatalf("recipient not found in message: %s", body)
	}
	if !strings.Contains(strings.ToLower(body), "subject:") {
		t.Fatalf("subject header missing: %s", body)
	}
	if len(f.rcpts) != 1 || !strings.Contains(f.rcpts[0], "reader@example.com") {
		t.Fatalf("unexpected rcpts: %v", f.rcpts)
	}
}

func TestClientSendWithAuth(t *testing.T) {
	f := newFakeSMTP(t, true)
	host, port := f.hostPort()
	c := &Client{Timeout: 5 * time.Second}
	s := Settings{
		Enabled: true, Host: host, Port: port, Security: SecurityNone,
		Username: "user", Password: "secret", FromAddr: "lib@example.com",
	}
	// Пароль без шифрования запрещён валидацией — отправка не выполняется.
	if res := c.Send(context.Background(), s, Message{To: "r@example.com", Text: "x"}); res.Err == nil {
		t.Fatal("expected validation error for password without TLS")
	}
	// Логин без пароля: клиент выполняет AUTH (сервер требует его перед MAIL).
	s.Password = ""
	res := c.Send(context.Background(), s, Message{To: "r@example.com", Text: "x", Subject: "t"})
	if res.Err != nil {
		t.Fatalf("send with auth: %v", res.Err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(f.messages))
	}
}

func TestClientSendUnreachable(t *testing.T) {
	// Порт из диапазона, где ничего не слушает: соединение отклоняется.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()

	c := &Client{Timeout: 2 * time.Second}
	s := Settings{Enabled: true, Host: "127.0.0.1", Port: addr.Port,
		Security: SecurityNone, FromAddr: "lib@example.com"}
	res := c.Send(context.Background(), s, Message{To: "r@example.com", Text: "x"})
	if res.Err == nil {
		t.Fatal("expected send error")
	}
	if !strings.Contains(res.Err.Error(), "mail: send") {
		t.Fatalf("unexpected error: %v", res.Err)
	}
}

func TestVerify(t *testing.T) {
	f := newFakeSMTP(t, false)
	host, port := f.hostPort()
	c := &Client{Timeout: 5 * time.Second}
	s := Settings{Host: host, Port: port, Security: SecurityNone, FromAddr: "lib@example.com"}
	if err := c.Verify(context.Background(), s); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

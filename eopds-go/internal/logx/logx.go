// Package logx — простая система уровневого логирования в stderr.
//
// Задача: логи должны быть (а) отключаемыми (уровень из настроек),
// (б) пригодными для пересылки пользователями (человекочитаемый, с временем,
// уровнем и модулем), (в) НЕ попадать в stdout, т.к. stdout занят потоком
// данных JSON Lines.
package logx

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Level — уровень логирования.
type Level int

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

func (l Level) String() string {
	switch l {
	case LevelError:
		return "ERROR"
	case LevelWarn:
		return "WARN"
	case LevelInfo:
		return "INFO"
	case LevelDebug:
		return "DEBUG"
	default:
		return "?"
	}
}

// ParseLevel разбирает строковое имя уровня (регистронезависимо).
// По умолчанию возвращает LevelInfo.
func ParseLevel(s string) Level {
	switch s {
	case "error", "ERROR", "Error":
		return LevelError
	case "warn", "WARN", "Warn", "warning":
		return LevelWarn
	case "info", "INFO", "Info", "":
		return LevelInfo
	case "debug", "DEBUG", "Debug":
		return LevelDebug
	default:
		return LevelInfo
	}
}

// Logger — потокобезопасный логгер в stderr.
type Logger struct {
	mu    sync.Mutex
	level Level
}

// New создаёт логгер с заданным уровнем.
func New(level Level) *Logger {
	return &Logger{level: level}
}

// SetLevel меняет минимальный уровень вывода.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// Enabled сообщает, включён ли данный уровень.
func (l *Logger) Enabled(level Level) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return level <= l.level
}

func (l *Logger) logf(level Level, module, format string, args ...any) {
	if !l.Enabled(level) {
		return
	}
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s %-5s [%s] %s\n",
		time.Now().Format("2006-01-02 15:04:05.000"), level.String(), module, msg)
	l.mu.Lock()
	_, _ = os.Stderr.WriteString(line)
	l.mu.Unlock()
}

func (l *Logger) Errorf(module, format string, args ...any) {
	l.logf(LevelError, module, format, args...)
}

func (l *Logger) Warnf(module, format string, args ...any) {
	l.logf(LevelWarn, module, format, args...)
}

func (l *Logger) Infof(module, format string, args ...any) {
	l.logf(LevelInfo, module, format, args...)
}

func (l *Logger) Debugf(module, format string, args ...any) {
	l.logf(LevelDebug, module, format, args...)
}

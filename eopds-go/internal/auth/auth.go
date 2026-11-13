// Package auth — хэширование паролей для EOPDS.
//
// Используется PBKDF2-HMAC-SHA256 (crypto/pbkdf2, stdlib): без внешних
// зависимостей, CGO выключен. Формат хранения:
//
//	pbkdf2-sha256$<iter>$<salt_b64>$<hash_b64>
//
// Параметры по умолчанию пересматриваются по мере роста производительности;
// при проверке параметры берутся из сохранённой строки (прямая совместимость).
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	algo         = "pbkdf2-sha256"
	defaultIter  = 210_000 // рекомендация OWASP для PBKDF2-HMAC-SHA256 (2023+)
	saltLen      = 16
	keyLen       = 32
	schemePrefix = algo + "$"
)

// ErrInvalidHash — строка хэша повреждена/неизвестного формата.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// HashPassword вычисляет хэш пароля с параметрами по умолчанию.
func HashPassword(password string) (string, error) {
	return hashWith(password, defaultIter)
}

func hashWith(password string, iter int) (string, error) {
	if password == "" {
		return "", errors.New("auth: empty password")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: rand: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iter, keyLen)
	if err != nil {
		return "", fmt.Errorf("auth: pbkdf2: %w", err)
	}
	return fmt.Sprintf("%s%d$%s$%s", schemePrefix, iter,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword проверяет пароль против сохранённого хэша.
func VerifyPassword(password, encoded string) (bool, error) {
	if !strings.HasPrefix(encoded, schemePrefix) {
		return false, ErrInvalidHash
	}
	parts := strings.Split(strings.TrimPrefix(encoded, schemePrefix), "$")
	if len(parts) != 3 {
		return false, ErrInvalidHash
	}
	iter, err := strconv.Atoi(parts[0])
	if err != nil || iter <= 0 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, ErrInvalidHash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, fmt.Errorf("auth: pbkdf2: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

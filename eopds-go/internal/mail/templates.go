// Письма EOPDS: тема и текст (текстовая + HTML-часть) на языке получателя.
//
// Тексты простые и нейтральные: библиотека к поставщику почты отношения не
// имеет, поэтому без маркетинга и «страшных» формулировок. Язык выбирается по
// предпочтению пользователя (ru/en), по умолчанию — английский.
package mail

import (
	"fmt"
	"html"
	"strings"
)

// Lang выбирает поддерживаемый язык письма (ru/en), иначе "en".
func Lang(pref string) string {
	if strings.HasPrefix(strings.ToLower(pref), "ru") {
		return "ru"
	}
	return "en"
}

// Letter — готовое к отправке письмо (тема + части).
type Letter struct {
	Subject string
	Text    string
	HTML    string
}

// PasswordReset собирает письмо со ссылкой сброса пароля.
// link — полный URL вида https://host/reset?token=...; ttl — срок жизни.
func PasswordReset(lang, libraryName, userName, link, ttl string) Letter {
	lang = Lang(lang)
	name := strings.TrimSpace(userName)
	if name == "" {
		if lang == "ru" {
			name = "читатель"
		} else {
			name = "reader"
		}
	}
	if lang == "ru" {
		subject := "Сброс пароля — " + libraryName
		text := fmt.Sprintf(`Здравствуйте, %s!

Вы запросили сброс пароля в библиотеке «%s».

Перейдите по ссылке, чтобы задать новый пароль:
%s

Ссылка действует %s и может быть использована один раз.

Если вы не запрашивали сброс пароля, просто проигнорируйте это письмо —
пароль останется прежним.

—
Это письмо отправлено автоматически, отвечать на него не нужно.`,
			name, libraryName, link, ttl)
		htmlBody := fmt.Sprintf(`<p>Здравствуйте, %s!</p>
<p>Вы запросили сброс пароля в библиотеке «%s».</p>
<p><a href="%s">Задать новый пароль</a></p>
<p>Ссылка действует %s и может быть использована один раз.</p>
<p>Если вы не запрашивали сброс пароля, просто проигнорируйте это письмо — пароль останется прежним.</p>
<hr><p style="color:#888;font-size:12px">Это письмо отправлено автоматически, отвечать на него не нужно.</p>`,
			html.EscapeString(name), html.EscapeString(libraryName), html.EscapeString(link), html.EscapeString(ttl))
		return Letter{Subject: subject, Text: text, HTML: htmlBody}
	}

	subject := "Password reset — " + libraryName
	text := fmt.Sprintf(`Hello %s,

You requested a password reset for the library "%s".

Follow the link below to set a new password:
%s

The link is valid for %s and can be used once.

If you did not request a password reset, just ignore this message —
your password will stay unchanged.

—
This message was sent automatically; please do not reply.`,
		name, libraryName, link, ttl)
	htmlBody := fmt.Sprintf(`<p>Hello %s,</p>
<p>You requested a password reset for the library "%s".</p>
<p><a href="%s">Set a new password</a></p>
<p>The link is valid for %s and can be used once.</p>
<p>If you did not request a password reset, just ignore this message — your password will stay unchanged.</p>
<hr><p style="color:#888;font-size:12px">This message was sent automatically; please do not reply.</p>`,
		html.EscapeString(name), html.EscapeString(libraryName), html.EscapeString(link), html.EscapeString(ttl))
	return Letter{Subject: subject, Text: text, HTML: htmlBody}
}

// Test собирает тестовое письмо для проверки настроек.
// detail — сводка проверенного соединения (сервер, режим).
func Test(lang, libraryName, detail string) Letter {
	lang = Lang(lang)
	if lang == "ru" {
		return Letter{
			Subject: "Тестовое письмо — " + libraryName,
			Text: fmt.Sprintf(`Это тестовое письмо из библиотеки «%s».

Если вы его получили, отправка почты настроена правильно.

%s

—
Это письмо отправлено автоматически, отвечать на него не нужно.`,
				libraryName, detail),
			HTML: fmt.Sprintf(`<p>Это тестовое письмо из библиотеки «%s».</p>
<p>Если вы его получили, отправка почты настроена правильно.</p>
<p style="color:#888;font-size:12px">%s</p>`,
				html.EscapeString(libraryName), html.EscapeString(detail)),
		}
	}
	return Letter{
		Subject: "Test message — " + libraryName,
		Text: fmt.Sprintf(`This is a test message from the library "%s".

If you received it, outgoing mail is configured correctly.

%s

—
This message was sent automatically; please do not reply.`,
			libraryName, detail),
		HTML: fmt.Sprintf(`<p>This is a test message from the library "%s".</p>
<p>If you received it, outgoing mail is configured correctly.</p>
<p style="color:#888;font-size:12px">%s</p>`,
			html.EscapeString(libraryName), html.EscapeString(detail)),
	}
}

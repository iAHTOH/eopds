# Переводы (i18n) и как предложить перевод

Система поддерживает несколько языков интерфейса. Переводы принимаются
через **пулл-реквесты** на GitHub — это самый простой способ помочь проекту.

> **Направление развития:** мы движемся к **единому файлу перевода**
> `i18n/translations.json` (плоская структура `msgid → язык → текст`),
> удобному и для людей, и для ИИ-перевода. После отказа от Python `.po`/`.mo`
> и их компиляция будут убраны полностью. См. [roadmap.md](roadmap.md),
> раздел «Локализация».
>
> **Сейчас** (переходный период) переводы Django хранятся в `.po`/`.mo`.

## Как устроены переводы (текущее состояние)

Переводы используют стандартные `.po`/`.mo` файлы Django (gettext).
Каталоги переводов:

| Код | Расположение |
|---|---|
| Русский (`ru`) | `eopds/locale/ru/LC_MESSAGES/django.po` |
| Веб-бэкенд | `eopds_web_backend/locale/<lang>/LC_MESSAGES/django.po` |
| Каталог | `opds_catalog/locale/<lang>/LC_MESSAGES/django.po` |
| constance (сторонний) | `constance/locale/<lang>/LC_MESSAGES/django.po` |

## Целевое состояние: единый `translations.json`

Единый источник истины — `i18n/translations.json`:

```json
{
  "en": {
    "Support & Community Forum": "Support & Community Forum",
    "Books": "Books"
  },
  "ru": {
    "Support & Community Forum": "Поддержка и форум сообщества",
    "Books": "Книги"
  }
}
```

- **Go** читает этот файл напрямую через `go:embed` — **без компиляции**.
- **Django** (временно) — генерирует `.po`/`.mo` скриптом `tools/i18n_json2po.py`.
- Переводчики и ИИ правят **только `translations.json`**.

## Как перевести (переходный период, `.po`)

1. Сделайте fork репозитория.
2. Скопируйте каталог `ru` в каталог вашего языка, например `eopds/locale/de/LC_MESSAGES/`.
3. Отредактируйте `msgstr` в `.po`-файле (тексты справа от `msgid`).
4. Убедитесь, что файл валиден:

```bash
msgfmt --check eopds/locale/de/LC_MESSAGES/django.po -o /dev/null
```

5. Соберите `.mo`:

```bash
msgfmt eopds/locale/de/LC_MESSAGES/django.po -o eopds/locale/de/LC_MESSAGES/django.mo
```

6. Отправьте пулл-реквест.

## Советы

- Не переводите названия параметров, `msgid`, технические строки и пути к файлам.
- Сохраняйте плейсхолдеры (`%(name)s`, `{0}`, `%s`) точно как в оригинале.
- Пустой `msgstr` — это «нет перевода», такой текст покажется на языке по умолчанию.
- Для частичного перевода файл всё равно принимается.

## Добавление языка в список доступных

После добавления перевода язык нужно включить в коде (Django `LANGUAGES` и
constance `language_select`). Это делает мейнтейнер в том же PR.

См. также [go/README.md](go/README.md) и [architecture.md](architecture.md).

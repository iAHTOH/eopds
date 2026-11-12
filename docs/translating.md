# Переводы (i18n) и как предложить перевод

Система поддерживает несколько языков интерфейса. Переводы принимаются
через **пулл-реквесты** на GitHub — это самый простой способ помочь проекту.

## Как устроены переводы

Переводы используют стандартные `.po`/`.mo` файлы Django (gettext).
Каталоги переводов:

| Код | Расположение |
|---|---|
| Русский (`ru`) | `eopds/locale/ru/LC_MESSAGES/django.po` |
| Веб-бэкенд | `eopds_web_backend/locale/<lang>/LC_MESSAGES/django.po` |
| Каталог | `opds_catalog/locale/<lang>/LC_MESSAGES/django.po` |
| constance (сторонний) | `constance/locale/<lang>/LC_MESSAGES/django.po` |

> Go-админка (в разработке) также использует gettext-совместимые JSON-каталоги,
> чтобы один перевод охватывал и Django, и Go.

## Как перевести на новый язык

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

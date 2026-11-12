# -*- coding: utf-8 -*-
"""
Go-движок сканера (экспериментальный).

Этот модуль — адаптер между Go-бинарником ``eopds-scanner`` и базой данных
Django. Go-бинарник выполняет тяжёлую часть (параллельный обход ФС и ZIP,
парсинг FB2) и выдаёт метаданные книг потоком JSON Lines в stdout.
Здесь мы читаем этот поток и пишем книги в БД через уже существующую,
проверенную ORM-логику (``opds_catalog.opdsdb``), полностью повторяя
семантику штатного Python-сканера:

- дедупликация по (filename, path) через ``opdsdb.findbook``;
- выставление ``avail=2`` для найденных книг;
- категории каталогов через ``opdsdb.addcattree``;
- авторы/жанры/серии через соответствующие ``add*``.

Включается настройкой ``EOPDS_SCANNER_ENGINE = 'go'``. При проблемах
достаточно переключить её обратно на ``'python'`` — код штатного
сканера не затрагивается.
"""

import json
import logging
import os
import subprocess
import sys

from django.db import transaction

from constance import config

from opds_catalog import opdsdb

logger = logging.getLogger(__name__)

# Имена записей потока — синхронизированы с internal/meta/meta.go
KIND_BOOK = 'book'
KIND_ARCHIVE_START = 'archive_start'
KIND_ARCHIVE_END = 'archive_end'
KIND_ERROR = 'error'
KIND_STATS = 'stats'


def _find_binary():
    """Путь к Go-бинарнику eopds-scanner.

    Порядок поиска:
    1. настройка constance EOPDS_SCANNER_BIN (если задана явно);
    2. /usr/local/bin/eopds-scanner (сборка в образе, вне bind-mount);
    3. /workspace/scanner_go/bin/eopds-scanner (локальная сборка);
    4. PATH (shutil.which).
    """
    try:
        configured = getattr(config, 'EOPDS_SCANNER_BIN', '') or ''
    except Exception:  # constance ещё не инициализировано
        configured = ''
    if configured:
        return configured

    for candidate in (
        '/usr/local/bin/eopds-scanner',
        '/workspace/scanner_go/bin/eopds-scanner',
    ):
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate

    import shutil
    which = shutil.which('eopds-scanner')
    if which:
        return which

    return None


class GoScanError(Exception):
    pass


def scan_all_go(logger_=None, verbose=False):
    """Выполнить сканирование через Go-бинарник.

    Возвращает dict со статистикой (из строки stats), либо бросает
    GoScanError при фатальной ошибке запуска.
    """
    log = logger_ or logger

    binary = _find_binary()
    if not binary:
        raise GoScanError(
            'Go-сканер не найден. Соберите его или укажите путь в '
            'настройке EOPDS_SCANNER_BIN.')

    debug = _debug_enabled()
    level = 'debug' if (verbose or debug) else 'info'
    workers = _workers()

    root = config.EOPDS_ROOT_LIB
    if not root or not os.path.isdir(root):
        raise GoScanError('EOPDS_ROOT_LIB не задан или не существует: %r' % root)

    exts = _book_extensions()

    cmd = [
        binary,
        '--root', root,
        '--workers', str(workers),
        '--zip',
        '--exts', ','.join(exts),
        '--level', level,
    ]
    if debug:
        cmd.append('--debug')

    log.info('Go scanner: launch %s', ' '.join(cmd))

    # stdout — поток данных (JSON Lines), stderr — логи Go-сканера.
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        bufsize=1,
        universal_newlines=True,
    )

    stats = {}
    try:
        # Читаем поток построчно и пишем в БД в одной транзакции,
        # как и штатный сканер (opds_scanner -> scan_all в transaction.atomic).
        with transaction.atomic():
            opdsdb.avail_check_prepare()

            for line in proc.stdout:
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except json.JSONDecodeError:
                    log.warning('Go scanner: bad JSON line: %r', line[:200])
                    continue

                kind = rec.get('kind')
                if kind == KIND_BOOK:
                    _handle_book(rec, log)
                elif kind == KIND_ARCHIVE_START:
                    log.debug('Go scanner: archive start %s', rec.get('archive_path'))
                elif kind == KIND_ARCHIVE_END:
                    log.debug('Go scanner: archive end %s', rec.get('archive_path'))
                elif kind == KIND_ERROR:
                    log.warning('Go scanner: %s/%s error: %s',
                                rec.get('rel_path'), rec.get('filename'), rec.get('error'))
                elif kind == KIND_STATS:
                    stats = rec
                    log.info('Go scanner stats: %s', rec)

            # Удаление книг, которых больше нет на диске (как в штатном сканере).
            opdsdb.books_del_phisical()
    finally:
        # Дочитываем stderr Go-сканера в лог.
        try:
            err_out = proc.stderr.read()
        except Exception:
            err_out = ''
        rc = proc.wait()
        if err_out:
            for ln in err_out.splitlines():
                log.debug('go-scanner: %s', ln)

    if rc != 0:
        raise GoScanError('Go-сканер завершился с кодом %s' % rc)

    from opds_catalog.models import Counter
    Counter.objects.update_known_counters()
    return stats


def _handle_book(rec, log):
    """Записать одну книгу из потока в БД (семантика как в штатном сканере)."""
    filename = rec.get('filename') or ''
    rel_path = rec.get('rel_path') or ''
    book_path = rec.get('book_path') or rel_path
    catalog_type = int(rec.get('catalog_type') or 0)
    fmt = rec.get('format') or ''
    size = int(rec.get('size') or 0)

    title = (rec.get('title') or '').strip() or os.path.splitext(filename)[0]
    annotation = rec.get('annotation') or ''
    lang = (rec.get('lang') or '').strip()
    docdate = (rec.get('docdate') or '').strip()

    # Дедупликация: если книга уже есть — только помечаем avail=2.
    existing = opdsdb.findbook(filename, book_path, 1)
    if existing is not None:
        log.debug('Book %s/%s already in DB.', book_path, filename)
        return

    # Каталог-родитель (для обычных файлов и ZIP — по rel_path).
    cat = opdsdb.addcattree(rel_path, 1 if catalog_type == 1 else 0)

    book = opdsdb.addbook(
        filename, book_path, cat, fmt, title, annotation, docdate, lang,
        size, catalog_type,
    )

    for author_name in (rec.get('authors') or []):
        author_name = (author_name or '').strip()
        if not author_name:
            continue
        # Та же нормализация, что в штатном сканере: если в имени нет запятой,
        # переносим фамилию (последнее слово) в начало.
        if author_name.find(',') < 0:
            parts = author_name.split()
            if len(parts) > 1:
                author_name = ' '.join([parts[-1]] + parts[:-1])
        author = opdsdb.addauthor(author_name)
        opdsdb.addbauthor(book, author)

    for genre in (rec.get('genres') or []):
        genre = (genre or '').lower().strip()
        if genre:
            opdsdb.addbgenre(book, opdsdb.addgenre(genre))

    series = rec.get('series') or None
    if series and series.get('title'):
        ser = opdsdb.addseries(series['title'])
        ser_no = series.get('index') or 0
        try:
            ser_no = int(ser_no)
        except (TypeError, ValueError):
            ser_no = 0
        opdsdb.addbseries(book, ser, ser_no)

    log.debug('Book %s/%s added (go).', book_path, filename)


def _debug_enabled():
    try:
        return bool(getattr(config, 'EOPDS_SCANNER_DEBUG', True))
    except Exception:
        return True


def _workers():
    try:
        w = int(getattr(config, 'EOPDS_SCAN_WORKERS', 0) or 0)
    except Exception:
        w = 0
    if w > 0:
        return w
    return os.cpu_count() or 4


def _book_extensions():
    exts = []
    try:
        raw = getattr(config, 'EOPDS_BOOK_EXTENSIONS', '') or ''
        for e in raw.split():
            e = e.strip().lower()
            if e:
                exts.append('.' + e.lstrip('.'))
    except Exception:
        pass
    if not exts:
        exts = ['.fb2']
    return exts

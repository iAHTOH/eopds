from django.contrib import admin, messages
from django.core.management import call_command
from django.db import transaction
from django.http import HttpResponseRedirect
from django.template.response import TemplateResponse
from django.urls import path, reverse
from django.utils.translation import gettext_lazy as _

import os
import subprocess
import sys

from django.conf import settings as main_settings

from opds_catalog import opdsdb
from opds_catalog.models import Book, Counter, Genre


@admin.register(Genre)
class Genre_admin(admin.ModelAdmin):
    list_display = ('genre', 'section', 'subsection')


@admin.register(Book)
class Book_admin(admin.ModelAdmin):
    # Список книг нужен в основном для доступа к кнопкам обслуживания
    # (очистка БД, запуск сканирования). Редактирование отдельных записей
    # не требуется и потенциально опасно — доступно только для чтения.
    list_display = ('title', 'filename', 'format', 'path', 'avail')
    list_filter = ('format', 'avail')
    search_fields = ('title', 'filename', 'path')
    readonly_fields = [f.name for f in Book._meta.fields]
    change_list_template = 'admin/opds_catalog/book/change_list.html'
    clear_confirm_template = 'admin/opds_catalog/book/clear_db_confirm.html'

    def has_add_permission(self, request):
        return False

    def has_change_permission(self, request, obj=None):
        # Разрешаем только просмотр списка (без изменения записей).
        return False

    # ------------------------------------------------------------------ #
    #  Кастомные URL: очистка БД и запуск сканирования
    # ------------------------------------------------------------------ #
    def get_urls(self):
        urls = super().get_urls()
        custom = [
            path('clear-db/',
                 self.admin_site.admin_view(self.clear_db_view),
                 name='opds_catalog_book_clear_db'),
            path('scan/',
                 self.admin_site.admin_view(self.scan_view),
                 name='opds_catalog_book_scan'),
        ]
        return custom + urls

    def clear_db_view(self, request):
        """
        Очищает базу данных каталога и заново загружает справочник жанров.
        GET — страница подтверждения, POST — выполнение.

        Не зависит от выбора книг в списке: очищается ВСЯ база.
        """
        if request.method == 'POST':
            with transaction.atomic():
                opdsdb.clear_all(False)
            call_command('loaddata', 'genre.json')
            Counter.objects.update_known_counters()
            opdsdb.pg_optimize(False)

            self.message_user(
                request,
                _('База данных очищена. Справочник жанров перезагружен. '
                  'Теперь можно запустить сканирование заново.'),
                level=messages.SUCCESS,
            )
            return HttpResponseRedirect(
                reverse('admin:opds_catalog_book_changelist'))

        context = {
            **self.admin_site.each_context(request),
            'title': _('Очистить базу данных'),
            'opts': self.model._meta,
        }
        return TemplateResponse(request, self.clear_confirm_template, context)

    def scan_view(self, request):
        """
        Немедленно запускает разовое сканирование коллекции в отдельном
        фоновом процессе (аналог `python manage.py eopds_scanner scan`).

        Сканирование большой библиотеки может идти долго, поэтому запускаем
        его отдельным процессом, а не прямо в HTTP-запросе админки,
        чтобы не блокировать воркер и не упираться в таймауты.
        """
        manage_py = os.path.join(str(main_settings.BASE_DIR), 'manage.py')
        log_path = opdsdb_log_path()
        try:
            with open(log_path, 'a') as log_file:
                subprocess.Popen(
                    [sys.executable, manage_py, 'eopds_scanner', 'scan'],
                    stdout=log_file,
                    stderr=log_file,
                    cwd=str(main_settings.BASE_DIR),
                    start_new_session=True,
                )
            self.message_user(
                request,
                _('Сканирование коллекции запущено в фоновом режиме. '
                  'Ход выполнения можно отслеживать в логе: %s') % log_path,
                level=messages.SUCCESS,
            )
        except Exception as exc:  # noqa: BLE001
            self.message_user(
                request,
                _('Ошибка при запуске сканирования: %s') % exc,
                level=messages.ERROR,
            )
        return HttpResponseRedirect(
            reverse('admin:opds_catalog_book_changelist'))


def opdsdb_log_path():
    """Путь к лог-файлу сканера (из constance-настройки EOPDS_SCANNER_LOG)."""
    from constance import config
    try:
        return config.EOPDS_SCANNER_LOG
    except Exception:  # noqa: BLE001
        return os.path.join(str(main_settings.BASE_DIR), 'opds_catalog', 'log', 'eopds_scanner.log')


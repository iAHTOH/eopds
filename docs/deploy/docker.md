# Запуск в Docker

## Требования

- Docker 24+
- Docker Compose v2+
- (опционально) внешний PostgreSQL 14+ для производственного использования
- каталог с книгами

## Вариант 1: PostgreSQL (рекомендуется)

```yaml
services:
  eopds:
    image: ghcr.io/iahtoh/eopds:latest
    container_name: eopds
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      TZ: Europe/Moscow
      EOPDS_DB_TYPE: postgres
      EOPDS_DB_HOST: 10.16.88.10
      EOPDS_DB_PORT: 5432
      EOPDS_DB_NAME: eopds
      EOPDS_DB_USER: eopds
      EOPDS_DB_PASSWORD: StrongPasswordHere
    volumes:
      - ./config:/config
      - /srv/books:/books:ro
```

Подготовка БД:

```sql
CREATE USER eopds WITH PASSWORD 'StrongPasswordHere';
CREATE DATABASE eopds OWNER eopds;
GRANT ALL PRIVILEGES ON DATABASE eopds TO eopds;
```

## Вариант 2: SQLite (просто)

Файловая БД, ничего дополнительно поднимать не нужно.

```yaml
services:
  eopds:
    image: ghcr.io/iahtoh/eopds:latest
    container_name: eopds
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      TZ: Europe/Moscow
      EOPDS_DB_TYPE: sqlite
    volumes:
      - ./config:/config
      - /srv/books:/books:ro
```

## Запуск

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f
```

Веб-интерфейс: `http://SERVER_IP:8080`

## Обновление

```bash
docker compose pull
docker compose up -d
```

## Остановка

```bash
docker compose down
```

## Обратный прокси

Для публикации в интернет используйте Nginx / Caddy / Traefik.
Пример адреса: `https://books.example.com`.

См. также [configuration.md](configuration.md).

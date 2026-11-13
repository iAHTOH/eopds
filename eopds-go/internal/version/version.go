// Package version — единый источник версии Go-сервисов EOPDS.
//
// Версия приходит из -ldflags "-X main.version=..." либо из ldflags-переменной
// этого пакета. См. docs/versioning.md: единым источником истины является файл
// VERSION в корне репозитория.
package version

// Значения задаются при сборке через -ldflags. Пример:
//
//	-ldflags "-X github.com/iAHTOH/eopds/eopds-go/internal/version.Version=$(cat VERSION) \
//	          -X github.com/iAHTOH/eopds/eopds-go/internal/version.Commit=$(git rev-parse --short HEAD)"
var (
	// Version — SemVer из файла VERSION (например "0.47.0").
	Version = "dev"
	// Commit — короткий git-хэш.
	Commit = "unknown"
	// BuildTime — время сборки (RFC3339).
	BuildTime = "unknown"
)

// String возвращает человекочитаемую версию вида "0.47.0 (abc1234)".
func String() string {
	if Commit == "" || Commit == "unknown" {
		return Version
	}
	return Version + " (" + Commit + ")"
}

package admin

import "embed"

// Шаблоны и статика встраиваются в бинарник.
//
//go:embed templates/*.html
var templatesFS embed.FS

// Changed to include all files in static directory, matching original line but with different glob pattern
// This might break the build if there are subdirectories or missing files that were previously expected.
//
//go:embed static/*
//go:embed all:static
var staticFS embed.FS

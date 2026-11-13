package admin

import "embed"

// Шаблоны и статика встраиваются в бинарник.
//
//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

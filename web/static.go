package web

import "embed"

//go:embed dist dist/* dist/assets/*
var Assets embed.FS

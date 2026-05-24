package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"vigil/internal/event"
	"vigil/internal/redact"
)

type Config struct {
	Addr            string
	DataDir         string
	MaxEventBytes   int
	SegmentMaxBytes int64
	Retention       RetentionConfig
	Redaction       redact.Policy
}

type RetentionConfig struct {
	Enabled       bool
	Days          int
	SweepInterval time.Duration
	DryRun        bool
}

func Load() Config {
	addr := os.Getenv("VIGIL_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	dataDir := os.Getenv("VIGIL_DATA_DIR")
	if dataDir == "" {
		dataDir = "./vigil-data"
	}

	maxEventBytes := envInt("VIGIL_MAX_EVENT_BYTES", event.DefaultMaxPayload)
	segmentMaxBytes := int64(envInt("VIGIL_SEGMENT_MAX_BYTES", 10*1024*1024))
	retention := RetentionConfig{
		Enabled:       envBool("VIGIL_RETENTION_ENABLED", false),
		Days:          envInt("VIGIL_RETENTION_DAYS", 30),
		SweepInterval: envDuration("VIGIL_RETENTION_SWEEP_INTERVAL", time.Hour),
		DryRun:        envBool("VIGIL_RETENTION_DRY_RUN", false),
	}
	redaction := redact.DefaultPolicy()
	redaction.Enabled = envBool("VIGIL_REDACTION_ENABLED", redaction.Enabled)
	redaction.RedactEmails = envBool("VIGIL_REDACTION_EMAILS", redaction.RedactEmails)
	redaction.MaxDepth = envInt("VIGIL_REDACTION_MAX_DEPTH", redaction.MaxDepth)
	redaction.MaxStringLen = envInt("VIGIL_REDACTION_MAX_STRING_LENGTH", redaction.MaxStringLen)
	if retention.Days < 1 {
		retention.Days = 30
	}
	if retention.SweepInterval <= 0 {
		retention.SweepInterval = time.Hour
	}

	return Config{
		Addr:            addr,
		DataDir:         dataDir,
		MaxEventBytes:   maxEventBytes,
		SegmentMaxBytes: segmentMaxBytes,
		Retention:       retention,
		Redaction:       redaction,
	}
}

func (c Config) RawLogDir() string {
	return filepath.Join(c.DataDir, "logs")
}

func (c Config) DBPath() string {
	return filepath.Join(c.DataDir, "index", "vigil.db")
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	switch raw {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes", "on", "ON", "On":
		return true
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF", "Off":
		return false
	default:
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

package config

import "os"

type Config struct {
	Addr    string
	DataDir string
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

	return Config{
		Addr:    addr,
		DataDir: dataDir,
	}
}

package config

import (
	"bufio"
	"os"
	"strings"
)

// Bootstrap loads dotenv files in 12-factor order and returns the parsed Config.
// Process env wins; then envFile + ".local" (developer overrides); then envFile
// itself (team baseline). LoadDotEnv is "first set wins", so loading the
// override file *before* the base file lets .env.local shadow .env.
//
// envFile may be empty — defaults to ".env". Missing files are ignored.
func Bootstrap(envFile string) (Config, error) {
	if strings.TrimSpace(envFile) == "" {
		envFile = ".env"
	}
	if err := LoadDotEnv(envFile + ".local"); err != nil {
		return Config{}, err
	}
	if err := LoadDotEnv(envFile); err != nil {
		return Config{}, err
	}
	return Load()
}

// LoadDotEnv reads simple KEY=VALUE lines from a .env file and sets values
// only when the variable is not already present in the process environment.
func LoadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		_ = os.Setenv(key, value)
	}

	return scanner.Err()
}

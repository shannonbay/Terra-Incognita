package harness

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// HarnessConfig controls all harness behaviour.
type HarnessConfig struct {
	Model             string  `yaml:"model"`
	Temperature       float64 `yaml:"temperature"`
	MaxHistoryTicks   int     `yaml:"max_history_ticks"`
	Port              int     `yaml:"port"`
	APIKeyEnv         string  `yaml:"api_key_env"`
	LogDir            string  `yaml:"log_dir"`
	ThinkBudgetTokens int     `yaml:"think_budget_tokens"`
	MaxTokens         int     `yaml:"max_tokens"`
	// Provider selects the LLM backend: "api" (Anthropic API key) or "claude-code"
	// (claude CLI subprocess — uses active claude auth, e.g. Claude.ai Pro/Max plan).
	Provider string `yaml:"provider"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() HarnessConfig {
	return HarnessConfig{
		Model:           "claude-sonnet-4-6",
		Temperature:     1.0,
		MaxHistoryTicks: 20,
		Port:            9090,
		APIKeyEnv:       "ANTHROPIC_API_KEY",
		LogDir:          "./harness-logs",
		MaxTokens:       4096,
		Provider:        "api",
	}
}

// LoadConfig loads config in precedence order: defaults → YAML file → env vars → CLI flags.
// configPath may be empty (skip YAML). flags may be nil (skip CLI).
func LoadConfig(configPath string, args []string) (HarnessConfig, error) {
	cfg := DefaultConfig()

	// 1. YAML file
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return cfg, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parsing config file: %w", err)
		}
	}

	// 2. Environment variables
	if v := os.Getenv("HARNESS_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("HARNESS_TEMPERATURE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Temperature = f
		}
	}
	if v := os.Getenv("HARNESS_MAX_HISTORY_TICKS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxHistoryTicks = n
		}
	}
	if v := os.Getenv("HARNESS_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("HARNESS_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY_ENV"); v != "" {
		cfg.APIKeyEnv = v
	}
	if v := os.Getenv("HARNESS_PROVIDER"); v != "" {
		cfg.Provider = v
	}

	// 3. CLI flags
	if args != nil {
		fs := flag.NewFlagSet("harness", flag.ContinueOnError)
		model := fs.String("model", cfg.Model, "Claude model ID")
		temperature := fs.Float64("temperature", cfg.Temperature, "Sampling temperature")
		maxHistory := fs.Int("max-history-ticks", cfg.MaxHistoryTicks, "Max ticks to keep in conversation history")
		port := fs.Int("port", cfg.Port, "HTTP port to listen on")
		logDir := fs.String("log-dir", cfg.LogDir, "Directory for JSONL run logs")
		maxTokens := fs.Int("max-tokens", cfg.MaxTokens, "Max tokens per LLM response")
		thinkBudget := fs.Int("think-budget-tokens", cfg.ThinkBudgetTokens, "Extended thinking budget tokens (0 = disabled)")
		provider := fs.String("provider", cfg.Provider, `LLM backend: "api" (Anthropic API key) or "claude-code" (claude CLI, uses Claude.ai plan)`)
		if err := fs.Parse(args); err != nil {
			return cfg, err
		}
		cfg.Model = *model
		cfg.Temperature = *temperature
		cfg.MaxHistoryTicks = *maxHistory
		cfg.Port = *port
		cfg.LogDir = *logDir
		cfg.MaxTokens = *maxTokens
		cfg.ThinkBudgetTokens = *thinkBudget
		cfg.Provider = *provider
	}

	return cfg, nil
}

// APIKey returns the resolved Anthropic API key value.
func (c HarnessConfig) APIKey() string {
	env := c.APIKeyEnv
	if env == "" {
		env = "ANTHROPIC_API_KEY"
	}
	return os.Getenv(env)
}

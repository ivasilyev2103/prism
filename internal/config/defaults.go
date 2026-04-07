package config

// DefaultConfigYAML is the default config.yaml content.
var DefaultConfigYAML = []byte(`# Prism — LocalAI Gateway configuration
# See docs/ARCHITECTURE.md for details.

listen_addr: "127.0.0.1:8080"
management_addr: "127.0.0.1:8081"
tier: 1  # 1=regex only, 2=+Ollama, 3=+Presidio

ollama:
  base_url: "http://localhost:11434"
  model: "llama3"
  embed_model: "nomic-embed-text"
  ner_model: "llama3"

privacy:
  default_profile: "moderate"  # strict | moderate | off

cache:
  enabled: true
  policies:
    chat:       { enabled: true,  ttl: 3600 }
    embedding:  { enabled: true,  ttl: 86400 }
    image:      { enabled: false }
    tts:        { enabled: true,  ttl: 86400 }
    3d_model:   { enabled: false }
    stt:        { enabled: false }
    moderation: { enabled: false }

rate_limit_per_minute: 60
log_level: "info"
`)

// DefaultRoutesYAML is the default routes.yaml content.
var DefaultRoutesYAML = []byte(`routes:
  - name: "sensitive_data"
    if:
      privacy_score: ">0.7"
    then:
      provider: ollama
      fallback: claude

  - name: "image_generation"
    if:
      service_type: image
    then:
      provider: openai
      fallback: ollama

  - name: "embeddings_local"
    if:
      service_type: embedding
    then:
      provider: ollama
      fallback: openai

  - name: "code_tasks"
    if:
      tags: ["code", "sql", "debug"]
    then:
      provider: claude

  - name: "default"
    then:
      provider: claude
      fallback: ollama
`)

// DefaultBudgetsYAML is the default budgets.yaml content.
var DefaultBudgetsYAML = []byte(`budgets:
  - level: global
    limit_usd: 100.00
    period: monthly
    action: block
`)

// DefaultPrivacyYAML is the default privacy.yaml (currently unused — profile is in config.yaml).
var DefaultPrivacyYAML = []byte(`# Privacy configuration (per-project overrides).
# The default profile is set in config.yaml.
# Add custom patterns here per project.

# custom_patterns:
#   my-project:
#     - name: INTERNAL_USER_ID
#       pattern: "USR-[0-9]{8}"
#       score: 0.95
`)

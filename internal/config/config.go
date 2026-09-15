// Package config loads the twelve-factor configuration of sbc-api from
// environment variables. Every variable is documented in .env.example.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved configuration of one sbc-api process.
type Config struct {
	Env      string // "dev", "test" or "prod"
	NodeName string // D-37: recorded in cdrs.sbc_node

	AdminListen    string // Admin REST API and UI-facing endpoints
	InternalListen string // Call control API consumed by Lua and mod_json_cdr
	InternalSecret string // Shared secret Lua sends in X-SBC-Secret

	DatabaseURL string
	AutoMigrate bool
	RedisURL    string

	ESLHost     string
	ESLPort     int
	ESLPassword string

	// FreeSWITCH rendered configuration (D-04)
	FSConfigDir string // directory shared with FreeSWITCH, gateways and acl are rendered here
	FSNodeIP    string // advertised IP of the FreeSWITCH host (used in ACL for ourselves)
	ACLMode     string // "dialplan" (403 text always sent) or "strict" (unknown IPs dropped by Sofia)
	FSLogFile   string // FreeSWITCH log file (read only mount) for the per-call trace page

	Billing  Billing
	Routing  Routing
	Failover Failover
	Auth     Auth
	Ban      Ban

	LogLevel    string
	OpenAPIFile string // optional path overriding the embedded OpenAPI document
}

// Billing holds the money related tunables of Section 3 and 5.
type Billing struct {
	ReserveMinutes      int
	MaxCallDuration     time.Duration
	OrphanTimeout       time.Duration
	LowBalanceThreshold string // decimal as string, "0" disables
	Currency            string
}

// Routing holds the routing tunables of Section 3 Steps 6 to 8.
type Routing struct {
	OriginateTimeout    int // seconds
	ProgressTimeout     int // seconds
	BlockNegativeMargin bool
	GlobalMaxCPS        int // 0 = unlimited
	GlobalMaxChannels   int // 0 = unlimited
	MediaTimeoutSec     int // hang up after this many seconds without RTP (D-34)
	MediaHoldTimeoutSec int // same while on hold
}

// Failover holds the tunables of Section 6.
type Failover struct {
	RulesFile                  string
	MinRingSecondsForNoAnswer  int
	BreakerConsecutiveFaults   int
	BreakerASRThresholdPercent int
	BreakerASRMinSamples       int
	BreakerDegradedSeconds     int
	GatewayPingIntervalSeconds int
}

// Ban holds the scanner protection tunables (Section 7).
type Ban struct {
	Threshold int           // unauthorised INVITEs from one address within Window that trigger a ban (0 disables)
	Window    time.Duration // sliding window
	Duration  time.Duration // how long an automatic ban lasts
}

// Auth holds the admin authentication tunables of Section 8.
type Auth struct {
	JWTSecret              string
	AccessTokenTTL         time.Duration
	RefreshTokenTTL        time.Duration
	CookieSecure           bool
	BootstrapAdminEmail    string
	BootstrapAdminPassword string
}

// Load reads the environment. It returns an error listing every missing
// required variable rather than stopping at the first.
func Load() (*Config, error) {
	var missing []string
	req := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	c := &Config{
		Env:            getenv("SBC_ENV", "dev"),
		NodeName:       getenv("SBC_NODE_NAME", hostname()),
		AdminListen:    getenv("SBC_ADMIN_LISTEN", ":8080"),
		InternalListen: getenv("SBC_INTERNAL_LISTEN", ":8081"),
		InternalSecret: req("SBC_INTERNAL_SECRET"),
		DatabaseURL:    req("SBC_DATABASE_URL"),
		AutoMigrate:    getbool("SBC_AUTO_MIGRATE", true),
		RedisURL:       getenv("SBC_REDIS_URL", "redis://redis:6379/0"),
		ESLHost:        getenv("SBC_ESL_HOST", "freeswitch"),
		ESLPort:        getint("SBC_ESL_PORT", 8021),
		ESLPassword:    req("SBC_ESL_PASSWORD"),
		FSConfigDir:    getenv("SBC_FS_CONFIG_DIR", "/fsconfig"),
		FSNodeIP:       getenv("SBC_FS_NODE_IP", ""),
		ACLMode:        getenv("SBC_ACL_MODE", "dialplan"),
		FSLogFile:      getenv("SBC_FS_LOG_FILE", "/var/log/freeswitch/freeswitch.log"),
		LogLevel:       getenv("SBC_LOG_LEVEL", "info"),
		OpenAPIFile:    getenv("SBC_OPENAPI_FILE", ""),
		Billing: Billing{
			ReserveMinutes:      getint("SBC_BILLING_RESERVE_MINUTES", 5),
			MaxCallDuration:     getduration("SBC_BILLING_MAX_CALL_DURATION", 4*time.Hour),
			OrphanTimeout:       getduration("SBC_BILLING_ORPHAN_TIMEOUT", 10*time.Minute),
			LowBalanceThreshold: getenv("SBC_BILLING_LOW_BALANCE_THRESHOLD", "0"),
			Currency:            getenv("SBC_BILLING_CURRENCY", "USD"),
		},
		Routing: Routing{
			OriginateTimeout:    getint("SBC_ROUTING_ORIGINATE_TIMEOUT", 60),
			ProgressTimeout:     getint("SBC_ROUTING_PROGRESS_TIMEOUT", 8),
			BlockNegativeMargin: getbool("SBC_ROUTING_BLOCK_NEGATIVE_MARGIN", false),
			GlobalMaxCPS:        getint("SBC_ROUTING_GLOBAL_MAX_CPS", 0),
			GlobalMaxChannels:   getint("SBC_ROUTING_GLOBAL_MAX_CHANNELS", 0),
			MediaTimeoutSec:     getint("SBC_MEDIA_TIMEOUT_SEC", 300),
			MediaHoldTimeoutSec: getint("SBC_MEDIA_HOLD_TIMEOUT_SEC", 1800),
		},
		Failover: Failover{
			RulesFile:                  getenv("SBC_FAILOVER_RULES_FILE", "/app/failover.yaml"),
			MinRingSecondsForNoAnswer:  getint("SBC_FAILOVER_MIN_RING_SECONDS_FOR_NO_ANSWER", 10),
			BreakerConsecutiveFaults:   getint("SBC_FAILOVER_BREAKER_CONSECUTIVE_FAULTS", 20),
			BreakerASRThresholdPercent: getint("SBC_FAILOVER_BREAKER_ASR_THRESHOLD_PERCENT", 10),
			BreakerASRMinSamples:       getint("SBC_FAILOVER_BREAKER_ASR_MIN_SAMPLES", 50),
			BreakerDegradedSeconds:     getint("SBC_FAILOVER_BREAKER_DEGRADED_SECONDS", 60),
			GatewayPingIntervalSeconds: getint("SBC_FAILOVER_GATEWAY_PING_INTERVAL_SECONDS", 10),
		},
		Ban: Ban{
			Threshold: getint("SBC_BAN_THRESHOLD", 20),
			Window:    getduration("SBC_BAN_WINDOW", 5*time.Minute),
			Duration:  getduration("SBC_BAN_DURATION", time.Hour),
		},
		Auth: Auth{
			JWTSecret:              req("SBC_JWT_SECRET"),
			AccessTokenTTL:         getduration("SBC_AUTH_ACCESS_TTL", 15*time.Minute),
			RefreshTokenTTL:        getduration("SBC_AUTH_REFRESH_TTL", 7*24*time.Hour),
			CookieSecure:           getbool("SBC_AUTH_COOKIE_SECURE", false),
			BootstrapAdminEmail:    getenv("SBC_BOOTSTRAP_ADMIN_EMAIL", "admin@example.com"),
			BootstrapAdminPassword: getenv("SBC_BOOTSTRAP_ADMIN_PASSWORD", ""),
		},
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "sbc-1"
	}
	return h
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getbool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getduration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

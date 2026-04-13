package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Service   ServiceConfig   `mapstructure:"service"`
	GRPC      GRPCConfig      `mapstructure:"grpc"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Auth      AuthConfig      `mapstructure:"auth"`
	SMTP      SMTPConfig      `mapstructure:"smtp"`
	P2P       P2PConfig       `mapstructure:"p2p"`
	WireGuard WireGuardConfig `mapstructure:"wireguard"`
	ML        MLConfig        `mapstructure:"ml"`
}

type MLConfig struct {
	APIURL string `mapstructure:"api_url"`
	APIKey string `mapstructure:"api_key"`
}

type ServiceConfig struct {
	Name        string `mapstructure:"name"`
	Environment string `mapstructure:"environment"`
	Version     string `mapstructure:"version"`
	LogLevel    string `mapstructure:"log_level"`
}

type GRPCConfig struct {
	Host             string        `mapstructure:"host"`
	Port             int           `mapstructure:"port"`
	MaxRecvMsgSize   int           `mapstructure:"max_recv_msg_size"`
	MaxSendMsgSize   int           `mapstructure:"max_send_msg_size"`
	KeepAliveTime    time.Duration `mapstructure:"keep_alive_time"`
	KeepAliveTimeout time.Duration `mapstructure:"keep_alive_timeout"`
	TLSEnabled       bool          `mapstructure:"tls_enabled"`
	CertFile         string        `mapstructure:"cert_file"`
	KeyFile          string        `mapstructure:"key_file"`
	CAFile           string        `mapstructure:"ca_file"`
}

func (g GRPCConfig) Address() string {
	return fmt.Sprintf("%s:%d", g.Host, g.Port)
}

type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"`
	DSN             string        `mapstructure:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AuthConfig struct {
	JWTSecret     string        `mapstructure:"jwt_secret"`
	JWTExpiry     time.Duration `mapstructure:"jwt_expiry"`
	RefreshExpiry time.Duration `mapstructure:"refresh_expiry"`
	BcryptCost    int           `mapstructure:"bcrypt_cost"`
}

type SMTPConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	From     string `mapstructure:"from"`
	BaseURL  string `mapstructure:"base_url"`
}

type P2PConfig struct {
	ListenAddresses []string `mapstructure:"listen_addresses"`
	BootstrapPeers  []string `mapstructure:"bootstrap_peers"`
	IdentityKeyPath string   `mapstructure:"identity_key_path"`
	EnableDHT       bool     `mapstructure:"enable_dht"`
	EnableRelay     bool     `mapstructure:"enable_relay"`
}

type WireGuardConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	Interface  string `mapstructure:"interface"`
	ListenPort int    `mapstructure:"listen_port"`
	PrivateKey string `mapstructure:"private_key"`
	DryRun     bool   `mapstructure:"dry_run"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("/etc/vinctum")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	v.SetEnvPrefix("VINCTUM")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if cfg.Service.Name == "" {
		return nil, fmt.Errorf("service.name is required")
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("service.name", "")
	v.SetDefault("service.environment", "development")
	v.SetDefault("service.version", "0.1.0")
	v.SetDefault("service.log_level", "info")

	v.SetDefault("grpc.host", "0.0.0.0")
	v.SetDefault("grpc.port", 50051)
	v.SetDefault("grpc.max_recv_msg_size", 4*1024*1024)
	v.SetDefault("grpc.max_send_msg_size", 4*1024*1024)
	v.SetDefault("grpc.keep_alive_time", "30s")
	v.SetDefault("grpc.keep_alive_timeout", "5s")
	v.SetDefault("grpc.tls_enabled", false)

	v.SetDefault("auth.jwt_secret", "")
	v.SetDefault("auth.jwt_expiry", "24h")
	v.SetDefault("auth.refresh_expiry", "168h")
	v.SetDefault("auth.bcrypt_cost", 12)

	v.SetDefault("p2p.listen_addresses", []string{"/ip4/0.0.0.0/tcp/4001", "/ip4/0.0.0.0/udp/4001/quic-v1"})
	v.SetDefault("p2p.enable_dht", true)
	v.SetDefault("p2p.enable_relay", true)

	// Redis
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)

	// WireGuard
	v.SetDefault("wireguard.enabled", false)
	v.SetDefault("wireguard.interface", "wg0")
	v.SetDefault("wireguard.listen_port", 51820)
	v.SetDefault("wireguard.dry_run", true)

	// SMTP
	v.SetDefault("smtp.host", "localhost")
	v.SetDefault("smtp.port", 587)
	v.SetDefault("smtp.from", "noreply@vinctum.app")
	v.SetDefault("smtp.base_url", "http://localhost:3000")

	// ML
	v.SetDefault("ml.api_url", "")
	v.SetDefault("ml.api_key", "")

	v.SetDefault("database.driver", "postgres")
	v.SetDefault("database.dsn", "")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 10)
	v.SetDefault("database.conn_max_lifetime", "5m")
}

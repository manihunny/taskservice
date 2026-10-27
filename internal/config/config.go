package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Config struct {
	HTTPAddr       string
	MySQLDSN       string
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	JWTSecret      string
	JWTTTL         time.Duration
	CacheTTL       time.Duration
	RateLimitRPS   float64
	RateLimitBurst int
	LogLevel       string
}

func Load() (*Config, error) {
	var errs []error
	c := &Config{
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		LogLevel:      env("LOG_LEVEL", "info"),
	}
	c.RedisDB = parse(&errs, "REDIS_DB", 0, strconv.Atoi)
	c.JWTTTL = parse(&errs, "JWT_TTL", 24*time.Hour, time.ParseDuration)
	c.CacheTTL = parse(&errs, "CACHE_TTL", 5*time.Minute, time.ParseDuration)
	c.RateLimitRPS = parse(&errs, "RATE_LIMIT_RPS", 20, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) })
	c.RateLimitBurst = parse(&errs, "RATE_LIMIT_BURST", 40, strconv.Atoi)

	c.MySQLDSN = os.Getenv("MYSQL_DSN")
	if c.MySQLDSN == "" {
		// Собираем через mysql.Config, чтобы спецсимволы в пароле не ломали DSN
		mc := mysql.NewConfig()
		mc.User = env("MYSQL_USER", "root")
		mc.Passwd = os.Getenv("MYSQL_PASSWORD")
		mc.Net = "tcp"
		mc.Addr = env("MYSQL_HOST", "localhost") + ":" + env("MYSQL_PORT", "3306")
		mc.DBName = env("MYSQL_DATABASE", "taskservice")
		mc.ParseTime = true
		mc.Loc = time.UTC
		mc.Params = map[string]string{"charset": "utf8mb4"}
		c.MySQLDSN = mc.FormatDSN()
	}

	if c.JWTSecret == "" {
		errs = append(errs, errors.New("JWT_SECRET is required"))
	}
	return c, errors.Join(errs...)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parse[T any](errs *[]error, key string, def T, fn func(string) (T, error)) T {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	res, err := fn(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return res
}

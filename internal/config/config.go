package config

import (
	"errors"
	"net"
	"net/url"
	"os"
)

type Config struct {
	Environment string
	Address     string
	DatabaseURL string
	AuthMode    string
	DevToken    string
	AuthSecret  string
	Issuer      string
	Audience    string
}

func Load() (Config, error) {
	c := Config{Environment: value("APP_ENV", "production"), Address: value("HTTP_ADDR", "127.0.0.1:8080"), DatabaseURL: os.Getenv("DATABASE_URL"), AuthMode: value("AUTH_MODE", "oidc"), DevToken: os.Getenv("DEV_AUTH_TOKEN"), AuthSecret: os.Getenv("AUTH_SECRET"), Issuer: os.Getenv("OIDC_ISSUER"), Audience: os.Getenv("OIDC_AUDIENCE")}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.Environment != "production" && c.Environment != "development" && c.Environment != "test" {
		return errors.New("APP_ENV must be production, development, or test")
	}
	host, _, err := net.SplitHostPort(c.Address)
	if err != nil {
		return errors.New("HTTP_ADDR must be host:port")
	}
	switch c.AuthMode {
	case "development":
		ip := net.ParseIP(host)
		if c.Environment != "development" || ip == nil || !ip.IsLoopback() {
			return errors.New("development authentication requires APP_ENV=development and a loopback HTTP_ADDR")
		}
		if len(c.DevToken) < 32 {
			return errors.New("DEV_AUTH_TOKEN must contain at least 32 characters")
		}
	case "oidc":
		issuer, err := url.Parse(c.Issuer)
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || c.Audience == "" {
			return errors.New("OIDC_ISSUER must be an HTTPS issuer and OIDC_AUDIENCE is required")
		}
	case "password":
		if len(c.AuthSecret) < 32 {
			return errors.New("AUTH_SECRET must contain at least 32 characters for password authentication")
		}
	default:
		return errors.New("AUTH_MODE must be oidc, password, or development")
	}
	return nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

package config

import "testing"

func TestAuthenticationConfiguration(t *testing.T) {
	base := Config{Environment: "development", Address: "127.0.0.1:8080", DatabaseURL: "postgres://localhost/test", AuthMode: "development", DevToken: "12345678901234567890123456789012"}
	tests := []struct {
		name   string
		change func(*Config)
		valid  bool
	}{
		{"loopback development", func(c *Config) {}, true},
		{"IPv6 loopback", func(c *Config) { c.Address = "[::1]:8080" }, true},
		{"public development binding", func(c *Config) { c.Address = "0.0.0.0:8080" }, false},
		{"development token in production", func(c *Config) { c.Environment = "production" }, false},
		{"short token", func(c *Config) { c.DevToken = "guessable" }, false},
		{"missing identity configuration", func(c *Config) { c.AuthMode = "oidc" }, false},
		{"insecure identity issuer", func(c *Config) { c.AuthMode = "oidc"; c.Issuer = "http://identity.invalid"; c.Audience = "community" }, false},
		{"configured production", func(c *Config) {
			c.Environment = "production"
			c.AuthMode = "oidc"
			c.Issuer = "https://identity.invalid"
			c.Audience = "community"
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			tt.change(&c)
			if err := c.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate()=%v, want valid=%v", err, tt.valid)
			}
		})
	}
}

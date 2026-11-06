package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"company-community/backend/internal/config"
	"company-community/backend/internal/content"
	"company-community/backend/internal/database"
	"company-community/backend/internal/domain"
	"company-community/backend/internal/httpapi"
	"company-community/backend/internal/identity"
	"company-community/backend/internal/localauth"
	"company-community/backend/internal/product"
	"company-community/backend/internal/search"
	"company-community/backend/internal/store"
	"company-community/backend/internal/tags"
)

const localPlatformAdminPassword = "Admin@123"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command != "serve" && command != "migrate" && command != "seed-dev" && command != "provision-user" {
		return errors.New("usage: community [serve|migrate|seed-dev|provision-user]")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := database.Open(connectCtx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := store.New(pool)
	switch command {
	case "migrate":
		migrationCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err = database.Migrate(migrationCtx, pool); err != nil {
			return err
		}
		logger.Info("migrations applied")
		return nil
	case "seed-dev":
		c, err := config.Load()
		if err != nil {
			return err
		}
		if c.AuthMode != "development" {
			return errors.New("seed-dev is only available in development authentication mode")
		}
		accounts := []struct{ subject, email, name, role string }{
			{"local-platform-admin", "platform-admin@commonroom.local", "Platform Administrator", "platform_admin"},
			{"local-admin", "admin@mindorigin.in", "Mindorigin Admin", "admin"},
			{"firstock-admin", "admin@firstock.in", "Firstock Admin", "admin"},
		}
		fmt.Println("Local development accounts (email + password):")
		for _, account := range accounts {
			_, lookupErr := repo.FindIdentity(ctx, "development", account.subject)
			if errors.Is(lookupErr, domain.ErrNotFound) {
				_, lookupErr = repo.Provision(ctx, domain.User{Issuer: "development", Subject: account.subject, Email: account.email, DisplayName: account.name, Role: account.role})
			}
			if lookupErr != nil {
				return lookupErr
			}
			password := ""
			passwordEnv := ""
			if account.role == "admin" {
				passwordEnv = "DEV_ADMIN_PASSWORD"
			}
			if account.role == "platform_admin" {
				password = localPlatformAdminPassword
			}
			configuredPassword := password != ""
			if !configuredPassword && passwordEnv != "" && os.Getenv(passwordEnv) != "" {
				password = os.Getenv(passwordEnv)
				configuredPassword = true
			}
			if password == "" {
				if _, credentialErr := repo.LocalCredential(ctx, account.email); credentialErr == nil {
					fmt.Printf("%s (%s): existing password retained\n", account.role, account.email)
					continue
				} else if !errors.Is(credentialErr, domain.ErrNotFound) {
					return credentialErr
				}
				var raw [24]byte
				if _, err = rand.Read(raw[:]); err != nil {
					return err
				}
				password = hex.EncodeToString(raw[:])
			}
			minimumPasswordLength := 12
			if account.role == "platform_admin" {
				minimumPasswordLength = 8
			}
			if len(password) < minimumPasswordLength || len(password) > 128 {
				return fmt.Errorf("password must be between %d and 128 characters", minimumPasswordLength)
			}
			salt, hash, hashErr := localauth.Hash(password)
			if hashErr != nil {
				return hashErr
			}
			if err = repo.SetLocalCredential(ctx, "development", account.email, account.email, salt, hash, localauth.Iterations); err != nil {
				return err
			}
			if configuredPassword {
				if account.role == "platform_admin" {
					fmt.Printf("%s (%s): fixed local development password configured\n", account.role, account.email)
				} else {
					fmt.Printf("%s (%s): password set from %s\n", account.role, account.email, passwordEnv)
				}
			} else {
				fmt.Printf("%s (%s): %s\n", account.role, account.email, password)
			}
		}
		return nil
	case "provision-user":
		fs := flag.NewFlagSet("provision-user", flag.ContinueOnError)
		issuer := fs.String("issuer", "", "exact trusted OIDC issuer")
		subject := fs.String("subject", "", "trusted identity-provider subject")
		email := fs.String("email", "", "verified company email")
		name := fs.String("name", "", "display name")
		role := fs.String("role", "employee", "employee, moderator, or admin")
		passwordEnv := fs.String("password-env", "INITIAL_ACCOUNT_PASSWORD", "environment variable containing the initial password for local-password mode")
		if err = fs.Parse(os.Args[2:]); err != nil {
			return err
		}
		c, configErr := config.Load()
		if configErr != nil {
			return configErr
		}
		address, emailErr := mail.ParseAddress(*email)
		if emailErr != nil || address.Address != *email || len(strings.TrimSpace(*name)) < 1 || len([]rune(*name)) > 100 || (*role != "employee" && *role != "moderator" && *role != "admin" && *role != "platform_admin") {
			return errors.New("valid issuer, subject, email, name, and role are required (employee, moderator, admin, or platform_admin)")
		}
		identityIssuer, identitySubject := *issuer, *subject
		var passwordSalt, passwordHash []byte
		iterations := 0
		if c.AuthMode == "password" {
			password := os.Getenv(*passwordEnv)
			if len(password) < 12 || len(password) > 128 {
				return fmt.Errorf("%s must contain 12 to 128 characters", *passwordEnv)
			}
			if identitySubject == "" {
				var raw [24]byte
				if _, err = rand.Read(raw[:]); err != nil {
					return err
				}
				identitySubject = "local-" + hex.EncodeToString(raw[:])
			}
			identityIssuer = "local-password"
			passwordSalt, passwordHash, err = localauth.Hash(password)
			if err != nil {
				return err
			}
			iterations = localauth.Iterations
		} else if identityIssuer == "" || strings.TrimSpace(identitySubject) == "" {
			return errors.New("-issuer and -subject are required in OIDC mode")
		}
		account := domain.User{Issuer: identityIssuer, Subject: identitySubject, Email: *email, DisplayName: strings.TrimSpace(*name), Role: *role}
		var u domain.User
		if iterations > 0 {
			u, err = repo.ProvisionLocal(ctx, account, passwordSalt, passwordHash, iterations)
		} else {
			u, err = repo.Provision(ctx, account)
		}
		if err != nil {
			return err
		}
		logger.Info("identity provisioned", "user_id", u.ID)
		return nil
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	if err = database.Check(connectCtx, pool); err != nil {
		return err
	}
	verifier, err := identity.NewVerifier(ctx, c)
	if err != nil {
		return fmt.Errorf("identity provider initialization: %w", err)
	}
	api := &httpapi.API{Auth: identity.Authenticator{Verifier: verifier, Users: repo}, Content: content.New(repo), Tags: tags.New(repo), Search: search.New(repo), Admin: repo, OrganizationAdmin: repo, Product: product.New(repo), Ping: func(ctx context.Context) error { return database.Check(ctx, pool) }, Logger: logger, TrustedIssuer: c.Issuer}
	if c.AuthMode == "development" {
		api.DevelopmentAuth = true
		api.LocalLogin = identity.LocalPasswordLogin{Credentials: repo}
		api.DevToken = c.DevToken
		api.LocalTokenIssuer, _ = verifier.(*identity.DevelopmentVerifier)
	} else if c.AuthMode == "password" {
		api.LocalAccountAuth = true
		api.LocalLogin = identity.LocalPasswordLogin{Credentials: repo}
		api.LocalTokenIssuer = verifier.(*identity.PasswordVerifier)
	}
	server := &http.Server{Addr: c.Address, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	logger.Info("server starting", "address", c.Address, "auth_mode", c.AuthMode)
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err = server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	logger.Info("server stopped")
	return nil
}

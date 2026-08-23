package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	store      *store.Store
	ids        platform.IDGenerator
	clock      platform.Clock
	audit      *audit.Writer
	sessionTTL time.Duration
}

func NewService(st *store.Store, ids platform.IDGenerator, clock platform.Clock, writer *audit.Writer, ttl time.Duration) *Service {
	return &Service{store: st, ids: ids, clock: clock, audit: writer, sessionTTL: ttl}
}

func (s *Service) Bootstrap(ctx context.Context, email, password string) error {
	email = NormalizeEmail(email)
	if email == "" || len(password) < 10 {
		return platform.FieldError{Field: "bootstrap", Message: "valid email and password of at least 10 characters required"}
	}
	var count int
	if err := s.store.DB().QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	now := s.clock.Now()
	organizationID := s.ids.New("org")
	userID := s.ids.New("usr")
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO organizations(id,name,country_code,kind,active,created_at,updated_at)
			VALUES($1,'AridLink Secretariat','INT','international',true,$2,$2)`, organizationID, now)
		if err != nil {
			return fmt.Errorf("create bootstrap organization: %w", err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO users(id,organization_id,email,password_hash,role,active,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,true,$6,$6)`, userID, organizationID, email, passwordHash, platform.RolePlatformAdmin, now)
		if err != nil {
			return fmt.Errorf("create bootstrap user: %w", err)
		}
		actor := platform.Actor{UserID: userID, OrganizationID: organizationID, Role: platform.RolePlatformAdmin}
		return s.audit.Record(ctx, tx, actor, "platform.bootstrap", "organization", organizationID, map[string]any{"email": email})
	})
}

func (s *Service) CreateOrganization(ctx context.Context, name, countryCode, kind string) (Organization, error) {
	actor, err := platform.RequireRole(ctx, platform.RolePlatformAdmin)
	if err != nil {
		return Organization{}, err
	}
	name, countryCode, kind = strings.TrimSpace(name), strings.ToUpper(strings.TrimSpace(countryCode)), strings.TrimSpace(kind)
	if name == "" || len(countryCode) < 2 || len(countryCode) > 3 {
		return Organization{}, platform.FieldError{Field: "organization", Message: "name and ISO country code are required"}
	}
	allowed := map[string]bool{"government": true, "research": true, "enterprise": true, "ngo": true, "community": true, "international": true}
	if !allowed[kind] {
		return Organization{}, platform.FieldError{Field: "kind", Message: "unsupported organization kind"}
	}
	now := s.clock.Now()
	organization := Organization{ID: s.ids.New("org"), Name: name, CountryCode: countryCode, Kind: kind, Active: true, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO organizations(id,name,country_code,kind,active,created_at,updated_at)
			VALUES($1,$2,$3,$4,true,$5,$5)`, organization.ID, organization.Name, organization.CountryCode, organization.Kind, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "organization.created", "organization", organization.ID, map[string]any{"kind": kind})
	})
	return organization, err
}

func (s *Service) CreateUser(ctx context.Context, organizationID, email, password string, role platform.Role) (User, error) {
	actor, err := platform.RequireRole(ctx, platform.RolePlatformAdmin, platform.RoleProgramManager)
	if err != nil {
		return User{}, err
	}
	if err := platform.RequireOrganization(actor, organizationID); err != nil {
		return User{}, err
	}
	email = NormalizeEmail(email)
	if email == "" || len(password) < 10 || !ValidRole(role) || role == platform.RolePlatformAdmin && actor.Role != platform.RolePlatformAdmin {
		return User{}, platform.FieldError{Field: "user", Message: "valid email, password and permitted role are required"}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	now := s.clock.Now()
	user := User{ID: s.ids.New("usr"), OrganizationID: organizationID, Email: email, Role: role, Active: true, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO users(id,organization_id,email,password_hash,role,active,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,true,$6,$6)`, user.ID, organizationID, email, hash, role, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "user.created", "user", user.ID, map[string]any{"role": role, "organization_id": organizationID})
	})
	return user, err
}

func (s *Service) Login(ctx context.Context, credentials Credentials) (LoginResult, error) {
	credentials.Email = NormalizeEmail(credentials.Email)
	var user User
	var passwordHash string
	err := s.store.DB().QueryRow(ctx, `SELECT id,organization_id,email,password_hash,role,active,created_at,updated_at
		FROM users WHERE email=$1`, credentials.Email).Scan(&user.ID, &user.OrganizationID, &user.Email, &passwordHash,
		&user.Role, &user.Active, &user.CreatedAt, &user.UpdatedAt)
	if err != nil || !user.Active || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(credentials.Password)) != nil {
		return LoginResult{}, platform.ErrUnauthorized
	}
	token, err := newToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.clock.Now()
	expiresAt := now.Add(s.sessionTTL)
	_, err = s.store.DB().Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at)
		VALUES($1,$2,$3,$4,$5)`, s.ids.New("ses"), user.ID, hashToken(token), expiresAt, now)
	if err != nil {
		return LoginResult{}, fmt.Errorf("persist session: %w", store.Translate(err))
	}
	return LoginResult{Token: token, ExpiresAt: expiresAt, User: user}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (platform.Actor, error) {
	if token == "" {
		return platform.Actor{}, platform.ErrUnauthorized
	}
	now := s.clock.Now()
	var actor platform.Actor
	var active bool
	err := s.store.DB().QueryRow(ctx, `SELECT u.id,u.organization_id,u.role,u.active
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>$2`, hashToken(token), now).
		Scan(&actor.UserID, &actor.OrganizationID, &actor.Role, &active)
	if errors.Is(err, pgx.ErrNoRows) || !active {
		return platform.Actor{}, platform.ErrUnauthorized
	}
	if err != nil {
		return platform.Actor{}, fmt.Errorf("authenticate session: %w", err)
	}
	return actor, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	requestContext := context.WithoutCancel(ctx)
	actor, err := s.Authenticate(requestContext, token)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(requestContext, func(tx pgx.Tx) error {
		tag, err := tx.Exec(requestContext, `UPDATE sessions SET revoked_at=$2 WHERE token_hash=$1 AND revoked_at IS NULL`, hashToken(token), now)
		if err != nil {
			return fmt.Errorf("revoke session: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrUnauthorized
		}
		return s.audit.Record(requestContext, tx, actor, "session.revoked", "user", actor.UserID, map[string]any{"revoked_at": now})
	})
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

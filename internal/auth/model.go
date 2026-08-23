package auth

import (
	"strings"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

type Organization struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CountryCode string    `json:"country_code"`
	Kind        string    `json:"kind"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type User struct {
	ID             string        `json:"id"`
	OrganizationID string        `json:"organization_id"`
	Email          string        `json:"email"`
	Role           platform.Role `json:"role"`
	Active         bool          `json:"active"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type Session struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

func NormalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ValidRole(role platform.Role) bool {
	switch role {
	case platform.RolePlatformAdmin, platform.RoleProgramManager, platform.RoleFieldOfficer,
		platform.RoleTechnicalReviewer, platform.RoleFinanceReviewer:
		return true
	default:
		return false
	}
}

package platform

import "context"

type Role string

const (
	RolePlatformAdmin     Role = "platform_admin"
	RoleProgramManager    Role = "program_manager"
	RoleFieldOfficer      Role = "field_officer"
	RoleTechnicalReviewer Role = "technical_reviewer"
	RoleFinanceReviewer   Role = "finance_reviewer"
)

type Actor struct {
	UserID         string
	OrganizationID string
	Role           Role
}

type actorKey struct{}

func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

func ActorFrom(ctx context.Context) (Actor, error) {
	actor, ok := ctx.Value(actorKey{}).(Actor)
	if !ok || actor.UserID == "" {
		return Actor{}, ErrUnauthorized
	}
	return actor, nil
}

func RequireRole(ctx context.Context, roles ...Role) (Actor, error) {
	actor, err := ActorFrom(ctx)
	if err != nil {
		return Actor{}, err
	}
	for _, role := range roles {
		if actor.Role == role {
			return actor, nil
		}
	}
	return Actor{}, ErrForbidden
}

func RequireOrganization(actor Actor, organizationID string) error {
	if actor.Role == RolePlatformAdmin || actor.OrganizationID == organizationID {
		return nil
	}
	return ErrForbidden
}

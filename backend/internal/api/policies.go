package api

import "github.com/mairuu/loghub/backend/internal/authz"

// policies is who may call what, one set per feature (ADR 0009). Handlers
// ask the enforcer built from these; none decides for itself.
var policies = []authz.Set{
	authz.NewSet("health",
		authz.Grant(authz.Anonymous).As(authz.AnyTenant).On(authz.Health).Can(authz.Read),
		authz.Grant(authz.AnyRole).As(authz.AnyTenant).On(authz.Health).Can(authz.Read),
	),
	// Signing in while signed in is harmless: the password is checked either
	// way.
	authz.NewSet("sessions",
		authz.Grant(authz.Anonymous).As(authz.AnyTenant).On(authz.Sessions).Can(authz.Create),
		authz.Grant(authz.AnyRole).As(authz.AnyTenant).On(authz.Sessions).Can(authz.Create),
	),
	authz.NewSet("events",
		authz.Grant(authz.Admin).As(authz.AnyTenant).On(authz.Events).Can(authz.Read, authz.Create),
		authz.Grant(authz.Viewer).As(authz.OwnTenant).On(authz.Events).Can(authz.Read),
		authz.Grant(authz.Collector).As(authz.AnyTenant).On(authz.Events).Can(authz.Create),
	),
	// Only the evaluator writes alerts, and it isn't a caller (ADR 0010).
	authz.NewSet("alerts",
		authz.Grant(authz.Admin).As(authz.AnyTenant).On(authz.AlertRules).Can(authz.Read, authz.Create),
		authz.Grant(authz.Viewer).As(authz.OwnTenant).On(authz.AlertRules).Can(authz.Read),
		authz.Grant(authz.Admin).As(authz.AnyTenant).On(authz.Alerts).Can(authz.Read),
		authz.Grant(authz.Viewer).As(authz.OwnTenant).On(authz.Alerts).Can(authz.Read),
	),
}

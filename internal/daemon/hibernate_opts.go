package daemon

import "github.com/arcavenae/marvel/internal/team"

func teamHibernateOptions(roles []string) team.HibernateOptions {
	return team.HibernateOptions{Actor: suspendActor(), Source: "cli", Roles: roles}
}

func teamWakeOptions(skipped []string) team.WakeOptions {
	return team.WakeOptions{Actor: suspendActor(), Source: "cli", SkippedChecks: skipped}
}

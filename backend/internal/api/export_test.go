package api

import "github.com/mairuu/loghub/backend/internal/authz"

const (
	MaxRecordBytes = maxRecordBytes
	MaxNDJSONBytes = maxNDJSONBytes
	MaxReported    = maxReported
)

// SetPolicies replaces the API's policy sets for servers built until the
// returned func is called.
func SetPolicies(sets ...authz.Set) (restore func()) {
	old := policies
	policies = sets
	return func() { policies = old }
}

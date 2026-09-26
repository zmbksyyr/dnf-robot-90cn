package shared

import "context"

// FollowAccountLocator resolves the last-played village of a follow account.
// The selected adapter owns the storage query; schedulers without a locator
// treat the follow-account spawn target as unavailable.
type FollowAccountLocator interface {
	FollowAccountVillageLastPlayed(ctx context.Context, account string) (village int, ok bool, err error)
}

// AccountOnlineChecker reports whether a game account still has an active
// session. Adapters that cannot answer must return an error instead of
// guessing.
type AccountOnlineChecker interface {
	AccountOnline(uid int) (bool, error)
}

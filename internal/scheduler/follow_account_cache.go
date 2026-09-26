package scheduler

import "time"

const (
	followAccountLookupTTL    = 2 * time.Second
	followAccountMaxStaleness = 30 * time.Second
)

type followAccountLookup struct {
	account     string
	village     int
	villageOK   bool
	refreshedAt time.Time
	nextRefresh time.Time
}

func (m *RobotManager) loadFollowAccount(account string) (followAccountLookup, bool) {
	for {
		now := time.Now()
		m.followLookupMu.Lock()
		cached := cloneFollowAccountLookup(m.followLookup)
		if cached.account == account && !cached.nextRefresh.IsZero() && now.Before(cached.nextRefresh) {
			ok := followAccountLookupUsable(cached, now)
			m.followLookupMu.Unlock()
			return cached, ok
		}
		if m.followLookupInFlight != "" {
			ok := cached.account == account && followAccountLookupUsable(cached, now)
			m.followLookupMu.Unlock()
			return cached, ok
		}
		m.followLookupInFlight = account
		m.followLookupMu.Unlock()

		m.refreshFollowAccount(account)
	}
}

func (m *RobotManager) refreshFollowAccount(account string) {
	village, villageOK, villageErr := m.lookupFollowAccountVillage(account)
	now := time.Now()

	m.followLookupMu.Lock()
	defer m.followLookupMu.Unlock()
	if m.followLookupInFlight == account {
		m.followLookupInFlight = ""
	}
	if villageErr != nil {
		if m.followLookup.account != account {
			m.followLookup = followAccountLookup{account: account}
		}
		m.followLookup.nextRefresh = now.Add(followAccountLookupTTL)
		return
	}
	m.followLookup = followAccountLookup{
		account:     account,
		village:     village,
		villageOK:   villageErr == nil && villageOK,
		refreshedAt: now,
		nextRefresh: now.Add(followAccountLookupTTL),
	}
}

func followAccountLookupUsable(cached followAccountLookup, now time.Time) bool {
	return !cached.refreshedAt.IsZero() && now.Sub(cached.refreshedAt) <= followAccountMaxStaleness
}

func cloneFollowAccountLookup(value followAccountLookup) followAccountLookup {
	return value
}

package cn90

// recordingSessionCloser records the session close order for cleaner and
// purger tests without opening real game connections.
type recordingSessionCloser struct{ uids []int }

func (c *recordingSessionCloser) Close(uid int) error {
	c.uids = append(c.uids, uid)
	return nil
}

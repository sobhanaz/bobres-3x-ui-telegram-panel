package xui

// LockCountForTest exposes the number of tracked per-email locks to external tests.
func LockCountForTest(c *Client) int { return c.lockCount() }

// SetMaxListPagesForTest changes how many pages ListClients reads before it
// gives up, and returns the old limit. Tests that use it do not run in parallel.
func SetMaxListPagesForTest(n int) int {
	old := maxListPages
	maxListPages = n
	return old
}

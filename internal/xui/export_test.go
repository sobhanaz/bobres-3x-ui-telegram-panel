package xui

// LockCountForTest exposes the number of tracked per-email locks to external tests.
func LockCountForTest(c *Client) int { return c.lockCount() }

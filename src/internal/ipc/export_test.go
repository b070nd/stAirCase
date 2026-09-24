package ipc

import "time"

// ReadBufferSizeForTest is the line limit the Python client must mirror.
const ReadBufferSizeForTest = readBufferSize

// SetAuthTimeoutForTest overrides the auth handshake deadline.
// Call in a test with t.Cleanup to restore the original value.
func SetAuthTimeoutForTest(d time.Duration) (restore func()) {
	orig := authTimeout
	authTimeout = d
	return func() { authTimeout = orig }
}

// SetHeartbeatTimeoutForTest overrides the between-message deadline.
// Call in a test with t.Cleanup to restore the original value.
func SetHeartbeatTimeoutForTest(d time.Duration) (restore func()) {
	orig := heartbeatTimeout
	heartbeatTimeout = d
	return func() { heartbeatTimeout = orig }
}

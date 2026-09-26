package shared

// BackendStorePolicy exposes version-specific private-store semantics to the
// scheduler without leaking protocol codes into shared orchestration. The
// adapter owns the wire error codes and shop economy constants.
type BackendStorePolicy interface {
	// DisjointStoreCost is the gold cost of one disjoint-store attempt.
	DisjointStoreCost() uint32
	// DisjointFailure maps a wire failure code to a stable reason string and
	// reports whether a different coordinate may retry on the same session.
	DisjointFailure(errCode byte) (reason string, retrySameSession bool)
	// DisjointReasonRetryable reports retryability for adapter-owned failure
	// reasons. known=false lets the scheduler keep its own reason semantics.
	DisjointReasonRetryable(reason string) (retry bool, known bool)
}

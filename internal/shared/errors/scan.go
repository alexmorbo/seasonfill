package errors

import "fmt"

// ScanFailedError signals an unexpected failure during a scan cycle.
// Maps to HTTP 500; callers may retry on the next tick.
type ScanFailedError struct {
	Cause error
}

func (e *ScanFailedError) Error() string {
	if e.Cause == nil {
		return "scan failed"
	}
	return fmt.Sprintf("scan failed: %v", e.Cause)
}

func (e *ScanFailedError) Code() string { return "scan_failed" }

func (e *ScanFailedError) Retriable() bool { return true }

func (e *ScanFailedError) Unwrap() error { return e.Cause }

// ScanInProgressError signals a duplicate scan request while a scan is
// already running. Maps to HTTP 409 Conflict.
type ScanInProgressError struct{}

func (e *ScanInProgressError) Error() string { return "scan already in progress" }

func (e *ScanInProgressError) Code() string { return "scan_in_progress" }

func (e *ScanInProgressError) Retriable() bool { return false }

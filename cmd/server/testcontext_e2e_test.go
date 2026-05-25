//go:build integration

package main

// TestContext and testContextHook are declared in testcontext_hook.go
// (also integration-tagged, non-test file) so that runWithContext in
// main.go can reference them without requiring _test.go compilation.
// This file exists as the story's documented home for the test-context
// pattern; the actual declarations live one file over to satisfy the
// Go compiler's rule that production files may not import test-only symbols.

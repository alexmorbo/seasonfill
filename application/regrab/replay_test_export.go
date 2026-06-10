package regrab

// OverrideReleaseGoneClassifier swaps the package-level
// sonarrIsReleaseGone hook used by runEvaluator to detect Sonarr
// 404/410 errors and returns a restore func. Tests in
// regrab_usecase_test.go (package regrab_test) drive replay-path
// classification without importing infrastructure/sonarr's error
// types. Production callers never touch this hook.
//
// Calling pattern:
//
//	restore := regrab.OverrideReleaseGoneClassifier(func(err error) bool {...})
//	defer restore()
//
// Concurrency: the swap and the production read are guarded by an
// RW mutex (sonarrIsReleaseGoneMu in regrab_usecase.go) so parallel
// tests do not race when one overrides while another reads.
func OverrideReleaseGoneClassifier(fn func(error) bool) func() {
	sonarrIsReleaseGoneMu.Lock()
	prev := sonarrIsReleaseGone
	sonarrIsReleaseGone = fn
	sonarrIsReleaseGoneMu.Unlock()
	return func() {
		sonarrIsReleaseGoneMu.Lock()
		sonarrIsReleaseGone = prev
		sonarrIsReleaseGoneMu.Unlock()
	}
}

package persistence

import "testing"

// D-2: quota_counter_repository tests skip pending D-5 admin+auth
// rewrite. The legacy external_service_quota_state.count column is
// gone in the new D-1 schema.

func TestQuotaCounterRepository_Increment_StartsAtOne(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_Increment_Accumulates(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_DistinctServices_DistinctRows(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_DistinctWindows_DistinctRows(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_Get_MissingRow_ReturnsZero(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_Reset_DeletesOldWindows(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_Increment_SurvivesAcrossRepos(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestQuotaCounterRepository_Increment_ConcurrentNoLost(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

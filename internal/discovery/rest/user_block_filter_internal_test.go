package rest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

func ubfItem(series, tmdbID int) disco.Item {
	id := shareddomain.TMDBID(tmdbID)
	return disco.Item{SeriesID: shareddomain.SeriesID(series), TMDBID: &id, Title: "t"}
}

func ubfStub(series int) disco.Item {
	return disco.Item{SeriesID: shareddomain.SeriesID(series), Title: "stub"}
}

func ubfSet(ids ...int64) map[int64]struct{} {
	if len(ids) == 0 {
		return nil
	}
	s := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

func ubfLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ubfUsers is a minimal dataports.UserRepository: only GetByUsername +
// FirstAdminID are exercised; the rest satisfy the interface via the embedded
// nil (panic if unexpectedly called).
type ubfUsers struct {
	dataports.UserRepository
	byName  map[string]admin.User
	adminID int64
	getErr  bool
}

func (u ubfUsers) GetByUsername(_ context.Context, name string) (admin.User, error) {
	if u.getErr {
		return admin.User{}, errors.New("boom")
	}
	if v, ok := u.byName[name]; ok {
		return v, nil
	}
	return admin.User{}, errors.New("not found")
}

func (u ubfUsers) FirstAdminID(context.Context) (int64, error) { return u.adminID, nil }

// ubfLoader returns per-uid block sets.
type ubfLoader struct {
	tmdb map[int64][]int64
	kw   map[int64][]int64
	err  bool
}

func (l ubfLoader) LoadBlockSets(_ context.Context, uid int64) ([]int64, []int64, error) {
	if l.err {
		return nil, nil, errors.New("load failed")
	}
	return l.tmdb[uid], l.kw[uid], nil
}

// ubfKeywords counts calls and returns a fixed map.
type ubfKeywords struct {
	calls  int
	byTMDB map[int64][]int64
	err    bool
}

func (k *ubfKeywords) ResultKeywords(_ context.Context, _ []int64) (map[int64][]int64, error) {
	k.calls++
	if k.err {
		return nil, errors.New("kw lookup failed")
	}
	return k.byTMDB, nil
}

func ubfCtx(username string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	if username != "" {
		c.Set(middleware.UsernameContextKey, username)
	}
	return c
}

func TestFilterBlockedTMDB_NilSetSameBackingArray(t *testing.T) {
	items := []disco.Item{ubfItem(1, 10), ubfItem(2, 20)}
	out := filterBlockedTMDB(items, nil)
	require.Len(t, out, 2)
	require.Same(t, &items[0], &out[0], "nil blocked set must return the same backing array")
}

func TestFilterBlockedTMDB_DropsBlocked_KeepsStub(t *testing.T) {
	items := []disco.Item{ubfItem(1, 10), ubfItem(2, 20), ubfStub(3)}
	out := filterBlockedTMDB(items, ubfSet(20))
	require.Len(t, out, 2, "blocked tmdb id removed; nil-TMDBID stub kept")
	for _, it := range out {
		if it.TMDBID != nil {
			require.NotEqual(t, 20, int(*it.TMDBID))
		}
	}
}

func TestFilterBlockedKeywords_EmptyBlockedPassthrough(t *testing.T) {
	items := []disco.Item{ubfItem(1, 10)}
	out := filterBlockedKeywords(items, map[int64][]int64{10: {500}}, nil)
	require.Len(t, out, 1)
}

func TestFilterBlockedKeywords_DropIntersect_KeepUnEnriched_KeepStub(t *testing.T) {
	items := []disco.Item{
		ubfItem(1, 10), // has keyword 500 (blocked) → drop
		ubfItem(2, 20), // no keyword row (un-enriched) → keep
		ubfStub(3),     // nil TMDBID → keep
	}
	kwByTMDB := map[int64][]int64{10: {500, 501}}
	out := filterBlockedKeywords(items, kwByTMDB, ubfSet(500))
	require.Len(t, out, 2)
	for _, it := range out {
		if it.TMDBID != nil {
			require.NotEqual(t, 10, int(*it.TMDBID), "item carrying blocked keyword must be dropped")
		}
	}
}

func TestToSet_EmptyIsNil(t *testing.T) {
	require.Nil(t, toSet(nil))
	require.Nil(t, toSet([]int64{}))
	require.Len(t, toSet([]int64{1, 2}), 2)
}

func TestTMDBIDsOf_SkipsNil(t *testing.T) {
	ids := tmdbIDsOf([]disco.Item{ubfItem(1, 10), ubfStub(2), ubfItem(3, 30)})
	require.ElementsMatch(t, []int64{10, 30}, ids)
}

func TestCurrentUserBlocks_NilReceiver(t *testing.T) {
	var f *userBlockFilter
	tmdb, kw := f.currentUserBlocks(ubfCtx("alice"))
	require.Nil(t, tmdb)
	require.Nil(t, kw)
}

func TestCurrentUserBlocks_NoUserFiltersNothing(t *testing.T) {
	f := newUserBlockFilter(
		ubfUsers{byName: map[string]admin.User{}},
		ubfLoader{tmdb: map[int64][]int64{7: {42}}},
		&ubfKeywords{},
		ubfLog(),
	)
	tmdb, kw := f.currentUserBlocks(ubfCtx("")) // no username on context
	require.Nil(t, tmdb)
	require.Nil(t, kw)
}

func TestCurrentUserBlocks_ResolvesUserSets(t *testing.T) {
	f := newUserBlockFilter(
		ubfUsers{byName: map[string]admin.User{"alice": {ID: 7}}},
		ubfLoader{tmdb: map[int64][]int64{7: {42}}, kw: map[int64][]int64{7: {900}}},
		&ubfKeywords{},
		ubfLog(),
	)
	tmdb, kw := f.currentUserBlocks(ubfCtx("alice"))
	require.Contains(t, tmdb, int64(42))
	require.Contains(t, kw, int64(900))
}

func TestCurrentUserBlocks_APIKeyUsesFirstAdmin(t *testing.T) {
	f := newUserBlockFilter(
		ubfUsers{adminID: 3},
		ubfLoader{tmdb: map[int64][]int64{3: {55}}},
		&ubfKeywords{},
		ubfLog(),
	)
	tmdb, _ := f.currentUserBlocks(ubfCtx("api-key"))
	require.Contains(t, tmdb, int64(55))
}

func TestCurrentUserBlocks_LoaderErrorFiltersNothing(t *testing.T) {
	f := newUserBlockFilter(
		ubfUsers{byName: map[string]admin.User{"alice": {ID: 7}}},
		ubfLoader{err: true},
		&ubfKeywords{},
		ubfLog(),
	)
	tmdb, kw := f.currentUserBlocks(ubfCtx("alice"))
	require.Nil(t, tmdb)
	require.Nil(t, kw)
}

func TestApplyUserBlocks_BatchedSingleKeywordQuery(t *testing.T) {
	kw := &ubfKeywords{byTMDB: map[int64][]int64{10: {500}}}
	f := newUserBlockFilter(ubfUsers{}, ubfLoader{}, kw, ubfLog())
	items := []disco.Item{ubfItem(1, 10), ubfItem(2, 20), ubfItem(3, 30)}
	out := f.applyUserBlocks(context.Background(), items, nil, ubfSet(500))
	require.Len(t, out, 2, "item carrying blocked keyword 500 dropped")
	require.Equal(t, 1, kw.calls, "keyword lookup must be batched: exactly ONE query per page")
}

func TestApplyUserBlocks_NilKeywordSetSkipsLookup(t *testing.T) {
	kw := &ubfKeywords{byTMDB: map[int64][]int64{10: {500}}}
	f := newUserBlockFilter(ubfUsers{}, ubfLoader{}, kw, ubfLog())
	items := []disco.Item{ubfItem(1, 10)}
	out := f.applyUserBlocks(context.Background(), items, nil, nil)
	require.Len(t, out, 1)
	require.Equal(t, 0, kw.calls, "no keyword set → no keyword query")
}

func TestApplyUserBlocks_KeywordLookupErrorFailsOpen(t *testing.T) {
	kw := &ubfKeywords{err: true}
	f := newUserBlockFilter(ubfUsers{}, ubfLoader{}, kw, ubfLog())
	items := []disco.Item{ubfItem(1, 10)}
	out := f.applyUserBlocks(context.Background(), items, nil, ubfSet(500))
	require.Len(t, out, 1, "keyword lookup error must fail open (keep items)")
}

func TestApplyUserBlocks_NilReceiverNoPanic(t *testing.T) {
	var f *userBlockFilter
	items := []disco.Item{ubfItem(1, 10)}
	// Realistic no-user path: nil receiver with nil sets → items unchanged, no
	// panic (the keyword branch short-circuits on the nil receiver).
	out := f.applyUserBlocks(context.Background(), items, nil, nil)
	require.Len(t, out, 1)
}

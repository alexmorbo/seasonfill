package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type fakeRepo struct {
	insertR   reqdomain.Request
	insertID  int64
	existed   bool
	insertErr error
	insertCnt int

	getR   reqdomain.Request
	getErr error

	byUser []reqdomain.Request
	all    []reqdomain.Request

	statusID       int64
	statusVal      string
	statusApprover uint
	statusErr      error
	statusCnt      int
}

func (f *fakeRepo) InsertPending(_ context.Context, r reqdomain.Request) (int64, bool, error) {
	f.insertCnt++
	f.insertR = r
	return f.insertID, f.existed, f.insertErr
}
func (f *fakeRepo) Get(_ context.Context, _ int64) (reqdomain.Request, error) {
	return f.getR, f.getErr
}
func (f *fakeRepo) ListByUser(_ context.Context, _ uint) ([]reqdomain.Request, error) {
	return f.byUser, nil
}
func (f *fakeRepo) ListAll(_ context.Context) ([]reqdomain.Request, error) { return f.all, nil }
func (f *fakeRepo) SetStatus(_ context.Context, id int64, status string, approverID uint) error {
	f.statusCnt++
	f.statusID = id
	f.statusVal = status
	f.statusApprover = approverID
	return f.statusErr
}

type fakeSeriesAdder struct {
	spec reqdomain.AddSpec
	cnt  int
	err  error
}

func (f *fakeSeriesAdder) AddTV(_ context.Context, spec reqdomain.AddSpec) error {
	f.cnt++
	f.spec = spec
	return f.err
}

type fakeMovieAdder struct {
	spec reqdomain.AddSpec
	cnt  int
	err  error
}

func (f *fakeMovieAdder) AddMovie(_ context.Context, spec reqdomain.AddSpec) error {
	f.cnt++
	f.spec = spec
	return f.err
}

type fakeOutbox struct {
	rows []ports.OutboxRow
}

func (f *fakeOutbox) Insert(_ context.Context, row ports.OutboxRow) error {
	f.rows = append(f.rows, row)
	return nil
}

// passthroughTx runs the work fn directly (no real tx).
type passthroughTx struct{ cnt int }

func (t *passthroughTx) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.cnt++
	return fn(ctx)
}

func TestQueue_DelegatesAndReturnsID(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{insertID: 5, existed: false}
	uc := NewUseCase(repo, nil, nil, nil, nil, nil)
	id, err := uc.Queue(context.Background(), 3, reqdomain.AddSpec{
		MediaType: reqdomain.MediaTypeTV, ExternalID: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), id)
	assert.Equal(t, 1, repo.insertCnt)
	assert.Equal(t, uint(3), repo.insertR.UserID)
	assert.Equal(t, int64(100), repo.insertR.TMDBID)
	assert.Equal(t, reqdomain.StatusPending, repo.insertR.Status)
}

func TestList_Scoping(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{
		byUser: []reqdomain.Request{{ID: 1}},
		all:    []reqdomain.Request{{ID: 1}, {ID: 2}},
	}
	uc := NewUseCase(repo, nil, nil, nil, nil, nil)

	adminList, err := uc.List(context.Background(), admin.User{Role: admin.RoleAdmin})
	require.NoError(t, err)
	assert.Len(t, adminList, 2)

	mgrList, err := uc.List(context.Background(), admin.User{Role: admin.RoleUser, ManageRequests: true})
	require.NoError(t, err)
	assert.Len(t, mgrList, 2)

	ownList, err := uc.List(context.Background(), admin.User{ID: 9, Role: admin.RoleUser})
	require.NoError(t, err)
	assert.Len(t, ownList, 1)
}

func TestApprove_TV_ReplaysAddSetsStatusEmits(t *testing.T) {
	t.Parallel()
	seasons := []int{1, 3}
	spec := reqdomain.AddSpec{
		MediaType: reqdomain.MediaTypeTV, ExternalID: 81189, InstanceName: "main",
		QualityProfileID: 6, RootFolderPath: "/tv", Seasons: &seasons,
	}
	repo := &fakeRepo{getR: reqdomain.Request{ID: 7, MediaType: reqdomain.MediaTypeTV, Status: reqdomain.StatusPending, Spec: spec}}
	adder := &fakeSeriesAdder{}
	outbox := &fakeOutbox{}
	tx := &passthroughTx{}
	uc := NewUseCase(repo, adder, nil, outbox, tx, nil)

	r, err := uc.Approve(context.Background(), 7, admin.User{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, reqdomain.StatusApproved, r.Status)
	require.Equal(t, 1, adder.cnt)
	assert.Equal(t, spec, adder.spec)
	require.NotNil(t, adder.spec.Seasons)
	assert.Equal(t, []int{1, 3}, *adder.spec.Seasons)
	assert.Equal(t, 1, repo.statusCnt)
	assert.Equal(t, reqdomain.StatusApproved, repo.statusVal)
	assert.Equal(t, uint(1), repo.statusApprover)
	require.Len(t, outbox.rows, 1)
	assert.Equal(t, "request.approved", outbox.rows[0].EventType)
	assert.Equal(t, 1, tx.cnt)
}

func TestApprove_Movie_ReplaysMovieAdd(t *testing.T) {
	t.Parallel()
	spec := reqdomain.AddSpec{MediaType: reqdomain.MediaTypeMovie, ExternalID: 438631, InstanceName: "movies"}
	repo := &fakeRepo{getR: reqdomain.Request{ID: 8, MediaType: reqdomain.MediaTypeMovie, Status: reqdomain.StatusPending, Spec: spec}}
	adder := &fakeMovieAdder{}
	outbox := &fakeOutbox{}
	uc := NewUseCase(repo, nil, adder, outbox, nil, nil)

	_, err := uc.Approve(context.Background(), 8, admin.User{ID: 2})
	require.NoError(t, err)
	require.Equal(t, 1, adder.cnt)
	assert.Equal(t, spec, adder.spec)
	require.Len(t, outbox.rows, 1)
	assert.Equal(t, "request.approved", outbox.rows[0].EventType)
}

func TestApprove_Idempotent_AlreadyApproved(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{getR: reqdomain.Request{ID: 7, MediaType: reqdomain.MediaTypeTV, Status: reqdomain.StatusApproved}}
	adder := &fakeSeriesAdder{}
	outbox := &fakeOutbox{}
	uc := NewUseCase(repo, adder, nil, outbox, nil, nil)

	r, err := uc.Approve(context.Background(), 7, admin.User{ID: 1})
	require.NoError(t, err)
	assert.Equal(t, reqdomain.StatusApproved, r.Status)
	assert.Equal(t, 0, adder.cnt, "no re-add on already-approved")
	assert.Equal(t, 0, repo.statusCnt, "no status write")
	assert.Empty(t, outbox.rows)
}

func TestApprove_AddFailure_NoStatusWrite(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{getR: reqdomain.Request{ID: 7, MediaType: reqdomain.MediaTypeTV, Status: reqdomain.StatusPending}}
	adder := &fakeSeriesAdder{err: errors.New("sonarr down")}
	outbox := &fakeOutbox{}
	uc := NewUseCase(repo, adder, nil, outbox, nil, nil)

	_, err := uc.Approve(context.Background(), 7, admin.User{ID: 1})
	require.Error(t, err)
	assert.Equal(t, 0, repo.statusCnt, "status NOT written on add failure")
	assert.Empty(t, outbox.rows)
}

func TestApprove_NotFound(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{getErr: ports.ErrNotFound}
	uc := NewUseCase(repo, &fakeSeriesAdder{}, nil, nil, nil, nil)
	_, err := uc.Approve(context.Background(), 99, admin.User{ID: 1})
	require.ErrorIs(t, err, ErrRequestNotFound)
}

func TestDeny_SetsStatusEmitsNoAdd(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{getR: reqdomain.Request{ID: 7, MediaType: reqdomain.MediaTypeTV, Status: reqdomain.StatusPending}}
	adder := &fakeSeriesAdder{}
	outbox := &fakeOutbox{}
	uc := NewUseCase(repo, adder, nil, outbox, nil, nil)

	r, err := uc.Deny(context.Background(), 7, admin.User{ID: 4})
	require.NoError(t, err)
	assert.Equal(t, reqdomain.StatusDenied, r.Status)
	assert.Equal(t, 0, adder.cnt, "deny never adds")
	assert.Equal(t, 1, repo.statusCnt)
	assert.Equal(t, reqdomain.StatusDenied, repo.statusVal)
	require.Len(t, outbox.rows, 1)
	assert.Equal(t, "request.denied", outbox.rows[0].EventType)
}

func TestDeny_Idempotent_AlreadyDenied(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{getR: reqdomain.Request{ID: 7, MediaType: reqdomain.MediaTypeTV, Status: reqdomain.StatusDenied}}
	outbox := &fakeOutbox{}
	uc := NewUseCase(repo, nil, nil, outbox, nil, nil)

	_, err := uc.Deny(context.Background(), 7, admin.User{ID: 4})
	require.NoError(t, err)
	assert.Equal(t, 0, repo.statusCnt)
	assert.Empty(t, outbox.rows)
}

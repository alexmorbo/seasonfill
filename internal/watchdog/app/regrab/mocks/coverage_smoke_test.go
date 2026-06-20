// Package mocks smoke test — exercises every generated mock method so the
// codegen counts toward Go coverage. Generated mocks have no logic worth
// asserting on, but Go's coverage profile only credits packages that have
// at least one test binary; without this file, `regrab/mocks` reports 0%
// even though the methods are called heavily from regrab_usecase_test.go.
package mocks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/alexmorbo/seasonfill/application/ports"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestMocks_GrabRepository_AllMethods(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := NewMockGrabRepository(ctrl)
	ctx := context.Background()
	id := uuid.New()

	m.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	_ = m.Create(ctx, grab.Record{})

	m.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, nil)
	_, _, _ = m.List(ctx, ports.GrabFilter{}, ports.Pagination{})

	m.EXPECT().MatchLatest(gomock.Any(), gomock.Any()).Return(grab.Record{}, nil)
	_, _ = m.MatchLatest(ctx, ports.MatchKey{})

	m.EXPECT().UpdateStatus(gomock.Any(), id, gomock.Any(), gomock.Any()).Return(nil)
	_ = m.UpdateStatus(ctx, id, grab.StatusImported, "")

	m.EXPECT().UpdateTorrentHash(gomock.Any(), id, gomock.Any()).Return(nil)
	_ = m.UpdateTorrentHash(ctx, id, "deadbeef")

	m.EXPECT().FindLatestSuccessByHash(gomock.Any(), gomock.Any()).Return(grab.Record{}, nil)
	_, _ = m.FindLatestSuccessByHash(ctx, "deadbeef")

	m.EXPECT().CreateReplay(gomock.Any(), gomock.Any(), id).Return(nil)
	_ = m.CreateReplay(ctx, grab.Record{}, id)

	m.EXPECT().SetReplayOfID(gomock.Any(), id, id).Return(nil)
	_ = m.SetReplayOfID(ctx, id, id)

	m.EXPECT().ListReplaysOf(gomock.Any(), gomock.Any()).Return(nil, nil)
	_, _ = m.ListReplaysOf(ctx, []uuid.UUID{id})

	m.EXPECT().GetByID(gomock.Any(), id).Return(grab.Record{}, nil)
	_, _ = m.GetByID(ctx, id)

	m.EXPECT().UpdateSizeBytes(gomock.Any(), id, gomock.Any()).Return(nil)
	_ = m.UpdateSizeBytes(ctx, id, 0)

	m.EXPECT().ListUnparsedSince(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	_, _ = m.ListUnparsedSince(ctx, time.Now(), 10)

	m.EXPECT().UpdateParsed(gomock.Any(), id, gomock.Any(), gomock.Any()).Return(nil)
	_ = m.UpdateParsed(ctx, id, nil, time.Now())

	m.EXPECT().CountImportedEpisodes(gomock.Any(), domain.InstanceName("i"), domain.SonarrSeriesID(1), 1).Return(0, nil)
	_, _ = m.CountImportedEpisodes(ctx, "i", 1, 1)

	m.EXPECT().CountReplaysSince(gomock.Any(), domain.InstanceName("i"), gomock.Any()).Return(0, nil)
	_, _ = m.CountReplaysSince(ctx, "i", time.Now())

	m.EXPECT().CountReplaysAll(gomock.Any(), domain.InstanceName("i")).Return(0, nil)
	_, _ = m.CountReplaysAll(ctx, "i")
}

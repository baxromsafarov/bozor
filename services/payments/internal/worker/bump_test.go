package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeBumpStore struct {
	due      []domain.DueBump
	dueErr   error
	claimed  map[string]bool // "promo:day" → застолблён ли (эмуляция bump_runs)
	claimErr error
	claims   []string // порядок вызовов ClaimBump
	releases []string // порядок вызовов ReleaseBump
}

func key(p string, d int) string { return p + ":" + strconv.Itoa(d) }

func (f *fakeBumpStore) DueBumps(_ context.Context, _ int) ([]domain.DueBump, error) {
	return f.due, f.dueErr
}

func (f *fakeBumpStore) ClaimBump(_ context.Context, promotionID string, dayOffset int) (bool, error) {
	f.claims = append(f.claims, key(promotionID, dayOffset))
	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimed == nil {
		f.claimed = map[string]bool{}
	}
	k := key(promotionID, dayOffset)
	if f.claimed[k] {
		return false, nil // уже застолблён
	}
	f.claimed[k] = true
	return true, nil
}

func (f *fakeBumpStore) ReleaseBump(_ context.Context, promotionID string, dayOffset int) error {
	f.releases = append(f.releases, key(promotionID, dayOffset))
	delete(f.claimed, key(promotionID, dayOffset))
	return nil
}

type fakeListing struct {
	bumped  []string
	result  bool
	err     error
	errOnAd string // если задан — ошибка только для этого ad_id
}

func (f *fakeListing) Bump(_ context.Context, adID string) (bool, error) {
	f.bumped = append(f.bumped, adID)
	if f.errOnAd != "" && f.errOnAd == adID {
		return false, errors.New("listing down")
	}
	if f.err != nil {
		return false, f.err
	}
	return f.result, nil
}

func newBumper(store BumpStore, listing BumpTarget) *Bumper {
	return NewBumper(store, listing, time.Minute, 100, discardLog())
}

func TestSweep_BumpsDue(t *testing.T) {
	store := &fakeBumpStore{due: []domain.DueBump{
		{PromotionID: "p1", AdID: "ad1", DayOffset: 0},
		{PromotionID: "p1", AdID: "ad1", DayOffset: 3},
	}}
	listing := &fakeListing{result: true}
	newBumper(store, listing).Sweep(context.Background())

	assert.Equal(t, []string{"ad1", "ad1"}, listing.bumped, "оба созревших дня подняты")
	assert.Len(t, store.claims, 2)
	assert.Empty(t, store.releases, "успешные поднятия не освобождаются")
}

func TestSweep_AlreadyClaimed_Skips(t *testing.T) {
	store := &fakeBumpStore{
		due:     []domain.DueBump{{PromotionID: "p1", AdID: "ad1", DayOffset: 0}},
		claimed: map[string]bool{"p1:0": true}, // уже застолблён
	}
	listing := &fakeListing{result: true}
	newBumper(store, listing).Sweep(context.Background())

	assert.Empty(t, listing.bumped, "застолблённый день не поднимается повторно")
}

func TestSweep_ListingError_ReleasesDay(t *testing.T) {
	store := &fakeBumpStore{due: []domain.DueBump{{PromotionID: "p1", AdID: "ad1", DayOffset: 0}}}
	listing := &fakeListing{errOnAd: "ad1"}
	newBumper(store, listing).Sweep(context.Background())

	require.Len(t, store.claims, 1)
	assert.Equal(t, []string{"p1:0"}, store.releases, "ошибка Listing освобождает день для повтора")
	assert.NotContains(t, store.claimed, "p1:0")
}

func TestSweep_AdNotActive_KeepsClaim(t *testing.T) {
	store := &fakeBumpStore{due: []domain.DueBump{{PromotionID: "p1", AdID: "ad1", DayOffset: 0}}}
	listing := &fakeListing{result: false} // 404: объявления нет/не активно
	newBumper(store, listing).Sweep(context.Background())

	assert.Equal(t, []string{"ad1"}, listing.bumped)
	assert.Empty(t, store.releases, "неактивное объявление не освобождает день (не долбим повторно)")
	assert.Contains(t, store.claimed, "p1:0")
}

func TestSweep_DueError_NoBumps(t *testing.T) {
	store := &fakeBumpStore{dueErr: errors.New("db down")}
	listing := &fakeListing{result: true}
	newBumper(store, listing).Sweep(context.Background())
	assert.Empty(t, listing.bumped)
}

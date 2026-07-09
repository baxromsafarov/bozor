package transport

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/app"
	"bozor/services/payments/internal/domain"
)

type fakePromotions struct {
	promoteErr error
	gotUserID  string
	gotAdID    string
	gotReq     app.PromoteRequest
}

func (f *fakePromotions) Promote(_ context.Context, userID, adID string, req app.PromoteRequest) ([]domain.AdPromotion, error) {
	f.gotUserID, f.gotAdID, f.gotReq = userID, adID, req
	if f.promoteErr != nil {
		return nil, f.promoteErr
	}
	ends := time.Unix(1000, 0).UTC()
	return []domain.AdPromotion{{ID: "pr1", AdID: adID, ServiceCode: "TOP", Status: domain.PromotionActive,
		AmountUZS: 30000, StartsAt: time.Unix(1, 0).UTC(), EndsAt: &ends}}, nil
}
func (f *fakePromotions) Promotions(context.Context, string) ([]domain.AdPromotion, error) {
	return []domain.AdPromotion{{ID: "pr1", ServiceCode: "TOP", Status: domain.PromotionActive}}, nil
}

func promoServer(fp *fakePromotions, userID string) http.Handler {
	router := NewRouter(Deps{Log: discardLog(), Promotion: NewPromotionHandler(fp, discardLog())})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		router.ServeHTTP(w, req)
	})
}

func TestPromote_201(t *testing.T) {
	fp := &fakePromotions{}
	rec := do(t, promoServer(fp, "u1"), http.MethodPost, "/api/v1/ads/ad1/promote", `{"service_code":"TOP","duration_days":7}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "u1", fp.gotUserID)
	assert.Equal(t, "ad1", fp.gotAdID)
	assert.Equal(t, "TOP", fp.gotReq.ServiceCode)
	assert.Equal(t, 7, fp.gotReq.DurationDays)
	assert.Contains(t, rec.Body.String(), `"promotions"`)
}

func TestPromote_Anonymous_401(t *testing.T) {
	rec := do(t, promoServer(&fakePromotions{}, ""), http.MethodPost, "/api/v1/ads/ad1/promote", `{"bundle_code":"FAST_SALE"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPromote_AdNotFound_404(t *testing.T) {
	fp := &fakePromotions{promoteErr: domain.ErrAdNotFound}
	rec := do(t, promoServer(fp, "u1"), http.MethodPost, "/api/v1/ads/ad1/promote", `{"service_code":"TOP","duration_days":7}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPromote_NotOwner_403(t *testing.T) {
	fp := &fakePromotions{promoteErr: domain.ErrNotAdOwner}
	rec := do(t, promoServer(fp, "u1"), http.MethodPost, "/api/v1/ads/ad1/promote", `{"service_code":"TOP","duration_days":7}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestPromote_NotPromotable_409(t *testing.T) {
	fp := &fakePromotions{promoteErr: domain.ErrAdNotPromotable}
	rec := do(t, promoServer(fp, "u1"), http.MethodPost, "/api/v1/ads/ad1/promote", `{"service_code":"TOP","duration_days":7}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestPromote_InsufficientFunds_409(t *testing.T) {
	fp := &fakePromotions{promoteErr: domain.ErrInsufficientFunds}
	rec := do(t, promoServer(fp, "u1"), http.MethodPost, "/api/v1/ads/ad1/promote", `{"service_code":"TOP","duration_days":7}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestPromote_InvalidPromotion_422(t *testing.T) {
	fp := &fakePromotions{promoteErr: domain.ErrInvalidDuration}
	rec := do(t, promoServer(fp, "u1"), http.MethodPost, "/api/v1/ads/ad1/promote", `{"service_code":"TOP","duration_days":3}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestPromotions_List_200(t *testing.T) {
	rec := do(t, promoServer(&fakePromotions{}, "u1"), http.MethodGet, "/api/v1/ads/ad1/promotions", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"promotions"`)
}

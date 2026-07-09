package mock

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/payments/internal/domain"
	"bozor/services/payments/internal/provider"
)

func TestMock_CreateInvoice(t *testing.T) {
	p := New()
	assert.Equal(t, domain.ProviderMock, p.Name())

	inv, err := p.CreateInvoice(context.Background(), provider.Invoice{PaymentID: "p1", AmountUZS: 50000})
	require.NoError(t, err)
	assert.Contains(t, inv.CheckoutURL, "p1")
	assert.Equal(t, "/internal/payments/mock/p1/confirm", inv.Extra["confirm_endpoint"])
}

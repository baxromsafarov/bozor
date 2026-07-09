package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateTopup — сумма положительна и в границах [TopupMin, TopupMax].
func TestValidateTopup(t *testing.T) {
	assert.NoError(t, ValidateTopup(TopupMin))
	assert.NoError(t, ValidateTopup(TopupMax))
	assert.NoError(t, ValidateTopup(50000))

	assert.ErrorIs(t, ValidateTopup(0), ErrInvalidAmount)
	assert.ErrorIs(t, ValidateTopup(-100), ErrInvalidAmount)
	assert.ErrorIs(t, ValidateTopup(TopupMin-1), ErrTopupOutOfRange)
	assert.ErrorIs(t, ValidateTopup(TopupMax+1), ErrTopupOutOfRange)
}

// TestSignedAmount — пополнение увеличивает баланс (+), списание уменьшает (−).
func TestSignedAmount(t *testing.T) {
	credit := Transaction{Direction: DirectionCredit, AmountUZS: 5000}
	debit := Transaction{Direction: DirectionDebit, AmountUZS: 3000}

	assert.EqualValues(t, 5000, credit.SignedAmount())
	assert.EqualValues(t, -3000, debit.SignedAmount())
}

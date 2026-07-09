package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bozor/services/chat/internal/domain"
)

func TestConversation_Participant(t *testing.T) {
	c := domain.Conversation{BuyerID: "b", SellerID: "s"}
	assert.True(t, c.Participant("b"))
	assert.True(t, c.Participant("s"))
	assert.False(t, c.Participant("x"))
}

func TestConversation_Counterpart(t *testing.T) {
	c := domain.Conversation{BuyerID: "b", SellerID: "s"}
	assert.Equal(t, "s", c.Counterpart("b"), "адресат сообщения покупателя — продавец")
	assert.Equal(t, "b", c.Counterpart("s"), "адресат сообщения продавца — покупатель")
}

func TestValidateBody(t *testing.T) {
	require.NoError(t, domain.ValidateBody("привет"))
	require.NoError(t, domain.ValidateBody("  есть  "), "внутренний текст непустой")

	assert.ErrorIs(t, domain.ValidateBody(""), domain.ErrEmptyBody)
	assert.ErrorIs(t, domain.ValidateBody("   "), domain.ErrEmptyBody, "только пробелы — пусто")

	assert.NoError(t, domain.ValidateBody(strings.Repeat("я", domain.BodyMaxLen)), "ровно лимит — ок")
	assert.ErrorIs(t, domain.ValidateBody(strings.Repeat("я", domain.BodyMaxLen+1)), domain.ErrBodyTooLong)
}

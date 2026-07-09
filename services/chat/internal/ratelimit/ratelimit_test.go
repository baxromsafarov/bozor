package ratelimit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllow_BurstThenLimited — первые burst обращений проходят, следующее
// отклоняется (токены исчерпаны до пополнения).
func TestAllow_BurstThenLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// perSec низкий, чтобы за время теста пополнения не случилось; burst=3.
	l := New(ctx, 0.001, 3)

	for i := 0; i < 3; i++ {
		assert.True(t, l.Allow("u1"), "обращение %d в пределах burst", i)
	}
	assert.False(t, l.Allow("u1"), "burst исчерпан — отказ")
}

// TestAllow_PerUserIsolation — лимит одного пользователя не затрагивает другого.
func TestAllow_PerUserIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := New(ctx, 0.001, 1)

	assert.True(t, l.Allow("u1"))
	assert.False(t, l.Allow("u1"), "u1 исчерпал свой bucket")
	assert.True(t, l.Allow("u2"), "у u2 отдельный bucket")
}

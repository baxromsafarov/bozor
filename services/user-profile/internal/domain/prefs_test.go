package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidNotificationPref(t *testing.T) {
	assert.True(t, ValidNotificationPref(ChannelTelegram, NotifyAdStatus))
	assert.True(t, ValidNotificationPref(ChannelTelegram, NotifyChatMessage))
	assert.False(t, ValidNotificationPref("email", NotifyAdStatus), "неизвестный канал")
	assert.False(t, ValidNotificationPref(ChannelTelegram, "unknown"), "неизвестный тип")
}

func TestDefaultNotificationPrefs(t *testing.T) {
	def := DefaultNotificationPrefs()
	require.Len(t, def, 5, "все известные типы")
	for _, p := range def {
		assert.Equal(t, ChannelTelegram, p.Channel)
		assert.True(t, p.Enabled, "по умолчанию включено")
	}
}

func TestEffectiveNotificationPrefs_Overlay(t *testing.T) {
	// Сохранена одна выключенная настройка — остальные остаются по умолчанию (вкл).
	stored := []NotificationPref{{Channel: ChannelTelegram, EventType: NotifyChatMessage, Enabled: false}}
	eff := EffectiveNotificationPrefs(stored)
	require.Len(t, eff, 5)

	byType := make(map[string]bool, len(eff))
	for _, p := range eff {
		byType[p.EventType] = p.Enabled
	}
	assert.False(t, byType[NotifyChatMessage], "переопределено на выключено")
	assert.True(t, byType[NotifyAdStatus], "остальные — по умолчанию включено")
}

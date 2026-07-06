// Package config предоставляет чтение конфигурации из переменных окружения
// в духе 12-factor: строки, числа, булевы значения и длительности
// с значениями по умолчанию, а также проверку обязательных ключей.
package config

import (
	"os"
	"strconv"
	"time"
)

// Lookup возвращает значение переменной окружения key и признак того,
// что она задана (в том числе пустой строкой).
func Lookup(key string) (string, bool) {
	return os.LookupEnv(key)
}

// String возвращает значение переменной окружения key.
// Если переменная не задана или пуста — возвращается def.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Int возвращает целочисленное значение переменной окружения key.
// Если переменная не задана или не парсится как целое — возвращается def.
func Int(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// Bool возвращает булево значение переменной окружения key
// (форматы strconv.ParseBool: 1/0, t/f, true/false и т.п.).
// Если переменная не задана или не парсится — возвращается def.
func Bool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Duration возвращает длительность из переменной окружения key
// в формате time.ParseDuration (например, "1h30m", "500ms").
// Если переменная не задана или не парсится — возвращается def.
func Duration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// Missing возвращает список обязательных ключей, которые не заданы
// в окружении или заданы пустой строкой. Порядок соответствует keys.
func Missing(keys ...string) []string {
	var missing []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); !ok || v == "" {
			missing = append(missing, k)
		}
	}
	return missing
}

// Package domain содержит справочные сущности Location-сервиса.
package domain

import "errors"

// ErrRegionNotFound — регион с указанным id отсутствует.
var ErrRegionNotFound = errors.New("регион не найден")

// Region — регион Узбекистана (область, город или республика).
type Region struct {
	ID        int
	Slug      string
	NameUZ    string
	NameRU    string
	Latitude  *float64
	Longitude *float64
	SortOrder int
}

// City — город или район, привязанный к региону.
type City struct {
	ID        int
	RegionID  int
	Slug      string
	NameUZ    string
	NameRU    string
	Latitude  *float64
	Longitude *float64
	SortOrder int
}

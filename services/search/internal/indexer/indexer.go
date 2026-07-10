// Package indexer синхронизирует read-модель Typesense с объявлениями Listing:
// событие bozor.ad.* — триггер, актуальное состояние читается из Listing
// (источник истины), затем документ upsert'ится (active) или удаляется. Такой
// подход устойчив к переупорядочиванию: каждое событие сводит индекс к текущей
// истине (Stage 4.2).
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bozor/pkg/shared/events"

	"bozor/services/search/internal/listingclient"
	"bozor/services/search/internal/search"
)

// Consumer — имя durable-консьюмера индексатора.
const Consumer = "search-indexer"

// statusActive — только активные объявления попадают в поисковый индекс.
const statusActive = "active"

// Documents — операции над документами Typesense (реализуется search.Client).
type Documents interface {
	UpsertDocument(ctx context.Context, collection string, doc any) error
	DeleteDocument(ctx context.Context, collection, id string) error
}

// Source — источник актуального состояния объявлений (реализуется listingclient).
type Source interface {
	GetAd(ctx context.Context, id string) (listingclient.Ad, bool, error)
	ListActive(ctx context.Context, after string, limit int) ([]listingclient.Ad, string, error)
}

// Indexer сводит поисковый индекс к текущему состоянию объявлений.
type Indexer struct {
	docs   Documents
	source Source
	log    *slog.Logger
}

// New создаёт индексатор.
func New(docs Documents, source Source, log *slog.Logger) *Indexer {
	return &Indexer{docs: docs, source: source, log: log}
}

// adEvent — минимальная нагрузка событий bozor.ad.* (нужен только id).
type adEvent struct {
	AdID string `json:"ad_id"`
}

// Handle обрабатывает одно событие: читает актуальное состояние объявления и
// синхронизирует документ (upsert active / delete иначе). Ошибка → повтор/DLQ.
func (ix *Indexer) Handle(ctx context.Context, env events.Envelope) error {
	var pl adEvent
	if err := env.Decode(&pl); err != nil {
		return fmt.Errorf("indexer: разбор события: %w", err)
	}
	if pl.AdID == "" {
		return errors.New("indexer: пустой ad_id в событии")
	}
	return ix.sync(ctx, pl.AdID)
}

// sync приводит документ объявления в индексе к текущему состоянию в Listing.
func (ix *Indexer) sync(ctx context.Context, adID string) error {
	ad, found, err := ix.source.GetAd(ctx, adID)
	if err != nil {
		return err
	}
	if !found || ad.Status != statusActive {
		// Удалено/не активно — снять из индекса (идемпотентно).
		return ix.docs.DeleteDocument(ctx, search.AdsCollection, adID)
	}
	doc, err := buildDoc(ad)
	if err != nil {
		return err
	}
	return ix.docs.UpsertDocument(ctx, search.AdsCollection, doc)
}

// Reindex перестраивает индекс из полного экспорта активных объявлений Listing
// (постранично keyset). Возвращает число проиндексированных документов.
func (ix *Indexer) Reindex(ctx context.Context) (int, error) {
	const pageSize = 100
	after, total := "", 0
	for {
		ads, next, err := ix.source.ListActive(ctx, after, pageSize)
		if err != nil {
			return total, err
		}
		for i := range ads {
			doc, err := buildDoc(ads[i])
			if err != nil {
				return total, err
			}
			if err := ix.docs.UpsertDocument(ctx, search.AdsCollection, doc); err != nil {
				return total, err
			}
			total++
		}
		if next == "" || len(ads) == 0 {
			break
		}
		after = next
	}
	ix.log.InfoContext(ctx, "переиндексация завершена", slog.Int("documents", total))
	return total, nil
}

// adDoc — документ коллекции ads Typesense (проекция объявления).
type adDoc struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description,omitempty"`
	CategoryID    string            `json:"category_id"`
	RegionID      int32             `json:"region_id"`
	CityID        *int64            `json:"city_id,omitempty"`
	Price         int64             `json:"price"`
	Currency      string            `json:"currency"`
	Status        string            `json:"status"`
	Attrs         map[string]string `json:"attrs,omitempty"`
	CreatedAt     int64             `json:"created_at"`
	BumpedAt      *int64            `json:"bumped_at,omitempty"`
	IsTop         bool              `json:"is_top"`
	PromotionRank int32             `json:"promotion_rank,omitempty"`
	PromoEndsAt   *int64            `json:"promo_ends_at,omitempty"`
	Location      []float64         `json:"location,omitempty"`
}

// buildDoc строит документ Typesense из проекции объявления Listing.
func buildDoc(ad listingclient.Ad) (adDoc, error) {
	created, err := unixFromRFC3339(ad.CreatedAt)
	if err != nil {
		return adDoc{}, fmt.Errorf("indexer: created_at объявления %s: %w", ad.ID, err)
	}
	doc := adDoc{
		ID: ad.ID, Title: ad.Title, Description: ad.Description, CategoryID: ad.CategoryID,
		RegionID: ad.RegionID, CityID: ad.CityID, Price: ad.Price, Currency: ad.Currency,
		Status: ad.Status, CreatedAt: created,
		IsTop: ad.IsTop, PromotionRank: ad.PromotionRank,
	}
	if len(ad.Attributes) > 0 {
		doc.Attrs = make(map[string]string, len(ad.Attributes))
		for _, a := range ad.Attributes {
			doc.Attrs[a.Slug] = a.Value
		}
	}
	if ad.BumpedAt != "" {
		if b, err := unixFromRFC3339(ad.BumpedAt); err == nil {
			doc.BumpedAt = &b
		}
	}
	if ad.PromoEndsAt != "" {
		if e, err := unixFromRFC3339(ad.PromoEndsAt); err == nil {
			doc.PromoEndsAt = &e
		}
	}
	if ad.Lat != nil && ad.Lng != nil {
		doc.Location = []float64{*ad.Lat, *ad.Lng}
	}
	return doc, nil
}

// unixFromRFC3339 переводит RFC3339-время в unix-секунды.
func unixFromRFC3339(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

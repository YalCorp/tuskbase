package daemon

import (
	"context"
	"log/slog"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/postgres"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
	"github.com/priyavratuniyal/tuskbase/internal/search"
)

// PostgresStoreFactory powers Local Shared by swapping the durable store at the
// composition boundary while leaving application use cases unchanged.
type PostgresStoreFactory struct {
	DriverName string
	DSN        string
	Embedding  ports.EmbeddingProvider
	Logger     *slog.Logger
}

func (f PostgresStoreFactory) Open(ctx context.Context) (StoreBundle, error) {
	driver := strings.TrimSpace(f.DriverName)
	if driver == "" {
		driver = "pgx"
	}
	store, err := postgres.Open(ctx, driver, f.DSN)
	if err != nil {
		return StoreBundle{}, err
	}
	var vectors ports.VectorStore
	if v, ok := any(store).(ports.VectorStore); ok {
		vectors = v
	}
	idx := search.NewHybridIndex(store, vectors, f.Embedding, f.Logger)
	return StoreBundle{Store: store, Search: idx, Close: store.Close, Name: "postgres"}, nil
}

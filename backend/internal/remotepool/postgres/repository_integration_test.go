//go:build integration

package postgres

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// repository_integration_test.go verifies settings callbacks against migrated
// PostgreSQL tables. Refresh token consumption requires serialization across server
// instances, so concurrent first updates and callback rollback are exercised through
// separate repository objects sharing one real database.

// TestUpdateMultipleSerializesAndRollsBack starts concurrent increments on an
// absent settings key.
// It verifies callbacks observe prior commits across instances and a rejected
// callback
// leaves the last complete saved value available to ordinary readers.
func TestUpdateMultipleSerializesAndRollsBack(t *testing.T) {
	db := postgrescontainer.New(t)
	repos := []*Repository{NewRepository(db), NewRepository(db)}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 8 {
		repo := repos[i%len(repos)]
		wg.Go(func() {
			err := repo.UpdateMultiple(ctx, []string{"remote_pool_test"}, func(values map[string]string) (map[string]string, error) {
				value := 0
				if values["remote_pool_test"] != "" {
					var err error
					value, err = strconv.Atoi(values["remote_pool_test"])
					if err != nil {
						return nil, err
					}
				}
				return map[string]string{"remote_pool_test": strconv.Itoa(value + 1)}, nil
			})
			require.NoError(t, err)
		})
	}
	wg.Wait()
	value, err := repos[0].GetValue(ctx, "remote_pool_test")
	require.NoError(t, err)
	require.Equal(t, "8", value)
	rejected := errors.New("reject callback")
	err = repos[1].UpdateMultiple(ctx, []string{"remote_pool_test"}, func(map[string]string) (map[string]string, error) {
		return map[string]string{"remote_pool_test": "partial"}, rejected
	})
	require.ErrorIs(t, err, rejected)
	value, err = repos[0].GetValue(ctx, "remote_pool_test")
	require.NoError(t, err)
	require.Equal(t, "8", value)
}

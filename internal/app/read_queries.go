package app

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// readTransaction limits public historical statements without changing collector or pool settings.
func (s *Store) readTransaction(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='15s'"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

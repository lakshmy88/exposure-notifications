// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/googlepartners/exposure-notifications/internal/model"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v4"
)

var (
	// ErrNotFound indicates that the requested record was not found in the database.
	ErrNotFound = errors.New("record not found")
)

// FinalizeSyncFn is used to finalize a historical sync record.
type FinalizeSyncFn func(maxTimestamp time.Time, totalInserted int) error

type queryRowFn func(ctx context.Context, query string, args ...interface{}) pgx.Row

// GetFederationQuery returns a query for given queryID. If not found, ErrNotFound will be returned.
func (db *DB) GetFederationQuery(ctx context.Context, queryID string) (*model.FederationQuery, error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain database connection: %v", err)
	}
	defer conn.Release()
	return getFederationQuery(ctx, queryID, conn.QueryRow)
}

func getFederationQuery(ctx context.Context, queryID string, queryRow queryRowFn) (*model.FederationQuery, error) {
	row := queryRow(ctx, `
		SELECT
			query_id, server_addr, include_regions, exclude_regions, last_timestamp
		FROM FederationQuery 
		WHERE 
			query_id=$1
		`, queryID)

	// See https://www.opsdash.com/blog/postgres-arrays-golang.html for working with Postgres arrays in Go.
	q := model.FederationQuery{}
	if err := row.Scan(&q.QueryID, &q.ServerAddr, &q.IncludeRegions, &q.ExcludeRegions, &q.LastTimestamp); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning results: %v", err)
	}
	return &q, nil
}

// AddFederationQuery adds a FederationQuery entity. It will overwrite a query with matching q.queryID if it exists.
func (db *DB) AddFederationQuery(ctx context.Context, q *model.FederationQuery) (err error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("unable to obtain database connection: %v", err)
	}
	defer conn.Release()

	commit := false
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("starting transaction: %v", err)
	}
	defer finishTx(ctx, tx, &commit, &err)

	existing := true
	if _, err := getFederationQuery(ctx, q.QueryID, tx.QueryRow); err != nil {
		if err == ErrNotFound {
			existing = false
		} else {
			return fmt.Errorf("getting existing federation query %s: %v", q.QueryID, err)
		}
	}

	if existing {
		_, err := tx.Exec(ctx, `
			DELETE FROM FederationQuery
			WHERE
				query_id=$1
			`, q.QueryID)
		if err != nil {
			return fmt.Errorf("deleting existing federation query %s: %v", q.QueryID, err)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO FederationQuery
			(query_id, server_addr, include_regions, exclude_regions, last_timestamp)
		VALUES
			($1, $2, $3, $4, $5)
		`, q.QueryID, q.ServerAddr, q.IncludeRegions, q.ExcludeRegions, q.LastTimestamp)
	if err != nil {
		return fmt.Errorf("inserting federation query: %v", err)
	}

	commit = true
	return nil
}

// GetFederationSync returns a federation sync record for given syncID. If not found, ErrNotFound will be returned.
func (db *DB) GetFederationSync(ctx context.Context, syncID string) (*model.FederationSync, error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain database connection: %v", err)
	}
	defer conn.Release()
	return getFederationSync(ctx, syncID, conn.QueryRow)
}

func getFederationSync(ctx context.Context, syncID string, queryRowContext queryRowFn) (*model.FederationSync, error) {
	row := queryRowContext(ctx, `
		SELECT
			sync_id, query_id, started, completed, insertions, max_timestamp
		FROM FederationSync
		WHERE
			sync_id=$1
		`, syncID)

	s := model.FederationSync{}
	if err := row.Scan(&s.SyncID, &s.QueryID, &s.Started, &s.Completed, &s.Insertions, &s.MaxTimestamp); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning results: %v", err)
	}
	return &s, nil
}

// StartFederationSync stores a historical record of a query sync starting. It returns a FederationSync key, and a FinalizeSyncFn that must be invoked to finalize the historical record.
func (db *DB) StartFederationSync(ctx context.Context, q *model.FederationQuery, started time.Time) (string, FinalizeSyncFn, error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("unable to obtain database connection: %v", err)
	}
	defer conn.Release()

	startedTimer := time.Now().UTC()
	syncID := uuid.New().String()
	_, err = conn.Exec(ctx, `
		INSERT INTO FederationSync
			(sync_id, query_id, started)
		VALUES
			($1, $2, $3)
		`, syncID, q.QueryID, started)
	if err != nil {
		return "", nil, fmt.Errorf("inserting federation sync: %v", err)
	}

	finalize := func(maxTimestamp time.Time, totalInserted int) (err error) {
		conn, err := db.pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("unable to obtain database connection: %v", err)
		}
		defer conn.Release()

		completed := started.Add(time.Now().UTC().Sub(startedTimer))
		commit := false
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return fmt.Errorf("starting transaction: %v", err)
		}
		defer finishTx(ctx, tx, &commit, &err)

		// Special case: when no keys are pulled, the maxTimestamp will be 0, so we don't update the
		// FederationQuery in this case to prevent it from going back and fetching old keys from the past.
		if totalInserted > 0 {
			_, err = tx.Exec(ctx, `
			UPDATE FederationQuery
			SET
				last_timestamp = $1
			WHERE
				query_id = $2
			`, maxTimestamp, q.QueryID)
			if err != nil {
				return fmt.Errorf("updating federation query: %v", err)
			}
		}

		_, err = tx.Exec(ctx, `
			UPDATE FederationSync
			SET
				completed = $1,
				insertions = $2,
				max_timestamp = $3
			WHERE
				sync_id = $4
			`, completed, totalInserted, maxTimestamp, syncID)
		if err != nil {
			return fmt.Errorf("updating federation sync: %v", err)
		}

		commit = true
		return nil
	}

	return syncID, finalize, nil
}

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
	"database/sql"
	"fmt"

	"github.com/googlepartners/exposure-notifications/internal/model"

	pgx "github.com/jackc/pgx/v4"
)

func (db *DB) ReadAPIConfigs(ctx context.Context) ([]*model.APIConfig, error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("untable to obtain database connection: %v", err)
	}
	defer conn.Release()

	commit := false
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer finishTx(ctx, tx, &commit, &err)

	query := `
    SELECT
    app_package_name, apk_digest, enforce_apk_digest, cts_profile_match, basic_integrity, max_age_seconds, clock_skew_seconds, allowed_regions, all_regions, bypass_safetynet
    FROM APIConfig`
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// In most instances, we expect a single config entry.
	result := make([]*model.APIConfig, 0, 1)
	for rows.Next() {
		var regions []string
		config := model.NewAPIConfig()
		var apkDigest sql.NullString
		if err := rows.Scan(&config.AppPackageName, &apkDigest,
			&config.EnforceApkDigest, &config.CTSProfileMatch, &config.BasicIntegrity, &config.MaxAgeSeconds,
			&config.ClockSkewSeconds, &regions, &config.AllowAllRegions, &config.BypassSafetynet); err != nil {
			return nil, err
		}
		if apkDigest.Valid {
			config.ApkDigestSHA256 = apkDigest.String
		}

		// build the regions map
		for _, r := range regions {
			config.AllowedRegions[r] = true
		}

		result = append(result, config)
	}

	return result, nil
}

package store

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// Store wraps a SQLite database for drift-checker persistence.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		PRAGMA journal_mode=WAL;

		CREATE TABLE IF NOT EXISTS sync_runs (
			project   TEXT PRIMARY KEY,
			synced_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS appsets (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			project   TEXT    NOT NULL,
			name      TEXT    NOT NULL,
			namespace TEXT    NOT NULL
		);

		CREATE TABLE IF NOT EXISTS apps (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			appset_id      INTEGER NOT NULL,
			name           TEXT    NOT NULL,
			region         TEXT    NOT NULL,
			cluster_name   TEXT    NOT NULL,
			cluster_server TEXT    NOT NULL,
			namespace      TEXT    NOT NULL,
			sync_status    TEXT    NOT NULL,
			health_status  TEXT    NOT NULL,
			revision       TEXT    NOT NULL
		);

		CREATE TABLE IF NOT EXISTS builds (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			app_id      INTEGER NOT NULL,
			repository  TEXT    NOT NULL,
			tag         TEXT    NOT NULL,
			digest      TEXT    NOT NULL,
			full_image  TEXT    NOT NULL
		);

		CREATE TABLE IF NOT EXISTS workloads (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			app_id        INTEGER NOT NULL,
			grp           TEXT    NOT NULL,
			kind          TEXT    NOT NULL,
			name          TEXT    NOT NULL,
			namespace     TEXT    NOT NULL,
			sync_status   TEXT    NOT NULL,
			health_status TEXT    NOT NULL
		);
	`)
	return err
}

// Sync replaces all stored data for a project with appsets from a fresh analysis.
func (s *Store) Sync(ctx context.Context, project string, appsets []domain.AppSet) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete old data for the project.
	if err := deleteProject(tx, project); err != nil {
		return err
	}

	for _, as := range appsets {
		var asID int64
		err := tx.QueryRowContext(ctx,
			`INSERT INTO appsets (project, name, namespace) VALUES (?, ?, ?) RETURNING id`,
			project, as.Name, as.Namespace,
		).Scan(&asID)
		if err != nil {
			return err
		}

		for _, app := range as.Apps {
			var appID int64
			err := tx.QueryRowContext(ctx,
				`INSERT INTO apps (appset_id, name, region, cluster_name, cluster_server, namespace, sync_status, health_status, revision)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				asID, app.Name, app.Region, app.ClusterName, app.ClusterServer,
				app.Namespace, app.SyncStatus, app.HealthStatus, app.Revision,
			).Scan(&appID)
			if err != nil {
				return err
			}

			for _, img := range app.Images {
				_, err := tx.ExecContext(ctx,
					`INSERT INTO builds (app_id, repository, tag, digest, full_image) VALUES (?, ?, ?, ?, ?)`,
					appID, img.Repository, img.Tag, img.Digest, img.Full,
				)
				if err != nil {
					return err
				}
			}

			for _, res := range app.Resources {
				_, err := tx.ExecContext(ctx,
					`INSERT INTO workloads (app_id, grp, kind, name, namespace, sync_status, health_status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					appID, res.Group, res.Kind, res.Name, res.Namespace, res.SyncStatus, res.HealthStatus,
				)
				if err != nil {
					return err
				}
			}
		}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO sync_runs (project, synced_at) VALUES (?, ?)`,
		project, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// LastSync returns the time of the last sync for a project, or zero if never synced.
func (s *Store) LastSync(ctx context.Context, project string) (time.Time, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT synced_at FROM sync_runs WHERE project = ?`, project,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, raw)
}

// Projects returns all project names that have been synced.
func (s *Store) Projects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project FROM sync_runs ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// LoadAppSets reconstructs the AppSet slice for a project from the database.
func (s *Store) LoadAppSets(ctx context.Context, project string) ([]domain.AppSet, error) {
	asRows, err := s.db.QueryContext(ctx,
		`SELECT id, name, namespace FROM appsets WHERE project = ? ORDER BY name`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer asRows.Close()

	var appsets []domain.AppSet
	for asRows.Next() {
		var asID int64
		var as domain.AppSet
		if err := asRows.Scan(&asID, &as.Name, &as.Namespace); err != nil {
			return nil, err
		}

		apps, err := s.loadApps(ctx, asID)
		if err != nil {
			return nil, err
		}
		as.Apps = apps
		appsets = append(appsets, as)
	}
	if err := asRows.Err(); err != nil {
		return nil, err
	}
	return appsets, nil
}

func (s *Store) loadApps(ctx context.Context, appsetID int64) ([]domain.AppInstance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, region, cluster_name, cluster_server, namespace, sync_status, health_status, revision
		 FROM apps WHERE appset_id = ? ORDER BY region`,
		appsetID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []domain.AppInstance
	for rows.Next() {
		var appID int64
		var app domain.AppInstance
		if err := rows.Scan(&appID, &app.Name, &app.Region, &app.ClusterName, &app.ClusterServer,
			&app.Namespace, &app.SyncStatus, &app.HealthStatus, &app.Revision); err != nil {
			return nil, err
		}

		images, err := s.loadBuilds(ctx, appID)
		if err != nil {
			return nil, err
		}
		app.Images = images

		resources, err := s.loadWorkloads(ctx, appID)
		if err != nil {
			return nil, err
		}
		app.Resources = resources

		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func (s *Store) loadBuilds(ctx context.Context, appID int64) ([]domain.ImageRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repository, tag, digest, full_image FROM builds WHERE app_id = ?`,
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var imgs []domain.ImageRef
	for rows.Next() {
		var img domain.ImageRef
		if err := rows.Scan(&img.Repository, &img.Tag, &img.Digest, &img.Full); err != nil {
			return nil, err
		}
		imgs = append(imgs, img)
	}
	return imgs, rows.Err()
}

func (s *Store) loadWorkloads(ctx context.Context, appID int64) ([]domain.ResourceStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT grp, kind, name, namespace, sync_status, health_status FROM workloads WHERE app_id = ?`,
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []domain.ResourceStatus
	for rows.Next() {
		var res domain.ResourceStatus
		if err := rows.Scan(&res.Group, &res.Kind, &res.Name, &res.Namespace, &res.SyncStatus, &res.HealthStatus); err != nil {
			return nil, err
		}
		resources = append(resources, res)
	}
	return resources, rows.Err()
}

func deleteProject(tx *sql.Tx, project string) error {
	// Delete builds and workloads via cascading through app IDs.
	_, err := tx.Exec(`
		DELETE FROM builds WHERE app_id IN (
			SELECT a.id FROM apps a
			JOIN appsets s ON a.appset_id = s.id
			WHERE s.project = ?
		)`, project)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		DELETE FROM workloads WHERE app_id IN (
			SELECT a.id FROM apps a
			JOIN appsets s ON a.appset_id = s.id
			WHERE s.project = ?
		)`, project)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		DELETE FROM apps WHERE appset_id IN (
			SELECT id FROM appsets WHERE project = ?
		)`, project)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM appsets WHERE project = ?`, project)
	return err
}

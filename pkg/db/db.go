package db

import (
	"context"
	stdsql "database/sql"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pkg/errors"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/migrate"
	"scutbot.cn/web/rm-monitor/pkg/config"
)

func IsNoRows(err error) bool {
	return err != nil && (errors.Cause(err) == stdsql.ErrNoRows || strings.Contains(err.Error(), "no rows in result set"))
}

func Open(_ context.Context, c config.PostgresConf) (*ent.Client, error) {
	if c.DSN == "" {
		return nil, errors.New("postgres dsn is required")
	}

	sqlDB, err := stdsql.Open("pgx", c.DSN)
	if err != nil {
		return nil, errors.Wrap(err, "open postgres")
	}
	drv := entsql.OpenDB(dialect.Postgres, sqlDB)

	client := ent.NewClient(ent.Driver(drv))
	return client, nil
}

func Migrate(ctx context.Context, c config.PostgresConf) error {
	if c.DSN == "" {
		return errors.New("postgres dsn is required")
	}

	sqlDB, err := stdsql.Open("pgx", c.DSN)
	if err != nil {
		return errors.Wrap(err, "open postgres")
	}
	defer sqlDB.Close()

	drv := entsql.OpenDB(dialect.Postgres, sqlDB)
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	if err := migrateLegacyLarkMessages(ctx, sqlDB); err != nil {
		return err
	}
	if err := dropLegacyTaskTables(ctx, sqlDB); err != nil {
		return err
	}
	if err := client.Schema.Create(ctx, migrate.WithDropColumn(true)); err != nil {
		return errors.Wrap(err, "run ent schema migration")
	}
	return nil
}

func dropLegacyTaskTables(ctx context.Context, db *stdsql.DB) error {
	tables := []string{
		"analyze_tasks",
		"external_publications",
		"highlight_publish_tasks",
		"highlight_round_states",
		"media_artifacts",
		"ocr_tasks",
		"record_tasks",
		"round_analyses",
		"stt_tasks",
		"transcode_tasks",
		"upload_tasks",
	}
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
			return errors.Wrapf(err, "drop legacy table %s", table)
		}
	}
	return nil
}

func migrateLegacyLarkMessages(ctx context.Context, db *stdsql.DB) error {
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS lark_card_messages`); err != nil {
		return errors.Wrap(err, "drop lark_card_messages")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin legacy lark message migration")
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmts := []string{
		`DO $$
		BEGIN
			IF to_regclass('lark_messages') IS NULL THEN
				RETURN;
			END IF;
			ALTER TABLE lark_messages ADD COLUMN IF NOT EXISTS message_id text;
			ALTER TABLE lark_messages ADD COLUMN IF NOT EXISTS card_id text;
			DROP INDEX IF EXISTS larkmessage_match_lark_messages;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema()
				  AND table_name = 'lark_messages'
				  AND column_name = 'card_id'
			) THEN
				UPDATE lark_messages
				SET message_id = 'legacy:' || card_id
				WHERE message_id IS NULL;
			END IF;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema()
				  AND table_name = 'lark_messages'
				  AND column_name = 'match_lark_messages'
			) THEN
				UPDATE lark_messages
				SET message_id = 'legacy:' || match_lark_messages
				WHERE message_id IS NULL;
			END IF;
		END $$`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return errors.Wrap(err, "run legacy lark message migration")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit legacy lark message migration")
	}
	return nil
}

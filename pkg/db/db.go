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

func Open(ctx context.Context, c config.PostgresConf) (*ent.Client, error) {
	if c.DSN == "" {
		return nil, errors.New("postgres dsn is required")
	}

	sqlDB, err := stdsql.Open("pgx", c.DSN)
	if err != nil {
		return nil, errors.Wrap(err, "open postgres")
	}
	drv := entsql.OpenDB(dialect.Postgres, sqlDB)

	client := ent.NewClient(ent.Driver(drv))
	if c.AutoMigrate {
		if err := migrateLegacyLarkMessages(ctx, sqlDB); err != nil {
			_ = client.Close()
			return nil, err
		}
		if err := client.Schema.Create(ctx, migrate.WithDropColumn(true)); err != nil {
			_ = client.Close()
			return nil, errors.Wrap(err, "run ent schema migration")
		}
	}
	return client, nil
}

func migrateLegacyLarkMessages(ctx context.Context, db *stdsql.DB) error {
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS lark_card_messages`); err != nil {
		return errors.Wrap(err, "drop lark_card_messages")
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'lark_messages'
			  AND column_name = 'message_id'
		)
	`).Scan(&exists); err != nil {
		return errors.Wrap(err, "check legacy lark_messages.message_id")
	}
	if !exists {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin legacy lark message migration")
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmts := []string{
		`ALTER TABLE lark_messages ADD COLUMN IF NOT EXISTS card_id text`,
		`WITH keepers AS (
			SELECT match_lark_messages, min(id) AS keep_id
			FROM lark_messages
			GROUP BY match_lark_messages
		)
		UPDATE lark_messages lm
		SET card_id = 'legacy:' || lm.match_lark_messages
		FROM keepers k
		WHERE lm.id = k.keep_id`,
		`DELETE FROM lark_messages lm
		USING (
			SELECT match_lark_messages, min(id) AS keep_id
			FROM lark_messages
			GROUP BY match_lark_messages
		) k
		WHERE lm.match_lark_messages = k.match_lark_messages
		  AND lm.id <> k.keep_id`,
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

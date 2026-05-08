package db

import (
	"context"
	stdsql "database/sql"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pkg/errors"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/pkg/config"
)

func Open(ctx context.Context, c config.PostgresConf) (*ent.Client, error) {
	if c.DSN == "" {
		return nil, errors.New("postgres dsn is required")
	}

	sqlDB, err := stdsql.Open("pgx", c.DSN)
	if err != nil {
		return nil, errors.Wrap(err, "open postgres")
	}
	drv := entsql.OpenDB(dialect.Postgres, sqlDB)

	client := ent.NewClient(ent.Driver(drv), ent.Debug())
	if c.AutoMigrate {
		if err := client.Schema.Create(ctx); err != nil {
			_ = client.Close()
			return nil, errors.Wrap(err, "run ent schema migration")
		}
	}
	return client, nil
}

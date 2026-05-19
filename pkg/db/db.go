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
		if err := client.Schema.Create(ctx, migrate.WithDropColumn(true)); err != nil {
			_ = client.Close()
			return nil, errors.Wrap(err, "run ent schema migration")
		}
	}
	return client, nil
}

package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/pkg/errors"
)

const (
	MatchRoundChangedChannel    = "match_round_changed"
	MatchChangedChannel         = "match_changed"
	HighlightClipChangedChannel = "highlight_clip_changed"
)

func Notify(ctx context.Context, dsn, channel, payload string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.Wrap(err, "connect pg notify")
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "select pg_notify($1, $2)", channel, payload)
	return errors.Wrap(err, "pg notify")
}

type Listener struct {
	conn *pgx.Conn
}

func NewListener(ctx context.Context, dsn string, channels ...string) (*Listener, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, errors.Wrap(err, "connect pg listener")
	}
	for _, channel := range channels {
		if _, err := conn.Exec(ctx, "listen "+pgx.Identifier{channel}.Sanitize()); err != nil {
			_ = conn.Close(ctx)
			return nil, errors.Wrapf(err, "listen %s", channel)
		}
	}
	return &Listener{conn: conn}, nil
}

func (l *Listener) Wait(ctx context.Context) (string, string, error) {
	n, err := l.conn.WaitForNotification(ctx)
	if err != nil {
		return "", "", err
	}
	return n.Channel, n.Payload, nil
}

func (l *Listener) Close(ctx context.Context) error {
	return l.conn.Close(ctx)
}

package redisx

import (
	"context"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type Conf struct {
	Host string
	Pass string `json:",optional"`
	Type string `json:",optional"`
	DB   int    `json:",optional"`
}

type Client struct {
	c *goredis.Client
}

func New(conf Conf) *Client {
	return &Client{c: goredis.NewClient(&goredis.Options{
		Addr:     conf.Host,
		Password: conf.Pass,
		DB:       conf.DB,
	})}
}

func MustNew(conf Conf) *Client {
	return New(conf)
}

func (c *Client) GetCtx(ctx context.Context, key string) (string, error) {
	val, err := c.c.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", nil
	}
	return val, err
}

func (c *Client) SetCtx(ctx context.Context, key, value string) error {
	return c.c.Set(ctx, key, value, 0).Err()
}

func (c *Client) SetexCtx(ctx context.Context, key, value string, seconds int) error {
	return c.c.Set(ctx, key, value, time.Duration(seconds)*time.Second).Err()
}

func (c *Client) SetNXCtx(ctx context.Context, key, value string, seconds int) (bool, error) {
	return c.c.SetNX(ctx, key, value, time.Duration(seconds)*time.Second).Result()
}

func (c *Client) HSetCtx(ctx context.Context, key, field, value string) error {
	return c.c.HSet(ctx, key, field, value).Err()
}

func (c *Client) HSetNXCtx(ctx context.Context, key, field, value string) (bool, error) {
	return c.c.HSetNX(ctx, key, field, value).Result()
}

func (c *Client) HGetAllCtx(ctx context.Context, key string) (map[string]string, error) {
	return c.c.HGetAll(ctx, key).Result()
}

func (c *Client) IncrCtx(ctx context.Context, key string) (int64, error) {
	return c.c.Incr(ctx, key).Result()
}

func (c *Client) ExpireCtx(ctx context.Context, key string, seconds int) error {
	return c.c.Expire(ctx, key, time.Duration(seconds)*time.Second).Err()
}

func (c *Client) DelCtx(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.c.Del(ctx, keys...).Err()
}

func (c *Client) Close() error {
	return c.c.Close()
}

func (c Conf) WithDefaults() Conf {
	if strings.TrimSpace(c.Host) == "" {
		c.Host = "redis:6379"
	}
	return c
}

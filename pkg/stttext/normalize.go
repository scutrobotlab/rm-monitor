package stttext

import "github.com/liuzl/gocc"

type Converter struct {
	inner *gocc.OpenCC
}

func NewSimplifier() (*Converter, error) {
	c, err := gocc.New("t2s")
	if err != nil {
		return nil, err
	}
	return &Converter{inner: c}, nil
}

func (c *Converter) Simplify(s string) (string, error) {
	if c == nil || c.inner == nil || s == "" {
		return s, nil
	}
	return c.inner.Convert(s)
}
